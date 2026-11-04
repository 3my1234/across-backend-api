package controllers

// Explicit isolated handler/SQL cost experiment. This is not a concurrency,
// JWT/proxy/media/device benchmark and does not change polling behavior.
import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pollCostTrace struct {
	mu           sync.Mutex
	queries      int
	milliseconds float64
}

func (p *pollCostTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, historyTraceKey{}, time.Now())
}
func (p *pollCostTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if start, ok := ctx.Value(historyTraceKey{}).(time.Time); ok {
		p.mu.Lock()
		p.queries++
		p.milliseconds += float64(time.Since(start)) / 1e6
		p.mu.Unlock()
	}
}
func (p *pollCostTrace) reset() { p.mu.Lock(); defer p.mu.Unlock(); p.queries = 0; p.milliseconds = 0 }

func measurePollCost(t *testing.T, db *pgxpool.Pool, setup func(*pgxpool.Pool) *fiber.App, path, actor, label string) {
	t.Helper()
	trace := &pollCostTrace{}
	cfg := db.Config().Copy()
	cfg.MaxConns = 8
	cfg.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	app := setup(pool)
	defer app.Shutdown()
	call := func(etag string) (int, string) {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("X-User", actor)
		req.Header.Set("Actor", actor) // Existing community test middleware.
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		res, e := app.Test(req, 10000)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		if _, e = io.Copy(io.Discard, res.Body); e != nil {
			t.Fatal(e)
		}
		return res.StatusCode, res.Header.Get("ETag")
	}
	status, etag := call("")
	if status != 200 || (etag == "" && label != "marketplace_list_50x100") {
		t.Fatalf("initial status=%d ETag=%q", status, etag)
	}
	for arm, conditional := range []bool{false, true, true, false} {
		conditional = conditional && etag != ""
		trace.reset()
		latencies := []float64{}
		for i := 0; i < 20; i++ {
			tag := ""
			want := 200
			if conditional {
				tag = etag
				want = 304
			}
			start := time.Now()
			status, next := call(tag)
			latencies = append(latencies, float64(time.Since(start))/1e6)
			if status != want || next != etag {
				t.Fatalf("conditional=%v status=%d expected=%d or ETag rollover; rerun separately", conditional, status, want)
			}
		}
		trace.mu.Lock()
		queries, ms := trace.queries, trace.milliseconds
		trace.mu.Unlock()
		raw, _ := json.Marshal(map[string]any{"case": label, "arm": arm, "conditional": conditional, "requests": 20, "sql_queries": queries, "sql_queries_per_request": float64(queries) / 20, "sql_roundtrip_ms_total": ms, "handler_p50_ms": historyPercentile(latencies, .5), "handler_p95_ms": historyPercentile(latencies, .95), "scope": "sequential actual handler with PostgreSQL; SQL wall time is not CPU; no capacity claim"})
		t.Log("POLL_COST_RESULT " + string(raw))
	}
}

func TestPollingCostActualHandlers(t *testing.T) {
	if os.Getenv("POLL_COST_LOCAL") != "true" {
		t.Skip("explicit disposable handler cost experiment")
	}
	t.Run("marketplace_list", func(t *testing.T) {
		db := communityTestDB(t)
		ctx := context.Background()
		actor, owner, provider := uuid.NewString(), uuid.NewString(), uuid.NewString()
		for _, sql := range []string{"ALTER TABLE provider_conversation_messages ADD COLUMN body text DEFAULT 'Synthetic poll message'", "CREATE INDEX poll_list_history ON provider_conversation_messages(conversation_id,created_at DESC,id)"} {
			if _, err := db.Exec(ctx, sql); err != nil {
				t.Fatal(err)
			}
		}
		statements := []struct {
			sql  string
			args []any
		}{
			{"INSERT INTO users(id,full_name) VALUES($1,'Synthetic buyer'),($2,'Synthetic provider')", []any{actor, owner}},
			{"INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status) VALUES($1,$2,'Synthetic provider','approved')", []any{provider, owner}},
			{"INSERT INTO provider_listings(id,provider_id,title,status,published_at) SELECT md5('poll-listing-'||i)::uuid,$1,'Synthetic listing','approved',now() FROM generate_series(1,50)i", []any{provider}},
			{"INSERT INTO provider_conversations(id,provider_id,user_id,listing_id,status,last_message_at) SELECT gen_random_uuid(),$1,$2,id,'open',now() FROM provider_listings", []any{provider, actor}},
			{"INSERT INTO provider_conversation_messages(id,conversation_id,sender_type,created_at) SELECT gen_random_uuid(),c.id,'provider',now()-j*interval '1 second' FROM provider_conversations c CROSS JOIN generate_series(1,100)j", nil},
		}
		for _, s := range statements {
			if _, err := db.Exec(ctx, s.sql, s.args...); err != nil {
				t.Fatal(err)
			}
		}
		setup := func(pool *pgxpool.Pool) *fiber.App {
			app := fiber.New()
			controller := &ProviderMarketplaceController{db: pool}
			app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("X-User")); return c.Next() })
			app.Get("/conversations", controller.ListBuyerConversations)
			return app
		}
		measurePollCost(t, db, setup, "/conversations?limit=50", actor, "marketplace_list_50x100")
	})
	t.Run("private", func(t *testing.T) {
		db := historyInvestigationDB(t)
		setup := func(pool *pgxpool.Pool) *fiber.App {
			app := fiber.New()
			controller := &ProviderMarketplaceController{db: pool}
			app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("X-User")); return c.Next() })
			app.Get("/history/:conversation_id", controller.BuyerConversationMessages)
			return app
		}
		for _, c := range []struct{ label, thread, user string }{{"private_hot_10000", "c3", "u3"}, {"private_grouped", "c5", "u4"}} {
			measurePollCost(t, db, setup, "/history/"+historyFixtureUUID(c.thread)+"?limit=50&mark_read=false", historyFixtureUUID(c.user), c.label)
		}
	})
	t.Run("town_hall", func(t *testing.T) {
		db := communityTestDB(t)
		actor, room := uuid.NewString(), uuid.NewString()
		ctx := context.Background()
		for _, query := range []string{"INSERT INTO users(id,full_name) VALUES($1,'Synthetic poll buyer')", "INSERT INTO chat_rooms(id,kind,owner_user_id,title) VALUES($2,'town_hall',$1,'Synthetic poll room')", "INSERT INTO chat_participants(room_id,user_id,role) VALUES($2,$1,'buyer')", "INSERT INTO chat_messages(room_id,sender_user_id,body,client_message_id) SELECT $2,$1,'Synthetic poll message',gen_random_uuid() FROM generate_series(1,1000)"} {
			args := []any{actor}
			if query != "INSERT INTO users(id,full_name) VALUES($1,'Synthetic poll buyer')" {
				args = append(args, room)
			}
			if _, err := db.Exec(ctx, query, args...); err != nil {
				t.Fatal(err)
			}
		}
		measurePollCost(t, db, communityApp, "/rooms/"+room+"/messages?limit=40&mark_read=false", actor, "town_hall_buyer_1000")
	})
}
