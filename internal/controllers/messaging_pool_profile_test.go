package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Opt-in diagnostic, not a replacement for authenticated k6 qualification.
// The same source is run against the unmodified and candidate controller code.
func TestMessagingPoolProfile(t *testing.T) {
	if os.Getenv("MESSAGING_POOL_PROFILE") != "true" {
		t.Skip("explicit disposable full-fixture diagnostic only")
	}
	dir := os.Getenv("MESSAGING_POOL_OUTPUT")
	if dir == "" {
		t.Fatal("evidence directory required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cfg, e := pgxpool.ParseConfig("postgres://lab@127.0.0.1:55439/townhall_profile?sslmode=disable")
	if e != nil {
		t.Fatal(e)
	}
	cfg.MaxConns = 8
	cfg.MinConns = 0
	cfg.ConnConfig.Tracer = hallProfileTrace{}
	var pools []*pgxpool.Pool
	for i := 0; i < 2; i++ {
		db, e := pgxpool.NewWithConfig(ctx, cfg.Copy())
		if e != nil {
			t.Fatal(e)
		}
		pools = append(pools, db)
		defer db.Close()
	}
	var marker string
	var count int
	if e = pools[0].QueryRow(ctx, `SELECT marker,(SELECT count(*) FROM capacity_lab_actors) FROM capacity_lab_identity`).Scan(&marker, &count); e != nil || marker != "disposable-compose-only" || count != 500 {
		t.Fatal("not the full disposable fixture", e)
	}
	type actor struct{ user, buyer, room, hot, conversation string }
	var actors []actor
	rows, e := pools[0].Query(ctx, `SELECT provider_user::text,buyer::text,room::text,hot_room::text,conversation::text FROM capacity_lab_actors ORDER BY slot LIMIT 100`)
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var a actor
		if e = rows.Scan(&a.user, &a.buyer, &a.room, &a.hot, &a.conversation); e != nil {
			t.Fatal(e)
		}
		actors = append(actors, a)
	}
	rows.Close()
	if rows.Err() != nil || len(actors) != 100 {
		t.Fatal("100 actors required", rows.Err())
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	var mu sync.Mutex
	var requests []hallProfileRequest
	started := time.Now()
	app.Use(func(c *fiber.Ctx) error {
		p := &hallProfileRequest{Op: strings.Clone(c.Get("Profile-Op")), start: time.Now(), Queries: map[string]float64{}}
		p.At = p.start.Sub(started).Seconds()
		c.Locals("hall_profile", p)
		c.Locals("user_id", c.Get("Actor"))
		e := c.Next()
		p.Status = c.Response().StatusCode()
		p.TotalMS = float64(time.Since(p.start)) / 1e6
		mu.Lock()
		requests = append(requests, *p)
		mu.Unlock()
		return e
	})
	for i, db := range pools {
		s := &CommunityController{db: db, market: &ProviderMarketplaceController{db: db}}
		prefix := fmt.Sprintf("/api%d", i)
		app.Post(prefix+"/hall/:room_id/offers", s.HallOffer)
		app.Post(prefix+"/rooms/:conversation_id/messages", s.Send)
		app.Post(prefix+"/market/:conversation_id/messages", s.market.SendBuyerConversationMessage)
		app.Get(prefix+"/market/:conversation_id/messages", s.market.BuyerConversationMessages)
		app.Get(prefix+"/inbox", s.Inbox)
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	served := make(chan error, 1)
	go func() { served <- app.Listener(ln) }()
	defer func() { _ = app.Shutdown(); <-served }()
	transport := &http.Transport{MaxIdleConns: 120, MaxIdleConnsPerHost: 120, MaxConnsPerHost: 120}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second}
	origin := "http://" + ln.Addr().String()
	failures := make(chan error, 100)
	var wg sync.WaitGroup
	// 100 active clients, ten eligible hot-room providers and ninety independent
	// private chats. Preserve a five-second action cadence with synchronized starts.
	started = time.Now()
	deadline := started.Add(60 * time.Second)
	for worker := 0; worker < 100; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			a := actors[worker]
			for cycle := 0; time.Now().Before(deadline); cycle++ {
				began := time.Now()
				op, path, user, body, want := "", "", a.buyer, "", 200
				if worker < 10 {
					user = a.user
					if cycle%2 == 0 {
						op = "hall_offer"
						path = "/hall/" + a.hot + "/offers"
						body = fmt.Sprintf(`{"unit_price":1200,"terms":"Pool profile","client_message_id":"%s"}`, uuid.NewString())
						want = 204
					} else {
						op = "hall_send"
						path = "/rooms/" + a.hot + "/messages"
						body = fmt.Sprintf(`{"message":"Pool profile","client_message_id":"%s"}`, uuid.NewString())
						want = 201
					}
				} else {
					switch cycle % 3 {
					case 0:
						op = "market_send"
						path = "/market/" + a.conversation + "/messages"
						body = fmt.Sprintf(`{"message":"Pool profile","client_message_id":"%s"}`, uuid.NewString())
						want = 201
					case 1:
						op = "market_history"
						path = "/market/" + a.conversation + "/messages?mark_read=false"
					case 2:
						op = "inbox"
						path = "/inbox"
					}
				}
				method := "GET"
				if body != "" {
					method = "POST"
				}
				req, e := http.NewRequestWithContext(ctx, method, origin+fmt.Sprintf("/api%d", worker%2)+path, strings.NewReader(body))
				if e != nil {
					failures <- e
					return
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Actor", user)
				req.Header.Set("Profile-Op", op)
				res, e := client.Do(req)
				if e != nil {
					failures <- e
					return
				}
				raw, e := io.ReadAll(res.Body)
				res.Body.Close()
				if e != nil || res.StatusCode != want {
					failures <- fmt.Errorf("%s status%d err%v body%.180s", op, res.StatusCode, e, raw)
					return
				}
				if pause := 5*time.Second - time.Since(began); pause > 0 {
					time.Sleep(pause)
				}
			}
		}(worker)
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		t.Error(e)
	}
	report := map[string]any{"clients": 100, "api_pools": 2, "connections_per_pool": 8, "seconds": time.Since(started).Seconds(), "requests": len(requests), "scope": "full fixture, actual handlers/triggers; 10 hot-room +90 private clients; auth bypassed, local PG18 Windows; not VPS capacity"}
	byOp := map[string]any{}
	for _, op := range []string{"hall_offer", "hall_send", "market_send", "market_history", "inbox"} {
		var total, pool, lock, hold []float64
		for _, r := range requests {
			if r.Op == op {
				total = append(total, r.TotalMS)
				pool = append(pool, r.PoolMS)
				lock = append(lock, r.LockMS)
				hold = append(hold, r.HoldMS)
			}
		}
		byOp[op] = map[string]any{"handler_ms": hallProfileStats(total), "pool_ms": hallProfileStats(pool), "room_lock_ms": hallProfileStats(lock), "lock_hold_ms": hallProfileStats(hold)}
	}
	report["by_operation"] = byOp
	if e = os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.MarshalIndent(report, "", "  ")
	if e = os.WriteFile(filepath.Join(dir, "result.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	raw, _ = json.Marshal(requests)
	if e = os.WriteFile(filepath.Join(dir, "requests.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	raw, _ = json.Marshal(report)
	t.Log("MESSAGING_POOL_RESULT " + string(raw))
}
