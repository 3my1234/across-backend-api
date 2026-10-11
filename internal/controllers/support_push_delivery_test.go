package controllers

import (
	"across/backend/internal/services"
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisteredDevicesReceiveRealSupportPush(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE support_tickets(id uuid PRIMARY KEY,user_id uuid,status text,updated_at timestamptz);
 CREATE TABLE support_messages(ticket_id uuid,sender_type text,sender_id uuid,message text);
 CREATE TABLE notifications(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),user_id uuid,order_id uuid,batch_id uuid,type text,title text,body text,data jsonb,event_key text UNIQUE,is_read boolean DEFAULT false,created_at timestamptz DEFAULT now());
 CREATE TABLE user_push_tokens(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),user_id uuid,expo_push_token text UNIQUE,platform text,sound_enabled boolean DEFAULT true,disabled_at timestamptz,last_seen_at timestamptz DEFAULT now(),updated_at timestamptz DEFAULT now());
 CREATE TABLE notification_push_deliveries(notification_id uuid,push_token_id uuid,status text DEFAULT 'pending',attempts int DEFAULT 0,next_attempt_at timestamptz DEFAULT now(),expo_ticket_id text,last_error text DEFAULT '',sent_at timestamptz,receipt_checked_at timestamptz,updated_at timestamptz DEFAULT now(),PRIMARY KEY(notification_id,push_token_id));`)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"069_device_notification_channels.sql", "048_immediate_notification_push.sql"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	user, admin, ticket := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO support_tickets VALUES($1,$2,'open',now())`, ticket, user); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	nc := NewNotificationsController(db)
	sc := NewSupportController(db)
	app.Post("/register", func(c *fiber.Ctx) error { c.Locals("user_id", user); return nc.RegisterPushToken(c) })
	app.Post("/support/:ticket_id", func(c *fiber.Ctx) error { c.Locals("admin_id", admin); return sc.AdminReply(c) })
	post := func(url, body string, want int) {
		t.Helper()
		req := httptest.NewRequest("POST", url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			b, _ := io.ReadAll(res.Body)
			t.Fatalf("%s: %d %s", url, res.StatusCode, b)
		}
	}
	post("/register", `{"token":"ExpoPushToken[new]","platform":"android","sound_enabled":true,"notification_channel":"atl-updates-v2"}`, 200)
	post("/register", `{"token":"ExpoPushToken[legacy]","platform":"android"}`, 200)
	post("/register", `{"token":"ExpoPushToken[ios]","platform":"ios","sound_enabled":true}`, 200)
	post("/register", `{"token":"ExpoPushToken[silent]","platform":"android","sound_enabled":false,"notification_channel":"atl-updates-v2"}`, 200)
	post("/register", `{"token":"ExpoPushToken[bad]","platform":"android","notification_channel":"arbitrary"}`, 400)
	post("/support/"+ticket, `{"message":"We have resolved your question."}`, 200)
	var queued int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM notification_push_deliveries`).Scan(&queued); err != nil || queued != 4 {
		t.Fatalf("durable support pushes=%d %v", queued, err)
	}
	seen := map[string]bool{}
	client := &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/--/api/v2/push/send" {
			t.Fatalf("unexpected URL %s", req.URL)
		}
		var messages []map[string]any
		if err := json.NewDecoder(req.Body).Decode(&messages); err != nil {
			t.Fatal(err)
		}
		tickets := []map[string]string{}
		for _, message := range messages {
			token := message["to"].(string)
			seen[token] = true
			if message["priority"] != "high" || message["title"] == "" || message["body"] == "" {
				t.Fatalf("not a visible high-priority push: %v", message)
			}
			data := message["data"].(map[string]any)
			if data["ticket_id"] != ticket || data["notification_type"] != "ticket_reply" {
				t.Fatalf("lost support routing %v", data)
			}
			if token == "ExpoPushToken[silent]" {
				if _, ok := message["sound"]; ok || message["channelId"] != "orders-silent" {
					t.Fatal("silent preference ignored")
				}
			} else {
				if message["sound"] != "default" {
					t.Fatal("missing sound payload")
				}
				want := "orders"
				if token == "ExpoPushToken[new]" {
					want = "atl-updates-v2"
				}
				if message["channelId"] != want {
					t.Fatalf("wrong channel for %s", token)
				}
			}
			tickets = append(tickets, map[string]string{"status": "ok", "id": token})
		}
		body, _ := json.Marshal(map[string]any{"data": tickets})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	count, err := services.RunPushDeliveryBatch(ctx, db, client)
	if err != nil || count != 4 || len(seen) != 4 {
		t.Fatalf("push send count=%d seen=%d %v", count, len(seen), err)
	}
	if _, err = db.Exec(ctx, `UPDATE notification_push_deliveries SET sent_at=now()-interval '20 minutes'`); err != nil {
		t.Fatal(err)
	}
	client.Transport = paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/--/api/v2/push/getReceipts" {
			t.Fatalf("unexpected URL %s", req.URL)
		}
		body := `{"data":{"ExpoPushToken[new]":{"status":"ok"},"ExpoPushToken[legacy]":{"status":"ok"},"ExpoPushToken[ios]":{"status":"error","message":"Credentials rejected","details":{"error":"InvalidCredentials"}},"ExpoPushToken[silent]":{"status":"ok"}}}`
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	count, err = services.RunPushReceiptBatch(ctx, db, client)
	if err != nil || count != 4 {
		t.Fatalf("receipts count=%d %v", count, err)
	}
	var status, lastError string
	if err = db.QueryRow(ctx, `SELECT d.status,d.last_error FROM notification_push_deliveries d JOIN user_push_tokens p ON p.id=d.push_token_id WHERE p.platform='ios'`).Scan(&status, &lastError); err != nil || status != "failed" || !strings.Contains(lastError, "InvalidCredentials") {
		t.Fatalf("missing actionable credential failure: %s %s %v", status, lastError, err)
	}
	// Receipt success means acceptance by APNs/FCM, not proof of physical audio.
}
