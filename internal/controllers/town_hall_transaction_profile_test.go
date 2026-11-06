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
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type hallProfileQuery struct {
	start time.Time
	sql   string
}
type hallProfileQueryKey struct{}
type hallProfileAcquireKey struct{}
type hallProfileRequest struct {
	Op                       string             `json:"op"`
	Status                   int                `json:"status"`
	TotalMS                  float64            `json:"total_ms"`
	PoolMS                   float64            `json:"pool_acquire_ms"`
	LockMS                   float64            `json:"lock_statement_ms"`
	HoldMS                   float64            `json:"lock_held_through_commit_ms"`
	CommitMS                 float64            `json:"commit_ms"`
	AfterMS                  float64            `json:"handler_after_commit_ms"`
	Queries                  map[string]float64 `json:"query_and_row_consume_ms"`
	At                       float64            `json:"arm_seconds"`
	start, locked, committed time.Time
}
type hallProfileTrace struct{}

func (hallProfileTrace) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	return context.WithValue(ctx, hallProfileAcquireKey{}, time.Now())
}
func (hallProfileTrace) TraceAcquireEnd(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireEndData) {
	if p, ok := ctx.Value("hall_profile").(*hallProfileRequest); ok {
		if at, ok := ctx.Value(hallProfileAcquireKey{}).(time.Time); ok {
			p.PoolMS += float64(time.Since(at)) / 1e6
		}
	}
}
func (hallProfileTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, hallProfileQueryKey{}, hallProfileQuery{time.Now(), d.SQL})
}
func (hallProfileTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	p, ok := ctx.Value("hall_profile").(*hallProfileRequest)
	if !ok {
		return
	}
	q, ok := ctx.Value(hallProfileQueryKey{}).(hallProfileQuery)
	if !ok {
		return
	}
	ms := float64(time.Since(q.start)) / 1e6
	sql := strings.ToLower(strings.Join(strings.Fields(q.sql), " "))
	name := sql
	if len(name) > 110 {
		name = name[:110]
	}
	if strings.HasPrefix(sql, "select kind,case when exists") && strings.Contains(sql, "for update") {
		name = "room_lock"
		p.LockMS += ms
		if d.Err == nil {
			p.locked = time.Now()
		}
	}
	if strings.HasPrefix(sql, "with recipients as materialized") {
		name = "notification_fanout_with_triggers"
	}
	if sql == "commit" {
		name = "commit"
		p.CommitMS += ms
		p.committed = time.Now()
		if !p.locked.IsZero() {
			p.HoldMS = float64(p.committed.Sub(p.locked)) / 1e6
		}
	}
	p.Queries[name] += ms
}

func hallProfileStats(values []float64) map[string]any {
	if len(values) == 0 {
		return map[string]any{"count": 0}
	}
	sort.Float64s(values)
	total := 0.0
	for _, v := range values {
		total += v
	}
	at := func(p float64) float64 { return values[int(float64(len(values)-1)*p)] }
	return map[string]any{"count": len(values), "mean_ms": total / float64(len(values)), "p50_ms": at(.5), "p95_ms": at(.95), "p99_ms": at(.99), "max_ms": values[len(values)-1]}
}

// Read the exact already-reviewed synthetic dump on a newly created loopback-only
// PostgreSQL instance. No production credentials, target URL or host is accepted.
func TestTownHallTransactionBreakdown(t *testing.T) {
	if os.Getenv("TOWN_HALL_TXN_PROFILE") != "true" {
		t.Skip("explicit local disposable transaction profile only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	cfg, e := pgxpool.ParseConfig("postgres://lab@127.0.0.1:55439/townhall_profile?sslmode=disable")
	if e != nil {
		t.Fatal(e)
	}
	cfg.MaxConns = 8
	cfg.MinConns = 0
	cfg.ConnConfig.Tracer = hallProfileTrace{}
	db, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var marker string
	var actors int
	if e = db.QueryRow(ctx, `SELECT marker,(SELECT count(*) FROM capacity_lab_actors) FROM capacity_lab_identity`).Scan(&marker, &actors); e != nil || marker != "disposable-compose-only" || actors != 500 {
		t.Fatal("full500-pair synthetic fixture required", e)
	}
	type actor struct{ user, room, hot string }
	var people []actor
	rows, e := db.Query(ctx, `SELECT provider_user::text,room::text,hot_room::text FROM capacity_lab_actors ORDER BY slot LIMIT 10`)
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var a actor
		if e = rows.Scan(&a.user, &a.room, &a.hot); e != nil {
			t.Fatal(e)
		}
		people = append(people, a)
	}
	rows.Close()
	if rows.Err() != nil || len(people) != 10 {
		t.Fatal("ten synthetic providers required", rows.Err())
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	var mu sync.Mutex
	var requests []hallProfileRequest
	armStart := time.Now()
	app.Use(func(c *fiber.Ctx) error {
		p := &hallProfileRequest{Op: strings.Clone(c.Get("Profile-Op")), start: time.Now(), Queries: map[string]float64{}}
		p.At = p.start.Sub(armStart).Seconds()
		c.Locals("hall_profile", p)
		c.Locals("user_id", c.Get("Actor"))
		err := c.Next()
		p.Status = c.Response().StatusCode()
		p.TotalMS = float64(time.Since(p.start)) / 1e6
		if !p.committed.IsZero() {
			p.AfterMS = float64(time.Since(p.committed)) / 1e6
		}
		mu.Lock()
		requests = append(requests, *p)
		mu.Unlock()
		return err
	})
	controller := &CommunityController{db: db, market: &ProviderMarketplaceController{db: db}}
	catalog := NewCatalogController(db, config.Config{})
	app.Post("/hall/:room_id/offers", controller.HallOffer)
	app.Post("/rooms/:conversation_id/messages", controller.Send)
	app.Get("/products", catalog.ListProducts)
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	served := make(chan error, 1)
	go func() { served <- app.Listener(ln) }()
	defer func() { _ = app.Shutdown(); <-served }()
	transport := &http.Transport{MaxIdleConns: 32, MaxIdleConnsPerHost: 32, MaxConnsPerHost: 32}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	origin := "http://" + ln.Addr().String()
	call := func(op, path, user, body string) error {
		method := "GET"
		if body != "" {
			method = "POST"
		}
		req, e := http.NewRequest(method, origin+path, strings.NewReader(body))
		if e != nil {
			return e
		}
		req.Header.Set("Actor", user)
		req.Header.Set("Profile-Op", op)
		req.Header.Set("Content-Type", "application/json")
		res, e := client.Do(req)
		if e != nil {
			return e
		}
		defer res.Body.Close()
		raw, e := io.ReadAll(res.Body)
		if e != nil {
			return e
		}
		want := 200
		if op == "offer" {
			want = 204
		}
		if op == "send" {
			want = 201
		}
		if res.StatusCode != want {
			return fmt.Errorf("%s status%d: %s", op, res.StatusCode, string(raw[:min(len(raw), 250)]))
		}
		return nil
	}
	all := []map[string]any{}
	arms := []struct {
		name    string
		seconds int
	}{{"catalogue-alone", 30}, {"hot", 60}, {"independent", 60}, {"independent", 60}, {"hot", 60}}
	for arm, scenario := range arms {
		requests = nil
		armStart = time.Now()
		deadline := armStart.Add(time.Duration(scenario.seconds) * time.Second)
		before := db.Stat()
		failures := make(chan error, 20)
		var wg sync.WaitGroup
		workers := 20
		if scenario.name == "catalogue-alone" {
			workers = 10
		}
		for worker := 0; worker < workers; worker++ {
			wg.Add(1)
			go func(worker int) {
				defer wg.Done()
				cycle := 0
				for time.Now().Before(deadline) {
					cycle++
					began := time.Now()
					var err error
					if scenario.name == "catalogue-alone" || worker >= 10 {
						err = call("catalogue", "/products?limit=20&search=Synthetic", "", "")
					} else {
						a := people[worker]
						room := a.room
						if scenario.name == "hot" {
							room = a.hot
						}
						if cycle%2 == 1 {
							body := fmt.Sprintf(`{"unit_price":1200,"terms":"Profile only","client_message_id":"%s"}`, uuid.NewString())
							err = call("offer", "/hall/"+room+"/offers", a.user, body)
						} else {
							body := fmt.Sprintf(`{"message":"Synthetic profile only","client_message_id":"%s"}`, uuid.NewString())
							err = call("send", "/rooms/"+room+"/messages", a.user, body)
						}
					}
					if err != nil {
						failures <- err
						return
					}
					pause := time.Second
					if worker < 10 && scenario.name != "catalogue-alone" {
						pause = 5 * time.Second
					}
					if delay := pause - time.Since(began); delay > 0 {
						time.Sleep(delay)
					}
				}
			}(worker)
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			t.Error(err)
		}
		dir := os.Getenv("TOWN_HALL_PROFILE_OUTPUT")
		if dir == "" {
			t.Fatal("explicit evidence directory required")
		}
		if e = os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
		for _, r := range requests {
			if r.Op != "offer" && r.Op != "send" && r.Op != "catalogue" {
				t.Fatalf("unknown request label %q", r.Op)
			}
		}
		rawRequests, _ := json.Marshal(requests)
		if e = os.WriteFile(filepath.Join(dir, fmt.Sprintf("requests-arm%d.json", arm)), rawRequests, 0600); e != nil {
			t.Fatal(e)
		}
		after := db.Stat()
		byOp := map[string]any{}
		for _, op := range []string{"offer", "send", "catalogue"} {
			metrics := map[string][]float64{}
			statement := map[string][]float64{}
			for _, r := range requests {
				if r.Op != op {
					continue
				}
				metrics["handler_ms"] = append(metrics["handler_ms"], r.TotalMS)
				metrics["pool_acquire_ms"] = append(metrics["pool_acquire_ms"], r.PoolMS)
				if op != "catalogue" {
					metrics["room_lock_statement_ms"] = append(metrics["room_lock_statement_ms"], r.LockMS)
					metrics["lock_held_through_commit_ms"] = append(metrics["lock_held_through_commit_ms"], r.HoldMS)
					metrics["commit_ms"] = append(metrics["commit_ms"], r.CommitMS)
					metrics["after_commit_ms"] = append(metrics["after_commit_ms"], r.AfterMS)
				}
				phase := "subsequent_handler_ms"
				if r.At < 5 {
					phase = "first5s_handler_ms"
				}
				metrics[phase] = append(metrics[phase], r.TotalMS)
				for q, ms := range r.Queries {
					statement[q] = append(statement[q], ms)
				}
			}
			report := map[string]any{}
			for k, v := range metrics {
				report[k] = hallProfileStats(v)
			}
			for k, v := range statement {
				report["sql: "+k] = hallProfileStats(v)
			}
			byOp[op] = report
		}
		result := map[string]any{"arm": arm, "distribution": scenario.name, "seconds": scenario.seconds, "started_utc": armStart.UTC(), "requests": len(requests), "by_operation": byOp, "pool_wait_total_ms": float64(after.AcquireDuration()-before.AcquireDuration()) / 1e6, "scope": "actual handlers and full restored synthetic schema/triggers; loopback PostgreSQL18 on Windows; 20clients/8pool; not VPS capacity"}
		all = append(all, result)
		raw, _ := json.Marshal(result)
		t.Log("HALL_TRANSACTION_RESULT " + string(raw))
	}
	dir := os.Getenv("TOWN_HALL_PROFILE_OUTPUT")
	if dir == "" {
		t.Fatal("explicit evidence directory required")
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.MarshalIndent(all, "", "  ")
	if e = os.WriteFile(filepath.Join(dir, "transaction-breakdown.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
}
