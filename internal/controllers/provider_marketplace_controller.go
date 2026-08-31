package controllers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"across/backend/internal/config"
	"across/backend/internal/storage"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const propertySafetyWarning = "Never pay a provider before you inspect and independently verify the property, its ownership, and the provider in person. Atlantic Express does not collect property or land payments."

var slugCleaner = regexp.MustCompile(`[^a-z0-9]+`)

type ProviderMarketplaceController struct {
	db         *pgxpool.Pool
	cfg        config.Config
	httpClient *http.Client
	s3         *storage.S3
}

func NewProviderMarketplaceController(db *pgxpool.Pool, cfg config.Config) *ProviderMarketplaceController {
	return &ProviderMarketplaceController{db: db, cfg: cfg, httpClient: &http.Client{Timeout: 12 * time.Second}, s3: storage.NewS3(cfg)}
}

type marketplaceCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

func encodeMarketplaceCursor(createdAt time.Time, id string) string {
	raw, _ := json.Marshal(marketplaceCursor{CreatedAt: createdAt, ID: id})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeMarketplaceCursor(raw string) (*time.Time, any, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, nil, err
	}
	var cursor marketplaceCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.CreatedAt.IsZero() || cursor.ID == "" {
		return nil, nil, fmt.Errorf("invalid cursor")
	}
	return &cursor.CreatedAt, cursor.ID, nil
}

func marketplaceLimit(c *fiber.Ctx) int {
	limit := c.QueryInt("limit", 20)
	if limit < 1 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func marketplaceSlug(value string) string {
	slug := strings.Trim(slugCleaner.ReplaceAllString(strings.ToLower(strings.TrimSpace(value)), "-"), "-")
	if slug == "" {
		slug = "listing"
	}
	return slug
}

func (m *ProviderMarketplaceController) providerForUser(ctx context.Context, userID string) (string, string, error) {
	var providerID, role string
	err := m.db.QueryRow(ctx, `SELECT member.provider_id::text, member.role FROM provider_members member JOIN provider_organizations provider ON provider.id=member.provider_id WHERE member.user_id=$1::uuid AND member.is_active=true AND provider.is_active=true`, userID).Scan(&providerID, &role)
	return providerID, role, err
}

func activeProviderPlan(ctx context.Context, db *pgxpool.Pool, providerID string) (int, error) {
	var limit int
	err := db.QueryRow(ctx, `SELECT plan.listing_limit
		FROM provider_subscriptions subscription
		JOIN provider_subscription_plans plan ON plan.id=subscription.plan_id AND plan.is_active=true
		WHERE subscription.provider_id=$1::uuid AND subscription.status='active'
		  AND subscription.current_period_end>now()
		ORDER BY subscription.current_period_end DESC LIMIT 1`, providerID).Scan(&limit)
	return limit, err
}

func validProviderAssetURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	return strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "/api/v1/public/images/view/")
}

func (m *ProviderMarketplaceController) UpdateMyProvider(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, role, err := m.providerForUser(c.Context(), userID)
	if err != nil || (role != "owner" && role != "manager") {
		return fiber.ErrForbidden
	}
	var req struct {
		BusinessName string `json:"business_name"`
		Description  string `json:"description"`
		ContactEmail string `json:"contact_email"`
		ContactPhone string `json:"contact_phone"`
		WebsiteURL   string `json:"website_url"`
		AddressLine  string `json:"address_line"`
		City         string `json:"city"`
		State        string `json:"state"`
		CountryCode  string `json:"country_code"`
		LogoURL      string `json:"logo_url"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.ErrBadRequest
	}
	if strings.TrimSpace(req.BusinessName) == "" || strings.TrimSpace(req.ContactEmail) == "" || strings.TrimSpace(req.ContactPhone) == "" {
		return fiber.NewError(fiber.StatusUnprocessableEntity, "business_name, contact_email, and contact_phone are required")
	}
	if req.LogoURL != "" && !validProviderAssetURL(req.LogoURL) {
		return fiber.NewError(fiber.StatusUnprocessableEntity, "logo_url must use approved HTTPS storage")
	}
	tag, err := m.db.Exec(c.Context(), `UPDATE provider_organizations SET business_name=$2,description=$3,contact_email=$4,contact_phone=$5,website_url=$6,address_line=$7,city=$8,state=$9,country_code=COALESCE(NULLIF($10,''),'NG'),logo_url=$11,updated_at=now() WHERE id=$1::uuid`, providerID, strings.TrimSpace(req.BusinessName), strings.TrimSpace(req.Description), strings.TrimSpace(req.ContactEmail), strings.TrimSpace(req.ContactPhone), strings.TrimSpace(req.WebsiteURL), strings.TrimSpace(req.AddressLine), strings.TrimSpace(req.City), strings.TrimSpace(req.State), strings.ToUpper(strings.TrimSpace(req.CountryCode)), strings.TrimSpace(req.LogoURL))
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if tag.RowsAffected() == 0 {
		return fiber.ErrNotFound
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (m *ProviderMarketplaceController) PresignProviderUpload(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, _, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.ErrForbidden
	}
	var req struct {
		Filename string `json:"filename"`
		MimeType string `json:"mime_type"`
		Purpose  string `json:"purpose"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.ErrBadRequest
	}
	mime := strings.ToLower(strings.TrimSpace(req.MimeType))
	purpose := strings.ToLower(strings.TrimSpace(req.Purpose))
	if purpose != "listing" && purpose != "verification" && purpose != "logo" {
		return fiber.NewError(fiber.StatusBadRequest, "purpose must be listing, verification, or logo")
	}
	allowed := supportedPortableImageMime(mime)
	if purpose == "verification" && mime == "application/pdf" {
		allowed = true
	}
	if strings.TrimSpace(req.Filename) == "" || !allowed {
		return fiber.NewError(fiber.StatusBadRequest, "only JPG, PNG, WebP, and verification PDFs are supported")
	}
	if !m.s3.Configured() {
		return fiber.NewError(fiber.StatusServiceUnavailable, "media storage is not configured")
	}
	key := storage.SafeKey("user-uploads/providers/"+providerID+"/"+purpose, req.Filename)
	uploadURL, err := m.s3.PresignPut(key, mime, 15*time.Minute)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	return c.JSON(fiber.Map{"upload_url": uploadURL, "view_url": m.s3.ObjectURL(key), "key": key, "expires_in": 900})
}

func (m *ProviderMarketplaceController) AddVerificationDocument(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, role, err := m.providerForUser(c.Context(), userID)
	if err != nil || (role != "owner" && role != "manager") {
		return fiber.ErrForbidden
	}
	var req struct {
		DocumentType string `json:"document_type"`
		DocumentURL  string `json:"document_url"`
	}
	if err := c.BodyParser(&req); err != nil || strings.TrimSpace(req.DocumentType) == "" || !validProviderAssetURL(req.DocumentURL) {
		return fiber.NewError(fiber.StatusUnprocessableEntity, "document_type and an approved document_url are required")
	}
	var id string
	err = m.db.QueryRow(c.Context(), `INSERT INTO provider_verification_documents(provider_id,document_type,document_url) VALUES($1::uuid,$2,$3) RETURNING id::text`, providerID, strings.TrimSpace(req.DocumentType), strings.TrimSpace(req.DocumentURL)).Scan(&id)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id, "status": "pending"})
}

func (m *ProviderMarketplaceController) ListVerificationDocuments(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, _, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.ErrForbidden
	}
	return m.listVerificationDocuments(c, providerID)
}

func (m *ProviderMarketplaceController) listVerificationDocuments(c *fiber.Ctx, providerID string) error {
	rows, err := m.db.Query(c.Context(), `SELECT id::text,document_type,document_url,status,review_notes,created_at,reviewed_at FROM provider_verification_documents WHERE provider_id=$1::uuid ORDER BY created_at DESC,id DESC LIMIT 100`, providerID)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var id, kind, url, status, notes string
		var created time.Time
		var reviewed *time.Time
		if err := rows.Scan(&id, &kind, &url, &status, &notes, &created, &reviewed); err != nil {
			return fiber.ErrInternalServerError
		}
		items = append(items, fiber.Map{"id": id, "document_type": kind, "document_url": url, "status": status, "review_notes": notes, "created_at": created, "reviewed_at": reviewed})
	}
	return c.JSON(fiber.Map{"items": items})
}

func (m *ProviderMarketplaceController) AdminListVerificationDocuments(c *fiber.Ctx) error {
	return m.listVerificationDocuments(c, c.Params("provider_id"))
}

func (m *ProviderMarketplaceController) AdminReviewVerificationDocument(c *fiber.Ctx) error {
	adminID, _ := c.Locals("admin_id").(string)
	var req struct {
		Status string `json:"status"`
		Notes  string `json:"notes"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.ErrBadRequest
	}
	status := strings.ToLower(strings.TrimSpace(req.Status))
	if status != "approved" && status != "rejected" {
		return fiber.NewError(fiber.StatusUnprocessableEntity, "status must be approved or rejected")
	}
	tag, err := m.db.Exec(c.Context(), `UPDATE provider_verification_documents SET status=$3,review_notes=$4,reviewed_at=now(),reviewed_by=$5::uuid WHERE id=$1::uuid AND provider_id=$2::uuid`, c.Params("document_id"), c.Params("provider_id"), status, strings.TrimSpace(req.Notes), adminID)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if tag.RowsAffected() == 0 {
		return fiber.ErrNotFound
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (m *ProviderMarketplaceController) Onboard(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	if _, err := uuid.Parse(userID); err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "sign in again to create a provider profile")
	}
	var req struct {
		BusinessName string `json:"business_name"`
		Description  string `json:"description"`
		ContactEmail string `json:"contact_email"`
		ContactPhone string `json:"contact_phone"`
		WebsiteURL   string `json:"website_url"`
		AddressLine  string `json:"address_line"`
		City         string `json:"city"`
		State        string `json:"state"`
		CountryCode  string `json:"country_code"`
		LogoURL      string `json:"logo_url"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid provider profile")
	}
	if strings.TrimSpace(req.BusinessName) == "" || strings.TrimSpace(req.ContactEmail) == "" || strings.TrimSpace(req.ContactPhone) == "" {
		return fiber.NewError(fiber.StatusUnprocessableEntity, "business_name, contact_email, and contact_phone are required")
	}
	baseSlug := marketplaceSlug(req.BusinessName)
	providerID := uuid.New()
	tx, err := m.db.Begin(c.Context())
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer tx.Rollback(c.Context())
	_, err = tx.Exec(c.Context(), `
		INSERT INTO provider_organizations(
			id, owner_user_id, business_name, slug, description, contact_email,
			contact_phone, website_url, address_line, city, state, country_code, logo_url
		)
		VALUES(
			$1::uuid, $2::uuid, $3::text, $4::text || '-' || left($1::text, 8),
			$5::text, $6::text, $7::text, $8::text, $9::text, $10::text,
			$11::text, COALESCE(NULLIF($12::text, ''), 'NG'), $13::text
		)
	`, providerID, userID, strings.TrimSpace(req.BusinessName), baseSlug, strings.TrimSpace(req.Description), strings.TrimSpace(req.ContactEmail), strings.TrimSpace(req.ContactPhone), strings.TrimSpace(req.WebsiteURL), strings.TrimSpace(req.AddressLine), strings.TrimSpace(req.City), strings.TrimSpace(req.State), strings.ToUpper(strings.TrimSpace(req.CountryCode)), strings.TrimSpace(req.LogoURL))
	if err != nil {
		log.Printf("provider onboarding organization insert failed user_id=%s provider_id=%s: %v", userID, providerID, err)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fiber.NewError(fiber.StatusConflict, "provider profile already exists")
		}
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return fiber.NewError(fiber.StatusUnauthorized, "your account session is no longer valid; sign in again")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "could not create provider profile")
	}
	if _, err = tx.Exec(c.Context(), `INSERT INTO provider_members(provider_id,user_id,role) VALUES($1::uuid,$2::uuid,'owner')`, providerID, userID); err != nil {
		log.Printf("provider onboarding membership insert failed user_id=%s provider_id=%s: %v", userID, providerID, err)
		return fiber.NewError(fiber.StatusInternalServerError, "could not assign provider account owner")
	}
	if _, err = tx.Exec(c.Context(), `INSERT INTO provider_marketplace_events(provider_id,actor_user_id,event_type) VALUES($1::uuid,$2::uuid,'provider_onboarded')`, providerID, userID); err != nil {
		log.Printf("provider onboarding audit insert failed user_id=%s provider_id=%s: %v", userID, providerID, err)
		return fiber.NewError(fiber.StatusInternalServerError, "could not record provider onboarding")
	}
	if err = tx.Commit(c.Context()); err != nil {
		log.Printf("provider onboarding commit failed user_id=%s provider_id=%s: %v", userID, providerID, err)
		return fiber.NewError(fiber.StatusInternalServerError, "could not finish provider onboarding")
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": providerID, "verification_status": "pending"})
}

func (m *ProviderMarketplaceController) MyProvider(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	var id, business, slug, description, email, phone, website, logo, address, city, state, country, verification, notes string
	var active bool
	var created time.Time
	var subStatus string
	var periodEnd *time.Time
	err := m.db.QueryRow(c.Context(), `SELECT p.id::text,p.business_name,p.slug,p.description,p.contact_email,p.contact_phone,p.website_url,p.logo_url,p.address_line,p.city,p.state,p.country_code,p.verification_status,p.verification_notes,p.is_active,p.created_at,COALESCE(s.status,'none'),s.current_period_end FROM provider_organizations p JOIN provider_members pm ON pm.provider_id=p.id AND pm.user_id=$1::uuid AND pm.is_active=true LEFT JOIN LATERAL (SELECT status,current_period_end FROM provider_subscriptions WHERE provider_id=p.id ORDER BY created_at DESC LIMIT 1) s ON true`, userID).Scan(&id, &business, &slug, &description, &email, &phone, &website, &logo, &address, &city, &state, &country, &verification, &notes, &active, &created, &subStatus, &periodEnd)
	if err == pgx.ErrNoRows {
		return fiber.NewError(fiber.StatusNotFound, "provider profile not found")
	}
	if err != nil {
		return fiber.ErrInternalServerError
	}
	return c.JSON(fiber.Map{"id": id, "business_name": business, "slug": slug, "description": description, "contact_email": email, "contact_phone": phone, "website_url": website, "logo_url": logo, "address_line": address, "city": city, "state": state, "country_code": country, "verification_status": verification, "verification_notes": notes, "is_active": active, "created_at": created, "subscription": fiber.Map{"status": subStatus, "current_period_end": periodEnd}})
}

type listingPayload struct {
	ListingType  string         `json:"listing_type"`
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	Category     string         `json:"category"`
	ContactEmail string         `json:"contact_email"`
	ContactPhone string         `json:"contact_phone"`
	AddressLine  string         `json:"address_line"`
	City         string         `json:"city"`
	State        string         `json:"state"`
	CountryCode  string         `json:"country_code"`
	Price        *float64       `json:"price"`
	CurrencyCode string         `json:"currency_code"`
	PricingUnit  string         `json:"pricing_unit"`
	Capacity     int            `json:"capacity"`
	MediaURLs    []string       `json:"media_urls"`
	Attributes   map[string]any `json:"attributes"`
}

func validListingType(t string) bool {
	switch t {
	case "hotel", "short_let", "car_rental", "car_wash", "shop_rental", "property", "land":
		return true
	}
	return false
}
func directBookingType(t string) bool {
	switch t {
	case "hotel", "short_let", "car_rental", "car_wash":
		return true
	}
	return false
}

func validateListing(req listingPayload) error {
	req.ListingType = strings.ToLower(strings.TrimSpace(req.ListingType))
	if !validListingType(req.ListingType) {
		return fmt.Errorf("unsupported listing_type")
	}
	if strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.Description) == "" || strings.TrimSpace(req.City) == "" {
		return fmt.Errorf("title, description, and city are required")
	}
	if len(req.MediaURLs) == 0 {
		return fmt.Errorf("at least one listing image is required")
	}
	if len(req.MediaURLs) > 20 {
		return fmt.Errorf("a listing can have at most 20 images")
	}
	for _, image := range req.MediaURLs {
		if !strings.HasPrefix(image, "https://") && !strings.HasPrefix(image, "/api/v1/public/images/view/") {
			return fmt.Errorf("listing images must use approved HTTPS storage")
		}
	}
	if req.Price != nil && *req.Price < 0 {
		return fmt.Errorf("price cannot be negative")
	}
	return nil
}

func (m *ProviderMarketplaceController) CreateListing(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, _, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusForbidden, "provider access required")
	}
	var req listingPayload
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid listing")
	}
	if err := validateListing(req); err != nil {
		return fiber.NewError(fiber.StatusUnprocessableEntity, err.Error())
	}
	listingLimit, err := activeProviderPlan(c.Context(), m.db, providerID)
	if err == pgx.ErrNoRows {
		return fiber.NewError(fiber.StatusPaymentRequired, "an active provider subscription is required to create listings")
	}
	if err != nil {
		return fiber.ErrInternalServerError
	}
	var listingCount int
	if err := m.db.QueryRow(c.Context(), `SELECT count(*) FROM provider_listings WHERE provider_id=$1::uuid AND status<>'archived'`, providerID).Scan(&listingCount); err != nil {
		return fiber.ErrInternalServerError
	}
	if listingCount >= listingLimit {
		return fiber.NewError(fiber.StatusConflict, "the active subscription plan listing limit has been reached")
	}
	if req.Capacity < 1 {
		req.Capacity = 1
	}
	if req.CurrencyCode == "" {
		req.CurrencyCode = "NGN"
	}
	attrs, _ := json.Marshal(req.Attributes)
	id := uuid.New()
	slug := marketplaceSlug(req.Title) + "-" + strings.ToLower(id.String()[:8])
	_, err = m.db.Exec(c.Context(), `INSERT INTO provider_listings(id,provider_id,listing_type,title,slug,description,category,contact_email,contact_phone,address_line,city,state,country_code,price,currency_code,pricing_unit,capacity,media_urls,attributes) VALUES($1,$2::uuid,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,COALESCE(NULLIF($13,''),'NG'),$14,COALESCE(NULLIF($15,''),'NGN'),$16,$17,$18,$19::jsonb)`, id, providerID, strings.ToLower(strings.TrimSpace(req.ListingType)), strings.TrimSpace(req.Title), slug, strings.TrimSpace(req.Description), strings.TrimSpace(req.Category), strings.TrimSpace(req.ContactEmail), strings.TrimSpace(req.ContactPhone), strings.TrimSpace(req.AddressLine), strings.TrimSpace(req.City), strings.TrimSpace(req.State), strings.ToUpper(strings.TrimSpace(req.CountryCode)), req.Price, strings.ToUpper(strings.TrimSpace(req.CurrencyCode)), strings.TrimSpace(req.PricingUnit), req.Capacity, req.MediaURLs, attrs)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "could not create listing")
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id, "slug": slug, "status": "draft"})
}

func (m *ProviderMarketplaceController) UpdateListing(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, _, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.ErrForbidden
	}
	var req listingPayload
	if err := c.BodyParser(&req); err != nil {
		return fiber.ErrBadRequest
	}
	if err := validateListing(req); err != nil {
		return fiber.NewError(fiber.StatusUnprocessableEntity, err.Error())
	}
	if req.Capacity < 1 {
		req.Capacity = 1
	}
	if req.CurrencyCode == "" {
		req.CurrencyCode = "NGN"
	}
	attrs, _ := json.Marshal(req.Attributes)
	tag, err := m.db.Exec(c.Context(), `UPDATE provider_listings SET listing_type=$3,title=$4,description=$5,category=$6,contact_email=$7,contact_phone=$8,address_line=$9,city=$10,state=$11,country_code=COALESCE(NULLIF($12,''),'NG'),price=$13,currency_code=COALESCE(NULLIF($14,''),'NGN'),pricing_unit=$15,capacity=$16,media_urls=$17,attributes=$18::jsonb,status=CASE WHEN status IN ('pending','approved') THEN 'pending' ELSE status END,moderation_notes='',updated_at=now() WHERE id=$1::uuid AND provider_id=$2::uuid AND status<>'archived'`, c.Params("listing_id"), providerID, strings.ToLower(strings.TrimSpace(req.ListingType)), strings.TrimSpace(req.Title), strings.TrimSpace(req.Description), strings.TrimSpace(req.Category), strings.TrimSpace(req.ContactEmail), strings.TrimSpace(req.ContactPhone), strings.TrimSpace(req.AddressLine), strings.TrimSpace(req.City), strings.TrimSpace(req.State), strings.ToUpper(strings.TrimSpace(req.CountryCode)), req.Price, strings.ToUpper(strings.TrimSpace(req.CurrencyCode)), strings.TrimSpace(req.PricingUnit), req.Capacity, req.MediaURLs, attrs)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if tag.RowsAffected() == 0 {
		return fiber.ErrNotFound
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (m *ProviderMarketplaceController) ArchiveListing(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, _, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.ErrForbidden
	}
	tag, err := m.db.Exec(c.Context(), `UPDATE provider_listings SET status='archived',updated_at=now() WHERE id=$1::uuid AND provider_id=$2::uuid AND status<>'archived'`, c.Params("listing_id"), providerID)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if tag.RowsAffected() == 0 {
		return fiber.ErrNotFound
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (m *ProviderMarketplaceController) SubmitListing(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, _, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.ErrForbidden
	}
	if _, err := activeProviderPlan(c.Context(), m.db, providerID); err == pgx.ErrNoRows {
		return fiber.NewError(fiber.StatusPaymentRequired, "an active provider subscription is required to publish listings")
	} else if err != nil {
		return fiber.ErrInternalServerError
	}
	tag, err := m.db.Exec(c.Context(), `UPDATE provider_listings SET status='pending',updated_at=now() WHERE id=$1::uuid AND provider_id=$2::uuid AND status IN ('draft','rejected')`, c.Params("listing_id"), providerID)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if tag.RowsAffected() == 0 {
		return fiber.NewError(fiber.StatusConflict, "listing cannot be submitted")
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (m *ProviderMarketplaceController) ListMyListings(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, _, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.ErrForbidden
	}
	return m.listListings(c, "provider", providerID)
}

func (m *ProviderMarketplaceController) ListPublicListings(c *fiber.Ctx) error {
	return m.listListings(c, "public", "")
}

func (m *ProviderMarketplaceController) listListings(c *fiber.Ctx, scope, providerID string) error {
	limit := marketplaceLimit(c)
	cursorTime, cursorID, err := decodeMarketplaceCursor(c.Query("cursor"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid cursor")
	}
	listingType := strings.ToLower(strings.TrimSpace(c.Query("type")))
	search := strings.TrimSpace(c.Query("search"))
	status := strings.ToLower(strings.TrimSpace(c.Query("status")))
	rows, err := m.db.Query(c.Context(), `SELECT l.id::text,l.provider_id::text,p.business_name,l.listing_type,l.title,l.slug,l.description,l.category,l.address_line,l.city,l.state,l.country_code,l.price,l.currency_code,l.pricing_unit,l.capacity,l.media_urls,l.attributes,l.status,l.published_at,l.created_at,COALESCE(s.active,false) FROM provider_listings l JOIN provider_organizations p ON p.id=l.provider_id LEFT JOIN LATERAL (SELECT true AS active FROM provider_subscriptions ps WHERE ps.provider_id=p.id AND ps.status='active' AND ps.current_period_end>now() ORDER BY ps.current_period_end DESC LIMIT 1) s ON true WHERE ($1='' OR l.listing_type=$1) AND ($2='' OR (l.title||' '||l.description||' '||l.city||' '||l.state) ILIKE '%%'||$2||'%%') AND ($3<>'public' OR (l.status='approved' AND p.verification_status='approved' AND p.is_active=true AND COALESCE(s.active,false))) AND ($3<>'provider' OR l.provider_id=$4::uuid) AND ($5='' OR l.status=$5) AND ($6::timestamptz IS NULL OR (l.created_at,l.id)<($6,$7::uuid)) ORDER BY l.created_at DESC,l.id DESC LIMIT $8`, listingType, search, scope, providerID, status, cursorTime, cursorID, limit+1)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "listings unavailable")
	}
	defer rows.Close()
	items := make([]fiber.Map, 0, limit)
	next := ""
	var lastCreated time.Time
	var lastID string
	for rows.Next() {
		var id, pid, business, lt, title, slug, desc, category, address, city, state, country, currency, unit, lstatus string
		var price *float64
		var capacity int
		var media []string
		var attrs []byte
		var published *time.Time
		var created time.Time
		var subscribed bool
		if err := rows.Scan(&id, &pid, &business, &lt, &title, &slug, &desc, &category, &address, &city, &state, &country, &price, &currency, &unit, &capacity, &media, &attrs, &lstatus, &published, &created, &subscribed); err != nil {
			return fiber.ErrInternalServerError
		}
		if len(items) == limit {
			next = encodeMarketplaceCursor(lastCreated, lastID)
			break
		}
		var attributes map[string]any
		_ = json.Unmarshal(attrs, &attributes)
		items = append(items, fiber.Map{"id": id, "provider_id": pid, "provider_name": business, "listing_type": lt, "title": title, "slug": slug, "description": desc, "category": category, "address_line": address, "city": city, "state": state, "country_code": country, "price": price, "currency_code": currency, "pricing_unit": unit, "capacity": capacity, "media_urls": media, "attributes": attributes, "status": lstatus, "published_at": published, "created_at": created, "contact_available": subscribed, "direct_booking": directBookingType(lt), "safety_warning": func() string {
			if lt == "property" || lt == "land" || lt == "shop_rental" {
				return propertySafetyWarning
			}
			return ""
		}()})
		lastCreated, lastID = created, id
	}
	return c.JSON(fiber.Map{"items": items, "next_cursor": next, "has_more": next != ""})
}

func (m *ProviderMarketplaceController) GetPublicListing(c *fiber.Ctx) error {
	var id, pid, business, lt, title, slug, desc, category, address, city, state, country, currency, unit string
	var price *float64
	var capacity int
	var media []string
	var attrs []byte
	err := m.db.QueryRow(c.Context(), `SELECT l.id::text,l.provider_id::text,p.business_name,l.listing_type,l.title,l.slug,l.description,l.category,l.address_line,l.city,l.state,l.country_code,l.price,l.currency_code,l.pricing_unit,l.capacity,l.media_urls,l.attributes FROM provider_listings l JOIN provider_organizations p ON p.id=l.provider_id WHERE l.id=$1::uuid AND l.status='approved' AND p.verification_status='approved' AND p.is_active=true AND EXISTS(SELECT 1 FROM provider_subscriptions s WHERE s.provider_id=p.id AND s.status='active' AND s.current_period_end>now())`, c.Params("listing_id")).Scan(&id, &pid, &business, &lt, &title, &slug, &desc, &category, &address, &city, &state, &country, &price, &currency, &unit, &capacity, &media, &attrs)
	if err == pgx.ErrNoRows {
		return fiber.ErrNotFound
	}
	if err != nil {
		return fiber.ErrInternalServerError
	}
	var attributes map[string]any
	_ = json.Unmarshal(attrs, &attributes)
	return c.JSON(fiber.Map{"id": id, "provider_id": pid, "provider_name": business, "listing_type": lt, "title": title, "slug": slug, "description": desc, "category": category, "address_line": address, "city": city, "state": state, "country_code": country, "price": price, "currency_code": currency, "pricing_unit": unit, "capacity": capacity, "media_urls": media, "attributes": attributes, "direct_booking": directBookingType(lt), "safety_warning": func() string {
		if !directBookingType(lt) {
			return propertySafetyWarning
		}
		return ""
	}()})
}

func (m *ProviderMarketplaceController) RevealContact(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	var req struct {
		SafetyAcknowledged bool `json:"safety_acknowledged"`
	}
	_ = c.BodyParser(&req)
	var providerID, lt, email, phone string
	err := m.db.QueryRow(c.Context(), `SELECT l.provider_id::text,l.listing_type,COALESCE(NULLIF(l.contact_email,''),p.contact_email),COALESCE(NULLIF(l.contact_phone,''),p.contact_phone) FROM provider_listings l JOIN provider_organizations p ON p.id=l.provider_id WHERE l.id=$1::uuid AND l.status='approved' AND p.verification_status='approved' AND p.is_active=true AND EXISTS(SELECT 1 FROM provider_subscriptions s WHERE s.provider_id=p.id AND s.status='active' AND s.current_period_end>now())`, c.Params("listing_id")).Scan(&providerID, &lt, &email, &phone)
	if err == pgx.ErrNoRows {
		return fiber.NewError(fiber.StatusPaymentRequired, "provider contact is unavailable until verification and subscription are active")
	}
	if err != nil {
		return fiber.ErrInternalServerError
	}
	requiresAck := lt == "property" || lt == "land" || lt == "shop_rental"
	if requiresAck && !req.SafetyAcknowledged {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"code": "SAFETY_ACK_REQUIRED", "message": propertySafetyWarning})
	}
	_, err = m.db.Exec(c.Context(), `INSERT INTO provider_contact_reveals(listing_id,provider_id,user_id,safety_acknowledged) VALUES($1::uuid,$2::uuid,$3::uuid,$4)`, c.Params("listing_id"), providerID, userID, req.SafetyAcknowledged)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	return c.JSON(fiber.Map{"email": email, "phone": phone, "safety_warning": func() string {
		if requiresAck {
			return propertySafetyWarning
		}
		return ""
	}()})
}

func (m *ProviderMarketplaceController) CreateRequest(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	var req struct {
		RequestType    string     `json:"request_type"`
		SlotID         string     `json:"slot_id"`
		Message        string     `json:"message"`
		IdempotencyKey string     `json:"idempotency_key"`
		StartsAt       *time.Time `json:"starts_at"`
		EndsAt         *time.Time `json:"ends_at"`
		PartySize      int        `json:"party_size"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request")
	}
	if req.PartySize < 1 {
		req.PartySize = 1
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return fiber.NewError(fiber.StatusBadRequest, "idempotency_key is required")
	}
	tx, err := m.db.Begin(c.Context())
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer tx.Rollback(c.Context())
	var existingID, existingStatus string
	err = tx.QueryRow(c.Context(), `SELECT id::text,status FROM provider_requests WHERE user_id=$1::uuid AND idempotency_key=$2`, userID, strings.TrimSpace(req.IdempotencyKey)).Scan(&existingID, &existingStatus)
	if err == nil {
		return c.JSON(fiber.Map{"id": existingID, "status": existingStatus, "idempotent_replay": true})
	}
	if err != pgx.ErrNoRows {
		return fiber.ErrInternalServerError
	}
	var providerID, lt, listingTitle, buyerName, buyerEmail, buyerPhone string
	err = tx.QueryRow(c.Context(), `SELECT l.provider_id::text,l.listing_type,l.title,u.full_name,u.email,COALESCE(u.phone,'') FROM provider_listings l JOIN provider_organizations p ON p.id=l.provider_id JOIN users u ON u.id=$2::uuid WHERE l.id=$1::uuid AND l.status='approved' AND p.verification_status='approved' AND EXISTS(SELECT 1 FROM provider_subscriptions s WHERE s.provider_id=p.id AND s.status='active' AND s.current_period_end>now())`, c.Params("listing_id"), userID).Scan(&providerID, &lt, &listingTitle, &buyerName, &buyerEmail, &buyerPhone)
	if err != nil {
		return fiber.ErrNotFound
	}
	wanted := strings.ToLower(strings.TrimSpace(req.RequestType))
	if directBookingType(lt) {
		if wanted != "booking" && wanted != "appointment" {
			return fiber.NewError(fiber.StatusUnprocessableEntity, "this service accepts booking or appointment requests")
		}
	} else if wanted != "inspection" && wanted != "enquiry" {
		return fiber.NewError(fiber.StatusUnprocessableEntity, "property, land, and shop listings are enquiry-only")
	}
	var slot any = nil
	if strings.TrimSpace(req.SlotID) != "" {
		slot = req.SlotID
		tag, err := tx.Exec(c.Context(), `UPDATE provider_availability_slots SET remaining=remaining-$2 WHERE id=$1::uuid AND listing_id=$3::uuid AND status='open' AND remaining>=$2`, req.SlotID, req.PartySize, c.Params("listing_id"))
		if err != nil {
			return fiber.ErrInternalServerError
		}
		if tag.RowsAffected() == 0 {
			return fiber.NewError(fiber.StatusConflict, "selected availability is no longer available")
		}
	}
	var requestID string
	searchText := strings.TrimSpace(strings.Join([]string{listingTitle, wanted, req.Message, buyerName, buyerEmail, buyerPhone}, " "))
	err = tx.QueryRow(c.Context(), `INSERT INTO provider_requests(listing_id,provider_id,user_id,request_type,slot_id,starts_at,ends_at,party_size,message,search_text,idempotency_key) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6,$7,$8,$9,$10,$11) ON CONFLICT(user_id,idempotency_key) DO NOTHING RETURNING id::text`, c.Params("listing_id"), providerID, userID, wanted, slot, req.StartsAt, req.EndsAt, req.PartySize, strings.TrimSpace(req.Message), searchText, strings.TrimSpace(req.IdempotencyKey)).Scan(&requestID)
	if err == pgx.ErrNoRows {
		_ = tx.Rollback(c.Context())
		if lookupErr := m.db.QueryRow(c.Context(), `SELECT id::text,status FROM provider_requests WHERE user_id=$1::uuid AND idempotency_key=$2`, userID, strings.TrimSpace(req.IdempotencyKey)).Scan(&existingID, &existingStatus); lookupErr != nil {
			return fiber.ErrConflict
		}
		return c.JSON(fiber.Map{"id": existingID, "status": existingStatus, "idempotent_replay": true})
	}
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if err = tx.Commit(c.Context()); err != nil {
		return fiber.ErrInternalServerError
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": requestID, "status": "pending"})
}

func (m *ProviderMarketplaceController) ListAvailability(c *fiber.Ctx) error {
	rows, err := m.db.Query(c.Context(), `SELECT id::text,starts_at,ends_at,capacity,remaining FROM provider_availability_slots WHERE listing_id=$1::uuid AND status='open' AND remaining>0 AND ends_at>now() ORDER BY starts_at,id LIMIT 200`, c.Params("listing_id"))
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var id string
		var startsAt, endsAt time.Time
		var capacity, remaining int
		if err := rows.Scan(&id, &startsAt, &endsAt, &capacity, &remaining); err != nil {
			return fiber.ErrInternalServerError
		}
		items = append(items, fiber.Map{"id": id, "starts_at": startsAt, "ends_at": endsAt, "capacity": capacity, "remaining": remaining})
	}
	return c.JSON(fiber.Map{"items": items})
}

func (m *ProviderMarketplaceController) UpsertAvailability(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, _, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.ErrForbidden
	}
	var req struct {
		StartsAt time.Time `json:"starts_at"`
		EndsAt   time.Time `json:"ends_at"`
		Capacity int       `json:"capacity"`
		Status   string    `json:"status"`
	}
	if err := c.BodyParser(&req); err != nil || req.StartsAt.IsZero() || !req.EndsAt.After(req.StartsAt) {
		return fiber.NewError(fiber.StatusBadRequest, "valid starts_at and ends_at are required")
	}
	if req.Capacity < 1 {
		req.Capacity = 1
	}
	if req.Status == "" {
		req.Status = "open"
	}
	if req.Status != "open" && req.Status != "blocked" && req.Status != "closed" {
		return fiber.NewError(fiber.StatusUnprocessableEntity, "invalid availability status")
	}
	var id string
	err = m.db.QueryRow(c.Context(), `INSERT INTO provider_availability_slots(listing_id,starts_at,ends_at,capacity,remaining,status) SELECT l.id,$2,$3,$4,$4,$5 FROM provider_listings l WHERE l.id=$1::uuid AND l.provider_id=$6::uuid ON CONFLICT(listing_id,starts_at,ends_at) DO UPDATE SET capacity=EXCLUDED.capacity,remaining=GREATEST(EXCLUDED.capacity-(provider_availability_slots.capacity-provider_availability_slots.remaining),0),status=EXCLUDED.status RETURNING id::text`, c.Params("listing_id"), req.StartsAt, req.EndsAt, req.Capacity, req.Status, providerID).Scan(&id)
	if err == pgx.ErrNoRows {
		return fiber.ErrNotFound
	}
	if err != nil {
		return fiber.ErrInternalServerError
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id})
}

func (m *ProviderMarketplaceController) ListMyRequests(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	return m.listRequests(c, "user", userID)
}

func (m *ProviderMarketplaceController) ListProviderRequests(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, _, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.ErrForbidden
	}
	return m.listRequests(c, "provider", providerID)
}

func (m *ProviderMarketplaceController) listRequests(c *fiber.Ctx, scope, ownerID string) error {
	limit := marketplaceLimit(c)
	cursorTime, cursorID, err := decodeMarketplaceCursor(c.Query("cursor"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid cursor")
	}
	status := strings.ToLower(strings.TrimSpace(c.Query("status")))
	search := strings.TrimSpace(c.Query("search"))
	rows, err := m.db.Query(c.Context(), `SELECT r.id::text,r.request_type,r.status,r.starts_at,r.ends_at,r.party_size,r.message,r.created_at,l.id::text,l.title,l.listing_type,p.business_name,u.full_name,u.email,COALESCE(u.phone,'') FROM provider_requests r JOIN provider_listings l ON l.id=r.listing_id JOIN provider_organizations p ON p.id=r.provider_id JOIN users u ON u.id=r.user_id WHERE (($1='user' AND r.user_id=$2::uuid) OR ($1='provider' AND r.provider_id=$2::uuid)) AND ($3='' OR r.status=$3) AND ($4='' OR r.search_text ILIKE '%%'||$4||'%%') AND ($5::timestamptz IS NULL OR (r.created_at,r.id)<($5,$6::uuid)) ORDER BY r.created_at DESC,r.id DESC LIMIT $7`, scope, ownerID, status, search, cursorTime, cursorID, limit+1)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer rows.Close()
	items := []fiber.Map{}
	var lastCreated time.Time
	var lastID, next string
	for rows.Next() {
		var id, requestType, requestStatus, message, listingID, title, listingType, providerName, buyerName, buyerEmail, buyerPhone string
		var startsAt, endsAt *time.Time
		var partySize int
		var createdAt time.Time
		if err := rows.Scan(&id, &requestType, &requestStatus, &startsAt, &endsAt, &partySize, &message, &createdAt, &listingID, &title, &listingType, &providerName, &buyerName, &buyerEmail, &buyerPhone); err != nil {
			return fiber.ErrInternalServerError
		}
		if len(items) == limit {
			next = encodeMarketplaceCursor(lastCreated, lastID)
			break
		}
		item := fiber.Map{"id": id, "request_type": requestType, "status": requestStatus, "starts_at": startsAt, "ends_at": endsAt, "party_size": partySize, "message": message, "created_at": createdAt, "listing_id": listingID, "listing_title": title, "listing_type": listingType, "provider_name": providerName}
		if scope == "provider" {
			item["buyer"] = fiber.Map{"full_name": buyerName, "email": buyerEmail, "phone": buyerPhone}
		}
		items = append(items, item)
		lastCreated, lastID = createdAt, id
	}
	return c.JSON(fiber.Map{"items": items, "next_cursor": next, "has_more": next != ""})
}

func (m *ProviderMarketplaceController) UpdateProviderRequest(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, _, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.ErrForbidden
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.ErrBadRequest
	}
	req.Status = strings.ToLower(strings.TrimSpace(req.Status))
	if req.Status != "accepted" && req.Status != "rejected" && req.Status != "completed" {
		return fiber.NewError(fiber.StatusUnprocessableEntity, "status must be accepted, rejected, or completed")
	}
	tag, err := m.db.Exec(c.Context(), `UPDATE provider_requests SET status=$1,updated_at=now() WHERE id=$2::uuid AND provider_id=$3::uuid AND (($1 IN ('accepted','rejected') AND status='pending') OR ($1='completed' AND status='accepted'))`, req.Status, c.Params("request_id"), providerID)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if tag.RowsAffected() == 0 {
		return fiber.NewError(fiber.StatusConflict, "request status has changed; reload and try again")
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (m *ProviderMarketplaceController) ReportListing(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	var req struct{ Reason, Details string }
	if err := c.BodyParser(&req); err != nil || strings.TrimSpace(req.Reason) == "" {
		return fiber.NewError(fiber.StatusBadRequest, "reason is required")
	}
	_, err := m.db.Exec(c.Context(), `INSERT INTO provider_listing_reports(listing_id,user_id,reason,details) VALUES($1::uuid,$2::uuid,$3,$4)`, c.Params("listing_id"), userID, strings.TrimSpace(req.Reason), strings.TrimSpace(req.Details))
	if err != nil {
		return fiber.ErrInternalServerError
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (m *ProviderMarketplaceController) ListPlans(c *fiber.Ctx) error {
	rows, err := m.db.Query(c.Context(), `SELECT id::text,code,name,description,amount_ngn,listing_limit,features FROM provider_subscription_plans WHERE is_active=true ORDER BY amount_ngn,id`)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var id, code, name, desc string
		var amount float64
		var limit int
		var features []byte
		if err := rows.Scan(&id, &code, &name, &desc, &amount, &limit, &features); err != nil {
			return fiber.ErrInternalServerError
		}
		var f map[string]any
		_ = json.Unmarshal(features, &f)
		items = append(items, fiber.Map{"id": id, "code": code, "name": name, "description": desc, "amount_ngn": amount, "listing_limit": limit, "features": f})
	}
	return c.JSON(fiber.Map{"items": items})
}

func (m *ProviderMarketplaceController) SubscriptionCheckout(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, _, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.ErrForbidden
	}
	var req struct {
		PlanID      string `json:"plan_id"`
		RedirectURL string `json:"redirect_url"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.ErrBadRequest
	}
	var planID, name string
	var amount float64
	var fwPlanID *int64
	err = m.db.QueryRow(c.Context(), `SELECT id::text,name,amount_ngn,flutterwave_plan_id FROM provider_subscription_plans WHERE id=$1::uuid AND is_active=true`, req.PlanID).Scan(&planID, &name, &amount, &fwPlanID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "subscription plan not found")
	}
	if fwPlanID == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "subscription checkout is not configured for this plan")
	}
	var email, fullName, phone string
	err = m.db.QueryRow(c.Context(), `SELECT email,full_name,COALESCE(phone,'') FROM users WHERE id=$1::uuid AND is_active=true AND email_verified_at IS NOT NULL`, userID).Scan(&email, &fullName, &phone)
	if err != nil {
		return fiber.NewError(fiber.StatusUnprocessableEntity, "verify your email and complete your profile first")
	}
	txRef := "PROVIDER-" + uuid.NewString()
	redirect := strings.TrimSpace(req.RedirectURL)
	if redirect == "" {
		redirect = "across://providers/subscription"
	}
	payload := map[string]any{"tx_ref": txRef, "amount": amount, "currency": "NGN", "redirect_url": redirect, "payment_options": "card", "payment_plan": *fwPlanID, "customer": buildFlutterwaveCustomer(email, fullName, phone), "customizations": map[string]any{"title": "Atlantic Express Provider Subscription", "description": name + " monthly plan"}, "meta": map[string]any{"payment_kind": "provider_subscription", "provider_id": providerID, "plan_id": planID}}
	body, _ := json.Marshal(payload)
	httpReq, err := http.NewRequestWithContext(c.Context(), http.MethodPost, "https://api.flutterwave.com/v3/payments", bytes.NewReader(body))
	if err != nil {
		return fiber.ErrInternalServerError
	}
	httpReq.Header.Set("Authorization", "Bearer "+m.cfg.FlutterwaveSecretKey)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := m.httpClient.Do(httpReq)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "payment gateway unavailable")
	}
	defer resp.Body.Close()
	var gateway map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&gateway); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "invalid payment gateway response")
	}
	if resp.StatusCode >= 300 {
		return c.Status(fiber.StatusBadGateway).JSON(gateway)
	}
	_, err = m.db.Exec(c.Context(), `INSERT INTO provider_subscriptions(provider_id,plan_id,status,tx_ref,customer_email) VALUES($1::uuid,$2::uuid,'pending',$3,$4)`, providerID, planID, txRef, email)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	link := ""
	if data, ok := gateway["data"].(map[string]any); ok {
		link, _ = data["link"].(string)
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"tx_ref": txRef, "checkout_link": link, "redirect_url": redirect})
}

func settleProviderSubscription(ctx context.Context, db *pgxpool.Pool, txRef, transactionID string, paidAmount float64, currency string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var subscriptionID, providerID string
	var expected float64
	err = tx.QueryRow(ctx, `SELECT s.id::text,s.provider_id::text,p.amount_ngn FROM provider_subscriptions s JOIN provider_subscription_plans p ON p.id=s.plan_id WHERE s.tx_ref=$1 FOR UPDATE`, txRef).Scan(&subscriptionID, &providerID, &expected)
	if err != nil {
		return fmt.Errorf("provider subscription not found")
	}
	if strings.ToUpper(strings.TrimSpace(currency)) != "NGN" || paidAmount+0.01 < expected {
		return fmt.Errorf("provider subscription amount mismatch")
	}
	tag, err := tx.Exec(ctx, `INSERT INTO provider_subscription_payments(subscription_id,flutterwave_transaction_id,tx_ref,amount,currency_code) VALUES($1::uuid,$2,$3,$4,$5) ON CONFLICT(flutterwave_transaction_id) DO NOTHING`, subscriptionID, transactionID, txRef, paidAmount, strings.ToUpper(strings.TrimSpace(currency)))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	_, err = tx.Exec(ctx, `UPDATE provider_subscriptions SET status='active',flutterwave_transaction_id=$2,starts_at=COALESCE(starts_at,now()),current_period_end=GREATEST(COALESCE(current_period_end,now()),now())+interval '1 month',last_payment_at=now(),updated_at=now(),version=version+1 WHERE id=$1::uuid`, subscriptionID, transactionID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO provider_marketplace_events(provider_id,event_type,metadata) VALUES($1::uuid,'subscription_activated',jsonb_build_object('subscription_id',$2,'tx_ref',$3))`, providerID, subscriptionID, txRef)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *ProviderMarketplaceController) AdminListProviders(c *fiber.Ctx) error {
	limit := marketplaceLimit(c)
	cursorTime, cursorID, err := decodeMarketplaceCursor(c.Query("cursor"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid cursor")
	}
	search := strings.TrimSpace(c.Query("search"))
	status := strings.ToLower(strings.TrimSpace(c.Query("status")))
	rows, err := m.db.Query(c.Context(), `SELECT p.id::text,p.business_name,p.contact_email,p.contact_phone,p.city,p.state,p.verification_status,p.created_at,COALESCE(s.status,'none'),s.current_period_end FROM provider_organizations p LEFT JOIN LATERAL(SELECT status,current_period_end FROM provider_subscriptions WHERE provider_id=p.id ORDER BY created_at DESC LIMIT 1)s ON true WHERE ($1='' OR p.verification_status=$1) AND ($2='' OR (p.business_name||' '||p.contact_email||' '||p.contact_phone||' '||p.city||' '||p.state) ILIKE '%'||$2||'%') AND ($3::timestamptz IS NULL OR (p.created_at,p.id)<($3,$4::uuid)) ORDER BY p.created_at DESC,p.id DESC LIMIT $5`, status, search, cursorTime, cursorID, limit+1)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer rows.Close()
	items := []fiber.Map{}
	var lastCreated time.Time
	var lastID, next string
	for rows.Next() {
		var id, name, email, phone, city, state, status, sub string
		var created time.Time
		var end *time.Time
		if err := rows.Scan(&id, &name, &email, &phone, &city, &state, &status, &created, &sub, &end); err != nil {
			return fiber.ErrInternalServerError
		}
		if len(items) == limit {
			next = encodeMarketplaceCursor(lastCreated, lastID)
			break
		}
		items = append(items, fiber.Map{"id": id, "business_name": name, "contact_email": email, "contact_phone": phone, "city": city, "state": state, "verification_status": status, "created_at": created, "subscription_status": sub, "subscription_ends_at": end})
		lastCreated, lastID = created, id
	}
	return c.JSON(fiber.Map{"items": items, "next_cursor": next, "has_more": next != ""})
}

func (m *ProviderMarketplaceController) AdminVerifyProvider(c *fiber.Ctx) error {
	adminID, _ := c.Locals("admin_id").(string)
	var req struct{ Status, Notes string }
	if err := c.BodyParser(&req); err != nil {
		return fiber.ErrBadRequest
	}
	status := strings.ToLower(strings.TrimSpace(req.Status))
	if status != "approved" && status != "rejected" && status != "suspended" {
		return fiber.NewError(fiber.StatusBadRequest, "invalid verification status")
	}
	tag, err := m.db.Exec(c.Context(), `UPDATE provider_organizations SET verification_status=$2,verification_notes=$3,verified_at=CASE WHEN $2='approved' THEN now() ELSE verified_at END,verified_by=$4::uuid,updated_at=now() WHERE id=$1::uuid AND ($2<>'approved' OR EXISTS(SELECT 1 FROM provider_verification_documents d WHERE d.provider_id=provider_organizations.id AND d.status='approved'))`, c.Params("provider_id"), status, strings.TrimSpace(req.Notes), adminID)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if tag.RowsAffected() == 0 {
		if status == "approved" {
			return fiber.NewError(fiber.StatusConflict, "approve at least one verification document before approving the provider")
		}
		return fiber.ErrNotFound
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (m *ProviderMarketplaceController) AdminListListings(c *fiber.Ctx) error {
	return m.listListings(c, "admin", "")
}

func (m *ProviderMarketplaceController) AdminModerateListing(c *fiber.Ctx) error {
	adminID, _ := c.Locals("admin_id").(string)
	var req struct{ Status, Notes string }
	if err := c.BodyParser(&req); err != nil {
		return fiber.ErrBadRequest
	}
	status := strings.ToLower(strings.TrimSpace(req.Status))
	if status != "approved" && status != "rejected" && status != "suspended" {
		return fiber.NewError(fiber.StatusBadRequest, "invalid moderation status")
	}
	tag, err := m.db.Exec(c.Context(), `UPDATE provider_listings SET status=$2,moderation_notes=$3,moderated_by=$4::uuid,moderated_at=now(),published_at=CASE WHEN $2='approved' THEN COALESCE(published_at,now()) ELSE published_at END,updated_at=now() WHERE id=$1::uuid AND status<>'archived'`, c.Params("listing_id"), status, strings.TrimSpace(req.Notes), adminID)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if tag.RowsAffected() == 0 {
		return fiber.ErrNotFound
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (m *ProviderMarketplaceController) AdminUpsertPlan(c *fiber.Ctx) error {
	var req struct {
		Code              string         `json:"code"`
		Name              string         `json:"name"`
		Description       string         `json:"description"`
		AmountNGN         float64        `json:"amount_ngn"`
		ListingLimit      int            `json:"listing_limit"`
		FlutterwavePlanID *int64         `json:"flutterwave_plan_id"`
		Features          map[string]any `json:"features"`
		IsActive          *bool          `json:"is_active"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.ErrBadRequest
	}
	if strings.TrimSpace(req.Code) == "" || strings.TrimSpace(req.Name) == "" || req.AmountNGN <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "code, name, and a positive amount_ngn are required")
	}
	if req.ListingLimit < 1 {
		req.ListingLimit = 20
	}
	features, _ := json.Marshal(req.Features)
	active := true
	if req.IsActive != nil {
		active = *req.IsActive
	}
	var id string
	err := m.db.QueryRow(c.Context(), `INSERT INTO provider_subscription_plans(code,name,description,amount_ngn,listing_limit,flutterwave_plan_id,features,is_active) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8) ON CONFLICT(code) DO UPDATE SET name=EXCLUDED.name,description=EXCLUDED.description,amount_ngn=EXCLUDED.amount_ngn,listing_limit=EXCLUDED.listing_limit,flutterwave_plan_id=EXCLUDED.flutterwave_plan_id,features=EXCLUDED.features,is_active=EXCLUDED.is_active,updated_at=now() RETURNING id::text`, marketplaceSlug(req.Code), strings.TrimSpace(req.Name), strings.TrimSpace(req.Description), req.AmountNGN, req.ListingLimit, req.FlutterwavePlanID, features, active).Scan(&id)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	return c.JSON(fiber.Map{"id": id})
}

func parseNumericID(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	default:
		return fmt.Sprint(v)
	}
}
