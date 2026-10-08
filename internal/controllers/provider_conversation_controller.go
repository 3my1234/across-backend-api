package controllers

import (
	"context"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A booking/enquiry is also a contact. Link its chat atomically so older apps
// see the thread without needing a second client request or a new APK.
func linkRequestConversation(ctx context.Context, tx pgx.Tx, requestID string) (string, error) {
	var conversationID string
	err := tx.QueryRow(ctx, `INSERT INTO provider_conversations(listing_id,provider_id,user_id,last_message_at)
	 SELECT listing_id,provider_id,user_id,created_at FROM provider_requests WHERE id=$1::uuid
	 ON CONFLICT(listing_id,user_id) DO UPDATE SET last_message_at=GREATEST(provider_conversations.last_message_at,EXCLUDED.last_message_at),updated_at=now()
	 RETURNING id::text`, requestID).Scan(&conversationID)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO provider_conversation_messages(conversation_id,sender_type,sender_user_id,body,created_at,source_request_id)
	 SELECT $2::uuid,'buyer',user_id,left(initcap(request_type)||' request'||CASE WHEN btrim(message)<>'' THEN ': '||btrim(message) ELSE '' END,2000),created_at,id
	 FROM provider_requests WHERE id=$1::uuid
	 ON CONFLICT(source_request_id) WHERE source_request_id IS NOT NULL DO NOTHING`, requestID, conversationID)
	return conversationID, err
}

func (m *ProviderMarketplaceController) StartProviderConversation(c *fiber.Ctx) error {
	return m.startConversation(c, false)
}
func (m *ProviderMarketplaceController) StartProductConversation(c *fiber.Ctx) error {
	return m.startConversation(c, true)
}
func (m *ProviderMarketplaceController) startConversation(c *fiber.Ctx, product bool) error {
	userID, _ := c.Locals("user_id").(string)
	if product {
		if _, err := uuid.Parse(c.Params("product_id")); err != nil {
			return fiber.NewError(400, "invalid product_id")
		}
	}
	message, err := m.parseConversationMessage(c)
	if err != nil {
		return err
	}
	body := message.Body
	var providerID, listingTitle string
	if !product {
		err = m.db.QueryRow(c.Context(), `SELECT l.provider_id::text,l.title
		FROM provider_listings l
		JOIN provider_organizations p ON p.id=l.provider_id
		WHERE l.id=$1::uuid AND l.status='approved' AND p.verification_status='approved' AND p.is_active=true
		  AND ($2::boolean OR EXISTS(SELECT 1 FROM provider_subscriptions s WHERE s.provider_id=p.id AND s.status='active' AND s.current_period_end>now()))`,
			c.Params("listing_id"), !m.subscriptionsRequired()).Scan(&providerID, &listingTitle)
	}
	if product {
		err = m.db.QueryRow(c.Context(), `SELECT pr.provider_id::text,pr.title FROM products pr JOIN provider_organizations p ON p.id=pr.provider_id
 WHERE pr.id=$1::uuid AND pr.is_active AND pr.moderation_status='approved' AND pr.payment_mode IN ('contact','both') AND p.verification_status='approved' AND p.is_active
 AND ($2::boolean OR EXISTS(SELECT 1 FROM provider_subscriptions s WHERE s.provider_id=p.id AND s.status='active' AND s.current_period_end>now()))`, c.Params("product_id"), !m.subscriptionsRequired()).Scan(&providerID, &listingTitle)
	}
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
	if !product {
		err = tx.QueryRow(c.Context(), `INSERT INTO provider_conversations(listing_id,provider_id,user_id,buyer_last_read_at)
		VALUES($1::uuid,$2::uuid,$3::uuid,now())
		ON CONFLICT(listing_id,user_id) DO UPDATE SET status='open',updated_at=now() WHERE provider_conversations.status<>'blocked'
		RETURNING id::text`, c.Params("listing_id"), providerID, userID).Scan(&conversationID)
	}
	if product {
		err = tx.QueryRow(c.Context(), `INSERT INTO provider_conversations(product_id,provider_id,user_id,buyer_last_read_at) VALUES($1::uuid,$2::uuid,$3::uuid,now())
 ON CONFLICT(product_id,user_id) WHERE product_id IS NOT NULL DO UPDATE SET status='open',updated_at=now() WHERE provider_conversations.status<>'blocked' RETURNING id::text`, c.Params("product_id"), providerID, userID).Scan(&conversationID)
	}
	if err == pgx.ErrNoRows {
		return fiber.NewError(409, "This conversation is blocked")
	}
	if err != nil {
		return fiber.ErrInternalServerError
	}
	messageID, savedAt, fresh, err := insertConversationMessage(c.Context(), tx, conversationID, "buyer", userID, message)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(c.Context(), `UPDATE provider_conversations SET last_message_at=GREATEST(last_message_at,$2::timestamptz),buyer_last_read_at=now(),updated_at=now() WHERE id=$1::uuid`, conversationID, savedAt); err != nil {
		return fiber.ErrInternalServerError
	}
	if err = tx.Commit(c.Context()); err != nil {
		return fiber.ErrInternalServerError
	}
	notifyCtx, cancelNotify := context.WithTimeout(c.Context(), 2*time.Second)
	defer cancelNotify()
	if fresh {
		m.queueProviderActivity(notifyCtx, providerID, c.Params("listing_id"), "conversation_message", "New buyer message", body, "provider-message:"+messageID.String(), map[string]any{"conversation_id": conversationID, "listing_title": listingTitle, "product_id": c.Params("product_id"), "message": body})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": conversationID, "message_id": messageID, "body": body, "created_at": savedAt, "media_urls": m.chatMediaURLs(message.MediaKeys)})
}

func scanConversationRows(rows pgx.Rows) ([]fiber.Map, error) {
	items := make([]fiber.Map, 0)
	for rows.Next() {
		var id, listingID, title, counterpart, status, lastMessage, productID string
		var lastMessageAt time.Time
		var unread int
		var subscriptionActive bool
		if err := rows.Scan(&id, &listingID, &title, &counterpart, &status, &lastMessage, &lastMessageAt, &unread, &subscriptionActive, &productID); err != nil {
			return nil, err
		}
		items = append(items, fiber.Map{"id": id, "listing_id": listingID, "product_id": productID, "listing_title": title, "counterpart_name": counterpart, "status": status, "last_message": lastMessage, "last_message_at": lastMessageAt, "unread_count": unread, "subscription_active": subscriptionActive})
	}
	return items, rows.Err()
}

func conversationListPage(c *fiber.Ctx) (adminPageRequest, error) {
	page, err := parseAdminPage(c)
	if page.CursorID != "" {
		if _, parseErr := uuid.Parse(page.CursorID); parseErr != nil {
			return page, fiber.NewError(400, "invalid cursor")
		}
	}
	if c.Query("limit") == "" {
		page.Limit = 100
	}
	return page, err
}

func conversationListResponse(c *fiber.Ctx, items []fiber.Map, page adminPageRequest) error {
	next := ""
	if len(items) > page.Limit {
		items = items[:page.Limit]
		last := items[len(items)-1]
		next = encodeAdminCursor(last["last_message_at"].(time.Time), last["id"].(string))
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"items": items, "next_cursor": next, "has_more": next != ""})
}

func (m *ProviderMarketplaceController) ListBuyerConversations(c *fiber.Ctx) error {
	page, err := conversationListPage(c)
	if err != nil {
		return err
	}
	userID, _ := c.Locals("user_id").(string)
	productID := strings.TrimSpace(c.Query("product_id"))
	if productID != "" {
		if _, err := uuid.Parse(productID); err != nil {
			return fiber.NewError(400, "invalid product_id")
		}
	}
	listingID := strings.TrimSpace(c.Query("listing_id"))
	if listingID != "" {
		if _, err := uuid.Parse(listingID); err != nil {
			return fiber.NewError(400, "invalid listing_id")
		}
	}
	c.Set("Cache-Control", "private, no-store")
	rows, err := m.db.Query(c.Context(), `SELECT c.id::text,COALESCE(l.id::text,''),COALESCE(l.title,pr.title),p.business_name,c.status,
		COALESCE((SELECT body FROM provider_conversation_messages WHERE conversation_id=c.id ORDER BY created_at DESC,id DESC LIMIT 1),''),
		c.last_message_at,
		(SELECT count(*)::int FROM provider_conversation_messages WHERE conversation_id=c.id AND sender_type='provider' AND created_at>COALESCE(c.buyer_last_read_at,'epoch'::timestamptz)),
		($2::boolean OR EXISTS(SELECT 1 FROM provider_subscriptions s WHERE s.provider_id=c.provider_id AND s.status='active' AND s.current_period_end>now())),COALESCE(c.product_id::text,'')
		FROM provider_conversations c LEFT JOIN provider_listings l ON l.id=c.listing_id LEFT JOIN products pr ON pr.id=c.product_id JOIN provider_organizations p ON p.id=c.provider_id
		WHERE c.user_id=$1::uuid AND ($7='' OR c.product_id=NULLIF($7,'')::uuid) AND ($3='' OR l.id=NULLIF($3,'')::uuid) AND ($4::timestamptz IS NULL OR (c.last_message_at,c.id)<($4::timestamptz,NULLIF($5,'')::uuid)) ORDER BY c.last_message_at DESC,c.id DESC LIMIT $6`, userID, !m.subscriptionsRequired(), listingID, page.CursorTime, page.CursorID, page.Limit+1, productID)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer rows.Close()
	items, err := scanConversationRows(rows)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	return conversationListResponse(c, items, page)
}

func (m *ProviderMarketplaceController) ListProviderConversations(c *fiber.Ctx) error {
	page, err := conversationListPage(c)
	if err != nil {
		return err
	}
	userID, _ := c.Locals("user_id").(string)
	providerID, _, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.ErrForbidden
	}
	rows, err := m.db.Query(c.Context(), `SELECT c.id::text,COALESCE(l.id::text,''),COALESCE(l.title,pr.title),u.full_name,c.status,
		COALESCE((SELECT body FROM provider_conversation_messages WHERE conversation_id=c.id ORDER BY created_at DESC,id DESC LIMIT 1),''),
		c.last_message_at,
		(SELECT count(*)::int FROM provider_conversation_messages WHERE conversation_id=c.id AND sender_type='buyer' AND created_at>COALESCE(c.provider_last_read_at,'epoch'::timestamptz)),
		($2::boolean OR EXISTS(SELECT 1 FROM provider_subscriptions s WHERE s.provider_id=c.provider_id AND s.status='active' AND s.current_period_end>now())),COALESCE(c.product_id::text,'')
		FROM provider_conversations c LEFT JOIN provider_listings l ON l.id=c.listing_id LEFT JOIN products pr ON pr.id=c.product_id JOIN users u ON u.id=c.user_id
		WHERE c.provider_id=$1::uuid AND ($3::timestamptz IS NULL OR (c.last_message_at,c.id)<($3::timestamptz,NULLIF($4,'')::uuid)) ORDER BY c.last_message_at DESC,c.id DESC LIMIT $5`, providerID, !m.subscriptionsRequired(), page.CursorTime, page.CursorID, page.Limit+1)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer rows.Close()
	items, err := scanConversationRows(rows)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	return conversationListResponse(c, items, page)
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
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	var cursorID any
	if page.CursorTime != nil {
		if _, err := uuid.Parse(page.CursorID); err != nil {
			return fiber.NewError(400, "invalid cursor")
		}
		cursorID = page.CursorID
	}
	c.Set("Cache-Control", "private, no-store")
	rows, err := m.db.Query(c.Context(), `SELECT id::text,sender_type,body,created_at,media_keys FROM provider_conversation_messages WHERE conversation_id=$1::uuid AND ($2::timestamptz IS NULL OR (created_at,id)<($2,$3::uuid)) ORDER BY created_at DESC,id DESC LIMIT $4`, c.Params("conversation_id"), page.CursorTime, cursorID, page.Limit+1)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer rows.Close()
	items := make([]fiber.Map, 0)
	for rows.Next() {
		var id, senderType, body string
		var createdAt time.Time
		var mediaKeys []string
		if rows.Scan(&id, &senderType, &body, &createdAt, &mediaKeys) != nil {
			return fiber.ErrInternalServerError
		}
		items = append(items, fiber.Map{"id": id, "sender_type": senderType, "body": body, "created_at": createdAt, "media_urls": m.chatMediaURLs(mediaKeys)})
	}
	if rows.Err() != nil {
		return fiber.ErrInternalServerError
	}
	cursor := ""
	if len(items) > page.Limit {
		items = items[:page.Limit]
		last := items[len(items)-1]
		cursor = encodeAdminCursor(last["created_at"].(time.Time), last["id"].(string))
	}
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
	return c.JSON(fiber.Map{"items": items, "next_cursor": cursor, "has_more": cursor != ""})
}

func (m *ProviderMarketplaceController) BuyerConversationMessages(c *fiber.Ctx) error {
	return m.conversationMessages(c, false)
}

func (m *ProviderMarketplaceController) ProviderConversationMessages(c *fiber.Ctx) error {
	return m.conversationMessages(c, true)
}

func (m *ProviderMarketplaceController) sendConversationMessage(c *fiber.Ctx, provider bool) error {
	userID, _ := c.Locals("user_id").(string)
	message, err := m.parseConversationMessage(c)
	if err != nil {
		return err
	}
	body := message.Body
	var providerID, listingID, listingTitle, buyerID, productID string
	if provider {
		memberProviderID, _, memberErr := m.providerForUser(c.Context(), userID)
		if memberErr != nil {
			return fiber.ErrForbidden
		}
		err = m.db.QueryRow(c.Context(), `SELECT c.provider_id::text,COALESCE(c.listing_id::text,''),COALESCE(l.title,pr.title),c.user_id::text,COALESCE(c.product_id::text,'')
			FROM provider_conversations c LEFT JOIN provider_listings l ON l.id=c.listing_id LEFT JOIN products pr ON pr.id=c.product_id
			WHERE c.id=$1::uuid AND c.provider_id=$2::uuid AND c.status='open'
			  AND ($3::boolean OR EXISTS(SELECT 1 FROM provider_subscriptions s WHERE s.provider_id=c.provider_id AND s.status='active' AND s.current_period_end>now()))`,
			c.Params("conversation_id"), memberProviderID, !m.subscriptionsRequired()).Scan(&providerID, &listingID, &listingTitle, &buyerID, &productID)
	} else {
		err = m.db.QueryRow(c.Context(), `SELECT c.provider_id::text,COALESCE(c.listing_id::text,''),COALESCE(l.title,pr.title),c.user_id::text,COALESCE(c.product_id::text,'')
			FROM provider_conversations c LEFT JOIN provider_listings l ON l.id=c.listing_id LEFT JOIN products pr ON pr.id=c.product_id
			WHERE c.id=$1::uuid AND c.user_id=$2::uuid AND c.status='open'
			  AND ($3::boolean OR EXISTS(SELECT 1 FROM provider_subscriptions s WHERE s.provider_id=c.provider_id AND s.status='active' AND s.current_period_end>now()))`,
			c.Params("conversation_id"), userID, !m.subscriptionsRequired()).Scan(&providerID, &listingID, &listingTitle, &buyerID, &productID)
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
	tx, err := m.db.Begin(c.Context())
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer tx.Rollback(c.Context())
	messageID, savedAt, fresh, err := insertConversationMessage(c.Context(), tx, c.Params("conversation_id"), senderType, userID, message)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(c.Context(), "UPDATE provider_conversations SET last_message_at=GREATEST(last_message_at,$2::timestamptz),"+readColumn+"=now(),updated_at=now() WHERE id=$1::uuid", c.Params("conversation_id"), savedAt); err != nil {
		return fiber.ErrInternalServerError
	}
	if err = tx.Commit(c.Context()); err != nil {
		return fiber.ErrInternalServerError
	}
	notifyCtx, cancelNotify := context.WithTimeout(c.Context(), 2*time.Second)
	defer cancelNotify()
	if fresh && provider {
		_ = CreateNotificationOnce(notifyCtx, m.db, buyerID, "", nil, "marketplace_message", "New provider message", body, map[string]any{"conversation_id": c.Params("conversation_id"), "listing_id": listingID, "product_id": productID, "listing_title": listingTitle}, "buyer-provider-message:"+messageID.String())
	} else if fresh {
		m.queueProviderActivity(notifyCtx, providerID, listingID, "conversation_message", "New buyer message", body, "provider-message:"+messageID.String(), map[string]any{"conversation_id": c.Params("conversation_id"), "listing_title": listingTitle, "product_id": productID, "message": body})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": messageID, "sender_type": senderType, "body": body, "created_at": savedAt, "media_urls": m.chatMediaURLs(message.MediaKeys)})
}

func (m *ProviderMarketplaceController) SendBuyerConversationMessage(c *fiber.Ctx) error {
	return m.sendConversationMessage(c, false)
}

func (m *ProviderMarketplaceController) SendProviderConversationMessage(c *fiber.Ctx) error {
	return m.sendConversationMessage(c, true)
}
