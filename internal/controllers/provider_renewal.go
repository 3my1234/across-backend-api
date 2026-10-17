package controllers

import (
	"context"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// This is a preference for the next checkout, not a mandate to charge a new tier.
// A new tier only becomes an entitlement after its payment is verified.
func (m *ProviderMarketplaceController) ProviderRenewalStatus(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, role, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.ErrForbidden
	}
	var status string
	var nextID, nextName *string
	var changeAt *time.Time
	err = m.db.QueryRow(c.Context(), `SELECT r.renewal_status,r.next_plan_id::text,p.name,CASE WHEN r.change_at IS NULL THEN NULL ELSE GREATEST(r.change_at,(SELECT MAX(s.current_period_end) FROM provider_subscriptions s WHERE s.provider_id=r.provider_id AND s.status='active')) END
 FROM provider_renewal_preferences r LEFT JOIN provider_subscription_plans p ON p.id=r.next_plan_id
 WHERE r.provider_id=$1::uuid AND NOT EXISTS (
 SELECT 1 FROM provider_subscriptions s WHERE s.provider_id=r.provider_id AND s.status='active'
 AND s.starts_at<=now() AND s.current_period_end>now() AND s.created_at>r.updated_at)`, providerID).Scan(&status, &nextID, &nextName, &changeAt)
	if err != nil && err != pgx.ErrNoRows {
		return fiber.ErrInternalServerError
	}
	var currentID *string
	if err = m.db.QueryRow(c.Context(), `SELECT (SELECT plan_id::text FROM provider_subscriptions WHERE provider_id=$1::uuid AND status='active' AND starts_at<=now() AND current_period_end>now() ORDER BY current_period_end DESC LIMIT 1)`, providerID).Scan(&currentID); err != nil {
		return fiber.ErrInternalServerError
	}
	return c.JSON(fiber.Map{"can_manage": role == "owner", "current_plan_id": currentID, "renewal_status": status, "next_plan_id": nextID, "next_plan_name": nextName, "change_at": changeAt})
}

func (m *ProviderMarketplaceController) ProviderManageRenewal(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, role, err := m.providerForUser(c.Context(), userID)
	if err != nil || role != "owner" {
		return fiber.ErrForbidden
	}
	var req struct {
		NextPlanID string `json:"next_plan_id"`
	}
	if err = c.BodyParser(&req); err != nil {
		return fiber.ErrBadRequest
	}
	req.NextPlanID = strings.TrimSpace(req.NextPlanID)
	if req.NextPlanID != "" {
		if _, err = uuid.Parse(req.NextPlanID); err != nil {
			return fiber.ErrBadRequest
		}
	}
	ctx, cancel := context.WithTimeout(c.Context(), 40*time.Second)
	defer cancel()
	conn, err := m.db.Acquire(ctx)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	var locked bool
	defer func() {
		if !locked {
			conn.Release()
			return
		}
		releaseCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if _, unlockErr := conn.Exec(releaseCtx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, "provider-renewal:"+providerID); unlockErr != nil {
			raw := conn.Hijack()
			_ = raw.Close(releaseCtx)
			return
		}
		conn.Release()
	}()
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, "provider-renewal:"+providerID).Scan(&locked); err != nil {
		return fiber.ErrInternalServerError
	}
	if !locked {
		return fiber.NewError(409, "A renewal change is already in progress. Check its status before retrying.")
	}
	var pending bool
	if err = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_subscriptions WHERE provider_id=$1::uuid AND status='pending')`, providerID).Scan(&pending); err != nil {
		return fiber.ErrInternalServerError
	}
	if pending {
		return fiber.NewError(409, "Check your pending payment before changing renewal settings.")
	}
	var paidThrough *time.Time
	if err = conn.QueryRow(ctx, `SELECT MAX(current_period_end) FROM provider_subscriptions WHERE provider_id=$1::uuid AND status='active' AND current_period_end>now()`, providerID).Scan(&paidThrough); err != nil {
		return fiber.ErrInternalServerError
	}
	if req.NextPlanID != "" {
		if paidThrough == nil {
			return fiber.NewError(409, "Your paid period has ended. Choose the plan and complete checkout instead.")
		}
		var valid bool
		if err = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_subscription_plans WHERE id=$1::uuid AND is_active AND amount_ngn>0)`, req.NextPlanID).Scan(&valid); err != nil {
			return fiber.ErrInternalServerError
		}
		if !valid {
			return fiber.NewError(422, "Choose an available paid plan.")
		}
	}
	// Store the request even if the gateway becomes unavailable. Never label an
	// uncertain cancellation as stopped, nor modify the paid subscription status.
	if _, err = conn.Exec(ctx, `INSERT INTO provider_renewal_preferences(provider_id,renewal_status,updated_by)
 VALUES($1::uuid,'uncertain',$2::uuid) ON CONFLICT(provider_id) DO UPDATE SET renewal_status='uncertain',updated_by=EXCLUDED.updated_by,updated_at=now()`, providerID, userID); err != nil {
		return fiber.ErrInternalServerError
	}
	fail := func(message string) error {
		return fiber.NewError(502, message+" Paid access is unchanged. Check renewal status and retry.")
	}
	rows, err := conn.Query(ctx, `SELECT s.id::text,s.customer_email,COALESCE(s.gateway_plan_id,p.flutterwave_plan_id),
 (SELECT receipt.flutterwave_transaction_id FROM provider_subscription_payments receipt WHERE receipt.subscription_id=s.id ORDER BY receipt.paid_at,receipt.flutterwave_transaction_id LIMIT 1)
 FROM provider_subscriptions s JOIN provider_subscription_plans p ON p.id=s.plan_id
 WHERE s.provider_id=$1::uuid AND s.billing_mode='recurring' AND EXISTS(SELECT 1 FROM provider_subscription_payments receipt WHERE receipt.subscription_id=s.id)
 ORDER BY s.id LIMIT 101`, providerID)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	type purchase struct {
		id, email   string
		plan        *int64
		transaction *string
	}
	purchases := []purchase{}
	for rows.Next() {
		var p purchase
		if err = rows.Scan(&p.id, &p.email, &p.plan, &p.transaction); err != nil {
			rows.Close()
			return fiber.ErrInternalServerError
		}
		purchases = append(purchases, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if len(purchases) > 100 {
		return fail("Your billing history needs Support review before cancellation can be confirmed.")
	}
	var untracked bool
	if err = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_subscriptions s WHERE s.provider_id=$1::uuid AND s.billing_mode='recurring' AND s.status='active' AND s.current_period_end>now() AND NOT EXISTS(SELECT 1 FROM provider_subscription_payments receipt WHERE receipt.subscription_id=s.id))`, providerID).Scan(&untracked); err != nil {
		return fiber.ErrInternalServerError
	}
	if untracked {
		return fail("Your recurring payment needs Support review before cancellation can be confirmed.")
	}
	targets := map[int64]gatewaySubscription{}
	for _, p := range purchases {
		if p.plan == nil || p.transaction == nil || strings.TrimSpace(p.email) == "" {
			return fail("A historical recurring payment needs Support review.")
		}
		items, e := m.paymentProvider.ListTransactionSubscriptions(ctx, *p.transaction, p.email)
		if e != nil {
			return fail("Flutterwave renewal status could not be checked.")
		}
		for _, item := range items {
			if item.PlanID != *p.plan || !strings.EqualFold(item.Customer.Email, p.email) {
				return fail("The gateway billing details need Support review.")
			}
			targets[item.ID] = item
		}
	}
	for id := range targets {
		tag, e := conn.Exec(ctx, `INSERT INTO provider_renewal_cancellations(gateway_subscription_id,provider_id,requested_by,status)
  VALUES($1,$2::uuid,$3::uuid,'requested') ON CONFLICT(gateway_subscription_id) DO UPDATE SET status='requested',updated_at=now()
  WHERE provider_renewal_cancellations.provider_id=EXCLUDED.provider_id`, id, providerID, userID)
		if e != nil {
			return fiber.ErrInternalServerError
		}
		if tag.RowsAffected() != 1 {
			return fail("The gateway subscription ownership needs Support review.")
		}
		if err = m.paymentProvider.CancelSubscription(ctx, id); err != nil {
			return fail("Flutterwave did not confirm cancellation.")
		}
	}
	// Verify against the same immutable payment ownership, including duplicates.
	for _, p := range purchases {
		remaining, e := m.paymentProvider.ListTransactionSubscriptions(ctx, *p.transaction, p.email)
		if e != nil || len(remaining) > 0 {
			return fail("Cancellation was requested but Flutterwave has not confirmed that renewal stopped.")
		}
	}
	if _, err = conn.Exec(ctx, `UPDATE provider_renewal_cancellations SET status='confirmed',updated_at=now() WHERE provider_id=$1::uuid AND status<>'confirmed'`, providerID); err != nil {
		return fiber.ErrInternalServerError
	}
	var next any
	var effective any
	if req.NextPlanID != "" {
		next = req.NextPlanID
		effective = paidThrough
	}
	if _, err = conn.Exec(ctx, `UPDATE provider_renewal_preferences SET renewal_status='stopped',next_plan_id=$2::uuid,change_at=$3,updated_at=now() WHERE provider_id=$1::uuid`, providerID, next, effective); err != nil {
		return fiber.ErrInternalServerError
	}
	m.gatewayMu.Lock()
	m.gatewayExpiry = time.Time{}
	m.gatewayMu.Unlock()
	return c.JSON(fiber.Map{"renewal_status": "stopped", "next_plan_id": next, "change_at": effective, "paid_through": paidThrough, "checkout_required": req.NextPlanID != ""})
}
