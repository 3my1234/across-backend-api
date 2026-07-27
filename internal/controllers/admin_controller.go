package controllers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"across/backend/internal/auth"
	"across/backend/internal/config"
	"across/backend/internal/storage"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type AdminController struct {
	db  *pgxpool.Pool
	cfg config.Config
	s3  *storage.S3
}

func NewAdminController(db *pgxpool.Pool, cfg config.Config) *AdminController {
	return &AdminController{db: db, cfg: cfg, s3: storage.NewS3(cfg)}
}

func (a *AdminController) Login(c *fiber.Ctx) error {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	var adminID, fullName, passwordHash, role string
	err := a.db.QueryRow(c.Context(), `
		SELECT id, full_name, password_hash, role
		FROM admins
		WHERE email = $1 AND is_active = true
	`, email).Scan(&adminID, &fullName, &passwordHash, &role)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)) != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "invalid admin credentials")
	}
	role = normalizeAdminRole(role)
	token, expiresAt, err := auth.Sign(adminID, a.cfg.JWTSecret, 12*time.Hour)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"access_token": token, "expires_at": expiresAt, "admin_id": adminID, "full_name": fullName, "role": role})
}

// Session validates the persisted admin token and returns the current server-side
// identity. The role is deliberately read from the database on every restore so
// deactivated accounts and role changes take effect without waiting for JWT expiry.
func (a *AdminController) Session(c *fiber.Ctx) error {
	adminID, ok := c.Locals("admin_id").(string)
	if !ok || adminID == "" {
		return fiber.NewError(fiber.StatusUnauthorized, "admin session unavailable")
	}

	var fullName, role string
	if err := a.db.QueryRow(c.Context(), `
		SELECT full_name, role
		FROM admins
		WHERE id = $1 AND is_active = true
	`, adminID).Scan(&fullName, &role); err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "admin account unavailable")
	}
	role = normalizeAdminRole(role)

	return c.JSON(fiber.Map{
		"admin_id":  adminID,
		"full_name": fullName,
		"role":      role,
	})
}

func (a *AdminController) Overview(c *fiber.Ctx) error {
	role, _ := c.Locals("admin_role").(string)
	if role == "procurement_admin" || role == "courier_admin" {
		var batches int64
		if err := a.db.QueryRow(c.Context(), `SELECT COUNT(*) FROM order_batches`).Scan(&batches); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "overview unavailable")
		}
		return c.JSON(fiber.Map{"batch_count": batches})
	}

	var orders, transactions, manifest int64
	if err := a.db.QueryRow(c.Context(), `
		SELECT
			(SELECT COUNT(*) FROM orders),
			(SELECT COUNT(*) FROM orders WHERE paid_at IS NOT NULL),
			(SELECT COUNT(*) FROM order_items oi
			 JOIN orders o ON o.id = oi.order_id
			 WHERE o.order_status IN ('Paid', 'Shipped'))
	`).Scan(&orders, &transactions, &manifest); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "overview unavailable")
	}
	return c.JSON(fiber.Map{
		"order_count":       orders,
		"transaction_count": transactions,
		"manifest_count":    manifest,
	})
}

func (a *AdminController) CreateAdmin(c *fiber.Ctx) error {
	var req struct {
		Email    string `json:"email"`
		FullName string `json:"full_name"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.FullName = strings.TrimSpace(req.FullName)
	req.Role = normalizeAdminRole(req.Role)
	if req.Role == "" {
		req.Role = "catalog_admin"
	}
	if req.Email == "" || req.FullName == "" || len(req.Password) < 10 || !isValidAdminRole(req.Role) {
		return fiber.NewError(fiber.StatusBadRequest, "email, name, 10+ character password, and valid role are required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	var id string
	err = a.db.QueryRow(c.Context(), `
		INSERT INTO admins(email, full_name, password_hash, role)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, req.Email, req.FullName, string(hash), req.Role).Scan(&id)
	if err != nil {
		return fiber.NewError(fiber.StatusConflict, "admin already exists")
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id, "email": req.Email, "role": req.Role})
}

func (a *AdminController) ListAdmins(c *fiber.Ctx) error {
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	var cursorID any
	if page.CursorTime != nil {
		cursorID = page.CursorID
	}
	rows, err := a.db.Query(c.Context(), `
		SELECT id, email, full_name, role, is_active, created_at, updated_at
			, COUNT(*) OVER() AS total_count
		FROM admins
		WHERE ($1 = '' OR email ILIKE '%' || $1 || '%' OR full_name ILIKE '%' || $1 || '%' OR role ILIKE '%' || $1 || '%')
		  AND ($2::timestamptz IS NULL OR (created_at, id) < ($2, $3::uuid))
		ORDER BY created_at DESC, id DESC
		LIMIT $4
	`, page.Search, page.CursorTime, cursorID, page.Limit+1)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "admins unavailable")
	}
	defer rows.Close()

	admins := make([]fiber.Map, 0)
	var total int64
	for rows.Next() {
		var id, email, fullName, role string
		var isActive bool
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &email, &fullName, &role, &isActive, &createdAt, &updatedAt, &total); err != nil {
			return err
		}
		admins = append(admins, fiber.Map{
			"id":         id,
			"email":      email,
			"full_name":  fullName,
			"role":       role,
			"is_active":  isActive,
			"created_at": createdAt,
			"updated_at": updatedAt,
		})
	}
	nextCursor := ""
	if len(admins) > page.Limit {
		admins = admins[:page.Limit]
		last := admins[len(admins)-1]
		nextCursor = encodeAdminCursor(last["created_at"].(time.Time), last["id"].(string))
	}
	return c.JSON(fiber.Map{"admins": admins, "page": adminPageMeta(page, total, len(admins), nextCursor)})
}

func (a *AdminController) ListUsers(c *fiber.Ctx) error {
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	var cursorID any
	if page.CursorTime != nil {
		cursorID = page.CursorID
	}
	rows, err := a.db.Query(c.Context(), `
		SELECT u.id, u.full_name, u.email, u.phone, cc.country_code, cc.currency_code, u.is_active, u.created_at, u.updated_at
			, COUNT(*) OVER() AS total_count
		FROM users u
		JOIN countries_config cc ON cc.id = u.country_id
		WHERE ($1 = '' OR u.email ILIKE '%' || $1 || '%' OR u.full_name ILIKE '%' || $1 || '%' OR u.phone ILIKE '%' || $1 || '%')
		  AND ($2::timestamptz IS NULL OR (u.created_at, u.id) < ($2, $3::uuid))
		ORDER BY u.created_at DESC, u.id DESC
		LIMIT $4
	`, page.Search, page.CursorTime, cursorID, page.Limit+1)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "users unavailable")
	}
	defer rows.Close()

	users := make([]fiber.Map, 0)
	var total int64
	for rows.Next() {
		var id, fullName, countryCode, currencyCode string
		var email, phone sql.NullString
		var isActive bool
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &fullName, &email, &phone, &countryCode, &currencyCode, &isActive, &createdAt, &updatedAt, &total); err != nil {
			return err
		}
		users = append(users, fiber.Map{
			"id":            id,
			"full_name":     fullName,
			"email":         nullableString(email),
			"phone":         nullableString(phone),
			"country_code":  countryCode,
			"currency_code": currencyCode,
			"is_active":     isActive,
			"created_at":    createdAt,
			"updated_at":    updatedAt,
		})
	}
	nextCursor := ""
	if len(users) > page.Limit {
		users = users[:page.Limit]
		last := users[len(users)-1]
		nextCursor = encodeAdminCursor(last["created_at"].(time.Time), last["id"].(string))
	}
	return c.JSON(fiber.Map{"users": users, "page": adminPageMeta(page, total, len(users), nextCursor)})
}

func (a *AdminController) DeleteUser(c *fiber.Ctx) error {
	userID := c.Params("user_id")
	tx, err := a.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())

	if _, err := tx.Exec(c.Context(), `DELETE FROM orders WHERE user_id = $1`, userID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to delete user orders")
	}
	if _, err := tx.Exec(c.Context(), `DELETE FROM reviews WHERE user_id = $1`, userID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to delete user reviews")
	}
	tag, err := tx.Exec(c.Context(), `DELETE FROM users WHERE id = $1`, userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to delete user")
	}
	if tag.RowsAffected() == 0 {
		return fiber.NewError(fiber.StatusNotFound, "user not found")
	}
	if err := tx.Commit(c.Context()); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (a *AdminController) ResetAdminPassword(c *fiber.Ctx) error {
	adminID := c.Params("admin_id")
	var req struct {
		Password string `json:"password"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	req.Password = strings.TrimSpace(req.Password)
	if len(req.Password) < 10 {
		return fiber.NewError(fiber.StatusBadRequest, "password must be at least 10 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	tag, err := a.db.Exec(c.Context(), `
		UPDATE admins
		SET password_hash = $2, updated_at = now()
		WHERE id = $1
	`, adminID, string(hash))
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to reset password")
	}
	if tag.RowsAffected() == 0 {
		return fiber.NewError(fiber.StatusNotFound, "admin not found")
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (a *AdminController) DeleteAdmin(c *fiber.Ctx) error {
	adminID := c.Params("admin_id")
	currentAdminID, _ := c.Locals("admin_id").(string)
	if currentAdminID != "" && currentAdminID == adminID {
		return fiber.NewError(fiber.StatusBadRequest, "you cannot delete your own account")
	}

	var targetRole string
	if err := a.db.QueryRow(c.Context(), `SELECT role FROM admins WHERE id = $1`, adminID).Scan(&targetRole); err != nil {
		return fiber.NewError(fiber.StatusNotFound, "admin not found")
	}

	if targetRole == "super_admin" {
		var activeSuperAdmins int
		if err := a.db.QueryRow(c.Context(), `
			SELECT COUNT(*)::int
			FROM admins
			WHERE role = 'super_admin' AND is_active = true
		`).Scan(&activeSuperAdmins); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "unable to verify super admin count")
		}
		if activeSuperAdmins <= 1 {
			return fiber.NewError(fiber.StatusConflict, "at least one super admin must remain active")
		}
	}

	tag, err := a.db.Exec(c.Context(), `DELETE FROM admins WHERE id = $1`, adminID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to delete admin")
	}
	if tag.RowsAffected() == 0 {
		return fiber.NewError(fiber.StatusNotFound, "admin not found")
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func nullableString(value sql.NullString) any {
	if value.Valid {
		return value.String
	}
	return nil
}

func (a *AdminController) ListOrders(c *fiber.Ctx) error {
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	var cursorID any
	if page.CursorTime != nil {
		cursorID = page.CursorID
	}
	rows, err := a.db.Query(c.Context(), `
		SELECT o.id, u.email, o.currency_code, o.total_amount, o.shipping_fee,
			COALESCE(o.customs_fee, 0), COALESCE(o.vat_fee, 0),
			o.order_status, o.current_tracking_stage, o.created_at,
			COUNT(*) OVER() AS total_count
		FROM orders o
		JOIN users u ON u.id = o.user_id
		WHERE ($1 = '' OR u.email ILIKE '%' || $1 || '%' OR o.id::text = $1
			OR o.order_status::text ILIKE '%' || $1 || '%' OR o.current_tracking_stage::text ILIKE '%' || $1 || '%')
		  AND ($2::timestamptz IS NULL OR (o.created_at, o.id) < ($2, $3::uuid))
		ORDER BY o.created_at DESC, o.id DESC
		LIMIT $4
	`, page.Search, page.CursorTime, cursorID, page.Limit+1)
	if err != nil {
		return err
	}
	defer rows.Close()
	orders := make([]fiber.Map, 0)
	var totalCount int64
	for rows.Next() {
		var id, email, currency, status, stage string
		var total, shipping, customs, vat float64
		var createdAt time.Time
		if err := rows.Scan(&id, &email, &currency, &total, &shipping, &customs, &vat, &status, &stage, &createdAt, &totalCount); err != nil {
			return err
		}
		orders = append(orders, fiber.Map{
			"id": id, "email": email, "currency": currency, "total_amount": total,
			"shipping_fee": shipping, "customs_fee": customs, "vat_fee": vat,
			"status": status, "stage": stage, "created_at": createdAt,
		})
	}
	nextCursor := ""
	if len(orders) > page.Limit {
		orders = orders[:page.Limit]
		last := orders[len(orders)-1]
		nextCursor = encodeAdminCursor(last["created_at"].(time.Time), last["id"].(string))
	}
	return c.JSON(fiber.Map{"orders": orders, "page": adminPageMeta(page, totalCount, len(orders), nextCursor)})
}

func (a *AdminController) ListTransactions(c *fiber.Ctx) error {
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	var cursorID any
	if page.CursorTime != nil {
		cursorID = page.CursorID
	}
	rows, err := a.db.Query(c.Context(), `
		SELECT o.id, u.email, o.total_amount, o.currency_code, o.order_status::text,
			COALESCE(o.flutterwave_tx_ref, ''),
			COALESCE(o.flutterwave_transaction_id, ''),
			o.paid_at, o.created_at, COUNT(*) OVER() AS total_count
		FROM orders o
		JOIN users u ON u.id = o.user_id
		WHERE ($1 = '' OR u.email ILIKE '%' || $1 || '%' OR o.id::text = $1
			OR o.flutterwave_tx_ref ILIKE '%' || $1 || '%' OR o.flutterwave_transaction_id ILIKE '%' || $1 || '%')
		  AND ($2::timestamptz IS NULL OR (o.created_at, o.id) < ($2, $3::uuid))
		ORDER BY o.created_at DESC, o.id DESC
		LIMIT $4
	`, page.Search, page.CursorTime, cursorID, page.Limit+1)
	if err != nil {
		return err
	}
	defer rows.Close()
	transactions := make([]fiber.Map, 0)
	var totalCount int64
	for rows.Next() {
		var id, email, currency, orderStatus, txRef, transactionID string
		var total float64
		var paidAt *time.Time
		var createdAt time.Time
		if err := rows.Scan(&id, &email, &total, &currency, &orderStatus, &txRef, &transactionID, &paidAt, &createdAt, &totalCount); err != nil {
			return err
		}
		paymentStatus := "pending"
		if paidAt != nil {
			paymentStatus = "settled"
		}
		transactions = append(transactions, fiber.Map{
			"id": id, "order_id": id, "email": email, "total_amount": total, "currency": currency,
			"order_status": orderStatus, "payment_status": paymentStatus,
			"flutterwave_tx_ref": txRef, "flutterwave_transaction_id": transactionID,
			"paid_at": paidAt, "created_at": createdAt,
		})
	}
	nextCursor := ""
	if len(transactions) > page.Limit {
		transactions = transactions[:page.Limit]
		last := transactions[len(transactions)-1]
		nextCursor = encodeAdminCursor(last["created_at"].(time.Time), last["id"].(string))
	}
	return c.JSON(fiber.Map{"transactions": transactions, "page": adminPageMeta(page, totalCount, len(transactions), nextCursor)})
}
func (a *AdminController) CreateProduct(c *fiber.Ctx) error {
	var req struct {
		SKU                  string         `json:"sku"`
		Title                string         `json:"title"`
		Description          string         `json:"description"`
		CategoryPath         []string       `json:"category_path"`
		Variants             map[string]any `json:"variants"`
		ImageURLs            []string       `json:"image_urls"`
		CostPriceRMB         float64        `json:"cost_price_rmb"`
		LocalSellingPrice    float64        `json:"local_selling_price"`
		CompareAtPrice       float64        `json:"compare_at_price"`
		ExchangeRateSnapshot float64        `json:"exchange_rate_snapshot"`
		InventoryCount       int            `json:"inventory_count"`
		FactoryName          string         `json:"factory_name"`
		FactoryContact       string         `json:"factory_contact"`
		FactoryLocation      string         `json:"factory_location"`
		OriginHubID          *string        `json:"origin_hub_id"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	req.SKU = strings.TrimSpace(req.SKU)
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" || req.LocalSellingPrice <= 0 || req.ExchangeRateSnapshot <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "title, price, and exchange rate are required")
	}
	if req.CategoryPath == nil {
		req.CategoryPath = []string{}
	}
	if req.SKU == "" {
		sku, err := a.generateSequentialSKU(c, req.CategoryPath)
		if err != nil {
			return err
		}
		req.SKU = sku
	}
	if req.ImageURLs == nil {
		req.ImageURLs = []string{}
	}
	variants := []byte("[]")
	if req.Variants != nil {
		raw, err := json.Marshal(req.Variants)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid variants")
		}
		variants = raw
	}
	factory := fiber.Map{
		"name": req.FactoryName, "contact": req.FactoryContact, "location": req.FactoryLocation,
	}
	factoryJSON, _ := json.Marshal(factory)

	var id string
	err := a.db.QueryRow(c.Context(), `
		INSERT INTO products(
			origin_hub_id, sku, title, description, category_path, variants, image_urls,
			cost_price_rmb, local_selling_price, compare_at_price, exchange_rate_snapshot,
			inventory_count, factory_details
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, 0), $11, $12, $13)
		RETURNING id
	`, req.OriginHubID, req.SKU, req.Title, req.Description, req.CategoryPath, variants, req.ImageURLs,
		req.CostPriceRMB, req.LocalSellingPrice, req.CompareAtPrice, req.ExchangeRateSnapshot,
		req.InventoryCount, factoryJSON).Scan(&id)
	if err != nil {
		log.Printf("create product failed: sku=%s title=%q category=%v price=%.2f compare_at=%.2f cost_rmb=%.2f exchange_rate=%.2f inventory=%d err=%v",
			req.SKU, req.Title, req.CategoryPath, req.LocalSellingPrice, req.CompareAtPrice, req.CostPriceRMB, req.ExchangeRateSnapshot, req.InventoryCount, err)
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	log.Printf("create product succeeded: id=%s sku=%s title=%q", id, req.SKU, req.Title)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id, "sku": req.SKU})
}

func (a *AdminController) ListProducts(c *fiber.Ctx) error {
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	var cursorID any
	if page.CursorTime != nil {
		cursorID = page.CursorID
	}
	rows, err := a.db.Query(c.Context(), `
		SELECT p.id, p.sku, p.title, p.description, p.category_path, p.image_urls,
			p.local_currency_code, p.local_selling_price, COALESCE(p.compare_at_price, 0),
			p.inventory_count, p.is_active, p.created_at, p.updated_at,
			COUNT(*) OVER() AS total_count
		FROM products p
		WHERE ($1 = '' OR p.sku ILIKE '%' || $1 || '%' OR p.title ILIKE '%' || $1 || '%'
			OR p.description ILIKE '%' || $1 || '%' OR array_to_string(p.category_path, ' ') ILIKE '%' || $1 || '%')
		  AND ($2::timestamptz IS NULL OR (p.created_at, p.id) < ($2, $3::uuid))
		ORDER BY p.created_at DESC, p.id DESC
		LIMIT $4
	`, page.Search, page.CursorTime, cursorID, page.Limit+1)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "products unavailable")
	}
	defer rows.Close()

	products := make([]fiber.Map, 0)
	var totalCount int64
	for rows.Next() {
		var id, sku, title, description, currency string
		var categories, images []string
		var price, compareAtPrice float64
		var inventory int
		var isActive bool
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &sku, &title, &description, &categories, &images, &currency, &price, &compareAtPrice, &inventory, &isActive, &createdAt, &updatedAt, &totalCount); err != nil {
			return err
		}
		products = append(products, fiber.Map{
			"id":                  id,
			"sku":                 sku,
			"title":               title,
			"description":         description,
			"category_path":       categories,
			"image_urls":          images,
			"currency":            currency,
			"local_selling_price": price,
			"compare_at_price":    compareAtPrice,
			"inventory_count":     inventory,
			"is_active":           isActive,
			"created_at":          createdAt,
			"updated_at":          updatedAt,
		})
	}
	nextCursor := ""
	if len(products) > page.Limit {
		products = products[:page.Limit]
		last := products[len(products)-1]
		nextCursor = encodeAdminCursor(last["created_at"].(time.Time), last["id"].(string))
	}
	return c.JSON(fiber.Map{"products": products, "page": adminPageMeta(page, totalCount, len(products), nextCursor)})
}

func (a *AdminController) UpdateProduct(c *fiber.Ctx) error {
	productID := c.Params("product_id")
	var req struct {
		Title             *string  `json:"title"`
		Description       *string  `json:"description"`
		LocalSellingPrice *float64 `json:"local_selling_price"`
		CompareAtPrice    *float64 `json:"compare_at_price"`
		InventoryCount    *int     `json:"inventory_count"`
		IsActive          *bool    `json:"is_active"`
		ImageURLs         []string `json:"image_urls"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	if req.Title == nil && req.Description == nil && req.LocalSellingPrice == nil && req.CompareAtPrice == nil && req.InventoryCount == nil && req.IsActive == nil && req.ImageURLs == nil {
		return fiber.NewError(fiber.StatusBadRequest, "no fields to update")
	}
	if req.LocalSellingPrice != nil && *req.LocalSellingPrice <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "price must be greater than zero")
	}
	if req.InventoryCount != nil && *req.InventoryCount < 0 {
		return fiber.NewError(fiber.StatusBadRequest, "inventory cannot be negative")
	}
	if req.Title != nil {
		trimmed := strings.TrimSpace(*req.Title)
		if trimmed == "" {
			return fiber.NewError(fiber.StatusBadRequest, "title cannot be empty")
		}
		req.Title = &trimmed
	}
	if req.ImageURLs != nil {
		req.ImageURLs = cleanStringList(req.ImageURLs)
	}

	var oldImageURLs []string
	if req.ImageURLs != nil {
		if err := a.db.QueryRow(c.Context(), `SELECT COALESCE(image_urls, '{}') FROM products WHERE id = $1`, productID).Scan(&oldImageURLs); err != nil {
			return fiber.NewError(fiber.StatusNotFound, "product not found")
		}
	}

	tag, err := a.db.Exec(c.Context(), `
		UPDATE products
		SET title = COALESCE($2, title),
			description = COALESCE($3, description),
			local_selling_price = COALESCE($4, local_selling_price),
			compare_at_price = CASE WHEN $5::numeric IS NULL THEN compare_at_price ELSE NULLIF($5, 0) END,
			inventory_count = COALESCE($6, inventory_count),
			is_active = COALESCE($7, is_active),
			image_urls = COALESCE($8, image_urls),
			updated_at = now()
		WHERE id = $1
	`, productID, req.Title, req.Description, req.LocalSellingPrice, req.CompareAtPrice, req.InventoryCount, req.IsActive, req.ImageURLs)
	if err != nil {
		log.Printf("update product failed: id=%s err=%v", productID, err)
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	if tag.RowsAffected() == 0 {
		return fiber.NewError(fiber.StatusNotFound, "product not found")
	}
	s3Failures := []string{}
	if req.ImageURLs != nil && a.s3.Configured() {
		s3Failures = a.s3.DeleteObjectsForURLs(removedStrings(oldImageURLs, req.ImageURLs))
	}
	log.Printf("update product succeeded: id=%s s3_failures=%v", productID, s3Failures)
	return c.JSON(fiber.Map{"id": productID, "updated": true, "s3_failures": s3Failures})
}

func (a *AdminController) DeleteProduct(c *fiber.Ctx) error {
	productID := c.Params("product_id")
	var imageURLs []string
	var orderItemCount int
	err := a.db.QueryRow(c.Context(), `
		SELECT COALESCE(p.image_urls, '{}'), (
			SELECT COUNT(*)::int FROM order_items oi WHERE oi.product_id = p.id
		)
		FROM products p
		WHERE p.id = $1
	`, productID).Scan(&imageURLs, &orderItemCount)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "product not found")
	}
	if orderItemCount > 0 {
		return fiber.NewError(fiber.StatusConflict, "product has order history and cannot be permanently deleted")
	}

	mediaURLs := append([]string{}, imageURLs...)
	rows, err := a.db.Query(c.Context(), `SELECT media_urls FROM reviews WHERE product_id = $1`, productID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var reviewMedia []string
			if err := rows.Scan(&reviewMedia); err == nil {
				mediaURLs = append(mediaURLs, reviewMedia...)
			}
		}
	}

	tag, err := a.db.Exec(c.Context(), `DELETE FROM products WHERE id = $1`, productID)
	if err != nil {
		return fiber.NewError(fiber.StatusConflict, "product could not be deleted")
	}
	if tag.RowsAffected() == 0 {
		return fiber.NewError(fiber.StatusNotFound, "product not found")
	}

	s3Failures := []string{}
	if a.s3.Configured() {
		s3Failures = a.s3.DeleteObjectsForURLs(mediaURLs)
	}

	return c.JSON(fiber.Map{
		"id":          productID,
		"deleted":     true,
		"s3_failures": s3Failures,
	})
}

func (a *AdminController) ListBatches(c *fiber.Ctx) error {
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	var cursorID any
	if page.CursorTime != nil {
		cursorID = page.CursorID
	}
	rows, err := a.db.Query(c.Context(), `
		SELECT b.id, b.batch_code, b.batch_date, b.status::text, b.transport_mode,
			b.total_ngn_collected, b.total_cny_sent, b.current_location, b.notes,
			COUNT(o.id)::int AS order_count, b.created_at,
			COUNT(*) OVER() AS total_count
		FROM order_batches b
		LEFT JOIN orders o ON o.batch_id = b.id
		WHERE ($1 = '' OR b.batch_code ILIKE '%' || $1 || '%' OR b.status::text ILIKE '%' || $1 || '%'
			OR b.transport_mode ILIKE '%' || $1 || '%' OR b.current_location ILIKE '%' || $1 || '%')
		  AND ($2::timestamptz IS NULL OR (b.created_at, b.id) < ($2, $3::uuid))
		GROUP BY b.id
		ORDER BY b.created_at DESC, b.id DESC
		LIMIT $4
	`, page.Search, page.CursorTime, cursorID, page.Limit+1)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "batches unavailable")
	}
	defer rows.Close()

	batches := make([]fiber.Map, 0)
	var totalCount int64
	for rows.Next() {
		var id, code, status, transport, location, notes string
		var batchDate time.Time
		var totalNgn, totalCny float64
		var orderCount int
		var createdAt time.Time
		if err := rows.Scan(&id, &code, &batchDate, &status, &transport, &totalNgn, &totalCny, &location, &notes, &orderCount, &createdAt, &totalCount); err != nil {
			return err
		}
		batches = append(batches, fiber.Map{
			"id":                  id,
			"batch_code":          code,
			"batch_date":          batchDate,
			"status":              status,
			"transport_mode":      transport,
			"total_ngn_collected": totalNgn,
			"total_cny_sent":      totalCny,
			"current_location":    location,
			"notes":               notes,
			"order_count":         orderCount,
			"created_at":          createdAt,
		})
	}
	nextCursor := ""
	if len(batches) > page.Limit {
		batches = batches[:page.Limit]
		last := batches[len(batches)-1]
		nextCursor = encodeAdminCursor(last["created_at"].(time.Time), last["id"].(string))
	}
	return c.JSON(fiber.Map{"batches": batches, "page": adminPageMeta(page, totalCount, len(batches), nextCursor)})
}

func (a *AdminController) ListBatchOrders(c *fiber.Ctx) error {
	batchID := c.Params("batch_id")
	deliverableOnly := c.QueryBool("deliverable", false)
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	var cursorID any
	if page.CursorTime != nil {
		cursorID = page.CursorID
	}
	rows, err := a.db.Query(c.Context(), `
		SELECT o.id, u.email, o.package_label, o.order_status, o.current_tracking_stage,
			o.currency_code, o.total_amount, o.customs_fee, o.vat_fee, o.created_at,
			COUNT(*) OVER() AS total_count
		FROM orders o
		JOIN users u ON u.id = o.user_id
		WHERE o.batch_id = $1
		  AND ($2 = '' OR u.email ILIKE '%' || $2 || '%' OR o.package_label ILIKE '%' || $2 || '%'
			OR o.order_status::text ILIKE '%' || $2 || '%' OR o.current_tracking_stage::text ILIKE '%' || $2 || '%')
		  AND ($3::timestamptz IS NULL OR (o.created_at, o.id) < ($3, $4::uuid))
		  AND (NOT $5 OR o.current_tracking_stage::text NOT IN ('Delivered', 'Completed'))
		ORDER BY o.created_at DESC, o.id DESC
		LIMIT $6
	`, batchID, page.Search, page.CursorTime, cursorID, deliverableOnly, page.Limit+1)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "batch orders unavailable")
	}
	defer rows.Close()

	orders := make([]fiber.Map, 0)
	var totalCount int64
	for rows.Next() {
		var id, email, packageLabel, status, stage, currency string
		var total, customs, vat float64
		var createdAt time.Time
		if err := rows.Scan(&id, &email, &packageLabel, &status, &stage, &currency, &total, &customs, &vat, &createdAt, &totalCount); err != nil {
			return err
		}
		orders = append(orders, fiber.Map{
			"id":            id,
			"email":         email,
			"package_label": packageLabel,
			"status":        status,
			"stage":         stage,
			"currency":      currency,
			"total_amount":  total,
			"customs_fee":   customs,
			"vat_fee":       vat,
			"created_at":    createdAt,
		})
	}
	nextCursor := ""
	if len(orders) > page.Limit {
		orders = orders[:page.Limit]
		last := orders[len(orders)-1]
		nextCursor = encodeAdminCursor(last["created_at"].(time.Time), last["id"].(string))
	}
	return c.JSON(fiber.Map{"orders": orders, "page": adminPageMeta(page, totalCount, len(orders), nextCursor)})
}

func (a *AdminController) UpdateBatch(c *fiber.Ctx) error {
	batchID := c.Params("batch_id")
	var req struct {
		Status          *string `json:"status"`
		TransportMode   *string `json:"transport_mode"`
		CurrentLocation *string `json:"current_location"`
		Notes           *string `json:"notes"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	if req.Status == nil && req.TransportMode == nil && req.CurrentLocation == nil && req.Notes == nil {
		return fiber.NewError(fiber.StatusBadRequest, "no fields to update")
	}

	tx, err := a.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())

	var statusValue, transportValue, locationValue, notesValue *string
	if req.Status != nil {
		normalized := normalizeBatchStatus(*req.Status)
		if normalized == "" {
			return fiber.NewError(fiber.StatusBadRequest, "invalid batch status")
		}
		statusValue = &normalized
	}
	if req.TransportMode != nil {
		normalized := strings.ToLower(strings.TrimSpace(*req.TransportMode))
		if normalized != "air" && normalized != "sea" {
			return fiber.NewError(fiber.StatusBadRequest, "invalid transport mode")
		}
		transportValue = &normalized
	}
	if req.CurrentLocation != nil {
		trimmed := strings.TrimSpace(*req.CurrentLocation)
		locationValue = &trimmed
	}
	if req.Notes != nil {
		trimmed := strings.TrimSpace(*req.Notes)
		notesValue = &trimmed
	}

	var updatedStatus string
	if err := tx.QueryRow(c.Context(), `
		UPDATE order_batches
		SET status = COALESCE($2, status)::batch_status,
			transport_mode = COALESCE($3, transport_mode),
			current_location = COALESCE($4, current_location),
			notes = COALESCE($5, notes),
			updated_at = now()
		WHERE id = $1
		RETURNING status::text
	`, batchID, statusValue, transportValue, locationValue, notesValue).Scan(&updatedStatus); err != nil {
		return fiber.NewError(fiber.StatusNotFound, "batch not found")
	}

	if err := syncBatchOrders(c.Context(), tx, batchID, updatedStatus); err != nil {
		return err
	}
	if _, err := tx.Exec(c.Context(), `
		INSERT INTO batch_events(batch_id, event_type, status, location, notes)
		VALUES ($1, 'status_update', $2::batch_status, COALESCE($3, ''), COALESCE($4, ''))
	`, batchID, updatedStatus, req.CurrentLocation, req.Notes); err != nil {
		return err
	}

	if err := tx.Commit(c.Context()); err != nil {
		return err
	}

	// Send notifications to buyers based on new batch status
	go a.sendBatchNotifications(batchID, updatedStatus, req.CurrentLocation)

	return c.JSON(fiber.Map{"batch_id": batchID, "status": updatedStatus, "updated": true})
}

var skuCleaner = regexp.MustCompile(`[^A-Z0-9]+`)

func categoryCodeForSKU(categories []string) string {
	// Keep codes stable and explicit (matches admin panel category list).
	if len(categories) > 0 {
		switch strings.TrimSpace(strings.ToLower(categories[0])) {
		case "male clothes":
			return "MCL"
		case "female clothes":
			return "FCL"
		case "mobile devices":
			return "MOB"
		case "laptops":
			return "LAP"
		case "accessories":
			return "ACC"
		case "shoes":
			return "SHO"
		case "beauty":
			return "BEA"
		case "home":
			return "HOM"
		case "electronics":
			return "ELC"
		case "kids":
			return "KID"
		}
	}
	return "PRD"
}

func (a *AdminController) generateSequentialSKU(c *fiber.Ctx, categories []string) (string, error) {
	code := categoryCodeForSKU(categories)

	tx, err := a.db.Begin(c.Context())
	if err != nil {
		return "", err
	}
	defer tx.Rollback(c.Context())

	var nextValue int
	// Atomic counter allocation:
	// - first time: next_value becomes 2 (we allocate 1)
	// - subsequent: increments by 1 and returns the new next_value
	if err := tx.QueryRow(c.Context(), `
		INSERT INTO sku_counters(category_code, next_value)
		VALUES ($1, 2)
		ON CONFLICT (category_code)
		DO UPDATE SET next_value = sku_counters.next_value + 1, updated_at = now()
		RETURNING next_value
	`, code).Scan(&nextValue); err != nil {
		return "", fiber.NewError(fiber.StatusInternalServerError, "sku counter unavailable")
	}

	allocated := nextValue - 1
	sku := fmt.Sprintf("%s-%06d", code, allocated)

	if err := tx.Commit(c.Context()); err != nil {
		return "", err
	}
	return sku, nil
}

func abbreviateSKU(value string, max int) string {
	clean := strings.Trim(skuCleaner.ReplaceAllString(strings.ToUpper(value), "-"), "-")
	if clean == "" {
		return "ITEM"
	}
	parts := strings.Split(clean, "-")
	out := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		out += part[:1]
		if len(out) >= max {
			return out[:max]
		}
	}
	if len(clean) <= max {
		return clean
	}
	return clean[:max]
}

func cleanStringList(values []string) []string {
	cleaned := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		item := strings.TrimSpace(value)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		cleaned = append(cleaned, item)
	}
	return cleaned
}

func removedStrings(before, after []string) []string {
	kept := make(map[string]struct{}, len(after))
	for _, value := range after {
		kept[strings.TrimSpace(value)] = struct{}{}
	}
	removed := make([]string, 0)
	for _, value := range before {
		item := strings.TrimSpace(value)
		if item == "" {
			continue
		}
		if _, ok := kept[item]; !ok {
			removed = append(removed, item)
		}
	}
	return removed
}

func syncBatchOrders(ctx context.Context, tx pgx.Tx, batchID, status string) error {
	trackingStage := ""
	orderStatus := ""
	deliveryComplete := false
	switch status {
	case "funds_sent_to_china":
		trackingStage = "Arrived at China Hub"
	case "purchasing":
		trackingStage = "Arrived at China Hub"
	case "enroute_nigeria":
		trackingStage = "In Transit Internationally"
	case "arrived_local":
		trackingStage = "Arrived at Local Hub"
	case "sorted":
		trackingStage = "Out for Delivery"
	case "completed":
		trackingStage = "Delivered"
		orderStatus = "Completed"
		deliveryComplete = true
	default:
		return nil
	}

	if orderStatus != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE orders
			SET current_tracking_stage = $2::tracking_stage,
				order_status = CASE WHEN $3 = '' THEN order_status ELSE $3::order_status END,
				delivered_at = CASE WHEN $4 THEN now() ELSE delivered_at END,
				updated_at = now()
			WHERE batch_id = $1
		`, batchID, trackingStage, orderStatus, deliveryComplete); err != nil {
			return err
		}
		return nil
	}

	_, err := tx.Exec(ctx, `
		UPDATE orders
		SET current_tracking_stage = $2::tracking_stage,
			updated_at = now()
		WHERE batch_id = $1
	`, batchID, trackingStage)
	return err
}

func normalizeAdminRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", "admin", "admin_i", "catalog_admin":
		return "catalog_admin"
	case "super_admin":
		return "super_admin"
	case "admin_ii", "procurement_admin":
		return "procurement_admin"
	case "admin_iii", "courier_admin":
		return "courier_admin"
	default:
		return ""
	}
}

func isValidAdminRole(role string) bool {
	switch role {
	case "super_admin", "catalog_admin", "procurement_admin", "courier_admin":
		return true
	default:
		return false
	}
}

func normalizeBatchStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "collecting_funds":
		return "collecting_funds"
	case "settled":
		return "settled"
	case "funds_sent_to_china":
		return "funds_sent_to_china"
	case "purchasing":
		return "purchasing"
	case "enroute_nigeria":
		return "enroute_nigeria"
	case "arrived_local":
		return "arrived_local"
	case "sorted":
		return "sorted"
	case "completed":
		return "completed"
	default:
		return ""
	}
}

func (a *AdminController) PendingManifest(c *fiber.Ctx) error {
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	var cursorID any
	if page.CursorTime != nil {
		cursorID = page.CursorID
	}
	rows, err := a.db.Query(c.Context(), `
		SELECT o.id, o.order_status, o.current_tracking_stage, oi.sku, oi.title, oi.quantity, lh.code,
			oi.id, oi.created_at, COUNT(*) OVER() AS total_count
		FROM orders o
		JOIN order_items oi ON oi.order_id = o.id
		LEFT JOIN logistics_hubs lh ON lh.id = oi.origin_hub_id
		WHERE o.order_status IN ('Paid', 'Shipped')
		  AND ($1 = '' OR oi.sku ILIKE '%' || $1 || '%' OR oi.title ILIKE '%' || $1 || '%'
			OR o.id::text = $1 OR o.order_status::text ILIKE '%' || $1 || '%')
		  AND ($2::timestamptz IS NULL OR (oi.created_at, oi.id) < ($2, $3::uuid))
		ORDER BY oi.created_at DESC, oi.id DESC
		LIMIT $4
	`, page.Search, page.CursorTime, cursorID, page.Limit+1)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "manifest unavailable")
	}
	defer rows.Close()

	items := make([]fiber.Map, 0)
	var totalCount int64
	for rows.Next() {
		var orderID, status, stage, sku, title, itemID string
		var hubCode *string
		var qty int
		var createdAt time.Time
		if err := rows.Scan(&orderID, &status, &stage, &sku, &title, &qty, &hubCode, &itemID, &createdAt, &totalCount); err != nil {
			return err
		}
		items = append(items, fiber.Map{"id": itemID, "order_id": orderID, "status": status, "stage": stage, "sku": sku, "title": title, "quantity": qty, "hub_code": hubCode, "created_at": createdAt})
	}
	nextCursor := ""
	if len(items) > page.Limit {
		items = items[:page.Limit]
		last := items[len(items)-1]
		nextCursor = encodeAdminCursor(last["created_at"].(time.Time), last["id"].(string))
	}
	return c.JSON(fiber.Map{"items": items, "page": adminPageMeta(page, totalCount, len(items), nextCursor)})
}

func (a *AdminController) BatchScanTracking(c *fiber.Ctx) error {
	var req struct {
		OrderIDs []string `json:"order_ids"`
		Stage    string   `json:"stage"`
		HubID    *string  `json:"hub_id"`
		Barcode  *string  `json:"barcode"`
	}
	if err := c.BodyParser(&req); err != nil || len(req.OrderIDs) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	tx, err := a.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())
	for _, orderID := range req.OrderIDs {
		_, err := tx.Exec(c.Context(), `
			UPDATE orders
			SET current_tracking_stage = $2::tracking_stage,
				order_status = CASE WHEN $2 = 'Delivered' THEN 'Delivered' ELSE order_status END,
				updated_at = now()
			WHERE id = $1
		`, orderID, req.Stage)
		if err != nil {
			return err
		}
		_, err = tx.Exec(c.Context(), `
			INSERT INTO tracking_events(order_id, hub_id, stage, barcode)
			VALUES ($1, $2, $3::tracking_stage, $4)
		`, orderID, req.HubID, req.Stage, req.Barcode)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(c.Context()); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (a *AdminController) sendBatchNotifications(batchID, status string, location *string) {
	loc := ""
	if location != nil {
		loc = *location
	}
	ctx := context.Background()

	var notifyType, title, body string
	switch status {
	case "funds_sent_to_china":
		notifyType = "enroute_international"
		title = "Your package is on its way!"
		body = "Your order has been shipped from China. It's heading to Nigeria now!"
	case "purchasing":
		notifyType = "product_purchased"
		title = "Your order has been secured"
		body = "Your items have been purchased from the supplier. They're being prepared for shipping."
	case "enroute_nigeria":
		notifyType = "enroute_international"
		title = "In transit to Nigeria"
		body = "Your package is on a flight/ship heading to Nigeria. Estimated arrival in 7-14 days."
	case "arrived_local":
		notifyType = "arrived_local"
		title = "Package arrived in Nigeria"
		body = "Your order has arrived at our local hub. We're clearing it for delivery."
		if loc != "" {
			body = "Your order has arrived at " + loc + ". We're preparing it for delivery."
		}
	case "sorted":
		notifyType = "out_for_delivery"
		title = "Out for delivery"
		body = "Your package is with our courier and will be delivered soon."
		if loc != "" {
			body = "Your package is out for delivery from " + loc + "."
		}
	case "completed":
		notifyType = "delivered"
		title = "Package delivered"
		body = "Your order has been delivered. You can now review your purchase in the app."
	}

	if notifyType != "" {
		data := map[string]any{"location": loc}
		_ = notifyBatchUsers(ctx, a.db, batchID, notifyType, title, body, data)
	}
}
