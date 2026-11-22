package realtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// This test refuses all nonlocal targets. No production data is read or written.
func TestMigrationCommittedSignals(t *testing.T) {
	url := os.Getenv("ATL_REALTIME_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set ATL_REALTIME_TEST_DATABASE_URL for isolated PostgreSQL test")
	}
	cfg, err := pgx.ParseConfig(url)
	if err != nil || cfg.Host != "127.0.0.1" || cfg.Port != 55481 {
		t.Fatal("isolated localhost PostgreSQL on port 55481 required")
	}
	ctx := context.Background()
	db, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	listener, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close(ctx)
	schema := "rt_" + stringsWithoutDashes(uuid.NewString())
	if _, err = db.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	if _, err = db.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	fixture := `CREATE TABLE chat_messages(room_id uuid,sender_user_id uuid,id uuid);
CREATE TABLE chat_participants(room_id uuid,user_id uuid,state text,last_read_at timestamptz);
CREATE TABLE town_hall_applications(room_id uuid,requester_user_id uuid,state text);
CREATE TABLE town_hall_offers(room_id uuid,unit_price numeric,terms text);
CREATE TABLE chat_rooms(id uuid,kind text,state text,context jsonb);
CREATE TABLE town_hall_requests(room_id uuid);
CREATE TABLE provider_conversations(id uuid,provider_id uuid,user_id uuid,buyer_last_read_at timestamptz,provider_last_read_at timestamptz);
CREATE TABLE provider_conversation_messages(id uuid,conversation_id uuid,sender_user_id uuid);
CREATE TABLE provider_members(provider_id uuid,user_id uuid,is_active boolean);
CREATE TABLE notifications(user_id uuid,is_read boolean);
CREATE TABLE message_receipts(channel text,message_id uuid,recipient_id uuid,delivered_at timestamptz,read_at timestamptz);`
	if _, err = db.Exec(ctx, fixture, pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "migrations", "127_realtime_signals.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(data), pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatal(err)
	}
	if _, err = listener.Exec(ctx, "LISTEN atl_realtime_wake"); err != nil {
		t.Fatal(err)
	}
	room, user := uuid.New(), uuid.New()
	if _, err = db.Exec(ctx, "INSERT INTO chat_participants(room_id,user_id,state) VALUES($1,$2,'active')", room, user); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, "UPDATE realtime_signal_policy SET enabled=true"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO chat_messages(room_id,sender_user_id,id) VALUES($1,$2,$3)", room, user, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	wait, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	_, err = listener.WaitForNotification(wait)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("rollback emitted or listener failed: %v", err)
	}
	if _, err = db.Exec(ctx, "INSERT INTO chat_messages(room_id,sender_user_id,id) VALUES($1,$2,$3)", room, user, uuid.New()); err != nil {
		t.Fatal(err)
	}
	wait, stop = context.WithTimeout(ctx, time.Second)
	note, err := listener.WaitForNotification(wait)
	stop()
	if err != nil {
		t.Fatal(err)
	}
	if len(note.Payload) != 73 || note.Payload[37:] != user.String() {
		t.Fatal("signal should contain only event and intended recipient IDs")
	}

	// Exercise a committed database write through two independent relay and
	// subscriber instances, with a single deduplicated Redis publication.
	redisServer := miniredis.RunT(t)
	cache := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer cache.Close()
	if err = cache.Set(ctx, EnabledKey, "on", 0).Err(); err != nil {
		t.Fatal(err)
	}
	poolConfig, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET search_path TO "+schema)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	firstHub, secondHub := NewHub(4, 2), NewHub(4, 2)
	first, _ := firstHub.Subscribe(user.String())
	defer first.Close()
	second, _ := secondHub.Subscribe(user.String())
	defer second.Close()
	firstBridge, secondBridge := NewRedisBridge(cache, firstHub), NewRedisBridge(cache, secondHub)
	firstRelay, secondRelay := &PostgresRelay{DB: pool, Redis: cache}, &PostgresRelay{DB: pool, Redis: cache}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{}, 4)
	for _, run := range []func(context.Context){firstBridge.Run, secondBridge.Run, firstRelay.Run, secondRelay.Run} {
		go func(f func(context.Context)) { f(runCtx); done <- struct{}{} }(run)
	}
	defer func() {
		cancel()
		for i := 0; i < 4; i++ {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("relay shutdown timed out")
			}
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !(firstBridge.Ready() && secondBridge.Ready() && firstRelay.Ready() && secondRelay.Ready()) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !(firstBridge.Ready() && secondBridge.Ready() && firstRelay.Ready() && secondRelay.Ready()) {
		t.Fatal("two-replica relay not ready")
	}
	if _, err = db.Exec(ctx, "INSERT INTO chat_messages(room_id,sender_user_id,id) VALUES($1,$2,$3)", room, user, uuid.New()); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []*Subscription{first, second} {
		select {
		case <-sub.Wakeups():
		case <-time.After(3 * time.Second):
			t.Fatal("committed write did not reach replica")
		}
	}
	first.WatchHall(true)
	if _, err = db.Exec(ctx, "INSERT INTO town_hall_requests(room_id) VALUES($1)", uuid.New()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.Wakeups():
	case <-time.After(3 * time.Second):
		t.Fatal("hall watcher did not receive request creation")
	}
	select {
	case <-second.Wakeups():
		t.Fatal("hall request reached an uninterested socket")
	case <-time.After(200 * time.Millisecond):
	}
	if _, err = db.Exec(ctx, "UPDATE chat_participants SET state='removed' WHERE room_id=$1 AND user_id=$2", room, user); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []*Subscription{first, second} {
		select {
		case <-sub.Wakeups():
		case <-time.After(3 * time.Second):
			t.Fatal("removed participant did not receive invalidation")
		}
	}
}

func stringsWithoutDashes(value string) string { return strings.ReplaceAll(value, "-", "") }
