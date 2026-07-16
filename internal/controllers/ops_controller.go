package controllers

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OpsController struct {
	db *pgxpool.Pool
}

func NewOpsController(db *pgxpool.Pool) *OpsController {
	return &OpsController{db: db}
}

// ---------- Admin II: Purchase Confirmation ----------

type BatchPurchaseItem struct {
	OrderItemID    string `json:"order_item_id"`
	PurchaseStatus string `json:"purchase_status"` // "purchased" or "failed"
	PurchaseNotes  string `json:"purchase_notes"`
}

// ConfirmPurchase lets Admin II mark individual order items as purchased/failed within a batch
func (o *OpsController) ConfirmPurchase(c *fiber.Ctx) error {
	batchID := c.Params("batch_id")
	var req struct {
		Items []BatchPurchaseItem `json:"items"`
	}
	if err := c.BodyParser(&req); err != nil || len(req.Items) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}

	tx, err := o.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())

	purchasedCount := 0
	failedCount := 0
	for _, item := range req.Items {
		normalized := strings.TrimSpace(strings.ToLower(item.PurchaseStatus))
		if normalized != "purchased" && normalized != "failed" {
			continue
		}
		tag, err := tx.Exec(c.Context(), `
			UPDATE order_items oi
			SET purchase_status = $2, purchase_notes = $3
			FROM orders o
			WHERE oi.id = $1 AND oi.order_id = o.id AND o.batch_id = $4
		`, item.OrderItemID, normalized, item.PurchaseNotes, batchID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			if normalized == "purchased" {
				purchasedCount++
			} else {
				failedCount++
			}
		}
	}

	if err := tx.Commit(c.Context()); err != nil {
		return err
	}

	// Send notifications for purchased items
	if purchasedCount > 0 {
		notifyType := "product_purchased"
		title := "Your order has been secured"
		body := fmt.Sprintf("Good news! %d item(s) from your order have been purchased from the supplier.", purchasedCount)
		_ = notifyBatchUsers(c.Context(), o.db, batchID, notifyType, title, body, nil)
	}

	return c.JSON(fiber.Map{
		"updated":         true,
		"purchased_count": purchasedCount,
		"failed_count":    failedCount,
	})
}

// GetPurchaseManifest returns all order items in a batch organized for Admin II procurement
func (o *OpsController) GetPurchaseManifest(c *fiber.Ctx) error {
	batchID := c.Params("batch_id")
	rows, err := o.db.Query(c.Context(), `
		SELECT oi.id, oi.sku, oi.title, oi.quantity, oi.unit_price,
			oi.purchase_status, oi.purchase_notes,
			u.id, u.full_name, u.email, u.phone,
			o.id, o.package_label
		FROM order_items oi
		JOIN orders o ON o.id = oi.order_id
		JOIN users u ON u.id = o.user_id
		WHERE o.batch_id = $1
		ORDER BY u.full_name, oi.created_at
	`, batchID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "manifest unavailable")
	}
	defer rows.Close()

	type BuyerOrder struct {
		BuyerID        string  `json:"buyer_id"`
		BuyerName      string  `json:"buyer_name"`
		BuyerEmail     string  `json:"buyer_email"`
		BuyerPhone     string  `json:"buyer_phone"`
		OrderID        string  `json:"order_id"`
		PackageLabel   string  `json:"package_label"`
		ItemID         string  `json:"item_id"`
		SKU            string  `json:"sku"`
		Title          string  `json:"title"`
		Quantity       int     `json:"quantity"`
		UnitPrice      float64 `json:"unit_price"`
		PurchaseStatus string  `json:"purchase_status"`
		PurchaseNotes  string  `json:"purchase_notes"`
	}
	items := make([]BuyerOrder, 0)
	for rows.Next() {
		var item BuyerOrder
		if err := rows.Scan(&item.ItemID, &item.SKU, &item.Title, &item.Quantity, &item.UnitPrice,
			&item.PurchaseStatus, &item.PurchaseNotes,
			&item.BuyerID, &item.BuyerName, &item.BuyerEmail, &item.BuyerPhone,
			&item.OrderID, &item.PackageLabel); err != nil {
			return err
		}
		items = append(items, item)
	}
	return c.JSON(fiber.Map{"items": items, "total_items": len(items)})
}

// ---------- Admin III: Delivery Management ----------

// ConfirmArrival marks a batch as arrived locally and sends pickup notifications
func (o *OpsController) ConfirmArrival(c *fiber.Ctx) error {
	batchID := c.Params("batch_id")
	var req struct {
		PickupLocation string `json:"pickup_location"`
		PickupPhone    string `json:"pickup_phone"`
		Notes          string `json:"notes"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}

	tx, err := o.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())

	req.PickupLocation = strings.TrimSpace(req.PickupLocation)
	req.PickupPhone = strings.TrimSpace(req.PickupPhone)

	// Update batch status to sorted (arrived + ready for sorting/dispatch)
	_, err = tx.Exec(c.Context(), `
		UPDATE order_batches
		SET status = 'sorted'::batch_status,
			current_location = COALESCE(NULLIF($2, ''), current_location),
			notes = CASE WHEN $3 <> '' THEN $3 ELSE notes END,
			updated_at = now()
		WHERE id = $1
	`, batchID, req.PickupLocation, req.Notes)
	if err != nil {
		return err
	}

	// Update all orders in batch with pickup info and tracking stage
	_, err = tx.Exec(c.Context(), `
		UPDATE orders
		SET current_tracking_stage = 'Arrived at Local Hub'::tracking_stage,
			pickup_location = COALESCE(NULLIF($2, ''), pickup_location),
			pickup_phone = COALESCE(NULLIF($3, ''), pickup_phone),
			delivery_notes = CASE WHEN $4 <> '' THEN $4 ELSE delivery_notes END,
			updated_at = now()
		WHERE batch_id = $1
	`, batchID, req.PickupLocation, req.PickupPhone, req.Notes)
	if err != nil {
		return err
	}

	if err := tx.Commit(c.Context()); err != nil {
		return err
	}

	// Send notification to all buyers in batch
	data := map[string]any{
		"pickup_location": req.PickupLocation,
		"pickup_phone":    req.PickupPhone,
	}
	_ = notifyBatchUsers(c.Context(), o.db, batchID, "ready_for_pickup",
		"Your package has arrived!",
		fmt.Sprintf("Your order has arrived at %s. Call %s for pickup or delivery arrangements.", req.PickupLocation, req.PickupPhone),
		data)

	return c.JSON(fiber.Map{"batch_id": batchID, "status": "sorted", "updated": true})
}

// ConfirmDelivered marks individual orders as delivered and sends confirmation notification
func (o *OpsController) ConfirmDelivered(c *fiber.Ctx) error {
	var req struct {
		OrderIDs []string `json:"order_ids"`
	}
	if err := c.BodyParser(&req); err != nil || len(req.OrderIDs) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}

	tx, err := o.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())

	for _, orderID := range req.OrderIDs {
		_, err := tx.Exec(c.Context(), `
			UPDATE orders
			SET current_tracking_stage = 'Delivered'::tracking_stage,
				order_status = 'Delivered',
				delivered_at = now(),
				updated_at = now()
			WHERE id = $1 AND order_status NOT IN ('Completed', 'Delivered')
		`, orderID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(c.Context(), `
			INSERT INTO tracking_events(order_id, stage, notes)
			VALUES ($1, 'Delivered'::tracking_stage, 'Package delivered by courier')
		`, orderID)
		if err != nil {
			return err
		}
	}

	if err := tx.Commit(c.Context()); err != nil {
		return err
	}

	// Send delivery notifications
	for _, orderID := range req.OrderIDs {
		data := map[string]any{
			"order_id": orderID,
			"message":  "Please confirm you received your package. You have 3 days to raise a dispute, or leave a review and earn ₦500 off your next order!",
		}
		_ = CreateNotification(c.Context(), o.db, "", orderID, nil, "confirm_receipt",
			"Package delivered! Confirm receipt",
			"Your package has been delivered. Please confirm receipt in the app. You have 3 days to raise a dispute, or leave a review to earn ₦500 off your next purchase!",
			data)
	}

	return c.JSON(fiber.Map{"delivered": true, "count": len(req.OrderIDs)})
}

// ---------- Review Reward ----------

// ClaimReviewReward lets a buyer claim their review reward
func (o *OpsController) ClaimReviewReward(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	orderID := c.Params("order_id")

	tag, err := o.db.Exec(c.Context(), `
		UPDATE review_rewards
		SET is_claimed = true, claimed_at = now()
		WHERE user_id = $1 AND order_id = $2 AND is_claimed = false
	`, userID, orderID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "claim failed")
	}
	if tag.RowsAffected() == 0 {
		return fiber.NewError(fiber.StatusNotFound, "no reward available for this order")
	}
	return c.JSON(fiber.Map{"claimed": true, "reward": "₦500 off your next order"})
}

// AutoCreateReviewReward is called when an order is completed
func AutoCreateReviewReward(db *pgxpool.Pool, userID, orderID string) {
	ctx := context.Background()
	_, err := db.Exec(ctx, `
		INSERT INTO review_rewards(user_id, order_id, reward_amount, reward_currency)
		VALUES ($1, $2, 500, 'NGN')
		ON CONFLICT (user_id, order_id) DO NOTHING
	`, userID, orderID)
	if err != nil {
		log.Printf("Failed to create review reward: %v", err)
		return
	}
	_ = CreateNotification(ctx, db, userID, orderID, nil, "review_request",
		"Review and earn ₦500!",
		"Your order is complete! Leave a review and earn ₦500 off your next purchase. You have 7 days to submit your review.",
		map[string]any{"reward_amount": 500})
}

// ---------- Delivery Auto-Confirm Worker ----------

// AutoConfirmDeliveries is a cron job that auto-confirms deliveries after 3 days
func (o *OpsController) AutoConfirmDeliveries(c *fiber.Ctx) error {
	tag, err := o.db.Exec(c.Context(), `
		UPDATE orders
		SET order_status = 'Completed',
			delivery_confirmed = true,
			confirmed_at = now(),
			updated_at = now()
		WHERE current_tracking_stage = 'Delivered'::tracking_stage
			AND delivery_confirmed = false
			AND delivered_at < now() - interval '3 days'
	`)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "auto-confirm failed")
	}
	count := tag.RowsAffected()
	return c.JSON(fiber.Map{"auto_confirmed": count})
}
