package controllers

import (
	"across/backend/internal/playbilling"
	"encoding/base64"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"regexp"
)

func (p *PlayBillingController) Notification(c *fiber.Ctx) error {
	c.Set("Cache-Control", "private, no-store")
	if p.store == nil || p.push == nil {
		return fiber.NewError(503, "Play subscription notifications are not configured")
	}
	if p.push.Validate(c.Context(), c.Get("Authorization")) != nil {
		return fiber.ErrUnauthorized
	}
	var envelope struct {
		Subscription string `json:"subscription"`
		Message      struct {
			ID   string `json:"messageId"`
			Data string `json:"data"`
		} `json:"message"`
	}
	if len(c.Body()) > 65536 || c.BodyParser(&envelope) != nil || envelope.Subscription != p.push.Subscription || !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`).MatchString(envelope.Message.ID) {
		return fiber.ErrBadRequest
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(envelope.Message.Data)
	if err != nil || len(raw) > 16384 {
		return fiber.ErrBadRequest
	}
	var notice struct {
		Package      string    `json:"packageName"`
		Test         *struct{} `json:"testNotification"`
		Subscription *struct {
			Token string `json:"purchaseToken"`
			Type  int    `json:"notificationType"`
		} `json:"subscriptionNotification"`
	}
	if json.Unmarshal(raw, &notice) != nil || notice.Package != playbilling.PackageName {
		return fiber.ErrBadRequest
	}
	if notice.Test != nil && notice.Subscription == nil {
		return c.SendStatus(204)
	}
	if notice.Subscription == nil || len(notice.Subscription.Token) < 16 || len(notice.Subscription.Token) > 4096 {
		return fiber.ErrBadRequest
	}
	// Retain an encrypted token for crashes before the app verifies a first
	// purchase. The worker derives its existing business identity from Google,
	// then verifies the complete receipt/order before granting anything.
	encrypted, err := p.store.Vault.Seal("rtdn:"+envelope.Message.ID, notice.Subscription.Token)
	if err != nil {
		return fiber.ErrBadRequest
	}
	_, err = p.db.Exec(c.Context(), `INSERT INTO play_provider_events(message_id,token_hash,encrypted_token) VALUES($1,$2,$3) ON CONFLICT(message_id) DO NOTHING`, envelope.Message.ID, playbilling.TokenHash(notice.Subscription.Token), encrypted)
	if err != nil {
		return fiber.NewError(503, "Play notification could not be recorded")
	}
	return c.SendStatus(204)
}
