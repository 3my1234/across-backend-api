package controllers

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type merchantProductPayload struct {
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	CategoryPath   []string `json:"category_path"`
	ImageURLs      []string `json:"image_urls"`
	Price          float64  `json:"local_selling_price"`
	CompareAtPrice *float64 `json:"compare_at_price"`
	FlashSalePrice *float64 `json:"flash_sale_price"`
	IsFlashSale    bool     `json:"is_flash_sale"`
	InventoryCount int      `json:"inventory_count"`
	MerchantSKU    string   `json:"sku"`
}

func validateMerchantProduct(req merchantProductPayload) error {
	if strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.Description) == "" {
		return fmt.Errorf("title and description are required")
	}
	if req.Price <= 0 || req.InventoryCount < 0 {
		return fmt.Errorf("price must be positive and inventory cannot be negative")
	}
	if len(req.ImageURLs) == 0 || len(req.ImageURLs) > 20 {
		return fmt.Errorf("between 1 and 20 product images are required")
	}
	for _, u := range req.ImageURLs {
		if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "/api/v1/public/images/view/") {
			return fmt.Errorf("product images must use approved HTTPS storage")
		}
	}
	if req.CompareAtPrice != nil && *req.CompareAtPrice > 0 && *req.CompareAtPrice <= req.Price {
		return fmt.Errorf("crossed-out price must be greater than the selling price")
	}
	if req.IsFlashSale && (req.FlashSalePrice == nil || *req.FlashSalePrice <= 0 || *req.FlashSalePrice >= req.Price) {
		return fmt.Errorf("flash sale price must be lower than the selling price")
	}
	return nil
}

func (m *ProviderMarketplaceController) CreateMerchantProduct(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, _, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusForbidden, "provider access required")
	}
	listingLimit, err := activeProviderPlan(c.Context(), m.db, providerID)
	if err == pgx.ErrNoRows {
		return fiber.NewError(fiber.StatusPaymentRequired, "an active provider subscription is required")
	} else if err != nil {
		return fiber.ErrInternalServerError
	}
	var productCount int
	if err = m.db.QueryRow(c.Context(), `SELECT COUNT(*) FROM products WHERE provider_id=$1::uuid AND moderation_status<>'archived'`, providerID).Scan(&productCount); err != nil {
		return fiber.ErrInternalServerError
	}
	if productCount >= listingLimit {
		return fiber.NewError(fiber.StatusPaymentRequired, "your current subscription product limit has been reached")
	}
	var req merchantProductPayload
	if c.BodyParser(&req) != nil {
		return fiber.ErrBadRequest
	}
	if err = validateMerchantProduct(req); err != nil {
		return fiber.NewError(fiber.StatusUnprocessableEntity, err.Error())
	}
	sku := strings.ToUpper(strings.TrimSpace(req.MerchantSKU))
	if sku == "" {
		sku = "MER-" + strings.ToUpper(strings.ReplaceAll(uuid.NewString()[:12], "-", ""))
	}
	id := uuid.New()
	factory, _ := json.Marshal(map[string]any{"merchant_local": true})
	err = m.db.QueryRow(c.Context(), `INSERT INTO products(id,provider_id,fulfillment_mode,moderation_status,sku,title,description,category_path,variants,image_urls,cost_price_rmb,local_selling_price,compare_at_price,exchange_rate_snapshot,inventory_count,factory_details,is_active,is_flash_sale,flash_sale_price) VALUES($1,$2::uuid,'merchant_local','draft',$3,$4,$5,$6,'[]'::jsonb,$7,0,$8,$9,1,$10,$11::jsonb,false,$12,CASE WHEN $12 THEN $13::numeric ELSE NULL END) RETURNING id`, id, providerID, sku, strings.TrimSpace(req.Title), strings.TrimSpace(req.Description), req.CategoryPath, req.ImageURLs, req.Price, req.CompareAtPrice, req.InventoryCount, factory, req.IsFlashSale, req.FlashSalePrice).Scan(&id)
	if err != nil {
		return fiber.NewError(fiber.StatusConflict, "could not create product; check that the SKU is unique")
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id, "sku": sku, "status": "draft"})
}

func (m *ProviderMarketplaceController) UpdateMerchantProduct(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, _, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.ErrForbidden
	}
	var req merchantProductPayload
	if c.BodyParser(&req) != nil {
		return fiber.ErrBadRequest
	}
	if err = validateMerchantProduct(req); err != nil {
		return fiber.NewError(fiber.StatusUnprocessableEntity, err.Error())
	}
	tag, err := m.db.Exec(c.Context(), `UPDATE products SET title=$3,description=$4,category_path=$5,image_urls=$6,local_selling_price=$7,compare_at_price=$8,inventory_count=$9,is_flash_sale=$10,flash_sale_price=CASE WHEN $10 THEN $11::numeric ELSE NULL END,moderation_status=CASE WHEN moderation_status IN ('pending','approved') THEN 'pending' ELSE moderation_status END,moderation_notes='',is_active=false,catalog_version=catalog_version+1,updated_at=now() WHERE id=$1::uuid AND provider_id=$2::uuid AND moderation_status<>'archived'`, c.Params("product_id"), providerID, strings.TrimSpace(req.Title), strings.TrimSpace(req.Description), req.CategoryPath, req.ImageURLs, req.Price, req.CompareAtPrice, req.InventoryCount, req.IsFlashSale, req.FlashSalePrice)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if tag.RowsAffected() == 0 {
		return fiber.ErrNotFound
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (m *ProviderMarketplaceController) SubmitMerchantProduct(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, _, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.ErrForbidden
	}
	var eligible bool
	err = m.db.QueryRow(c.Context(), `SELECT p.verification_status='approved' AND p.is_active AND EXISTS(SELECT 1 FROM provider_subscriptions s WHERE s.provider_id=p.id AND s.status='active' AND s.current_period_end>now()) FROM provider_organizations p WHERE p.id=$1::uuid`, providerID).Scan(&eligible)
	if err != nil || !eligible {
		return fiber.NewError(fiber.StatusPaymentRequired, "verified provider profile and active subscription are required")
	}
	tag, err := m.db.Exec(c.Context(), `UPDATE products SET moderation_status='pending',is_active=false,updated_at=now() WHERE id=$1::uuid AND provider_id=$2::uuid AND moderation_status IN ('draft','rejected')`, c.Params("product_id"), providerID)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if tag.RowsAffected() == 0 {
		return fiber.NewError(fiber.StatusConflict, "product cannot be submitted")
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (m *ProviderMarketplaceController) ArchiveMerchantProduct(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, _, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.ErrForbidden
	}
	tag, err := m.db.Exec(c.Context(), `UPDATE products SET moderation_status='archived',is_active=false,catalog_version=catalog_version+1,updated_at=now() WHERE id=$1::uuid AND provider_id=$2::uuid`, c.Params("product_id"), providerID)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if tag.RowsAffected() == 0 {
		return fiber.ErrNotFound
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (m *ProviderMarketplaceController) ListMyMerchantProducts(c *fiber.Ctx) error {
	return m.listMerchantProducts(c, false)
}
func (m *ProviderMarketplaceController) AdminListMerchantProducts(c *fiber.Ctx) error {
	return m.listMerchantProducts(c, true)
}
func (m *ProviderMarketplaceController) listMerchantProducts(c *fiber.Ctx, admin bool) error {
	providerID := ""
	if !admin {
		userID, _ := c.Locals("user_id").(string)
		var err error
		providerID, _, err = m.providerForUser(c.Context(), userID)
		if err != nil {
			return fiber.ErrForbidden
		}
	}
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	var cursorID any
	if page.CursorTime != nil {
		cursorID = page.CursorID
	}
	status := strings.ToLower(strings.TrimSpace(c.Query("status")))
	rows, err := m.db.Query(c.Context(), `SELECT p.id::text,p.provider_id::text,o.business_name,p.sku,p.title,p.description,p.category_path,p.image_urls,p.local_selling_price,p.compare_at_price,p.inventory_count,p.is_flash_sale,p.flash_sale_price,p.moderation_status,p.moderation_notes,p.is_active,p.created_at FROM products p JOIN provider_organizations o ON o.id=p.provider_id WHERE ($1 OR p.provider_id=NULLIF($2,'')::uuid) AND ($3='' OR p.moderation_status=$3) AND ($4='' OR p.sku ILIKE '%'||$4||'%' OR p.title ILIKE '%'||$4||'%') AND ($5::timestamptz IS NULL OR (p.created_at,p.id)<($5,$6::uuid)) ORDER BY p.created_at DESC,p.id DESC LIMIT $7`, admin, providerID, status, page.Search, page.CursorTime, cursorID, page.Limit+1)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer rows.Close()
	items := make([]fiber.Map, 0, page.Limit+1)
	for rows.Next() {
		var id, pid, business, sku, title, desc, moderation, notes string
		var cats, images []string
		var price float64
		var compare, flash *float64
		var stock int
		var isFlash, active bool
		var created time.Time
		if rows.Scan(&id, &pid, &business, &sku, &title, &desc, &cats, &images, &price, &compare, &stock, &isFlash, &flash, &moderation, &notes, &active, &created) != nil {
			return fiber.ErrInternalServerError
		}
		items = append(items, fiber.Map{"id": id, "provider_id": pid, "provider_name": business, "sku": sku, "title": title, "description": desc, "category_path": cats, "image_urls": images, "local_selling_price": price, "compare_at_price": compare, "inventory_count": stock, "is_flash_sale": isFlash, "flash_sale_price": flash, "moderation_status": moderation, "moderation_notes": notes, "is_active": active, "created_at": created})
	}
	next := ""
	if len(items) > page.Limit {
		items = items[:page.Limit]
		last := items[len(items)-1]
		next = encodeAdminCursor(last["created_at"].(time.Time), last["id"].(string))
	}
	return c.JSON(fiber.Map{"items": items, "page": fiber.Map{"limit": page.Limit, "next_cursor": next, "has_more": next != ""}})
}

func (m *ProviderMarketplaceController) AdminModerateMerchantProduct(c *fiber.Ctx) error {
	adminID, _ := c.Locals("admin_id").(string)
	var req struct {
		Status string `json:"status"`
		Notes  string `json:"notes"`
	}
	if c.BodyParser(&req) != nil {
		return fiber.ErrBadRequest
	}
	req.Status = strings.ToLower(strings.TrimSpace(req.Status))
	if req.Status != "approved" && req.Status != "rejected" && req.Status != "suspended" {
		return fiber.NewError(fiber.StatusUnprocessableEntity, "invalid moderation status")
	}
	tag, err := m.db.Exec(c.Context(), `UPDATE products SET moderation_status=$2,moderation_notes=$3,moderated_by=$4::uuid,moderated_at=now(),published_at=CASE WHEN $2='approved' THEN COALESCE(published_at,now()) ELSE published_at END,is_active=($2='approved' AND inventory_count>0),catalog_version=catalog_version+1,updated_at=now() WHERE id=$1::uuid AND provider_id IS NOT NULL AND moderation_status<>'archived'`, c.Params("product_id"), req.Status, strings.TrimSpace(req.Notes), adminID)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if tag.RowsAffected() == 0 {
		return fiber.ErrNotFound
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// ListMyMerchantOrders exposes only paid local-fulfilment orders owned by the
// authenticated provider. It is cursor-paginated and returns immutable product
// and buyer snapshots so fulfilment remains auditable after later profile edits.
func (m *ProviderMarketplaceController) ListMyMerchantOrders(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, _, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.ErrForbidden
	}
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	var cursorID any
	if page.CursorTime != nil {
		cursorID = page.CursorID
	}
	rows, err := m.db.Query(c.Context(), `SELECT o.id::text,COALESCE(o.package_label,''),o.currency_code,o.total_amount,o.order_status::text,o.current_tracking_stage::text,o.fulfillment_contact_snapshot,o.created_at,COALESCE(jsonb_agg(jsonb_build_object('id',oi.id,'sku',oi.sku,'title',oi.title,'quantity',oi.quantity,'unit_price',oi.unit_price,'product_snapshot',oi.product_snapshot) ORDER BY oi.created_at) FILTER(WHERE oi.id IS NOT NULL),'[]'::jsonb) FROM orders o LEFT JOIN order_items oi ON oi.order_id=o.id WHERE o.provider_id=$1::uuid AND o.fulfillment_mode='merchant_local' AND o.paid_at IS NOT NULL AND ($2::timestamptz IS NULL OR (o.created_at,o.id)<($2,$3::uuid)) GROUP BY o.id ORDER BY o.created_at DESC,o.id DESC LIMIT $4`, providerID, page.CursorTime, cursorID, page.Limit+1)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "merchant orders unavailable")
	}
	defer rows.Close()
	items := make([]fiber.Map, 0, page.Limit+1)
	for rows.Next() {
		var id, label, currency, status, stage string
		var total float64
		var contact, orderItems []byte
		var created time.Time
		if rows.Scan(&id, &label, &currency, &total, &status, &stage, &contact, &created, &orderItems) != nil {
			return fiber.ErrInternalServerError
		}
		var contactMap map[string]any
		var orderItemsValue []map[string]any
		_ = json.Unmarshal(contact, &contactMap)
		_ = json.Unmarshal(orderItems, &orderItemsValue)
		items = append(items, fiber.Map{"id": id, "package_label": label, "currency_code": currency, "total_amount": total, "status": status, "tracking_stage": stage, "fulfillment_contact": contactMap, "items": orderItemsValue, "created_at": created})
	}
	next := ""
	if len(items) > page.Limit {
		items = items[:page.Limit]
		last := items[len(items)-1]
		next = encodeAdminCursor(last["created_at"].(time.Time), last["id"].(string))
	}
	return c.JSON(fiber.Map{"items": items, "page": fiber.Map{"limit": page.Limit, "next_cursor": next, "has_more": next != ""}})
}
