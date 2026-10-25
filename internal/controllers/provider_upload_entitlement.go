package controllers

import (
	"context"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Upload checks prevent new uploads against an already over-limit listing.
// Legacy clients can prepare media before a draft exists; final attachment is
// always checked atomically by the database, independent of client context.
func (m *ProviderMarketplaceController) checkPostUpload(ctx context.Context, provider, id, kind, purpose string) error {
	policy, err := m.postPolicy(ctx, provider)
	if err != nil {
		return fiber.NewError(503, "Your upload allowance could not be checked. Retry.")
	}
	if purpose == "post_video" && policy.Videos == 0 {
		return fiber.NewError(403, "Upgrade to Diamond or Gold to upload videos.")
	}
	if id == "" {
		return nil
	}
	if uuid.Validate(id) != nil || (kind != "products" && kind != "listings") {
		return fiber.NewError(400, "Choose a valid listing for this upload.")
	}
	query := `SELECT jsonb_array_length(COALESCE(to_jsonb(image_urls),'[]'::jsonb)),jsonb_array_length(COALESCE(factory_details->'video_urls','[]'::jsonb)) FROM products WHERE id=$1::uuid AND provider_id=$2::uuid AND moderation_status<>'archived'`
	if kind == "listings" {
		query = `SELECT jsonb_array_length(COALESCE(to_jsonb(media_urls),'[]'::jsonb)),jsonb_array_length(COALESCE(attributes->'video_urls','[]'::jsonb)) FROM provider_listings WHERE id=$1::uuid AND provider_id=$2::uuid AND status<>'archived'`
	}
	var images, videos int
	err = m.db.QueryRow(ctx, query, id, provider).Scan(&images, &videos)
	if err == pgx.ErrNoRows {
		return fiber.ErrNotFound
	}
	if err != nil {
		return fiber.NewError(503, "This listing's upload allowance could not be checked.")
	}
	if images > policy.Images || videos > policy.Videos {
		return fiber.NewError(409, "This listing keeps its existing media after a downgrade. Remove excess media and save, or upgrade, before uploading more.")
	}
	return nil
}
