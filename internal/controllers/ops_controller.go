package controllers

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
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
	OrderItemID         string `json:"order_item_id"`
	PurchaseStatus      string `json:"purchase_status"` // "purchased" or "failed"
	PurchaseNotes       string `json:"purchase_notes"`
	ExceptionResolution string `json:"exception_resolution"`
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
	adminID, _ := c.Locals("admin_id").(string)
	actorID := nullableAdminID(adminID)

	var batchStatus string
	if err := tx.QueryRow(c.Context(), `
		SELECT status::text
		FROM order_batches
		WHERE id = $1
		FOR UPDATE
	`, batchID).Scan(&batchStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "batch not found")
		}
		return err
	}
	if batchStatus != "purchasing" {
		return fiber.NewError(fiber.StatusConflict, "manifest items can only be changed while procurement is active")
	}

	purchasedCount := 0
	failedCount := 0
	for _, item := range req.Items {
		normalized := strings.TrimSpace(strings.ToLower(item.PurchaseStatus))
		if normalized != "purchased" && normalized != "failed" {
			return fiber.NewError(fiber.StatusBadRequest, "purchase status must be purchased or failed")
		}
		notes := strings.TrimSpace(item.PurchaseNotes)
		resolution := strings.TrimSpace(strings.ToLower(item.ExceptionResolution))
		if normalized == "purchased" {
			resolution = "none"
		} else {
			if notes == "" {
				return fiber.NewError(fiber.StatusBadRequest, "failed items require a reason")
			}
			if !containsString([]string{"pending", "refunded", "substituted", "cancelled"}, resolution) {
				return fiber.NewError(fiber.StatusBadRequest, "failed items require an exception resolution")
			}
		}
		tag, err := tx.Exec(c.Context(), `
			UPDATE order_items oi
			SET purchase_status = $2,
				purchase_notes = $3,
				exception_resolution = $4,
				exception_resolved_at = CASE
					WHEN $4 IN ('refunded', 'substituted', 'cancelled') THEN now()
					ELSE NULL
				END,
				exception_resolved_by = CASE
					WHEN $4 IN ('refunded', 'substituted', 'cancelled') THEN $5::uuid
					ELSE NULL
				END
			FROM orders o
			WHERE oi.id = $1 AND oi.order_id = o.id AND o.batch_id = $6
		`, item.OrderItemID, normalized, notes, resolution, actorID, batchID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			var userID, orderID string
			if err := tx.QueryRow(c.Context(), `
				SELECT o.user_id, o.id
				FROM order_items oi
				JOIN orders o ON o.id = oi.order_id
				WHERE oi.id = $1 AND o.batch_id = $2
			`, item.OrderItemID, batchID).Scan(&userID, &orderID); err != nil {
				return err
			}
			if normalized == "purchased" {
				purchasedCount++
				if _, err := tx.Exec(c.Context(), `
					INSERT INTO notifications(
						user_id, order_id, batch_id, type, title, body, data, event_key
					)
					VALUES (
						$1, $2, $3, 'product_purchased', 'Your item has been secured',
						'Your item has been purchased from the supplier.',
						jsonb_build_object('order_item_id', $4, 'purchase_status', 'purchased'),
						'purchase-item:' || $4
					)
					ON CONFLICT DO NOTHING
				`, userID, orderID, batchID, item.OrderItemID); err != nil {
					return err
				}
			} else {
				failedCount++
				if _, err := tx.Exec(c.Context(), `
					INSERT INTO notifications(
						user_id, order_id, batch_id, type, title, body, data, event_key
					)
					VALUES (
						$1, $2, $3, 'procurement_exception', 'Item procurement update',
						$4,
						jsonb_build_object(
							'order_item_id', $5,
							'purchase_status', 'failed',
							'resolution', $6
						),
						'purchase-exception:' || $5 || ':' || $6
					)
					ON CONFLICT DO NOTHING
				`, userID, orderID, batchID, notes, item.OrderItemID, resolution); err != nil {
					return err
				}
			}
		}
	}

	if err := tx.Commit(c.Context()); err != nil {
		return err
	}

	return c.JSON(fiber.Map{
		"updated":         true,
		"purchased_count": purchasedCount,
		"failed_count":    failedCount,
	})
}

// GetPurchaseManifest returns a bounded, searchable page of order items for Admin II.
func (o *OpsController) GetPurchaseManifest(c *fiber.Ctx) error {
	batchID := c.Params("batch_id")
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	var cursorID any
	if page.CursorTime != nil {
		cursorID = page.CursorID
	}
	rows, err := o.db.Query(c.Context(), `
		SELECT oi.id, oi.sku, oi.title, oi.quantity, oi.unit_price,
			oi.purchase_status, oi.purchase_notes, oi.exception_resolution,
			u.id, u.full_name, u.email, u.phone,
			o.id, o.package_label, oi.created_at,
			COUNT(*) OVER() AS total_count
		FROM order_items oi
		JOIN orders o ON o.id = oi.order_id
		JOIN users u ON u.id = o.user_id
		WHERE o.batch_id = $1
		  AND ($2 = '' OR oi.sku ILIKE '%' || $2 || '%' OR oi.title ILIKE '%' || $2 || '%'
			OR u.full_name ILIKE '%' || $2 || '%' OR u.email ILIKE '%' || $2 || '%'
			OR oi.purchase_status ILIKE '%' || $2 || '%')
		  AND ($3::timestamptz IS NULL OR (oi.created_at, oi.id) < ($3, $4::uuid))
		ORDER BY oi.created_at DESC, oi.id DESC
		LIMIT $5
	`, batchID, page.Search, page.CursorTime, cursorID, page.Limit+1)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "manifest unavailable")
	}
	defer rows.Close()

	type BuyerOrder struct {
		BuyerID             string    `json:"buyer_id"`
		BuyerName           string    `json:"buyer_name"`
		BuyerEmail          string    `json:"buyer_email"`
		BuyerPhone          string    `json:"buyer_phone"`
		OrderID             string    `json:"order_id"`
		PackageLabel        string    `json:"package_label"`
		ItemID              string    `json:"item_id"`
		SKU                 string    `json:"sku"`
		Title               string    `json:"title"`
		Quantity            int       `json:"quantity"`
		UnitPrice           float64   `json:"unit_price"`
		PurchaseStatus      string    `json:"purchase_status"`
		PurchaseNotes       string    `json:"purchase_notes"`
		ExceptionResolution string    `json:"exception_resolution"`
		CreatedAt           time.Time `json:"created_at"`
	}
	items := make([]BuyerOrder, 0)
	var totalCount int64
	for rows.Next() {
		var item BuyerOrder
		if err := rows.Scan(&item.ItemID, &item.SKU, &item.Title, &item.Quantity, &item.UnitPrice,
			&item.PurchaseStatus, &item.PurchaseNotes, &item.ExceptionResolution,
			&item.BuyerID, &item.BuyerName, &item.BuyerEmail, &item.BuyerPhone,
			&item.OrderID, &item.PackageLabel, &item.CreatedAt, &totalCount); err != nil {
			return err
		}
		items = append(items, item)
	}
	nextCursor := ""
	if len(items) > page.Limit {
		items = items[:page.Limit]
		last := items[len(items)-1]
		nextCursor = encodeAdminCursor(last.CreatedAt, last.ItemID)
	}
	return c.JSON(fiber.Map{
		"items":       items,
		"total_items": totalCount,
		"page":        adminPageMeta(page, totalCount, len(items), nextCursor),
	})
}

// ---------- Admin III: Delivery Management ----------

// ConfirmDelivered marks individual orders as delivered and sends confirmation notification
func (o *OpsController) ConfirmDelivered(c *fiber.Ctx) error {
	var req struct {
		BatchID  string   `json:"batch_id"`
		OrderIDs []string `json:"order_ids"`
	}
	if err := c.BodyParser(&req); err != nil || strings.TrimSpace(req.BatchID) == "" || len(req.OrderIDs) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}

	tx, err := o.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())

	type deliveredOrder struct {
		OrderID string
		UserID  string
	}
	deliveredOrders := make([]deliveredOrder, 0, len(req.OrderIDs))
	for _, orderID := range req.OrderIDs {
		var delivered deliveredOrder
		err := tx.QueryRow(c.Context(), `
			UPDATE orders
			SET current_tracking_stage = 'Delivered'::tracking_stage,
				order_status = 'Delivered',
				delivered_at = now(),
				updated_at = now()
			WHERE id = $1
			  AND batch_id = $2
			  AND order_status NOT IN ('Completed', 'Delivered')
			  AND EXISTS (
				SELECT 1
				FROM order_batches b
				WHERE b.id = $2
				  AND b.status = 'ready_for_pickup'::batch_status
			  )
			RETURNING id, user_id
		`, orderID, req.BatchID).Scan(&delivered.OrderID, &delivered.UserID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fiber.NewError(fiber.StatusConflict, "order is not deliverable from the selected batch")
			}
			return err
		}
		deliveredOrders = append(deliveredOrders, delivered)
		_, err = tx.Exec(c.Context(), `
			INSERT INTO tracking_events(order_id, stage, notes)
			VALUES ($1, 'Delivered'::tracking_stage, 'Package delivered by courier')
		`, orderID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(c.Context(), `
			INSERT INTO notifications(
				user_id, order_id, batch_id, type, title, body, data, event_key
			)
			VALUES (
				$1, $2, $3, 'confirm_receipt', 'Package delivered!',
				'Confirm that you received your package. After confirmation, leave a review to earn ₦500 off your next purchase.',
				jsonb_build_object('order_id', $2, 'confirmation_required', true),
				'confirm-receipt:' || $2::text
			)
			ON CONFLICT DO NOTHING
		`, delivered.UserID, delivered.OrderID, req.BatchID); err != nil {
			return err
		}
	}

	if err := tx.Commit(c.Context()); err != nil {
		return err
	}

	return c.JSON(fiber.Map{"delivered": true, "count": len(deliveredOrders)})
}

// ConfirmReceipt allows a buyer to confirm only their own delivered order.
func (o *OpsController) ConfirmReceipt(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	orderID := c.Params("order_id")
	tx, err := o.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())

	var batchID *string
	tag, err := tx.Exec(c.Context(), `
		UPDATE orders
		SET order_status = 'Completed',
			delivery_confirmed = true,
			confirmed_at = COALESCE(confirmed_at, now()),
			updated_at = now()
		WHERE id = $1
		  AND user_id = $2
		  AND order_status = 'Delivered'
		  AND current_tracking_stage = 'Delivered'::tracking_stage
		  AND delivery_confirmed = false
	`, orderID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fiber.NewError(fiber.StatusConflict, "order is not awaiting receipt confirmation")
	}
	if err := tx.QueryRow(c.Context(), `SELECT batch_id FROM orders WHERE id = $1`, orderID).Scan(&batchID); err != nil {
		return err
	}
	if err := createReviewRewardTx(c.Context(), tx, userID, orderID); err != nil {
		return err
	}
	if batchID != nil {
		if err := completeBatchIfResolved(c.Context(), tx, *batchID); err != nil {
			return err
		}
	}
	if err := tx.Commit(c.Context()); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"confirmed": true, "order_id": orderID, "review_reward_available": true})
}

// ---------- Review Reward ----------

// ClaimReviewReward lets a buyer claim their review reward
func (o *OpsController) ClaimReviewReward(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	orderID := c.Params("order_id")
	claimed, err := creditReviewReward(c.Context(), o.db, userID, orderID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "claim failed")
	}
	if !claimed {
		return fiber.NewError(fiber.StatusNotFound, "submit a review before claiming this reward")
	}
	return c.JSON(fiber.Map{"claimed": true, "reward": "₦500 off your next order", "xp_credited": 500})
}

func creditReviewReward(ctx context.Context, db *pgxpool.Pool, userID, orderID string) (bool, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE review_rewards
		SET is_claimed = true, claimed_at = now()
		WHERE user_id = $1
		  AND order_id = $2
		  AND is_claimed = false
		  AND EXISTS (
			SELECT 1
			FROM reviews r
			WHERE r.user_id = $1 AND r.order_id = $2
		  )
	`, userID, orderID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO xp_transactions(user_id, amount, reason, reference_id)
		VALUES ($1, 500, 'review_reward', 'review-reward-' || $2)
		ON CONFLICT DO NOTHING
	`, userID, orderID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO notifications(user_id, order_id, type, title, body, data, event_key)
		VALUES (
			$1, $2, 'xp_earned', 'Review reward claimed',
			'₦500 has been added to your rewards balance.',
			jsonb_build_object('xp', 500, 'naira_value', 500, 'reason', 'review_reward'),
			'review-reward-claimed:' || $2::text
		)
		ON CONFLICT DO NOTHING
	`, userID, orderID); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// AutoCreateReviewReward is called when an order is completed
func AutoCreateReviewReward(db *pgxpool.Pool, userID, orderID string) {
	ctx := context.Background()
	tx, err := db.Begin(ctx)
	if err != nil {
		log.Printf("Failed to create review reward: %v", err)
		return
	}
	defer tx.Rollback(ctx)
	if err := createReviewRewardTx(ctx, tx, userID, orderID); err != nil {
		log.Printf("Failed to create review reward: %v", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("Failed to commit review reward: %v", err)
	}
}

// ---------- Delivery Auto-Confirm Worker ----------

// AutoConfirmDeliveries is a cron job that auto-confirms deliveries after 3 days
func (o *OpsController) AutoConfirmDeliveries(c *fiber.Ctx) error {
	count, err := AutoConfirmExpiredDeliveries(c.Context(), o.db)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "auto-confirm failed")
	}
	return c.JSON(fiber.Map{"auto_confirmed": count})
}

func createReviewRewardTx(ctx context.Context, tx pgx.Tx, userID, orderID string) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO review_rewards(user_id, order_id, reward_amount, reward_currency)
		VALUES ($1, $2, 500, 'NGN')
		ON CONFLICT (user_id, order_id) DO NOTHING
	`, userID, orderID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO notifications(user_id, order_id, type, title, body, data, event_key)
		VALUES (
			$1, $2, 'review_request', 'Review and earn ₦500!',
			'Your order is complete. Leave a review to earn ₦500 off your next purchase.',
			jsonb_build_object('reward_amount', 500),
			'review-request:' || $2::text
		)
		ON CONFLICT DO NOTHING
	`, userID, orderID)
	return err
}

func completeBatchIfResolved(ctx context.Context, tx pgx.Tx, batchID string) error {
	var unresolved int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*)::int
		FROM orders
		WHERE batch_id = $1
		  AND order_status NOT IN ('Completed', 'Cancelled')
	`, batchID).Scan(&unresolved); err != nil {
		return err
	}
	if unresolved > 0 {
		return nil
	}
	var previousStatus string
	if err := tx.QueryRow(ctx, `
		SELECT status::text
		FROM order_batches
		WHERE id = $1
		FOR UPDATE
	`, batchID).Scan(&previousStatus); err != nil {
		return err
	}
	if previousStatus == "completed" {
		return nil
	}
	tag, err := tx.Exec(ctx, `
		UPDATE order_batches
		SET status = 'completed'::batch_status,
			membership_locked = true,
			completed_at = COALESCE(completed_at, now()),
			version = version + 1,
			updated_at = now()
		WHERE id = $1 AND status <> 'completed'::batch_status
	`, batchID)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO batch_events(batch_id, event_type, previous_status, status, notes, metadata)
		VALUES (
			$1, 'all_orders_resolved', $2::batch_status, 'completed',
			'All batch orders resolved',
			jsonb_build_object('source', 'order_completion')
		)
	`, batchID, previousStatus)
	return err
}

// AutoConfirmExpiredDeliveries completes expired deliveries and creates rewards
// transactionally. Conditional updates make it safe across multiple API replicas.
func AutoConfirmExpiredDeliveries(ctx context.Context, db *pgxpool.Pool) (int64, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		UPDATE orders
		SET order_status = 'Completed',
			delivery_confirmed = true,
			confirmed_at = COALESCE(confirmed_at, now()),
			updated_at = now()
		WHERE current_tracking_stage = 'Delivered'::tracking_stage
		  AND delivery_confirmed = false
		  AND delivered_at < now() - interval '3 days'
		RETURNING id, user_id, batch_id
	`)
	if err != nil {
		return 0, err
	}
	type completedOrder struct {
		ID      string
		UserID  string
		BatchID *string
	}
	completed := make([]completedOrder, 0)
	for rows.Next() {
		var order completedOrder
		if err := rows.Scan(&order.ID, &order.UserID, &order.BatchID); err != nil {
			rows.Close()
			return 0, err
		}
		completed = append(completed, order)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	batches := map[string]struct{}{}
	for _, order := range completed {
		if err := createReviewRewardTx(ctx, tx, order.UserID, order.ID); err != nil {
			return 0, err
		}
		if order.BatchID != nil {
			batches[*order.BatchID] = struct{}{}
		}
	}
	for batchID := range batches {
		if err := completeBatchIfResolved(ctx, tx, batchID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int64(len(completed)), nil
}
