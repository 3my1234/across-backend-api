package controllers

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func senderFastPathFixture(t *testing.T) (*pgxpool.Pool, string, string, string) {
	t.Helper()
	db := communityTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE provider_marketplace_events(id uuid PRIMARY KEY,provider_id uuid,metadata jsonb,event_type text);
CREATE TABLE user_push_tokens(id uuid PRIMARY KEY,user_id uuid,disabled_at timestamptz);
CREATE TABLE notification_push_deliveries(notification_id uuid,push_token_id uuid,UNIQUE(notification_id,push_token_id));`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"048_immediate_notification_push.sql", "073_provider_web_push.sql", "100_notification_sender_identity.sql"} {
		raw, err := os.ReadFile("../../migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, string(raw)); err != nil {
			t.Fatal(name, err)
		}
	}
	var old string
	if err := db.QueryRow(ctx, `SELECT pg_get_functiondef('notification_sender_identity(jsonb)'::regprocedure)`).Scan(&old); err != nil {
		t.Fatal(err)
	}
	old = strings.Replace(old, ".notification_sender_identity(", ".notification_sender_identity_legacy(", 1)
	if _, err := db.Exec(ctx, old); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../migrations/109_notification_sender_fast_path.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	user, other, provider := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO users(id,full_name,avatar_url) VALUES($1,'Sender','user.png'),($2,NULL,NULL)`, user, other); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_organizations(id,owner_user_id,business_name,logo_url) VALUES($1,$2,'Business','business.png')`, provider, user); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_members(provider_id,user_id,role) VALUES($1,$2,'owner')`, provider, user); err != nil {
		t.Fatal(err)
	}
	return db, user, other, provider
}

func TestNotificationSenderFastPathMatchesLegacy(t *testing.T) {
	db, user, other, provider := senderFastPathFixture(t)
	ctx := context.Background()
	for _, active := range []bool{true, false} {
		if _, err := db.Exec(ctx, `UPDATE provider_members SET is_active=$1`, active); err != nil {
			t.Fatal(err)
		}
		for _, blank := range []bool{false, true} {
			name, logo := "Business", "business.png"
			if blank {
				name, logo = "", ""
			}
			if _, err := db.Exec(ctx, `UPDATE provider_organizations SET business_name=$1,logo_url=$2`, name, logo); err != nil {
				t.Fatal(err)
			}
			for _, sender := range []any{nil, "invalid", uuid.NewString(), user, other} {
				for _, pid := range []any{nil, "invalid", uuid.NewString(), provider} {
					for _, flag := range []any{nil, false, true, "true", "false"} {
						data, _ := json.Marshal(map[string]any{"sender_user_id": sender, "provider_id": pid, "sender_is_provider": flag})
						var same bool
						if err := db.QueryRow(ctx, `SELECT notification_sender_identity($1::jsonb) IS NOT DISTINCT FROM notification_sender_identity_legacy($1::jsonb)`, string(data)).Scan(&same); err != nil || !same {
							t.Fatalf("identity differs for %s: %v", data, err)
						}
					}
				}
			}
		}
	}
	for _, data := range []string{`{}`, `null`, `{"sender_user_id":1}`, `{"sender_user_id":""}`} {
		var same bool
		if err := db.QueryRow(ctx, `SELECT notification_sender_identity($1::jsonb) IS NOT DISTINCT FROM notification_sender_identity_legacy($1::jsonb)`, data).Scan(&same); err != nil || !same {
			t.Fatal(data, err)
		}
	}
}

func TestNotificationSenderFastPathKeepsDurableDeliveryAtomic(t *testing.T) {
	db, user, recipient, _ := senderFastPathFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO user_push_tokens VALUES($1,$2,NULL)`, uuid.NewString(), recipient); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO web_push_subscriptions(user_id,endpoint,p256dh,auth,vapid_public_key) VALUES($1,'https://example.invalid/push','key','auth','vapid')`, recipient); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"sender_user_id": user, "sender_name": "forged", "view": "messages"})
	for _, commit := range []bool{false, true} {
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO notifications(user_id,type,title,body,data,event_key) VALUES($1,'social_message','New chat','Private preview',$2::jsonb,'sender-fast-path') ON CONFLICT DO NOTHING`, recipient, string(data)); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		var native, web int
		var name string
		if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM notification_push_deliveries),(SELECT count(*) FROM web_push_deliveries),(SELECT data->>'sender_name' FROM notifications WHERE event_key='sender-fast-path')`).Scan(&native, &web, &name); err != nil || native != 1 || web != 1 || name != "Sender" {
			_ = tx.Rollback(ctx)
			t.Fatalf("transaction lost delivery or identity: %d %d %s %v", native, web, name, err)
		}
		if commit {
			err = tx.Commit(ctx)
		} else {
			err = tx.Rollback(ctx)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM notification_push_deliveries),(SELECT count(*) FROM web_push_deliveries)`).Scan(&native, &web); err != nil {
			t.Fatal(err)
		}
		want := 0
		if commit {
			want = 1
		}
		if native != want || web != want {
			t.Fatal("delivery escaped transaction", native, web)
		}
	}
	if _, err := db.Exec(ctx, `INSERT INTO notifications(user_id,type,title,body,data,event_key) VALUES($1,'social_message','New chat','Private preview',$2::jsonb,'sender-fast-path') ON CONFLICT DO NOTHING`, recipient, string(data)); err != nil {
		t.Fatal(err)
	}
	var native, web int
	if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM notification_push_deliveries),(SELECT count(*) FROM web_push_deliveries)`).Scan(&native, &web); err != nil || native != 1 || web != 1 {
		t.Fatal("retry duplicated delivery", native, web, err)
	}
}
