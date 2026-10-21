package controllers

import (
	"across/backend/internal/config"
	"context"
	"encoding/json"
	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestProviderBrowserPushSelfTestOwnershipAndDeduplication(t *testing.T) {
	db := settlementTestDB(t)
	defer db.Close()
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY);CREATE TABLE provider_members(provider_id uuid,user_id uuid,is_active bool);CREATE TABLE provider_marketplace_events(id uuid,provider_id uuid,event_type text,metadata jsonb);CREATE TABLE notifications(id uuid,user_id uuid,type text,data jsonb);`)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../migrations/073_provider_web_push.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	actor, other := uuid.NewString(), uuid.NewString()
	endpoint := "https://fcm.googleapis.com/fcm/send/test-phone"
	if _, err = db.Exec(ctx, `INSERT INTO users VALUES($1),($2)`, actor, other); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO web_push_subscriptions(user_id,endpoint,p256dh,auth,vapid_public_key) VALUES($1,$2,'fixture','fixture',$3)`, actor, endpoint, public); err != nil {
		t.Fatal(err)
	}
	controller := NewWebPushController(db, config.Config{WebPushPublicKey: public, WebPushPrivateKey: private, WebPushSubject: "mailto:test@example.test"})
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("Actor")); return c.Next() })
	app.Post("/test", controller.Test)
	app.Get("/test/:delivery_id", controller.TestStatus)
	call := func(method, path, user string, want int) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(`{"endpoint":"`+endpoint+`"}`))
		req.Header.Set("Actor", user)
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d", method, path, res.StatusCode)
		}
		body := map[string]any{}
		json.NewDecoder(res.Body).Decode(&body)
		return body
	}
	first := call("POST", "/test", actor, 202)["id"].(string)
	if next := call("POST", "/test", actor, 202)["id"]; next != first {
		t.Fatal("repeated tap created another notification")
	}
	call("POST", "/test", other, 409)
	if body := call("GET", "/test/"+first, actor, 200); body["status"] != "pending" {
		t.Fatal(body)
	}
	call("GET", "/test/"+first, other, 404)
	if _, err = db.Exec(ctx, `UPDATE web_push_deliveries SET status='sent' WHERE id=$1`, first); err != nil {
		t.Fatal(err)
	}
	if body := call("GET", "/test/"+first, actor, 200); body["status"] != "sent" {
		t.Fatal(body)
	}
}
