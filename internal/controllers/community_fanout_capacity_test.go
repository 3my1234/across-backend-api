package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func fanoutFixture(t *testing.T, n int) (*pgxpool.Pool, string, string) {
	t.Helper()
	db := communityTestDB(t)
	ctx := context.Background()
	room, actor := uuid.NewString(), uuid.NewString()
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`INSERT INTO users(id,full_name) VALUES($1,'Sender')`, actor)
	must(`INSERT INTO chat_rooms(id,kind,owner_user_id) VALUES($1,'town_hall',$2)`, room, actor)
	for i := 1; i <= n; i++ {
		user := fmt.Sprintf("10000000-0000-4000-8000-%012x", i)
		must(`INSERT INTO users(id,full_name) VALUES($1,'Recipient')`, user)
		must(`INSERT INTO chat_participants(room_id,user_id,role) VALUES($1,$2,'member')`, room, user)
	}
	// A row-trigger probe verifies that batching and duplicate conflicts preserve
	// the durable enqueue boundary; actual worker lease recovery is tested apart.
	must(`CREATE TABLE fanout_queue_probe(notification_id uuid PRIMARY KEY REFERENCES notifications(id),user_id uuid);
 CREATE FUNCTION probe_fanout_enqueue() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO fanout_queue_probe VALUES(NEW.id,NEW.user_id);RETURN NEW;END;$$;
 CREATE TRIGGER fanout_enqueue AFTER INSERT ON notifications FOR EACH ROW EXECUTE FUNCTION probe_fanout_enqueue();`)
	return db, room, actor
}

func TestCommunityFanoutBoundariesConcurrentRetriesAndFailure(t *testing.T) {
	db, room, actor := fanoutFixture(t, 140)
	ctx := context.Background()
	s := &CommunityController{db: db}
	message := uuid.NewString()
	call := func() error {
		tx, err := db.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if err = s.notifyRoom(ctx, tx, room, actor, message, "New chat", "private body"); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); failures <- call() }()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertCounts := func(want int) {
		t.Helper()
		var notifications, queued int
		if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM notifications),(SELECT count(*) FROM fanout_queue_probe)`).Scan(&notifications, &queued); err != nil || notifications != want || queued != want {
			t.Fatalf("notifications=%d queued=%d want=%d err=%v", notifications, queued, want, err)
		}
	}
	assertCounts(140)
	// Earlier pages now all conflict. New higher UUID recipients still need a
	// notification: progress must not depend on INSERT RETURNING row counts.
	for _, user := range []string{"fffffff0-0000-4000-8000-000000000001", "fffffff1-0000-4000-8000-000000000002"} {
		if _, err := db.Exec(ctx, `INSERT INTO users(id,full_name) VALUES($1,'New recipient')`, user); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO chat_participants(room_id,user_id,role) VALUES($1,$2,'member')`, room, user); err != nil {
			t.Fatal(err)
		}
	}
	if err := call(); err != nil {
		t.Fatal(err)
	}
	assertCounts(142)
	// Reject a recipient in the final page. Previous pages and the associated
	// business message must roll back, including per-row enqueue effects.
	if _, err := db.Exec(ctx, `CREATE FUNCTION fail_last_fanout() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.user_id='fffffff1-0000-4000-8000-000000000002'::uuid THEN RAISE EXCEPTION 'synthetic final-page failure';END IF;RETURN NEW;END;$$;
 CREATE TRIGGER fanout_fail BEFORE INSERT ON notifications FOR EACH ROW EXECUTE FUNCTION fail_last_fanout();`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	failedMessage := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO chat_messages(id,room_id,sender_user_id,body,client_message_id) VALUES($1,$2,$3,'Must roll back',$4)`, failedMessage, room, actor, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err = s.notifyRoom(ctx, tx, room, actor, failedMessage, "New chat", "private"); err == nil {
		t.Fatal("expected final-page failure")
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	assertCounts(142)
	var leaked int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM chat_messages WHERE id=$1`, failedMessage).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("business message survived failed fan-out", err)
	}
}

// Exact previous API fan-out path, retained only for matched comparison.
func legacyFanoutForComparison(ctx context.Context, tx pgx.Tx, room, actor, message string) error {
	rows, err := tx.Query(ctx, `SELECT p.user_id::text FROM chat_participants p WHERE p.room_id=$1::uuid AND p.state='active' AND p.user_id<>$2::uuid AND NOT EXISTS(SELECT 1 FROM social_blocks b WHERE (b.blocker_id=p.user_id AND b.blocked_id=$2::uuid) OR (b.blocked_id=p.user_id AND b.blocker_id=$2::uuid))`, room, actor)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = insertNotification(ctx, tx, id, "", nil, "social_message", "New chat", "Open your chat to read the message.", map[string]any{"view": "messages", "chat_room_id": room, "sender_user_id": actor}, "shared-chat:"+message+":"+id); err != nil {
			return err
		}
	}
	return nil
}

func TestCommunityFanoutMatchedBenchmark(t *testing.T) {
	if os.Getenv("FANOUT_BENCHMARK") != "true" {
		t.Skip("explicit isolated benchmark only")
	}
	db, room, actor := fanoutFixture(t, 10)
	ctx := context.Background()
	controller := &CommunityController{db: db}
	for arm, mode := range []string{"original", "batched", "batched", "original"} {
		if _, err := db.Exec(ctx, `SELECT public.pg_stat_statements_reset()`); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		for i := 0; i < 100; i++ {
			tx, err := db.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			message := uuid.NewString()
			if mode == "original" {
				err = legacyFanoutForComparison(ctx, tx, room, actor, message)
			} else {
				err = controller.notifyRoom(ctx, tx, room, actor, message, "New chat", "private")
			}
			if err != nil {
				tx.Rollback(ctx)
				t.Fatal(err)
			}
			var n, q int
			if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM notifications),(SELECT count(*) FROM fanout_queue_probe)`).Scan(&n, &q); err != nil || n != 10 || q != 10 {
				tx.Rollback(ctx)
				t.Fatal("batch changed recipient or row-trigger effects", n, q, err)
			}
			if err = tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
		}
		elapsed := time.Since(start).Seconds()
		var calls int64
		var sqlMS float64
		var hit, read int64
		if err := db.QueryRow(ctx, `SELECT COALESCE(sum(calls),0)::bigint,COALESCE(sum(total_exec_time),0)::float8,COALESCE(sum(shared_blks_hit),0)::bigint,COALESCE(sum(shared_blks_read),0)::bigint FROM public.pg_stat_statements WHERE query NOT LIKE '%pg_stat_statements%' AND (query LIKE '%INSERT INTO notifications%' OR query LIKE 'SELECT p.user_id%')`).Scan(&calls, &sqlMS, &hit, &read); err != nil {
			t.Fatal(err)
		}
		wantCalls := int64(1100)
		if mode == "batched" {
			wantCalls = 100
		}
		if calls != wantCalls {
			t.Fatalf("fanout SQL calls=%d want=%d", calls, wantCalls)
		}
		result, _ := json.Marshal(map[string]any{"arm": arm + 1, "mode": mode, "transactions": 100, "recipients_per_transaction": 10, "seconds": elapsed, "transactions_per_second": 100 / elapsed, "fanout_sql_calls": calls, "fanout_sql_execution_ms": sqlMS, "shared_blocks_hit": hit, "shared_blocks_read": read, "scope": "isolated controller transactions with row-trigger probe; not full application capacity"})
		t.Log(string(result))
	}
}
