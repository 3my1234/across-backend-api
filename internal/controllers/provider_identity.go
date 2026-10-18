package controllers

import (
	"across/backend/internal/storage"
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func productContactRequired() error {
	return fiber.NewError(fiber.StatusForbidden, "Product checkout is no longer available. Message the seller to arrange payment and delivery. Flutterwave is used only for provider subscriptions.")
}

// One bounded query per response, rather than one subscription lookup per card.
// Only public identity fields are attached; eligibility is computed in PostgreSQL.
func providerIdentityJSON(c *fiber.Ctx, db *pgxpool.Pool, payload fiber.Map, assets ...*storage.S3) error {
	targets := map[string][]fiber.Map{}
	var visit func(any)
	visit = func(value any) {
		switch item := value.(type) {
		case fiber.Map:
			id, _ := item["provider_id"].(string)
			if id == "" && item["business_name"] != nil && item["verification_status"] != nil {
				id, _ = item["id"].(string)
			}
			if _, err := uuid.Parse(id); err == nil {
				targets[id] = append(targets[id], item)
			}
			for _, nested := range item {
				visit(nested)
			}
		case []fiber.Map:
			for _, nested := range item {
				visit(nested)
			}
		case []any:
			for _, nested := range item {
				visit(nested)
			}
		}
	}
	visit(payload)
	if len(targets) == 0 {
		return c.JSON(payload)
	}
	ids := make([]string, 0, len(targets))
	for id := range targets {
		ids = append(ids, id)
	}
	rows, err := db.Query(c.Context(), `SELECT i.provider_id::text,i.name,i.avatar_url,i.verified,i.paid_active,i.badge_tier,i.listing_limit,COALESCE(to_jsonb(p)->>'banner_url','') FROM provider_public_identity i JOIN provider_organizations p ON p.id=i.provider_id WHERE i.provider_id=ANY($1::uuid[])`, ids)
	if err != nil {
		return fiber.NewError(503, "Provider information is temporarily unavailable. Please refresh.")
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, avatar, tier, banner string
		var verified, paid bool
		var limit int
		if err := rows.Scan(&id, &name, &avatar, &verified, &paid, &tier, &limit, &banner); err != nil {
			return fiber.ErrInternalServerError
		}
		if avatar != "" && len(assets) > 0 && assets[0] != nil {
			normalizer := &CatalogController{s3: assets[0]}
			urls := normalizer.normalizeImageURLs([]string{avatar})
			if len(urls) > 0 {
				avatar = urls[0]
			}
		}
		if banner != "" && len(assets) > 0 && assets[0] != nil {
			urls := (&CatalogController{s3: assets[0]}).normalizeImageURLs([]string{banner})
			if len(urls) > 0 {
				banner = urls[0]
			}
		}
		label := "Provider"
		if verified {
			label = "Verified provider"
			if !paid {
				label += " - Free plan"
			}
		}
		if tier != "" {
			label += " · " + strings.ToUpper(tier[:1]) + tier[1:] + " subscription"
		}
		identity := fiber.Map{"id": id, "name": name, "avatar_url": avatar, "banner_url": banner, "verified": verified, "paid_active": paid, "tier": tier, "label": label, "listing_limit": limit, "combined_limit": !paid}
		for _, target := range targets[id] {
			target["provider_identity"] = identity
			target["provider_name"] = name
			target["provider_logo_url"] = avatar
			target["provider_banner_url"] = banner
			target["provider_verified"] = verified
			target["provider_badge_tier"] = tier
			if target["sku"] != nil {
				target["payment_mode"] = "contact"
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fiber.ErrInternalServerError
	}
	return c.JSON(payload)
}

func (m *ProviderMarketplaceController) requirePublishingEligibility(ctx context.Context, providerID string) error {
	var eligible bool
	err := m.db.QueryRow(ctx, `SELECT verified FROM provider_public_identity WHERE provider_id=$1::uuid`, providerID).Scan(&eligible)
	if err != nil {
		return fiber.NewError(fiber.StatusForbidden, "Provider document approval is required before publishing.")
	}
	if !eligible {
		return fiber.NewError(fiber.StatusPaymentRequired, "Your business documents must be approved before publishing.")
	}
	return nil
}

func providerPlanTier(code string) string {
	switch code {
	case "provider-monthly":
		return "bronze"
	case "provider-growth":
		return "silver"
	case "provider-business":
		return "gold"
	case "provider-diamond":
		return "diamond"
	}
	return ""
}

func providerCapacityError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23514" && (strings.Contains(pgErr.Message, "listing limit") || strings.Contains(pgErr.Message, "Free allows") || strings.HasPrefix(pgErr.Message, "Your plan") || strings.HasPrefix(pgErr.Message, "Driver verification")) {
		return fiber.NewError(409, pgErr.Message)
	}
	return nil
}
