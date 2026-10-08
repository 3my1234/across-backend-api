package controllers

import (
	"context"
	"net/http"
	"path"
	"strings"
	"time"

	"across/backend/internal/storage"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type conversationMessageInput struct {
	Body      string   `json:"message"`
	MediaKeys []string `json:"media_keys"`
	ClientID  string   `json:"client_message_id"`
}

func (m *ProviderMarketplaceController) PresignChatImage(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	var req struct {
		Filename string `json:"filename"`
		Mime     string `json:"mime_type"`
		Size     int64  `json:"size"`
	}
	if c.BodyParser(&req) != nil {
		return fiber.ErrBadRequest
	}
	if !supportedPortableImageMime(req.Mime) || req.Size <= 0 || req.Size > 5<<20 {
		return fiber.NewError(422, "Choose a JPG, PNG or WebP image no larger than 5 MB")
	}
	extension := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}[req.Mime]
	key := storage.SafeKey("user-uploads/private-chat/"+userID, "photo"+extension)
	if m.s3 == nil {
		return fiber.NewError(503, "Image uploads are unavailable")
	}
	url, err := m.s3.PresignPut(key, req.Mime, 10*time.Minute)
	if err != nil {
		return fiber.NewError(503, "Image uploads are unavailable")
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"upload_url": url, "key": key})
}

func (m *ProviderMarketplaceController) parseConversationMessage(c *fiber.Ctx) (conversationMessageInput, error) {
	var req conversationMessageInput
	if c.BodyParser(&req) != nil {
		return req, fiber.NewError(400, "invalid message")
	}
	req.Body = strings.TrimSpace(req.Body)
	if len([]rune(req.Body)) > 2000 || (req.Body == "" && len(req.MediaKeys) == 0) || len(req.MediaKeys) > 4 {
		return req, fiber.NewError(422, "Send a message of up to 2000 characters or up to four images")
	}
	if req.ClientID != "" {
		if _, err := uuid.Parse(req.ClientID); err != nil {
			return req, fiber.NewError(400, "invalid message reference")
		}
	}
	userID, _ := c.Locals("user_id").(string)
	seen := map[string]bool{}
	ctx, cancel := context.WithTimeout(c.Context(), 6*time.Second)
	defer cancel()
	for _, key := range req.MediaKeys {
		if !strings.HasPrefix(key, "user-uploads/private-chat/"+userID+"/") || strings.Contains(key, "..") || seen[key] || (path.Ext(key) != ".jpg" && path.Ext(key) != ".png" && path.Ext(key) != ".webp") {
			return req, fiber.NewError(422, "Only your uploaded chat images can be attached")
		}
		seen[key] = true
		if m.s3 == nil {
			return req, fiber.NewError(503, "Image uploads are unavailable")
		}
		url, err := m.s3.ObjectHeadURL(key, time.Minute)
		if err != nil {
			return req, fiber.NewError(503, "Image uploads are unavailable")
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
		if err != nil {
			return req, fiber.ErrInternalServerError
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return req, fiber.NewError(502, "Could not verify the uploaded image. Please retry")
		}
		response.Body.Close()
		if response.StatusCode != 200 || response.ContentLength <= 0 || response.ContentLength > 5<<20 || !supportedPortableImageMime(strings.Split(response.Header.Get("Content-Type"), ";")[0]) {
			return req, fiber.NewError(422, "Image upload is incomplete or exceeds 5 MB")
		}
	}
	if req.Body == "" {
		req.Body = "Photo"
	}
	if req.MediaKeys == nil {
		req.MediaKeys = []string{}
	}
	return req, nil
}

func (m *ProviderMarketplaceController) chatMediaURLs(keys []string) []string {
	urls := make([]string, 0, len(keys))
	if m.s3 == nil {
		return urls
	}
	for _, key := range keys {
		if strings.HasPrefix(key, "user-uploads/private-chat/") && !strings.Contains(key, "..") {
			if url, err := m.s3.ObjectGetURL(key, 15*time.Minute); err == nil {
				urls = append(urls, url)
			}
		}
	}
	return urls
}

// A retry with the same reference returns the same durable message. Reusing
// that reference for a different thread or body is rejected.
func insertConversationMessage(ctx context.Context, tx pgx.Tx, conversationID, senderType, userID string, input conversationMessageInput) (uuid.UUID, time.Time, bool, error) {
	requestedID := uuid.New()
	var id uuid.UUID
	var createdAt time.Time
	err := tx.QueryRow(ctx, `INSERT INTO provider_conversation_messages(id,conversation_id,sender_type,sender_user_id,body,media_keys,client_message_id)
 VALUES($1,$2::uuid,$3,$4::uuid,$5,$6,NULLIF($7,'')::uuid)
 ON CONFLICT(sender_user_id,client_message_id) WHERE client_message_id IS NOT NULL
 DO UPDATE SET id=provider_conversation_messages.id
 WHERE provider_conversation_messages.conversation_id=EXCLUDED.conversation_id AND provider_conversation_messages.body=EXCLUDED.body AND provider_conversation_messages.media_keys=EXCLUDED.media_keys
 RETURNING id,created_at`, requestedID, conversationID, senderType, userID, input.Body, input.MediaKeys, input.ClientID).Scan(&id, &createdAt)
	if err == pgx.ErrNoRows {
		return id, createdAt, false, fiber.NewError(409, "This message reference was already used for another message")
	}
	if err != nil {
		return id, createdAt, false, fiber.ErrInternalServerError
	}
	return id, createdAt, id == requestedID, nil
}
