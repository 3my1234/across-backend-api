package controllers

import (
	"across/backend/internal/playbilling"
	"context"
	"encoding/json"
	"errors"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type PlayBillingController struct {
	db    *pgxpool.Pool
	store *playbilling.Store
	push  *playbilling.PushVerifier
}

func NewPlayBillingController(db *pgxpool.Pool) *PlayBillingController {
	store, _ := playbilling.FromEnvironment(db) // Disabled/misconfigured purchase paths fail closed, without logging credentials.
	var push *playbilling.PushVerifier
	if store != nil {
		push = store.Push
	}
	return &PlayBillingController{db: db, store: store, push: push}
}
func (p *PlayBillingController) owner(c *fiber.Ctx) (string, error) {
	c.Set("Cache-Control", "private, no-store")
	user, _ := c.Locals("user_id").(string)
	if user == "" {
		return "", fiber.ErrUnauthorized
	}
	var provider string
	err := p.db.QueryRow(c.Context(), `SELECT o.id::text FROM provider_organizations o JOIN provider_members m ON m.provider_id=o.id JOIN users u ON u.id=m.user_id WHERE m.user_id=$1::uuid AND m.role='owner' AND m.is_active AND o.is_active AND u.is_active AND u.email_verified`, user).Scan(&provider)
	if err != nil {
		return "", fiber.ErrForbidden
	}
	return provider, nil
}
func (p *PlayBillingController) Catalog(c *fiber.Ctx) error {
	provider, err := p.owner(c)
	if err != nil {
		return err
	}
	if p.store == nil {
		return fiber.NewError(503, "Google Play billing is not configured yet")
	}
	items, err := p.store.Catalog(c.Context(), provider)
	if err != nil {
		return fiber.NewError(503, "Google Play plans could not load")
	}
	// Benefits describe the existing plan ID; never change Play product IDs/offers.
	for index := range items {
		item := &items[index]
		var code string
		if err = p.db.QueryRow(c.Context(), `SELECT code,listing_limit,amount_ngn FROM provider_subscription_plans WHERE id=$1::uuid`, item.PlanID).Scan(&code, &item.ListingLimit, &item.MonthlyAmount); err != nil {
			return fiber.NewError(503, "Google Play plan benefits could not load")
		}
		item.BadgeTier = providerPlanTier(code)
		item.PostPolicy, _ = json.Marshal(planPostPolicy(code, item.ListingLimit))
	}
	var status, state string
	var expires *time.Time
	var auto, ack bool
	err = p.db.QueryRow(c.Context(), `SELECT s.status,p.state,s.current_period_end,p.auto_renews,p.needs_acknowledgement FROM play_provider_purchases p JOIN provider_subscriptions s ON s.id=p.subscription_id WHERE p.provider_id=$1::uuid ORDER BY p.checked_at DESC LIMIT 1`, provider).Scan(&status, &state, &expires, &auto, &ack)
	// No purchase is a normal empty status; query failures are not a free plan.
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fiber.NewError(503, "Google Play billing status could not load")
	}
	return c.JSON(fiber.Map{"items": items, "account_id": playbilling.AccountID(provider), "status": status, "state": state, "expires_at": expires, "auto_renews": auto, "needs_acknowledgement": ack, "manage_url": "https://play.google.com/store/account/subscriptions?package=" + playbilling.PackageName})
}
func (p *PlayBillingController) Verify(c *fiber.Ctx) error {
	provider, err := p.owner(c)
	if err != nil {
		return err
	}
	if p.store == nil {
		return fiber.NewError(503, "Google Play billing is not configured yet")
	}
	var input struct {
		Token   string `json:"purchase_token"`
		Product string `json:"product_id"`
		Base    string `json:"base_plan_id"`
	}
	if len(c.Body()) > 8192 || c.BodyParser(&input) != nil {
		return fiber.ErrBadRequest
	}
	ctx, cancel := context.WithTimeout(c.Context(), 30*time.Second)
	defer cancel()
	result, err := p.store.Process(ctx, provider, input.Token, input.Product, input.Base)
	if errors.Is(err, playbilling.ErrBusy) {
		c.Set("Retry-After", "2")
		return fiber.NewError(409, "Billing is being checked. Restore purchases before paying again")
	}
	if err != nil {
		return fiber.NewError(422, "Purchase could not be confirmed. Restore purchases or contact Support before paying again")
	}
	return c.JSON(result)
}

func (p *PlayBillingController) Prepare(c *fiber.Ctx) error {
	provider, err := p.owner(c)
	if err != nil {
		return err
	}
	if p.store == nil {
		return fiber.NewError(503, "Google Play billing is not configured yet")
	}
	var input struct {
		Product string `json:"product_id"`
		Base    string `json:"base_plan_id"`
	}
	if len(c.Body()) > 2048 || c.BodyParser(&input) != nil {
		return fiber.ErrBadRequest
	}
	if err = p.store.Prepare(c.Context(), provider, input.Product, input.Base); err != nil {
		return fiber.NewError(409, "Check your current or pending subscription before starting another purchase. If checkout was closed, wait 15 minutes or restore purchases")
	}
	return c.JSON(fiber.Map{"account_id": playbilling.AccountID(provider), "product_id": input.Product, "base_plan_id": input.Base})
}
