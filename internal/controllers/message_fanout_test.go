package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"across/backend/internal/config"
	"across/backend/internal/services"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func asyncFanoutDB(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	db, sender, _, _ := senderFastPathFixture(t)
	if _, err := db.Exec(context.Background(), `ALTER TABLE notifications ADD COLUMN created_at timestamptz NOT NULL DEFAULT now()`); err != nil {
		t.Fatal(err)
	}
	baseline, err := os.ReadFile("../../migrations/085_provider_native_notifications.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(context.Background(), string(baseline)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../migrations/110_durable_message_notifications.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(context.Background(), string(raw)); err != nil {
		t.Fatal(err)
	}
	return db, sender
}

func drainFanout(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	for {
		worked, err := services.RunMessageFanoutBatch(context.Background(), db)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			return
		}
	}
}

func TestAsyncMessageFanoutSnapshotRollbackAndRetries(t *testing.T) {
	db, sender := asyncFanoutDB(t)
	ctx := context.Background()
	room, message := uuid.NewString(), uuid.NewString()
	users := make([]string, 6)
	for i := range users {
		users[i] = uuid.NewString()
	}
	_, err := db.Exec(ctx, `INSERT INTO chat_rooms(id,kind,owner_user_id,title) VALUES($1,'town_hall',$2,'Synthetic');
INSERT INTO users(id,full_name) SELECT unnest($3::uuid[]),'Peer';
INSERT INTO chat_participants(room_id,user_id,role,state) SELECT $1,unnest($3::uuid[]),'member','active';
UPDATE chat_participants SET state='removed' WHERE user_id=$4;
INSERT INTO social_blocks(blocker_id,blocked_id) VALUES($2,$5),($6,$2);`, pgx.QueryExecModeSimpleProtocol, room, sender, users, users[0], users[1], users[2])
	if err != nil {
		t.Fatal(err)
	}
	c := &CommunityController{db: db, market: &ProviderMarketplaceController{db: db, cfg: config.Config{AsyncMessageNotifications: true}}}
	enqueue := func(commit bool) {
		t.Helper()
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if err = c.notifyRoom(ctx, tx, room, sender, message, "New chat message", "SECRET BODY"); err != nil {
			t.Fatal(err)
		}
		if commit {
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	enqueue(false)
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM message_notification_jobs`).Scan(&count); err != nil || count != 0 {
		t.Fatal("job escaped message rollback", count, err)
	}
	enqueue(true)
	enqueue(true)
	if err = db.QueryRow(ctx, `SELECT count(*) FROM notifications`).Scan(&count); err != nil || count != 0 {
		t.Fatal("producer still ran delivery triggers", count, err)
	}
	// A membership change after commit must not change the authorized recipient
	// snapshot. A deleted account, however, must never be recreated/notified.
	_, err = db.Exec(ctx, `UPDATE chat_participants SET state='removed' WHERE user_id=$1;
UPDATE chat_participants SET state='active' WHERE user_id=$2;DELETE FROM chat_participants WHERE user_id=$3;DELETE FROM users WHERE id=$3`, pgx.QueryExecModeSimpleProtocol, users[3], users[0], users[5])
	if err != nil {
		t.Fatal(err)
	}
	drainFanout(t, db)
	drainFanout(t, db)
	rows, err := db.Query(ctx, `SELECT user_id::text,body,event_key,created_at=(SELECT created_at FROM message_notification_jobs) FROM notifications ORDER BY user_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := map[string]bool{users[3]: true, users[4]: true}
	for rows.Next() {
		var user, body, key string
		var sameTime bool
		if err = rows.Scan(&user, &body, &key, &sameTime); err != nil {
			t.Fatal(err)
		}
		if !want[user] || body != "Open your chat to read the message." || key != "shared-chat:"+message+":"+user || !sameTime {
			t.Fatal("privacy, snapshot, event key or event timestamp changed")
		}
		delete(want, user)
	}
	if rows.Err() != nil || len(want) != 0 {
		t.Fatal("missing snapshot recipients", want, rows.Err())
	}
}

func TestAsyncMessageFanoutAtomicPageFailureAndRecovery(t *testing.T) {
	db, sender := asyncFanoutDB(t)
	ctx := context.Background()
	key := "failure:" + uuid.NewString()
	_, err := db.Exec(ctx, `INSERT INTO user_push_tokens(id,user_id) VALUES(gen_random_uuid(),$1);
INSERT INTO message_notification_jobs(event_key,recipient_ids,type,title,body,data) VALUES($2,ARRAY[$1::uuid],'social_message','Test','Generic','{}');
CREATE FUNCTION reject_page() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected delivery failure';END $$;
CREATE TRIGGER zz_reject_page AFTER INSERT ON notification_push_deliveries FOR EACH ROW EXECUTE FUNCTION reject_page();`, pgx.QueryExecModeSimpleProtocol, sender, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = services.RunMessageFanoutBatch(ctx, db); err == nil {
		t.Fatal("injected delivery error was ignored")
	}
	var clean bool
	if err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM notifications)=0 AND (SELECT count(*) FROM notification_push_deliveries)=0 AND next_recipient=0 AND completed_at IS NULL AND attempts=1 AND available_at>now() FROM message_notification_jobs`).Scan(&clean); err != nil || !clean {
		t.Fatal("failed page partially committed or retry was not scheduled", err)
	}
	_, err = db.Exec(ctx, `DROP TRIGGER zz_reject_page ON notification_push_deliveries;UPDATE message_notification_jobs SET available_at=now()`)
	if err != nil {
		t.Fatal(err)
	}
	drainFanout(t, db)
	if err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM notifications)=1 AND (SELECT count(*) FROM notification_push_deliveries)=1 AND next_recipient=1 AND completed_at IS NOT NULL FROM message_notification_jobs`).Scan(&clean); err != nil || !clean {
		t.Fatal("retry lost or duplicated notification", err)
	}
}

// These are recipient/job correctness sizes, NOT 100/1,000/10,000 active HTTP
// sessions or capacity certification. No performance threshold is inferred.
func TestAsyncMessageFanoutRecipientSizesAndConcurrentWorkers(t *testing.T) {
	for _, n := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			db, _ := asyncFanoutDB(t)
			ctx := context.Background()
			_, err := db.Exec(ctx, `INSERT INTO users(id,full_name) SELECT md5('fanout-recipient:'||i)::uuid,'Synthetic' FROM generate_series(1,$1::int)i;
INSERT INTO user_push_tokens(id,user_id) SELECT md5('token:'||i)::uuid,md5('fanout-recipient:'||i)::uuid FROM generate_series(1,$1::int)i;
INSERT INTO message_notification_jobs(event_key,recipient_ids,type,title,body,data)
SELECT 'sized:'||(i%4),array_agg(md5('fanout-recipient:'||i)::uuid ORDER BY i),'social_message','Test','Generic','{}'::jsonb
FROM generate_series(1,$1::int)i GROUP BY i%4`, pgx.QueryExecModeSimpleProtocol, n)
			if err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			failures := make(chan error, 4)
			for worker := 0; worker < 4; worker++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						worked, err := services.RunMessageFanoutBatch(ctx, db)
						if err != nil {
							failures <- err
							return
						}
						if !worked {
							return
						}
					}
				}()
			}
			wg.Wait()
			close(failures)
			for err := range failures {
				t.Fatal(err)
			}
			var correct bool
			if err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM notifications)=$1 AND (SELECT count(*) FROM notification_push_deliveries)=$1 AND (SELECT count(*) FROM message_notification_jobs WHERE completed_at IS NOT NULL AND next_recipient=cardinality(recipient_ids))=4`, n).Scan(&correct); err != nil || !correct {
				t.Fatal("fanout inventory or concurrent claiming failed", n, err)
			}
			// Simulate replay of a completed page after an ambiguous outcome. Both
			// recipient event keys and delivery uniqueness must remain idempotent.
			_, err = db.Exec(ctx, `UPDATE message_notification_jobs SET next_recipient=0,completed_at=NULL`)
			if err != nil {
				t.Fatal(err)
			}
			drainFanout(t, db)
			if err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM notifications)=$1 AND (SELECT count(*) FROM notification_push_deliveries)=$1`, n).Scan(&correct); err != nil || !correct {
				t.Fatal("page replay duplicated delivery", n, err)
			}
		})
	}
}

func TestAsyncProviderFanoutTransactionScopeAndMemberSnapshot(t *testing.T) {
	db, sender := asyncFanoutDB(t)
	ctx := context.Background()
	provider, event := uuid.NewString(), uuid.NewString()
	_, err := db.Exec(ctx, `INSERT INTO provider_organizations(id,business_name) VALUES($1,'Synthetic');INSERT INTO provider_members(provider_id,user_id,role) VALUES($1,$2,'owner');`, pgx.QueryExecModeSimpleProtocol, provider, sender)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT set_config('app.async_message_fanout','on',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO provider_marketplace_events VALUES($1,$2,'{}','conversation_message')`, event, provider); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM notifications`).Scan(&count); err != nil || count != 0 {
		t.Fatal("provider producer performed fanout", err)
	}
	_, err = db.Exec(ctx, `UPDATE provider_members SET is_active=false WHERE provider_id=$1`, provider)
	if err != nil {
		t.Fatal(err)
	}
	drainFanout(t, db)
	if err = db.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id=$1 AND event_key=$2`, sender, "provider-native:"+event+":"+sender).Scan(&count); err != nil || count != 1 {
		t.Fatal("provider recipient snapshot changed", count, err)
	}
	// SET LOCAL must not leak to another transaction or alter unrelated writes.
	var setting string
	if err = db.QueryRow(ctx, `SELECT coalesce(current_setting('app.async_message_fanout',true),'')`).Scan(&setting); err != nil || setting == "on" {
		t.Fatal("producer transaction setting leaked", setting, err)
	}
}

func TestAsyncMessageFanoutFairPagesAndCompletedRetention(t *testing.T) {
	db, sender := asyncFanoutDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `INSERT INTO message_notification_jobs(event_key,recipient_ids,type,title,body,data,available_at)
VALUES('a-large',array_fill($1::uuid,ARRAY[128]),'social_message','Test','Generic','{}',now()-interval '2 seconds'),
('b-small',ARRAY[$1::uuid],'social_message','Test','Generic','{}',now()-interval '1 second');`, sender)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = services.RunMessageFanoutBatch(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	var fair bool
	if err = db.QueryRow(ctx, `SELECT (SELECT next_recipient FROM message_notification_jobs WHERE event_key='a-large')=64
AND (SELECT completed_at IS NOT NULL FROM message_notification_jobs WHERE event_key='b-small')`).Scan(&fair); err != nil || !fair {
		t.Fatal("large fanout monopolized queue", err)
	}
	_, err = db.Exec(ctx, `UPDATE message_notification_jobs SET completed_at=now()-interval '8 days' WHERE event_key='b-small'`)
	if err != nil {
		t.Fatal(err)
	}
	if err = services.PruneCompletedMessageFanout(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT count(*)=1 AND bool_and(event_key='a-large' AND completed_at IS NULL) FROM message_notification_jobs`).Scan(&fair); err != nil || !fair {
		t.Fatal("retention discarded pending notifications", err)
	}
}

func TestAsyncMessageFanoutHTTPMessageAndRetryCommitTogether(t *testing.T) {
	db, sender := asyncFanoutDB(t)
	ctx := context.Background()
	room, peer, ref := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err := db.Exec(ctx, `INSERT INTO users(id,full_name) VALUES($1,'Peer');
INSERT INTO chat_rooms(id,kind,owner_user_id,title) VALUES($2,'town_hall',$3,'Synthetic');
INSERT INTO chat_participants(room_id,user_id,role,state) VALUES($2,$3,'buyer','active'),($2,$1,'member','active')`, pgx.QueryExecModeSimpleProtocol, peer, room, sender)
	if err != nil {
		t.Fatal(err)
	}
	controller := NewCommunityController(db, config.Config{AsyncMessageNotifications: true})
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", sender); return c.Next() })
	app.Post("/rooms/:conversation_id/messages", controller.Send)
	var firstID string
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/rooms/"+room+"/messages", strings.NewReader(`{"message":"Hello","client_message_id":"`+ref+`"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req, 10000)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			ID string `json:"id"`
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 201 || body.ID == "" {
			t.Fatal("durable send failed", resp.StatusCode, err)
		}
		if i == 0 {
			firstID = body.ID
		} else if firstID != body.ID {
			t.Fatal("retry duplicated message")
		}
	}
	var correct bool
	if err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM chat_messages)=1 AND (SELECT count(*) FROM message_notification_jobs)=1 AND (SELECT count(*) FROM notifications)=0`).Scan(&correct); err != nil || !correct {
		t.Fatal("message and durable fanout did not commit exactly once", err)
	}
	drainFanout(t, db)
	if err = db.QueryRow(ctx, `SELECT count(*)=1 AND bool_and(user_id=$1::uuid) FROM notifications`, peer).Scan(&correct); err != nil || !correct {
		t.Fatal("peer notification missing after drain", err)
	}
}
