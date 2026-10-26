package controllers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// Closing the browser is not payment cancellation. Disable the stored hosted
// link first, then verify with the gateway before releasing a pending attempt.
// Keep the reference and ledger so a delayed verified webhook can still settle.
func (p *PaymentController) CancelUnpaidProviderCheckout(c *fiber.Ctx) error {
	user, _ := c.Locals("user_id").(string)
	if user == "" {
		return fiber.ErrUnauthorized
	}
	var req struct {
		TxRef string `json:"tx_ref"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.ErrBadRequest
	}
	ctx, cancel := context.WithTimeout(c.Context(), 40*time.Second)
	defer cancel()
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer tx.Rollback(context.Background())
	var provider string
	err = tx.QueryRow(ctx, `SELECT p.id::text FROM provider_organizations p JOIN provider_members m ON m.provider_id=p.id AND m.user_id=$1::uuid AND m.is_active=true AND m.role='owner' WHERE p.is_active=true ORDER BY p.id LIMIT 1`, user).Scan(&provider)
	if err != nil {
		return fiber.ErrForbidden
	}
	var locked bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('provider-renewal:'||$1,0))`, provider).Scan(&locked); err != nil {
		return fiber.ErrInternalServerError
	}
	if !locked {
		return fiber.NewError(409, "Another billing action is running. Try again shortly.")
	}
	rows, err := tx.Query(ctx, `SELECT s.tx_ref,COALESCE(pay.checkout_url,'') FROM provider_subscriptions s LEFT JOIN payments pay ON pay.provider_subscription_id=s.id AND pay.provider='flutterwave' WHERE s.provider_id=$1::uuid AND s.status='pending' AND COALESCE(to_jsonb(s)->>'billing_provider','flutterwave')='flutterwave' AND ($2='' OR s.tx_ref=$2) ORDER BY s.created_at LIMIT 11`, provider, strings.TrimSpace(req.TxRef))
	if err != nil {
		return fiber.ErrInternalServerError
	}
	type attempt struct{ ref, link string }
	attempts := []attempt{}
	for rows.Next() {
		var a attempt
		if err = rows.Scan(&a.ref, &a.link); err != nil {
			rows.Close()
			return fiber.ErrInternalServerError
		}
		attempts = append(attempts, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if len(attempts) > 10 {
		return fiber.NewError(409, "Contact Support to reconcile your pending checkouts.")
	}
	closer, ok := p.provider.(interface {
		DisableCheckout(context.Context, string) error
	})
	if !ok {
		return fiber.NewError(503, "Checkout cancellation is temporarily unavailable.")
	}
	for _, a := range attempts {
		if a.link == "" {
			return fiber.NewError(409, "The previous checkout is still being checked. Contact Support before paying again.")
		}
		if err = closer.DisableCheckout(ctx, a.link); err != nil {
			return fiber.NewError(503, "Could not confirm checkout cancellation. Check payment status before paying again.")
		}
		verified, verifyErr := p.provider.VerifyPayment(ctx, "", a.ref)
		if verifyErr == nil && successfulFlutterwaveStatus(verified.Status) {
			if verified.Reference != a.ref {
				return fiber.NewError(503, "Payment reference could not be verified.")
			}
			amount, amountErr := amountValue(verified.Amount)
			if amountErr != nil {
				return fiber.ErrServiceUnavailable
			}
			// Settlement uses its own transaction. Release our advisory lock first.
			if err = tx.Commit(ctx); err != nil {
				return fiber.ErrInternalServerError
			}
			if err = settleProviderSubscription(ctx, p.db, a.ref, verified.TransactionID, amount, verified.Currency); err != nil {
				return fiber.NewError(503, "Payment was verified; activation is still being checked. Do not pay again.")
			}
			return c.JSON(fiber.Map{"payment_state": "settled"})
		}
		terminal := verifyErr == nil && verified.Reference == a.ref && (strings.EqualFold(verified.Status, "failed") || strings.EqualFold(verified.Status, "cancelled") || strings.EqualFold(verified.Status, "canceled"))
		if !terminal && !errors.Is(verifyErr, errGatewayPaymentNotFound) {
			return fiber.NewError(409, "Payment is processing or could not be verified. Do not pay again; check its status shortly.")
		}
		// A webhook can win this race. Never replace active/paid access with cancelled.
		tag, updateErr := tx.Exec(ctx, `UPDATE provider_subscriptions SET status='cancelled',updated_at=now() WHERE provider_id=$1::uuid AND tx_ref=$2 AND status='pending'`, provider, a.ref)
		if updateErr != nil {
			return fiber.ErrInternalServerError
		}
		if tag.RowsAffected() == 0 {
			return fiber.NewError(409, "Payment status changed. Refresh billing before continuing.")
		}
		if _, err = tx.Exec(ctx, `UPDATE payments SET payment_status='cancelled',provider_status='checkout_disabled',updated_at=now() WHERE provider='flutterwave' AND purpose='provider_subscription' AND provider_reference=$1 AND payment_status IN ('pending','processing','failed')`, a.ref); err != nil {
			return fiber.ErrInternalServerError
		}
		if _, err = tx.Exec(ctx, `INSERT INTO provider_marketplace_events(provider_id,event_type,metadata) VALUES($1::uuid,'subscription_checkout_cancelled',jsonb_build_object('tx_ref',$2::text))`, provider, a.ref); err != nil {
			return fiber.ErrInternalServerError
		}
	}
	var pending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_subscriptions WHERE provider_id=$1::uuid AND status='pending')`, provider).Scan(&pending); err != nil {
		return fiber.ErrInternalServerError
	}
	if err = tx.Commit(ctx); err != nil {
		return fiber.ErrInternalServerError
	}
	if pending {
		return c.Status(202).JSON(fiber.Map{"payment_state": "pending"})
	}
	return c.JSON(fiber.Map{"payment_state": "cancelled"})
}
