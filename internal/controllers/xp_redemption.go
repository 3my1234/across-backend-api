package controllers

import (
	"context"
	"errors"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"math"
)

const xpUsage = " 1 XP = NGN 1. Withdraw at least 1,000 XP to your Nigerian bank account after admin review. Existing points remain eligible. XP cannot be used at checkout."

func xpDiscount(points int, fee float64, currency string) int {
	if currency != "NGN" || points <= 0 || math.IsNaN(fee) || math.IsInf(fee, 0) || fee < 1 {
		return 0
	}
	cap := int(math.Floor(roundMoney(fee)))
	if points < cap {
		return points
	}
	return cap
}

// All reservation/attempt transitions serialize per buyer, then per order.
func lockXPUser(ctx context.Context, tx pgx.Tx, userID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('xp:' || $1::text,0))`, userID)
	return err
}

func releaseUnusedXP(ctx context.Context, tx pgx.Tx, userID string, replace bool) error {
	// An initialized/ambiguous gateway charge must never lose its reservation.
	_, err := tx.Exec(ctx, `UPDATE xp_redemptions r SET status='released',updated_at=now()
 WHERE user_id=$1 AND status='reserved' AND ($2::boolean OR expires_at<=now())
 AND NOT EXISTS(SELECT 1 FROM payments p WHERE p.order_id=r.order_id)`, userID, replace)
	return err
}

func availableXP(ctx context.Context, tx pgx.Tx, userID string) (int, int, error) {
	var total, reserved int
	err := tx.QueryRow(ctx, `SELECT
 COALESCE((SELECT SUM(amount) FROM xp_transactions WHERE user_id=$1),0)::int,
 COALESCE((SELECT SUM(points) FROM xp_redemptions WHERE user_id=$1 AND status='reserved'),0)::int`, userID).Scan(&total, &reserved)
	return total - reserved, reserved, err
}

func validateXPReservation(ctx context.Context, tx pgx.Tx, orderID string) error {
	var discount int
	if err := tx.QueryRow(ctx, `SELECT COALESCE((to_jsonb(o)->>'xp_discount')::int,0) FROM orders o WHERE id=$1 FOR UPDATE`, orderID).Scan(&discount); err != nil {
		return err
	}
	if discount == 0 {
		return nil
	}
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM xp_redemptions r WHERE order_id=$1 AND status='reserved'
 AND (expires_at>now() OR EXISTS(SELECT 1 FROM payments p WHERE p.order_id=r.order_id)))`, orderID).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return fiber.NewError(409, "XP quote expired or replaced. Review your total again.")
	}
	return nil
}

func consumeXP(ctx context.Context, tx pgx.Tx, orderID string) error {
	// Old orders remain payable before migration 059; only discounted orders need it.
	var discount int
	if err := tx.QueryRow(ctx, `SELECT COALESCE((to_jsonb(o)->>'xp_discount')::int,0) FROM orders o WHERE id=$1`, orderID).Scan(&discount); err != nil {
		return err
	}
	if discount == 0 {
		return nil
	}
	var userID string
	var points int
	err := tx.QueryRow(ctx, `UPDATE xp_redemptions SET status='consumed',updated_at=now()
 WHERE order_id=$1 AND status='reserved' RETURNING user_id::text,points`, orderID).Scan(&userID, &points)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("XP reservation unavailable for paid order")
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO xp_transactions(user_id,amount,reason,reference_id)
 VALUES($1,-$2::integer,'service_fee_redemption',$3) ON CONFLICT DO NOTHING`, userID, points, "xp-order:"+orderID)
	return err
}

func (o *OrderController) ReleaseXPQuote(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	tx, err := o.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())
	if err = lockXPUser(c.Context(), tx, userID); err != nil {
		return err
	}
	var status string
	if err = tx.QueryRow(c.Context(), `SELECT order_status::text FROM orders WHERE id=$1 AND user_id=$2 FOR UPDATE`, c.Params("order_id"), userID).Scan(&status); err != nil {
		return fiber.NewError(404, "Order not found")
	}
	var started bool
	if err = tx.QueryRow(c.Context(), `SELECT EXISTS(SELECT 1 FROM payments WHERE order_id=$1)`, c.Params("order_id")).Scan(&started); err != nil {
		return err
	}
	if started || status != "Pending" {
		return fiber.NewError(409, "Payment has started. Check payment status before changing rewards.")
	}
	if _, err = tx.Exec(c.Context(), `UPDATE xp_redemptions SET status='released',updated_at=now() WHERE order_id=$1 AND user_id=$2 AND status='reserved'`, c.Params("order_id"), userID); err != nil {
		return err
	}
	if err = tx.Commit(c.Context()); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"released": true})
}
