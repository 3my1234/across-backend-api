package controllers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChatUnreadIndexOnLargeHistory(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE provider_conversation_messages(id uuid DEFAULT gen_random_uuid(),conversation_id uuid,sender_type text,created_at timestamptz);
 INSERT INTO provider_conversation_messages(conversation_id,sender_type,created_at)
 SELECT '00000000-0000-0000-0000-000000000001','provider',now()-n*interval '1 second' FROM generate_series(1,50000)n;`)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "072_chat_unread_query_index.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `ANALYZE provider_conversation_messages`); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(ctx, `EXPLAIN (ANALYZE,BUFFERS) SELECT count(*) FROM provider_conversation_messages WHERE conversation_id='00000000-0000-0000-0000-000000000001' AND sender_type='provider' AND created_at>now()-interval '1 minute'`)
	if err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err = rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line + "\n")
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "provider_conversation_unread_idx") {
		t.Fatalf("unread count did not use the range index:\n%s", plan.String())
	}
	started := time.Now()
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM provider_conversation_messages WHERE conversation_id='00000000-0000-0000-0000-000000000001' AND sender_type='provider' AND created_at>now()-interval '1 minute'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count < 50 || count > 60 {
		t.Fatalf("unexpected unread count %d", count)
	}
	t.Logf("50,000-message history unread count: %s (local isolated database)", time.Since(started))
	t.Log(plan.String())
}
