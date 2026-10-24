package controllers

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestNativePushStatusOwnershipAndPrivacy(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE notifications(id uuid PRIMARY KEY,user_id uuid,type text,data jsonb,created_at timestamptz DEFAULT now());
 CREATE TABLE user_push_tokens(id uuid PRIMARY KEY,user_id uuid,expo_push_token text);
 CREATE TABLE notification_push_deliveries(notification_id uuid,push_token_id uuid,status text,attempts int,updated_at timestamptz DEFAULT now(),sent_at timestamptz,receipt_checked_at timestamptz,last_error text,expo_ticket_id text);`)
	if err != nil {
		t.Fatal(err)
	}
	owner, other, phone, note := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO user_push_tokens VALUES($1,$2,'ExpoPushToken[PRIVATE]');
 `, phone, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO notifications VALUES($1,$2,'sound_test',jsonb_build_object('test_push_token_id',$3::text),now());`, note, owner, phone); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO notification_push_deliveries VALUES($1,$2,'pending',0,now(),NULL,NULL,'PRIVATE error','PRIVATE ticket')`, note, phone); err != nil {
		t.Fatal(err)
	}
	actor := owner
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", actor); return c.Next() })
	app.Get("/test/:notification_id", NewNotificationsController(db).TestPushStatus)
	call := func(id string, want int) map[string]any {
		t.Helper()
		res, err := app.Test(httptest.NewRequest("GET", "/test/"+id, nil))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		if res.StatusCode != want {
			t.Fatalf("status %d want %d: %s", res.StatusCode, want, raw)
		}
		if strings.Contains(string(raw), "PRIVATE") {
			t.Fatal("private delivery details exposed")
		}
		if !strings.Contains(res.Header.Get("Cache-Control"), "no-store") {
			t.Fatal("missing private cache policy")
		}
		var result map[string]any
		_ = json.Unmarshal(raw, &result)
		return result
	}
	call("invalid", 400)
	call(uuid.NewString(), 404)
	for _, state := range []string{"pending", "sending", "sent", "delivered", "retry", "failed", "disabled"} {
		if _, err = db.Exec(ctx, `UPDATE notification_push_deliveries SET status=$1`, state); err != nil {
			t.Fatal(err)
		}
		if got := call(note, 200); got["status"] != state {
			t.Fatal(got)
		}
	}
	actor = other
	call(note, 404)
	actor = owner
	if _, err = db.Exec(ctx, `UPDATE user_push_tokens SET user_id=$1`, other); err != nil {
		t.Fatal(err)
	}
	call(note, 404)
	if _, err = db.Exec(ctx, `UPDATE user_push_tokens SET user_id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE notifications SET type='order_update'`); err != nil {
		t.Fatal(err)
	}
	call(note, 404)
}
