package controllers

import (
	"encoding/json"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OrderController struct {
	db *pgxpool.Pool
}

func NewOrderController(db *pgxpool.Pool) *OrderController {
	return &OrderController{db: db}
}

func (o *OrderController) BootstrapProfile(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	var profile struct {
		ID                  string
		CountryCode         string
		CurrencyCode        string
		HasFlutterwaveToken bool
	}
	err := o.db.QueryRow(c.Context(), `
		SELECT u.id, cc.country_code, cc.currency_code, u.flutterwave_token IS NOT NULL
		FROM users u
		JOIN countries_config cc ON cc.id = u.country_id
		WHERE u.id = $1 AND u.is_active = true
	`, userID).Scan(&profile.ID, &profile.CountryCode, &profile.CurrencyCode, &profile.HasFlutterwaveToken)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "profile unavailable")
	}
	return c.JSON(fiber.Map{
		"id":                    profile.ID,
		"country_code":          profile.CountryCode,
		"currency_code":         profile.CurrencyCode,
		"has_flutterwave_token": profile.HasFlutterwaveToken,
	})
}

func (o *OrderController) QuoteCheckout(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	var req struct {
		CountryCode string `json:"country_code"`
		Items       []struct {
			ProductID   string         `json:"product_id"`
			SKU         string         `json:"sku"`
			Quantity    int            `json:"quantity"`
			OriginHubID string         `json:"origin_hub_id"`
			Variant     map[string]any `json:"variant"`
		} `json:"items"`
	}
	if err := c.BodyParser(&req); err != nil || len(req.Items) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid cart")
	}

	tx, err := o.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())

	var countryID, currency string
	if err := tx.QueryRow(c.Context(), `
		SELECT id, currency_code
		FROM countries_config
		WHERE country_code = $1 AND is_active = true
	`, req.CountryCode).Scan(&countryID, &currency); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "unsupported country")
	}

	var itemsTotal float64
	for _, item := range req.Items {
		if item.Quantity <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "quantity must be positive")
		}
		var unitPrice float64
		if err := tx.QueryRow(c.Context(), `
			SELECT local_selling_price
			FROM products
			WHERE id = $1 AND sku = $2 AND is_active = true AND inventory_count >= $3
		`, item.ProductID, item.SKU, item.Quantity).Scan(&unitPrice); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "product unavailable")
		}
		itemsTotal += unitPrice * float64(item.Quantity)
	}

	customsFee := roundMoney(itemsTotal * 0.20)
	shippingFee := estimateShipping(req.Items)
	vatFee := 100.0
	grandTotal := roundMoney(itemsTotal + customsFee + shippingFee + vatFee)
	deliveryPromise := time.Now().UTC().Add(21 * 24 * time.Hour)

	var orderID string
	if err := tx.QueryRow(c.Context(), `
		INSERT INTO orders(user_id, country_id, currency_code, total_amount, shipping_fee, customs_fee, vat_fee, delivery_promised_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id
	`, userID, countryID, currency, grandTotal, shippingFee, customsFee, vatFee, deliveryPromise).Scan(&orderID); err != nil {
		return err
	}

	for _, item := range req.Items {
		var title string
		var unitPrice float64
		if err := tx.QueryRow(c.Context(), `
			SELECT title, local_selling_price
			FROM products
			WHERE id = $1
		`, item.ProductID).Scan(&title, &unitPrice); err != nil {
			return err
		}
		variantJSON, err := json.Marshal(item.Variant)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid variant")
		}
		_, err = tx.Exec(c.Context(), `
			INSERT INTO order_items(order_id, product_id, origin_hub_id, sku, title, variant, quantity, unit_price)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, orderID, item.ProductID, item.OriginHubID, item.SKU, title, variantJSON, item.Quantity, unitPrice)
		if err != nil {
			return err
		}
	}

	if err := tx.Commit(c.Context()); err != nil {
		return err
	}
	return c.JSON(fiber.Map{
		"order_id":     orderID,
		"items_total":  itemsTotal,
		"customs_fee":  customsFee,
		"shipping_fee": shippingFee,
		"vat_fee":      vatFee,
		"grand_total":  grandTotal,
		"currency":     currency,
	})
}

func (o *OrderController) Tracking(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	orderID := c.Params("order_id")

	rows, err := o.db.Query(c.Context(), `
		SELECT te.stage, true AS completed, COALESCE(lh.name, ''), te.occurred_at
		FROM tracking_events te
		JOIN orders o ON o.id = te.order_id
		LEFT JOIN logistics_hubs lh ON lh.id = te.hub_id
		WHERE te.order_id = $1 AND o.user_id = $2
		ORDER BY te.occurred_at ASC
	`, orderID, userID)
	if err != nil {
		return err
	}
	defer rows.Close()

	events := make([]fiber.Map, 0)
	for rows.Next() {
		var stage, location string
		var completed bool
		var occurredAt time.Time
		if err := rows.Scan(&stage, &completed, &location, &occurredAt); err != nil {
			return err
		}
		events = append(events, fiber.Map{
			"stage":       stage,
			"completed":   completed,
			"location":    location,
			"occurred_at": occurredAt,
		})
	}
	return c.JSON(fiber.Map{"events": events})
}

func estimateShipping(items []struct {
	ProductID   string         `json:"product_id"`
	SKU         string         `json:"sku"`
	Quantity    int            `json:"quantity"`
	OriginHubID string         `json:"origin_hub_id"`
	Variant     map[string]any `json:"variant"`
}) float64 {
	totalItems := 0
	warehouses := make(map[string]struct{})
	for _, item := range items {
		totalItems += item.Quantity
		warehouses[item.OriginHubID] = struct{}{}
	}
	return 2500 + float64(totalItems*900) + float64(len(warehouses))*1200
}

func roundMoney(value float64) float64 {
	return float64(int(value*100+0.5)) / 100
}
