package controllers

import (
	"context"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPushSoundTestOwnershipPreferenceAndQueue(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE user_push_tokens(id uuid PRIMARY KEY,user_id uuid,expo_push_token text,sound_enabled boolean,disabled_at timestamptz);
 CREATE TABLE notifications(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),user_id uuid,type text,title text,body text,data jsonb,is_read boolean,created_at timestamptz DEFAULT now());
 CREATE TABLE notification_push_deliveries(notification_id uuid,push_token_id uuid,PRIMARY KEY(notification_id,push_token_id));`)
	if err != nil {
		t.Fatal(err)
	}
	buyer, other, tokenID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO user_push_tokens VALUES($1,$2,'ExpoPushToken[phone]',true,NULL),($3,$4,'ExpoPushToken[other]',true,NULL)`, tokenID, buyer, uuid.NewString(), other); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", buyer); return c.Next() })
	app.Post("/test", NewNotificationsController(db).TestPush)
	request := func(token string, want int) {
		t.Helper()
		req := httptest.NewRequest("POST", "/test", strings.NewReader(`{"token":"`+token+`"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("status %d, want %d", resp.StatusCode, want)
		}
	}
	request("ExpoPushToken[other]", 409)
	if _, err = db.Exec(ctx, `UPDATE user_push_tokens SET sound_enabled=false WHERE id=$1`, tokenID); err != nil {
		t.Fatal(err)
	}
	request("ExpoPushToken[phone]", 409)
	if _, err = db.Exec(ctx, `UPDATE user_push_tokens SET sound_enabled=true WHERE id=$1`, tokenID); err != nil {
		t.Fatal(err)
	}
	request("ExpoPushToken[phone]", 202)
	request("ExpoPushToken[phone]", 429)
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM notification_push_deliveries WHERE push_token_id=$1`, tokenID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("queued %d: %v", count, err)
	}
}
