package controllers

import (
	"across/backend/internal/config"
	"across/backend/internal/services"
	"context"
	"encoding/base64"
	"encoding/json"
	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type webPushTestTransport func(*http.Request) (*http.Response, error)

func (f webPushTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBrowserPushDurabilityRetryOwnershipAndEncryption(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY);
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,is_active bool);
 CREATE TABLE provider_marketplace_events(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),provider_id uuid,event_type text,metadata jsonb);
 CREATE TABLE notifications(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),user_id uuid,type text,data jsonb DEFAULT '{}');`)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "073_provider_web_push.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	routing, err := os.ReadFile(filepath.Join("..", "..", "migrations", "076_web_push_destinations.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(routing)); err != nil {
		t.Fatal(err)
	}
	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{WebPushPublicKey: public, WebPushPrivateKey: private, WebPushSubject: "mailto:support@example.test"}
	if !services.WebPushConfigured(cfg) {
		t.Fatal("valid pair rejected")
	}
	bad := cfg
	bad.WebPushPrivateKey = "wrong"
	if services.WebPushConfigured(bad) {
		t.Fatal("invalid pair accepted")
	}
	actor, other, provider := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO users VALUES($1),($2);`, actor, other); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_members VALUES($1,$2,true)`, provider, actor); err != nil {
		t.Fatal(err)
	}
	controller := NewWebPushController(db, cfg)
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("X-User")); return c.Next() })
	app.Post("/push", controller.Register)
	app.Delete("/push", controller.Unregister)
	endpoint := "https://fcm.googleapis.com/fcm/send/fixture"
	subscription := webpush.Subscription{Endpoint: endpoint, Keys: webpush.Keys{P256dh: public, Auth: base64.RawURLEncoding.EncodeToString(make([]byte, 16))}}
	call := func(method, user string, body any, want int) {
		t.Helper()
		payload, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "/push", strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User", user)
		res, e := app.Test(req)
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("%s status=%d want=%d", method, res.StatusCode, want)
		}
	}
	call("POST", actor, subscription, 204)
	unsafe := subscription
	unsafe.Endpoint = "https://127.0.0.1/private"
	call("POST", actor, unsafe, 422)
	if _, err = db.Exec(ctx, `INSERT INTO provider_marketplace_events(provider_id,event_type,metadata) VALUES($1,'conversation_message','{"message":"PRIVATE MESSAGE"}')`, provider); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err = db.QueryRow(ctx, `SELECT payload::text FROM web_push_deliveries LIMIT 1`).Scan(&payload); err != nil || strings.Contains(payload, "PRIVATE MESSAGE") {
		t.Fatal("private content leaked or event missing", err)
	}
	// Notifications and their push jobs commit/roll back together.
	tx, _ := db.Begin(ctx)
	ticketID := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO notifications(user_id,type,data) VALUES($1,'ticket_reply',jsonb_build_object('ticket_id',$2::text))`, actor, ticketID); err != nil {
		t.Fatal(err)
	}
	var linkedTicket string
	if err = tx.QueryRow(ctx, `SELECT payload->>'ticket_id' FROM web_push_deliveries WHERE source_key LIKE 'notification:%' LIMIT 1`).Scan(&linkedTicket); err != nil || linkedTicket != ticketID {
		t.Fatal("support browser push must carry exact destination", err, linkedTicket)
	}
	tx.Rollback(ctx)
	var count int
	db.QueryRow(ctx, `SELECT count(*) FROM web_push_deliveries`).Scan(&count)
	if count != 1 {
		t.Fatal("rolled back push survived")
	}
	if _, err = db.Exec(ctx, `INSERT INTO notifications(user_id,type) SELECT $1,'ticket_reply' FROM generate_series(1,19)`, actor); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	client := &http.Client{Transport: webPushTestTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("Content-Encoding") != "aes128gcm" || !strings.HasPrefix(r.Header.Get("Authorization"), "vapid ") || len(body) < 80 {
			t.Error("unencrypted/unsigned push")
		}
		return &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	var group sync.WaitGroup
	for i := 0; i < 4; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := services.RunWebPushBatch(ctx, db, cfg, client); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	db.QueryRow(ctx, `SELECT count(*) FROM web_push_deliveries WHERE status='sent'`).Scan(&count)
	if count != 20 || calls.Load() != 20 {
		t.Fatalf("claim duplication/loss sent=%d calls=%d", count, calls.Load())
	}
	for _, code := range []int{429, 410} {
		if _, err = db.Exec(ctx, `INSERT INTO notifications(user_id,type) VALUES($1,'ticket_reply')`, actor); err != nil {
			t.Fatal(err)
		}
		client.Transport = webPushTestTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(""))}, nil
		})
		if _, err = services.RunWebPushBatch(ctx, db, cfg, client); err != nil {
			t.Fatal(err)
		}
		var status string
		var attempts int
		if err = db.QueryRow(ctx, `SELECT status,attempts FROM web_push_deliveries ORDER BY created_at DESC LIMIT 1`).Scan(&status, &attempts); err != nil {
			t.Fatal(err)
		}
		want := "retry"
		if code == 410 {
			want = "failed"
		}
		if status != want || attempts != 1 {
			t.Fatal("incorrect retry decision", status, attempts)
		}
	}
	var disabled bool
	db.QueryRow(ctx, `SELECT disabled_at IS NOT NULL FROM web_push_subscriptions`).Scan(&disabled)
	if !disabled {
		t.Fatal("expired subscription retained")
	}
	// A shared phone changing accounts must not receive the previous account's queued events.
	call("POST", other, subscription, 204)
	db.QueryRow(ctx, `SELECT count(*) FROM web_push_deliveries`).Scan(&count)
	if count != 0 {
		t.Fatal("old account jobs survived takeover")
	}
	call("DELETE", actor, map[string]string{"endpoint": endpoint}, 204)
	db.QueryRow(ctx, `SELECT disabled_at IS NOT NULL FROM web_push_subscriptions`).Scan(&disabled)
	if disabled {
		t.Fatal("another account disabled subscription")
	}
	call("DELETE", other, map[string]string{"endpoint": endpoint}, 204)
	db.QueryRow(ctx, `SELECT disabled_at IS NOT NULL FROM web_push_subscriptions`).Scan(&disabled)
	if !disabled {
		t.Fatal("logout did not disable subscription")
	}
}
