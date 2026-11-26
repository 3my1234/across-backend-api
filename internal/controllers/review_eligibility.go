package controllers

import (
	"context"
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// answeredInChat: the customer wrote to the business about this item, and the business
// replied at least once. A reply may sit in any chat of the same customer and business
// (one inbox thread covers all their items). The business's own staff never qualify.
const answeredInChatSQL = `EXISTS(SELECT 1 FROM provider_conversations asked WHERE asked.user_id=$1::uuid AND asked.provider_id=$3::uuid AND %s)
 AND EXISTS(SELECT 1 FROM provider_conversations pair JOIN provider_conversation_messages reply ON reply.conversation_id=pair.id WHERE pair.user_id=$1::uuid AND pair.provider_id=$3::uuid AND reply.sender_type='provider')
 AND NOT EXISTS(SELECT 1 FROM provider_members member WHERE member.provider_id=$3::uuid AND member.user_id=$1::uuid AND member.is_active)`

func answeredInChat(ctx context.Context, db rowQuerier, user, item string, product bool) (bool, error) {
	if uuid.Validate(user) != nil || uuid.Validate(item) != nil {
		return false, nil
	}
	owner, about := `SELECT provider_id::text FROM provider_listings WHERE id=$1::uuid`, "asked.listing_id=$2::uuid"
	if product {
		owner, about = `SELECT provider_id::text FROM products WHERE id=$1::uuid`, "asked.product_id=$2::uuid"
	}
	var provider string
	if err := db.QueryRow(ctx, owner, item).Scan(&provider); err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	var ok bool
	err := db.QueryRow(ctx, `SELECT `+fmt.Sprintf(answeredInChatSQL, about), user, item, provider).Scan(&ok)
	return ok, err
}

// MyListingReview tells the app whether this customer may rate the service, and shows
// their own review so that they can change it.
func (m *ProviderMarketplaceController) MyListingReview(c *fiber.Ctx) error {
	c.Set("Cache-Control", "private, no-store")
	user, _ := c.Locals("user_id").(string)
	listing := c.Params("listing_id")
	if uuid.Validate(listing) != nil {
		return fiber.ErrBadRequest
	}
	var rating int
	var text string
	err := m.db.QueryRow(c.Context(), `SELECT rating,review_text FROM provider_listing_reviews WHERE listing_id=$1::uuid AND user_id=$2::uuid ORDER BY (request_id IS NULL) DESC,updated_at DESC LIMIT 1`, listing, user).Scan(&rating, &text)
	if err != nil && err != pgx.ErrNoRows {
		return fiber.ErrServiceUnavailable
	}
	can, checkErr := answeredInChat(c.Context(), m.db, user, listing, false)
	if checkErr != nil {
		return fiber.ErrServiceUnavailable
	}
	var review any
	if err == nil {
		review = fiber.Map{"rating": rating, "review_text": text}
	}
	return c.JSON(fiber.Map{"can_review": can, "review": review})
}
