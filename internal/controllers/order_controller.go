package controllers

import (
	"database/sql"
	"encoding/json"
	"strings"
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

	var email, fullName, phone, address, city, state, postalCode string
	if err := o.db.QueryRow(c.Context(), `
		SELECT COALESCE(email, ''), COALESCE(full_name, ''), COALESCE(phone, ''),
			COALESCE(address, ''), COALESCE(city, ''), COALESCE(state, ''), COALESCE(postal_code, '')
		FROM users
		WHERE id = $1 AND is_active = true
	`, userID).Scan(&email, &fullName, &phone, &address, &city, &state, &postalCode); err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "user not found")
	}
	if missing := missingPurchasingProfileFields(email, fullName, phone); len(missing) > 0 {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"code":           "PROFILE_INCOMPLETE",
			"message":        "Complete your profile before purchasing: " + strings.Join(missing, ", "),
			"missing_fields": missing,
		})
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
	vatFee := 100.0
	grandTotal := roundMoney(itemsTotal + customsFee + vatFee)
	deliveryPromise := time.Now().UTC().Add(21 * 24 * time.Hour)
	contactSnapshot, err := json.Marshal(fiber.Map{
		"full_name": fullName, "email": email, "phone": phone,
		"address": address, "city": city, "state": state, "postal_code": postalCode,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "could not prepare fulfillment contact")
	}

	var orderID string
	if err := tx.QueryRow(c.Context(), `
		INSERT INTO orders(user_id, country_id, currency_code, total_amount, shipping_fee, customs_fee, vat_fee, stamp_duty_fee, delivery_promised_at, fulfillment_contact_snapshot)
		VALUES ($1, $2, $3, $4, 0, $5, $6, 0, $7, $8::jsonb)
		RETURNING id
	`, userID, countryID, currency, grandTotal, customsFee, vatFee, deliveryPromise, contactSnapshot).Scan(&orderID); err != nil {
		return err
	}

	for _, item := range req.Items {
		var title, description, hubCode, hubName, hubCity, hubAddress string
		var unitPrice, supplierCostRMB float64
		var imageURLs []string
		var factoryRaw []byte
		if err := tx.QueryRow(c.Context(), `
			SELECT p.title, p.description, p.local_selling_price, p.cost_price_rmb,
				p.image_urls, p.factory_details,
				COALESCE(lh.code, ''), COALESCE(lh.name, ''), COALESCE(lh.city, ''), COALESCE(lh.address, '')
			FROM products p
			LEFT JOIN logistics_hubs lh ON lh.id = COALESCE(NULLIF($2, '')::uuid, p.origin_hub_id)
			WHERE p.id = $1
		`, item.ProductID, item.OriginHubID).Scan(
			&title, &description, &unitPrice, &supplierCostRMB, &imageURLs, &factoryRaw,
			&hubCode, &hubName, &hubCity, &hubAddress,
		); err != nil {
			return err
		}
		factoryDetails := map[string]any{}
		_ = json.Unmarshal(factoryRaw, &factoryDetails)
		productSnapshot, err := json.Marshal(map[string]any{
			"description":       description,
			"image_urls":        imageURLs,
			"supplier_cost_rmb": supplierCostRMB,
			"factory_details":   factoryDetails,
			"origin_hub": map[string]any{
				"code": hubCode, "name": hubName, "city": hubCity, "address": hubAddress,
			},
		})
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "product snapshot failed")
		}
		variantJSON, err := json.Marshal(item.Variant)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid variant")
		}
		// Use NULL for empty origin_hub_id to avoid FK violation
		hubID := item.OriginHubID
		if hubID == "" || hubID == "00000000-0000-0000-0000-000000000000" {
			hubID = ""
		}
		var originHubID *string
		if hubID != "" {
			originHubID = &hubID
		}
		_, err = tx.Exec(c.Context(), `
		INSERT INTO order_items(order_id, product_id, origin_hub_id, sku, title, variant, quantity, unit_price, product_snapshot)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, orderID, item.ProductID, originHubID, item.SKU, title, variantJSON, item.Quantity, unitPrice, productSnapshot)
		if err != nil {
			return err
		}
	}

	if _, err := tx.Exec(c.Context(), `
		INSERT INTO tracking_events(order_id, stage, notes)
		VALUES ($1, 'Order Placed', 'Order created')
	`, orderID); err != nil {
		return err
	}

	if err := tx.Commit(c.Context()); err != nil {
		return err
	}
	return c.JSON(fiber.Map{
		"order_id":    orderID,
		"items_total": itemsTotal,
		"customs_fee": customsFee,
		"vat_fee":     vatFee,
		"grand_total": grandTotal,
		"currency":    currency,
	})
}

func missingPurchasingProfileFields(email, fullName, phone string) []string {
	missing := make([]string, 0, 3)
	if strings.TrimSpace(fullName) == "" {
		missing = append(missing, "full name")
	}
	if strings.TrimSpace(email) == "" {
		missing = append(missing, "email")
	}
	if strings.TrimSpace(phone) == "" {
		missing = append(missing, "phone number")
	}
	return missing
}

func (o *OrderController) ListOrders(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	rows, err := o.db.Query(c.Context(), `
		SELECT o.id, o.currency_code, o.total_amount, o.shipping_fee, o.customs_fee, o.vat_fee,
			o.order_status::text, o.current_tracking_stage::text, COALESCE(o.package_label, ''),
			o.created_at,
			COALESCE((SELECT SUM(oi.quantity)::int FROM order_items oi WHERE oi.order_id = o.id), 0),
			COALESCE((SELECT string_agg(oi.title, ', ' ORDER BY oi.created_at) FROM order_items oi WHERE oi.order_id = o.id), '')
		FROM orders o
		WHERE o.user_id = $1
		  AND o.order_status != 'Pending'
		ORDER BY o.created_at DESC
	`, userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "orders unavailable")
	}
	defer rows.Close()

	orders := make([]fiber.Map, 0)
	for rows.Next() {
		var id, currency, status, stage, packageLabel, itemsSummary string
		var totalAmount, shippingFee, customsFee, vatFee float64
		var createdAt time.Time
		var itemCount int
		if err := rows.Scan(&id, &currency, &totalAmount, &shippingFee, &customsFee, &vatFee,
			&status, &stage, &packageLabel, &createdAt, &itemCount, &itemsSummary); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "orders unavailable")
		}
		orders = append(orders, fiber.Map{
			"id": id, "currency": currency, "total_amount": totalAmount,
			"shipping_fee": shippingFee, "customs_fee": customsFee, "vat_fee": vatFee,
			"order_status": status, "current_tracking_stage": stage,
			"package_label": packageLabel, "created_at": createdAt,
			"item_count": itemCount, "items_summary": itemsSummary,
		})
	}
	if err := rows.Err(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "orders unavailable")
	}
	return c.JSON(fiber.Map{"orders": orders})
}

func (o *OrderController) Tracking(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	orderID := c.Params("order_id")

	var batchSummary struct {
		BatchID       *string
		BatchCode     *string
		BatchStatus   *string
		BatchLocation *string
		TransportMode *string
		PackageLabel  *string
		CurrentStatus string
		CurrentStage  string
	}
	if err := o.db.QueryRow(c.Context(), `
		SELECT o.order_status, o.current_tracking_stage, o.package_label,
			ob.id, ob.batch_code, ob.status::text, ob.current_location, ob.transport_mode
		FROM orders o
		LEFT JOIN order_batches ob ON ob.id = o.batch_id
		WHERE o.id = $1 AND o.user_id = $2
	`, orderID, userID).Scan(&batchSummary.CurrentStatus, &batchSummary.CurrentStage, &batchSummary.PackageLabel,
		&batchSummary.BatchID, &batchSummary.BatchCode, &batchSummary.BatchStatus, &batchSummary.BatchLocation, &batchSummary.TransportMode); err != nil {
		return fiber.NewError(fiber.StatusNotFound, "tracking unavailable")
	}

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

	batchEvents := make([]fiber.Map, 0)
	if batchSummary.BatchID != nil {
		rows, err := o.db.Query(c.Context(), `
			SELECT event_type, COALESCE(status::text, ''), location, notes, created_at
			FROM batch_events
			WHERE batch_id = $1
			ORDER BY created_at ASC
		`, *batchSummary.BatchID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var eventType, status, location, notes string
			var occurredAt time.Time
			if err := rows.Scan(&eventType, &status, &location, &notes, &occurredAt); err != nil {
				return err
			}
			batchEvents = append(batchEvents, fiber.Map{
				"event_type":  eventType,
				"status":      status,
				"location":    location,
				"notes":       notes,
				"occurred_at": occurredAt,
			})
		}
	}

	return c.JSON(fiber.Map{
		"batch": fiber.Map{
			"batch_code":       batchSummary.BatchCode,
			"status":           batchSummary.BatchStatus,
			"current_location": batchSummary.BatchLocation,
			"transport_mode":   batchSummary.TransportMode,
			"package_label":    batchSummary.PackageLabel,
		},
		"events":       events,
		"batch_events": batchEvents,
	})
}

func (o *OrderController) PaymentStatus(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	orderID := c.Params("order_id")

	var status struct {
		OrderStatus              string
		CurrentStage             string
		FlutterwaveTxRef         sql.NullString
		FlutterwaveTransactionID sql.NullString
		PaidAt                   sql.NullTime
	}
	if err := o.db.QueryRow(c.Context(), `
		SELECT order_status::text, current_tracking_stage::text,
			flutterwave_tx_ref, flutterwave_transaction_id, paid_at
		FROM orders
		WHERE id = $1 AND user_id = $2
	`, orderID, userID).Scan(
		&status.OrderStatus,
		&status.CurrentStage,
		&status.FlutterwaveTxRef,
		&status.FlutterwaveTransactionID,
		&status.PaidAt,
	); err != nil {
		return fiber.NewError(fiber.StatusNotFound, "payment status unavailable")
	}

	paymentState := "pending"
	if status.OrderStatus == "Paid" || status.OrderStatus == "Shipped" || status.OrderStatus == "Delivered" || status.OrderStatus == "Completed" {
		paymentState = "settled"
	} else if status.FlutterwaveTxRef.Valid {
		paymentState = "processing"
	}

	return c.JSON(fiber.Map{
		"order_id":                   orderID,
		"order_status":               status.OrderStatus,
		"current_tracking_stage":     status.CurrentStage,
		"flutterwave_tx_ref":         status.FlutterwaveTxRef.String,
		"flutterwave_transaction_id": status.FlutterwaveTransactionID.String,
		"paid_at": func() any {
			if status.PaidAt.Valid {
				return status.PaidAt.Time
			}
			return nil
		}(),
		"payment_state": paymentState,
	})
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
