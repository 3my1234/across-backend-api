package controllers

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"across/backend/internal/auth"
	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type AdminController struct {
	db  *pgxpool.Pool
	cfg config.Config
}

func NewAdminController(db *pgxpool.Pool, cfg config.Config) *AdminController {
	return &AdminController{db: db, cfg: cfg}
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
	var adminID, passwordHash, role string
	err := a.db.QueryRow(c.Context(), `
		SELECT id, password_hash, role
		FROM admins
		WHERE email = $1 AND is_active = true
	`, email).Scan(&adminID, &passwordHash, &role)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)) != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "invalid admin credentials")
	}
	token, expiresAt, err := auth.Sign(adminID, a.cfg.JWTSecret, 12*time.Hour)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"access_token": token, "expires_at": expiresAt, "admin_id": adminID, "role": role})
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
	req.Role = strings.TrimSpace(req.Role)
	if req.Role == "" {
		req.Role = "admin"
	}
	if req.Email == "" || req.FullName == "" || len(req.Password) < 10 || (req.Role != "admin" && req.Role != "super_admin") {
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

func (a *AdminController) ListOrders(c *fiber.Ctx) error {
	rows, err := a.db.Query(c.Context(), `
		SELECT o.id, u.email, o.currency_code, o.total_amount, o.shipping_fee,
			COALESCE(o.customs_fee, 0), COALESCE(o.vat_fee, 0),
			o.order_status, o.current_tracking_stage, o.created_at
		FROM orders o
		JOIN users u ON u.id = o.user_id
		ORDER BY o.created_at DESC
		LIMIT 500
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	orders := make([]fiber.Map, 0)
	for rows.Next() {
		var id, email, currency, status, stage string
		var total, shipping, customs, vat float64
		var createdAt time.Time
		if err := rows.Scan(&id, &email, &currency, &total, &shipping, &customs, &vat, &status, &stage, &createdAt); err != nil {
			return err
		}
		orders = append(orders, fiber.Map{
			"id": id, "email": email, "currency": currency, "total_amount": total,
			"shipping_fee": shipping, "customs_fee": customs, "vat_fee": vat,
			"status": status, "stage": stage, "created_at": createdAt,
		})
	}
	return c.JSON(fiber.Map{"orders": orders})
}

func (a *AdminController) ListTransactions(c *fiber.Ctx) error {
	rows, err := a.db.Query(c.Context(), `
		SELECT o.id, u.email, o.total_amount, o.currency_code, o.order_status,
			COALESCE(el.escrow_status::text, 'not_initialized'),
			COALESCE(el.dispute_status::text, 'none'),
			COALESCE(el.flutterwave_tx_ref, ''), o.created_at
		FROM orders o
		JOIN users u ON u.id = o.user_id
		LEFT JOIN escrow_ledger el ON el.order_id = o.id
		ORDER BY o.created_at DESC
		LIMIT 500
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	transactions := make([]fiber.Map, 0)
	for rows.Next() {
		var id, email, currency, orderStatus, escrowStatus, disputeStatus, txRef string
		var total float64
		var createdAt time.Time
		if err := rows.Scan(&id, &email, &total, &currency, &orderStatus, &escrowStatus, &disputeStatus, &txRef, &createdAt); err != nil {
			return err
		}
		transactions = append(transactions, fiber.Map{
			"order_id": id, "email": email, "total_amount": total, "currency": currency,
			"order_status": orderStatus, "escrow_status": escrowStatus, "dispute_status": disputeStatus,
			"flutterwave_tx_ref": txRef, "created_at": createdAt,
		})
	}
	return c.JSON(fiber.Map{"transactions": transactions})
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
		req.SKU = generateProductSKU(req.CategoryPath, req.Title)
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
		return fiber.NewError(fiber.StatusConflict, "product could not be created")
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id, "sku": req.SKU})
}

func (a *AdminController) ListProducts(c *fiber.Ctx) error {
	rows, err := a.db.Query(c.Context(), `
		SELECT p.id, p.sku, p.title, p.description, p.category_path, p.image_urls,
			p.local_currency_code, p.local_selling_price, COALESCE(p.compare_at_price, 0),
			p.inventory_count, p.is_active, p.created_at, p.updated_at
		FROM products p
		ORDER BY p.created_at DESC
		LIMIT 500
	`)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "products unavailable")
	}
	defer rows.Close()

	products := make([]fiber.Map, 0)
	for rows.Next() {
		var id, sku, title, description, currency string
		var categories, images []string
		var price, compareAtPrice float64
		var inventory int
		var isActive bool
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &sku, &title, &description, &categories, &images, &currency, &price, &compareAtPrice, &inventory, &isActive, &createdAt, &updatedAt); err != nil {
			return err
		}
		products = append(products, fiber.Map{
			"id":                    id,
			"sku":                   sku,
			"title":                 title,
			"description":           description,
			"category_path":         categories,
			"image_urls":            images,
			"currency":              currency,
			"local_selling_price":   price,
			"compare_at_price":      compareAtPrice,
			"inventory_count":       inventory,
			"is_active":             isActive,
			"created_at":            createdAt,
			"updated_at":            updatedAt,
		})
	}
	return c.JSON(fiber.Map{"products": products})
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
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	if req.Title == nil && req.Description == nil && req.LocalSellingPrice == nil && req.CompareAtPrice == nil && req.InventoryCount == nil && req.IsActive == nil {
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

	tag, err := a.db.Exec(c.Context(), `
		UPDATE products
		SET title = COALESCE($2, title),
			description = COALESCE($3, description),
			local_selling_price = COALESCE($4, local_selling_price),
			compare_at_price = CASE WHEN $5::numeric IS NULL THEN compare_at_price ELSE NULLIF($5, 0) END,
			inventory_count = COALESCE($6, inventory_count),
			is_active = COALESCE($7, is_active),
			updated_at = now()
		WHERE id = $1
	`, productID, req.Title, req.Description, req.LocalSellingPrice, req.CompareAtPrice, req.InventoryCount, req.IsActive)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fiber.NewError(fiber.StatusNotFound, "product not found")
	}
	return c.JSON(fiber.Map{"id": productID, "updated": true})
}

func (a *AdminController) DeleteProduct(c *fiber.Ctx) error {
	productID := c.Params("product_id")
	tag, err := a.db.Exec(c.Context(), `
		UPDATE products
		SET is_active = false, updated_at = now()
		WHERE id = $1 AND is_active = true
	`, productID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fiber.NewError(fiber.StatusNotFound, "product not found")
	}
	return c.JSON(fiber.Map{"id": productID, "deleted": true})
}

var skuCleaner = regexp.MustCompile(`[^A-Z0-9]+`)

func generateProductSKU(categories []string, title string) string {
	category := "PRD"
	if len(categories) > 0 && strings.TrimSpace(categories[0]) != "" {
		category = categories[0]
	}
	prefix := abbreviateSKU(category, 3)
	body := abbreviateSKU(title, 8)
	suffix := time.Now().UTC().Format("060102150405")
	return fmt.Sprintf("%s-%s-%s", prefix, body, suffix)
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

func (a *AdminController) PendingManifest(c *fiber.Ctx) error {
	rows, err := a.db.Query(c.Context(), `
		SELECT o.id, o.order_status, o.current_tracking_stage, oi.sku, oi.title, oi.quantity, lh.code
		FROM orders o
		JOIN order_items oi ON oi.order_id = o.id
		LEFT JOIN logistics_hubs lh ON lh.id = oi.origin_hub_id
		WHERE o.order_status IN ('Paid', 'Shipped')
		ORDER BY o.created_at ASC
		LIMIT 1000
	`)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "manifest unavailable")
	}
	defer rows.Close()

	items := make([]fiber.Map, 0)
	for rows.Next() {
		var orderID, status, stage, sku, title string
		var hubCode *string
		var qty int
		if err := rows.Scan(&orderID, &status, &stage, &sku, &title, &qty, &hubCode); err != nil {
			return err
		}
		items = append(items, fiber.Map{"order_id": orderID, "status": status, "stage": stage, "sku": sku, "title": title, "quantity": qty, "hub_code": hubCode})
	}
	return c.JSON(fiber.Map{"items": items})
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

func (a *AdminController) SettleEscrow(c *fiber.Ctx) error {
	var req struct {
		OrderIDs []string `json:"order_ids"`
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
		tag, err := tx.Exec(c.Context(), `
		UPDATE escrow_ledger
		SET escrow_status = 'released', released_at = now(), updated_at = now()
		WHERE order_id = $1 AND dispute_status <> 'active'
	`, orderID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		_, err = tx.Exec(c.Context(), `
		UPDATE orders
		SET order_status = 'Completed', ready_for_manual_settlement = false, updated_at = now()
		WHERE id = $1
	`, orderID)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(c.Context()); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (a *AdminController) FreezeDispute(c *fiber.Ctx) error {
	orderID := c.Params("order_id")
	tx, err := a.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())
	_, err = tx.Exec(c.Context(), `
		UPDATE escrow_ledger
		SET escrow_status = 'frozen', dispute_status = 'active', updated_at = now()
		WHERE order_id = $1
	`, orderID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(c.Context(), `
		UPDATE orders
		SET order_status = 'Disputed', updated_at = now()
		WHERE id = $1
	`, orderID)
	if err != nil {
		return err
	}
	if err := tx.Commit(c.Context()); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
