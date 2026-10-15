package controllers

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestCommunityProviderNativeNotificationRecipients(t *testing.T) {
	db := communityTestDB(t)
	defer db.Close()
	ctx := context.Background()
	if ProviderNativeNotificationsReady(ctx, db) {
		t.Fatal("missing native notification migration must not be ready")
	}
	_, err := db.Exec(ctx, `CREATE TABLE provider_marketplace_events(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),provider_id uuid,listing_id uuid,event_type text,metadata jsonb);
 CREATE TABLE web_push_subscriptions(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),user_id uuid,disabled_at timestamptz);
 CREATE TABLE web_push_deliveries(subscription_id uuid,source_key text,payload jsonb);
 CREATE TABLE user_push_tokens(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),user_id uuid,disabled_at timestamptz);
 CREATE TABLE notification_push_deliveries(notification_id uuid,push_token_id uuid,UNIQUE(notification_id,push_token_id));`)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../migrations/085_provider_native_notifications.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	repairSQL, err := os.ReadFile("../../migrations/090_provider_notification_conflict_target.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(repairSQL)); err != nil {
		t.Fatal(err)
	}
	pushSQL, err := os.ReadFile("../../migrations/048_immediate_notification_push.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(pushSQL)); err != nil {
		t.Fatal(err)
	}
	if !ProviderNativeNotificationsReady(ctx, db) {
		t.Fatal("native notification migration not detected")
	}
	if _, err = db.Exec(ctx, `CREATE TRIGGER user_test_push AFTER INSERT ON notifications FOR EACH ROW EXECUTE FUNCTION enqueue_user_web_push()`); err != nil {
		t.Fatal(err)
	}
	provider, owner, staff, inactive, event := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, id := range []string{owner, staff, inactive} {
		if _, err = db.Exec(ctx, `INSERT INTO users(id,full_name)VALUES($1,'Test member')`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_members(provider_id,user_id,is_active)VALUES($1,$2,true),($1,$3,true),($1,$4,false);`, provider, owner, staff, inactive); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO web_push_subscriptions(user_id)VALUES($1)`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO user_push_tokens(user_id)VALUES($1),($2)`, owner, staff); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_marketplace_events(id,provider_id,event_type,metadata)VALUES($1,$2,'conversation_message',jsonb_build_object('conversation_id',$3::text,'message','Private text'))`, event, provider, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	// Replay the trigger for the same event: the production partial index must
	// suppress duplicate native notifications without failing the transaction.
	if _, err = db.Exec(ctx, `CREATE TRIGGER replay_provider_native AFTER UPDATE ON provider_marketplace_events FOR EACH ROW EXECUTE FUNCTION enqueue_provider_native_notification(); UPDATE provider_marketplace_events SET event_type=event_type`); err != nil {
		t.Fatal(err)
	}
	var count, web, native int
	if err = db.QueryRow(ctx, "SELECT count(*) FROM notifications WHERE type='provider_activity' AND body NOT LIKE '%Private text%'").Scan(&count); err != nil || count != 2 {
		t.Fatalf("recipients=%d err=%v", count, err)
	}
	if err = db.QueryRow(ctx, "SELECT count(*) FROM web_push_deliveries").Scan(&web); err != nil || web != 0 {
		t.Fatalf("duplicate web push=%d err=%v", web, err)
	}
	if err = db.QueryRow(ctx, "SELECT count(*) FROM notification_push_deliveries").Scan(&native); err != nil || native != 2 {
		t.Fatalf("native queue=%d err=%v", native, err)
	}
}
