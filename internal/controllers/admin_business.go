package controllers

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// Match permanent Free/paid-access policy without evaluating the identity view
// once per provider. One provider counts once even with overlapping paid periods.
const adminPaidAccessSQL = `WITH paid_access AS (
 SELECT DISTINCT ON (s.provider_id) s.id,s.provider_id,s.plan_id,s.current_period_end
 FROM provider_subscriptions s JOIN provider_organizations p ON p.id=s.provider_id AND p.is_active
 WHERE s.status='active' AND s.current_period_end>now() AND (s.starts_at IS NULL OR s.starts_at<=now())
 AND EXISTS(SELECT 1 FROM provider_subscription_payments pay WHERE pay.subscription_id=s.id AND pay.currency_code='NGN' AND pay.amount>0)
 ORDER BY s.provider_id,s.current_period_end DESC,s.created_at DESC,s.id DESC
) `

func (a *AdminController) BusinessOverview(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.UserContext(), 5*time.Second)
	defer cancel()
	var raw json.RawMessage
	err := a.db.QueryRow(ctx, adminPaidAccessSQL+`SELECT jsonb_build_object(
 'generated_at',now(),
 'active_accounts',(SELECT count(*) FROM users WHERE is_active),
 'active_providers',(SELECT count(*) FROM provider_organizations WHERE is_active),
 'verified_providers',(SELECT count(*) FROM provider_organizations WHERE is_active AND verification_status='approved'),
 'paid_providers',(SELECT count(*) FROM paid_access),
 'expiring_providers',(SELECT count(*) FROM paid_access WHERE current_period_end<=now()+interval '7 days'),
 'pending_providers',(SELECT count(*) FROM provider_organizations WHERE is_active AND verification_status='pending'),
 'pending_services',(SELECT count(*) FROM provider_listings WHERE status='pending'),
 'pending_products',(SELECT count(*) FROM products WHERE provider_id IS NOT NULL AND moderation_status='pending'),
 'open_support',(SELECT count(*) FROM support_tickets WHERE status='open'),
 'open_reports',(SELECT count(*) FROM chat_reports WHERE state='open'),
 'unresolved_complaints',(SELECT count(*) FROM complaints WHERE status='unresolved')
 )`).Scan(&raw)
	if err != nil {
		return fiber.NewError(503, "Business overview could not finish. Please retry.")
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(raw)
}

// This directory describes paid app access, not gateway renewal registrations.
func (a *AdminController) Subscribers(c *fiber.Ctx) error {
	limit := c.QueryInt("limit", 25)
	if limit < 1 || limit > 100 {
		return fiber.NewError(400, "limit must be between 1 and 100")
	}
	search := strings.TrimSpace(c.Query("search"))
	if len(search) > 120 {
		return fiber.NewError(400, "search is too long")
	}
	status := c.Query("status", "active")
	if status != "active" && status != "expiring" {
		return fiber.NewError(400, "invalid subscriber status")
	}
	cursorTime, cursorID, err := decodeMarketplaceCursor(c.Query("cursor"))
	if err != nil {
		return fiber.NewError(400, "invalid cursor")
	}
	if cursorID != nil {
		if _, err = uuid.Parse(cursorID.(string)); err != nil {
			return fiber.NewError(400, "invalid cursor")
		}
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), 5*time.Second)
	defer cancel()
	rows, err := a.db.Query(ctx, adminPaidAccessSQL+`SELECT p.id::text,p.business_name,p.contact_email,p.verification_status,plan.name,s.current_period_end,s.id::text,s.billing_mode,s.duration_months,s.last_payment_at
 FROM paid_access access JOIN provider_subscriptions s ON s.id=access.id
 JOIN provider_organizations p ON p.id=access.provider_id JOIN provider_subscription_plans plan ON plan.id=access.plan_id
 WHERE ($1='' OR p.business_name ILIKE '%'||$1||'%' OR p.contact_email ILIKE '%'||$1||'%' OR plan.name ILIKE '%'||$1||'%')
 AND ($2='active' OR s.current_period_end<=now()+interval '7 days')
 AND ($3::timestamptz IS NULL OR (s.current_period_end,s.id)>($3,$4::uuid))
 ORDER BY s.current_period_end,s.id LIMIT $5`, search, status, cursorTime, cursorID, limit+1)
	if err != nil {
		return fiber.NewError(503, "Subscribers could not load. Please retry.")
	}
	defer rows.Close()
	items := []fiber.Map{}
	var lastTime time.Time
	var lastID, next string
	for rows.Next() {
		var provider, name, email, verification, plan, id, mode string
		var end time.Time
		var months int
		var lastPaid *time.Time
		if err = rows.Scan(&provider, &name, &email, &verification, &plan, &end, &id, &mode, &months, &lastPaid); err != nil {
			return fiber.NewError(503, "Subscribers could not load. Please retry.")
		}
		if len(items) == limit {
			next = encodeMarketplaceCursor(lastTime, lastID)
			break
		}
		items = append(items, fiber.Map{"provider_id": provider, "business_name": name, "contact_email": email, "verification_status": verification, "plan_name": plan, "ends_at": end, "subscription_id": id, "billing_mode": mode, "duration_months": months, "last_payment_at": lastPaid})
		lastTime, lastID = end, id
	}
	if rows.Err() != nil {
		return fiber.NewError(503, "Subscribers could not load. Please retry.")
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"items": items, "has_more": next != "", "next_cursor": next})
}
