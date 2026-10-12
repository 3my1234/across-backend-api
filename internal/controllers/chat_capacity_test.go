package controllers

import (
	"context"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"
)

func TestChatConditionalReadsConcurrentClientsAndBackdatedMessages(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	buyer, other, thread := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(ctx, `CREATE TABLE provider_conversations(id uuid PRIMARY KEY,user_id uuid,provider_id uuid,buyer_last_read_at timestamptz,provider_last_read_at timestamptz);
 CREATE TABLE provider_conversation_messages(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),conversation_id uuid,sender_type text,body text,created_at timestamptz DEFAULT now(),media_keys text[] DEFAULT '{}');
 CREATE INDEX chat_page_idx ON provider_conversation_messages(conversation_id,created_at,id);`); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "074_chat_history_revision.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_conversations(id,user_id) VALUES($1,$2)`, thread, buyer); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_conversation_messages(conversation_id,sender_type,body) SELECT $1,'buyer','Fixture' FROM generate_series(1,500)`, thread); err != nil {
		t.Fatal(err)
	}
	controller := &ProviderMarketplaceController{db: db}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("X-User")); return c.Next() })
	app.Get("/chat/:conversation_id", controller.BuyerConversationMessages)
	call := func(actor, tag string, want int) string {
		t.Helper()
		req := httptest.NewRequest("GET", "/chat/"+thread+"?mark_read=false&limit=50", nil)
		req.Header.Set("X-User", actor)
		req.Header.Set("If-None-Match", tag)
		res, err := app.Test(req, 6000)
		if err != nil {
			t.Error(err)
			return ""
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("status=%d want=%d", res.StatusCode, want)
		}
		return res.Header.Get("ETag")
	}
	tag := call(buyer, "", 200)
	if tag == "" {
		t.Fatal("etag missing")
	}
	call(buyer, tag, 304)
	call(other, tag, 404)
	const clients = 100
	var group sync.WaitGroup
	times := make([]time.Duration, clients)
	for i := 0; i < clients; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			start := time.Now()
			call(buyer, tag, 304)
			times[i] = time.Since(start)
		}(i)
	}
	group.Wait()
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	t.Logf("100 simultaneous local conditional chat reads: p95=%s; pool max=%d; in-process HTTP, not production capacity", times[94], db.Stat().MaxConns())
	// A late/backdated commit must invalidate the cached page despite its old timestamp.
	if _, err = db.Exec(ctx, `INSERT INTO provider_conversation_messages(conversation_id,sender_type,body,created_at) VALUES($1,'buyer','Backdated','2020-01-01')`, thread); err != nil {
		t.Fatal(err)
	}
	newer := call(buyer, tag, 200)
	if newer == tag {
		t.Fatal("backdated message did not invalidate history")
	}
	if _, err = db.Exec(ctx, `DELETE FROM provider_conversation_messages WHERE body='Backdated'`); err != nil {
		t.Fatal(err)
	}
	call(buyer, newer, 200)
	if _, err = db.Exec(ctx, `ALTER TABLE provider_conversations RENAME COLUMN history_revision TO revision_hidden`); err != nil {
		t.Fatal(err)
	}
	if got := call(buyer, newer, 200); got != "" {
		t.Fatal("pre-migration schema must not produce cached 304 responses")
	}
}
