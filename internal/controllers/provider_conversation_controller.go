package controllers

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func providerMessageBody(c *fiber.Ctx) (string, error) {
	var req struct {
		Message string `json:"message"`
	}
	if err := c.BodyParser(&req); err != nil {
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid message")
	}
	body := strings.TrimSpace(req.Message)
	if body == "" || len([]rune(body)) > 2000 {
		return "", fiber.NewError(fiber.StatusUnprocessableEntity, "message must be between 1 and 2000 characters")
	}
	return body, nil
}

func (m *ProviderMarketplaceController) StartProviderConversation(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	body, err := providerMessageBody(c)
	if err != nil {
		return err
	}
	var providerID, listingTitle string
	err = m.db.QueryRow(c.Context(), `SELECT l.provider_id::text,l.title
		FROM provider_listings l
		JOIN provider_organizations p ON p.id=l.provider_id
		WHERE l.id=$1::uuid AND l.status='approved' AND p.verification_status='approved' AND p.is_active=true
		  AND ($2::boolean OR EXISTS(SELECT 1 FROM provider_subscriptions s WHERE s.provider_id=p.id AND s.status='active' AND s.current_period_end>now()))`,
		c.Params("listing_id"), !m.subscriptionsRequired()).Scan(&providerID, &listingTitle)
	if err == pgx.ErrNoRows {
		return fiber.NewError(fiber.StatusPaymentRequired, "messaging is available only for verified providers with an active subscription")
	}
	if err != nil {
		return fiber.ErrInternalServerError
	}
	tx, err := m.db.Begin(c.Context())
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer tx.Rollback(c.Context())
	var conversationID string
	err = tx.QueryRow(c.Context(), `INSERT INTO provider_conversations(listing_id,provider_id,user_id,buyer_last_read_at)
		VALUES($1::uuid,$2::uuid,$3::uuid,now())
		ON CONFLICT(listing_id,user_id) DO UPDATE SET status='open',updated_at=now()
		RETURNING id::text`, c.Params("listing_id"), providerID, userID).Scan(&conversationID)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	messageID := uuid.New()
	if _, err = tx.Exec(c.Context(), `INSERT INTO provider_conversation_messages(id,conversation_id,sender_type,sender_user_id,body)
		VALUES($1::uuid,$2::uuid,'buyer',$3::uuid,$4)`, messageID, conversationID, userID, body); err != nil {
		return fiber.ErrInternalServerError
	}
	if _, err = tx.Exec(c.Context(), `UPDATE provider_conversations SET last_message_at=now(),buyer_last_read_at=now(),updated_at=now() WHERE id=$1::uuid`, conversationID); err != nil {
		return fiber.ErrInternalServerError
	}
	if err = tx.Commit(c.Context()); err != nil {
		return fiber.ErrInternalServerError
	}
	m.queueProviderActivity(c.Context(), providerID, c.Params("listing_id"), "conversation_message", "New buyer message", body, "provider-message:"+messageID.String(), map[string]any{"conversation_id": conversationID, "listing_title": listingTitle, "message": body})
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": conversationID, "message_id": messageID})
}

func scanConversationRows(rows pgx.Rows) ([]fiber.Map, error) {
	items := make([]fiber.Map, 0)
	for rows.Next() {
		var id, listingID, title, counterpart, status, lastMessage string
		var lastMessageAt time.Time
		var unread int
		var subscriptionActive bool
		if err := rows.Scan(&id, &listingID, &title, &counterpart, &status, &lastMessage, &lastMessageAt, &unread, &subscriptionActive); err != nil {
			return nil, err
		}
		items = append(items, fiber.Map{"id": id, "listing_id": listingID, "listing_title": title, "counterpart_name": counterpart, "status": status, "last_message": lastMessage, "last_message_at": lastMessageAt, "unread_count": unread, "subscription_active": subscriptionActive})
	}
	return items, rows.Err()
}

func (m *ProviderMarketplaceController) ListBuyerConversations(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	rows, err := m.db.Query(c.Context(), `SELECT c.id::text,l.id::text,l.title,p.business_name,c.status,
		COALESCE((SELECT body FROM provider_conversation_messages WHERE conversation_id=c.id ORDER BY created_at DESC,id DESC LIMIT 1),''),
		c.last_message_at,
		(SELECT count(*)::int FROM provider_conversation_messages WHERE conversation_id=c.id AND sender_type='provider' AND created_at>COALESCE(c.buyer_last_read_at,'epoch'::timestamptz)),
		($2::boolean OR EXISTS(SELECT 1 FROM provider_subscriptions s WHERE s.provider_id=c.provider_id AND s.status='active' AND s.current_period_end>now()))
		FROM provider_conversations c JOIN provider_listings l ON l.id=c.listing_id JOIN provider_organizations p ON p.id=c.provider_id
		WHERE c.user_id=$1::uuid ORDER BY c.last_message_at DESC,c.id DESC LIMIT 100`, userID, !m.subscriptionsRequired())
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer rows.Close()
	items, err := scanConversationRows(rows)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	return c.JSON(fiber.Map{"items": items})
}

func (m *ProviderMarketplaceController) ListProviderConversations(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, _, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.ErrForbidden
	}
	rows, err := m.db.Query(c.Context(), `SELECT c.id::text,l.id::text,l.title,u.full_name,c.status,
		COALESCE((SELECT body FROM provider_conversation_messages WHERE conversation_id=c.id ORDER BY created_at DESC,id DESC LIMIT 1),''),
		c.last_message_at,
		(SELECT count(*)::int FROM provider_conversation_messages WHERE conversation_id=c.id AND sender_type='buyer' AND created_at>COALESCE(c.provider_last_read_at,'epoch'::timestamptz)),
		($2::boolean OR EXISTS(SELECT 1 FROM provider_subscriptions s WHERE s.provider_id=c.provider_id AND s.status='active' AND s.current_period_end>now()))
		FROM provider_conversations c JOIN provider_listings l ON l.id=c.listing_id JOIN users u ON u.id=c.user_id
		WHERE c.provider_id=$1::uuid ORDER BY c.last_message_at DESC,c.id DESC LIMIT 100`, providerID, !m.subscriptionsRequired())
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer rows.Close()
	items, err := scanConversationRows(rows)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	return c.JSON(fiber.Map{"items": items})
}

func (m *ProviderMarketplaceController) conversationMessages(c *fiber.Ctx, provider bool) error {
	userID, _ := c.Locals("user_id").(string)
	var providerID string
	var err error
	if provider {
		providerID, _, err = m.providerForUser(c.Context(), userID)
		if err != nil {
			return fiber.ErrForbidden
		}
	}
	var authorized bool
	if provider {
		err = m.db.QueryRow(c.Context(), `SELECT EXISTS(SELECT 1 FROM provider_conversations WHERE id=$1::uuid AND provider_id=$2::uuid)`, c.Params("conversation_id"), providerID).Scan(&authorized)
	} else {
		err = m.db.QueryRow(c.Context(), `SELECT EXISTS(SELECT 1 FROM provider_conversations WHERE id=$1::uuid AND user_id=$2::uuid)`, c.Params("conversation_id"), userID).Scan(&authorized)
	}
	if err != nil || !authorized {
		return fiber.ErrNotFound
	}
	readColumn := "buyer_last_read_at"
	if provider {
		readColumn = "provider_last_read_at"
	}
	if _, err = m.db.Exec(c.Context(), "UPDATE provider_conversations SET "+readColumn+"=now() WHERE id=$1::uuid", c.Params("conversation_id")); err != nil {
		return fiber.ErrInternalServerError
	}
	rows, err := m.db.Query(c.Context(), `SELECT id::text,sender_type,body,created_at FROM provider_conversation_messages WHERE conversation_id=$1::uuid ORDER BY created_at,id LIMIT 500`, c.Params("conversation_id"))
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer rows.Close()
	items := make([]fiber.Map, 0)
	for rows.Next() {
		var id, senderType, body string
		var createdAt time.Time
		if rows.Scan(&id, &senderType, &body, &createdAt) != nil {
			return fiber.ErrInternalServerError
		}
		items = append(items, fiber.Map{"id": id, "sender_type": senderType, "body": body, "created_at": createdAt})
	}
	return c.JSON(fiber.Map{"items": items})
}

func (m *ProviderMarketplaceController) BuyerConversationMessages(c *fiber.Ctx) error {
	return m.conversationMessages(c, false)
}

func (m *ProviderMarketplaceController) ProviderConversationMessages(c *fiber.Ctx) error {
	return m.conversationMessages(c, true)
}

func (m *ProviderMarketplaceController) sendConversationMessage(c *fiber.Ctx, provider bool) error {
	userID, _ := c.Locals("user_id").(string)
	body, err := providerMessageBody(c)
	if err != nil {
		return err
	}
	var providerID, listingID, listingTitle, buyerID string
	if provider {
		memberProviderID, _, memberErr := m.providerForUser(c.Context(), userID)
		if memberErr != nil {
			return fiber.ErrForbidden
		}
		err = m.db.QueryRow(c.Context(), `SELECT c.provider_id::text,c.listing_id::text,l.title,c.user_id::text
			FROM provider_conversations c JOIN provider_listings l ON l.id=c.listing_id
			WHERE c.id=$1::uuid AND c.provider_id=$2::uuid AND c.status='open'
			  AND ($3::boolean OR EXISTS(SELECT 1 FROM provider_subscriptions s WHERE s.provider_id=c.provider_id AND s.status='active' AND s.current_period_end>now()))`,
			c.Params("conversation_id"), memberProviderID, !m.subscriptionsRequired()).Scan(&providerID, &listingID, &listingTitle, &buyerID)
	} else {
		err = m.db.QueryRow(c.Context(), `SELECT c.provider_id::text,c.listing_id::text,l.title,c.user_id::text
			FROM provider_conversations c JOIN provider_listings l ON l.id=c.listing_id
			WHERE c.id=$1::uuid AND c.user_id=$2::uuid AND c.status='open'
			  AND ($3::boolean OR EXISTS(SELECT 1 FROM provider_subscriptions s WHERE s.provider_id=c.provider_id AND s.status='active' AND s.current_period_end>now()))`,
			c.Params("conversation_id"), userID, !m.subscriptionsRequired()).Scan(&providerID, &listingID, &listingTitle, &buyerID)
	}
	if err == pgx.ErrNoRows {
		return fiber.NewError(fiber.StatusPaymentRequired, "this conversation is unavailable while the provider subscription is inactive")
	}
	if err != nil {
		return fiber.ErrInternalServerError
	}
	senderType := "buyer"
	readColumn := "buyer_last_read_at"
	if provider {
		senderType = "provider"
		readColumn = "provider_last_read_at"
	}
	messageID := uuid.New()
	tx, err := m.db.Begin(c.Context())
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer tx.Rollback(c.Context())
	if _, err = tx.Exec(c.Context(), `INSERT INTO provider_conversation_messages(id,conversation_id,sender_type,sender_user_id,body) VALUES($1::uuid,$2::uuid,$3,$4::uuid,$5)`, messageID, c.Params("conversation_id"), senderType, userID, body); err != nil {
		return fiber.ErrInternalServerError
	}
	if _, err = tx.Exec(c.Context(), "UPDATE provider_conversations SET last_message_at=now(),"+readColumn+"=now(),updated_at=now() WHERE id=$1::uuid", c.Params("conversation_id")); err != nil {
		return fiber.ErrInternalServerError
	}
	if err = tx.Commit(c.Context()); err != nil {
		return fiber.ErrInternalServerError
	}
	if provider {
		_ = CreateNotificationOnce(c.Context(), m.db, buyerID, "", nil, "marketplace_message", "New provider message", body, map[string]any{"conversation_id": c.Params("conversation_id"), "listing_id": listingID, "listing_title": listingTitle}, "buyer-provider-message:"+messageID.String())
	} else {
		m.queueProviderActivity(c.Context(), providerID, listingID, "conversation_message", "New buyer message", body, "provider-message:"+messageID.String(), map[string]any{"conversation_id": c.Params("conversation_id"), "listing_title": listingTitle, "message": body})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": messageID, "sender_type": senderType, "body": body, "created_at": time.Now().UTC()})
}

func (m *ProviderMarketplaceController) SendBuyerConversationMessage(c *fiber.Ctx) error {
	return m.sendConversationMessage(c, false)
}

func (m *ProviderMarketplaceController) SendProviderConversationMessage(c *fiber.Ctx) error {
	return m.sendConversationMessage(c, true)
}
