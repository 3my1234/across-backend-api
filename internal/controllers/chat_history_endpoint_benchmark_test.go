package controllers

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type historyTraceKey struct{}
type historyTrace struct {
	mu      sync.Mutex
	samples []float64
}

func (s *historyTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "SELECT msg.id::text") {
		return context.WithValue(ctx, historyTraceKey{}, time.Now())
	}
	return ctx
}
func (s *historyTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if began, ok := ctx.Value(historyTraceKey{}).(time.Time); ok {
		s.mu.Lock()
		s.samples = append(s.samples, float64(time.Since(began))/1e6)
		s.mu.Unlock()
	}
}
func historyPercentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	sort.Float64s(v)
	return v[int(math.Ceil(float64(len(v))*p))-1]
}
func historyFixtureUUID(s string) string {
	h := md5.Sum([]byte(s))
	v := hex.EncodeToString(h[:])
	return v[:8] + "-" + v[8:12] + "-" + v[12:16] + "-" + v[16:20] + "-" + v[20:]
}

func TestBusinessHistoryEndpointPerformance(t *testing.T) {
	if os.Getenv("CHAT_HISTORY_INVESTIGATE") != "true" {
		t.Skip("explicit loopback-only performance experiment")
	}
	variant := os.Getenv("CHAT_HISTORY_VARIANT")
	if variant != "original" && variant != "optimized" {
		t.Fatal("explicit original/optimized label required")
	}
	db := historyInvestigationDB(t)
	ctx := context.Background()
	trace := &historyTrace{}
	cfg := db.Config().Copy()
	cfg.MaxConns = 8
	cfg.MinConns = 8
	cfg.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	held := make([]*pgxpool.Conn, 8)
	pids := make([]uint32, 8)
	for i := range held {
		held[i], err = pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		pids[i] = held[i].Conn().PgConn().PID()
	}
	for _, c := range held {
		c.Release()
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	controller := &ProviderMarketplaceController{db: pool}
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("X-User")); return c.Next() })
	app.Get("/history/:conversation_id", controller.BuyerConversationMessages)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- app.Listener(ln) }()
	defer func() { _ = app.Shutdown(); <-served }()
	transport := &http.Transport{MaxIdleConns: 32, MaxIdleConnsPerHost: 32, MaxConnsPerHost: 32}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	type scenario struct {
		name, path, user string
		expected         []string
	}
	scenarios := []scenario{}
	for _, v := range []struct {
		name         string
		thread, user int
	}{{"small", 1, 1}, {"large", 2, 2}, {"hot", 3, 3}, {"grouped", 5, 4}} {
		thread := historyFixtureUUID(fmt.Sprintf("c%d", v.thread))
		user := historyFixtureUUID(fmt.Sprintf("u%d", v.user))
		for _, cursor := range []bool{false, true} {
			path := "http://" + ln.Addr().String() + "/history/" + thread + "?mark_read=false&limit=50"
			name := v.name + "_first"
			var ct any
			var cid any
			if cursor {
				stamp := time.Date(2026, 1, 1, 0, 2, 30, 0, time.UTC)
				cid = "ffffffff-ffff-ffff-ffff-ffffffffffff"
				ct = stamp
				path += "&cursor=" + encodeAdminCursor(stamp, cid.(string))
				name = v.name + "_cursor"
			}
			rows, e := db.Query(ctx, originalHistoryQuery(), thread, ct, cid, 51)
			if e != nil {
				t.Fatal(e)
			}
			expected := []string{}
			for rows.Next() {
				values, e := rows.Values()
				if e != nil {
					t.Fatal(e)
				}
				expected = append(expected, values[0].(string))
			}
			if rows.Err() != nil {
				t.Fatal(rows.Err())
			}
			rows.Close()
			if len(expected) > 50 {
				expected = expected[:50]
			}
			for a, b := 0, len(expected)-1; a < b; a, b = a+1, b-1 {
				expected[a], expected[b] = expected[b], expected[a]
			}
			scenarios = append(scenarios, scenario{name, path, user, expected})
		}
	}
	call := func(v scenario) error {
		req, _ := http.NewRequest("GET", v.path, nil)
		req.Header.Set("X-User", v.user)
		res, e := client.Do(req)
		if e != nil {
			return e
		}
		defer res.Body.Close()
		raw, e := io.ReadAll(res.Body)
		if e != nil {
			return e
		}
		if res.StatusCode != 200 {
			return fmt.Errorf("status %d", res.StatusCode)
		}
		var data struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		if e = json.Unmarshal(raw, &data); e != nil {
			return e
		}
		if len(data.Items) != len(v.expected) {
			return fmt.Errorf("%s incorrect page length", v.name)
		}
		for i, item := range data.Items {
			if item.ID != v.expected[i] {
				return fmt.Errorf("%s page identity/order mismatch", v.name)
			}
		}
		return nil
	}
	// Warm each case equally, independent of the source variant.
	for i := 0; i < 80; i++ {
		if err = call(scenarios[i%len(scenarios)]); err != nil {
			t.Fatal(err)
		}
	}
	trace.mu.Lock()
	trace.samples = nil
	trace.mu.Unlock()
	var countBefore int64
	if err = db.QueryRow(ctx, "SELECT count(*) FROM provider_conversation_messages").Scan(&countBefore); err != nil {
		t.Fatal(err)
	}
	if countBefore != 317105 {
		t.Fatal("unexpected fixture count", countBefore)
	}
	before := pool.Stat()
	cpuStart, err := historyBackendCPU(pids)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	deadline := started.Add(8 * time.Second)
	var group sync.WaitGroup
	var mu sync.Mutex
	latencies := []float64{}
	errors := 0
	byCase := map[string][]float64{}
	for worker := 0; worker < 16; worker++ {
		group.Add(1)
		go func(n int) {
			defer group.Done()
			for i := n; time.Now().Before(deadline); i++ {
				v := scenarios[i%len(scenarios)]
				began := time.Now()
				e := call(v)
				ms := float64(time.Since(began)) / 1e6
				mu.Lock()
				if e != nil {
					errors++
				} else {
					latencies = append(latencies, ms)
					byCase[v.name] = append(byCase[v.name], ms)
				}
				mu.Unlock()
			}
		}(worker)
	}
	group.Wait()
	elapsed := time.Since(started)
	cpuEnd, err := historyBackendCPU(pids)
	if err != nil {
		t.Fatal(err)
	}
	after := pool.Stat()
	var countAfter int64
	if err = db.QueryRow(ctx, "SELECT count(*) FROM provider_conversation_messages").Scan(&countAfter); err != nil {
		t.Fatal(err)
	}
	if countAfter != countBefore {
		t.Fatal("read experiment changed message count", countAfter, countBefore)
	}
	caseMetrics := map[string]any{}
	for name, v := range byCase {
		caseMetrics[name] = map[string]any{"requests": len(v), "p95_ms": historyPercentile(v, .95), "p99_ms": historyPercentile(v, .99)}
	}
	result := map[string]any{"variant": variant, "run": os.Getenv("CHAT_HISTORY_RUN"), "scope": "actual buyer-history handler over loopback HTTP; synthetic PostgreSQL18/Windows; 16 clients/8 DB connections; read-only workload; not whole-platform capacity", "successful_requests": len(latencies), "errors": errors, "elapsed_seconds": elapsed.Seconds(), "requests_per_second": float64(len(latencies)) / elapsed.Seconds(), "p50_ms": historyPercentile(latencies, .5), "p95_ms": historyPercentile(latencies, .95), "p99_ms": historyPercentile(latencies, .99), "history_query_p95_ms": historyPercentile(trace.samples, .95), "history_query_p99_ms": historyPercentile(trace.samples, .99), "backend_cpu_seconds": cpuEnd - cpuStart, "backend_cpu_percent_one_core": (cpuEnd - cpuStart) / elapsed.Seconds() * 100, "backend_cpu_ms_per_request": (cpuEnd - cpuStart) * 1000 / float64(len(latencies)), "pool_acquire_total_ms": float64(after.AcquireDuration()-before.AcquireDuration()) / 1e6, "pool_empty_acquires": after.EmptyAcquireCount() - before.EmptyAcquireCount(), "message_count_unchanged": true, "by_case": caseMetrics}
	raw, _ := json.MarshalIndent(result, "", "  ")
	dir := os.Getenv("CHAT_HISTORY_EVIDENCE_DIR")
	if dir == "" {
		t.Fatal("evidence directory required")
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, variant+"-http-"+os.Getenv("CHAT_HISTORY_RUN")+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log(string(raw))
	if errors != 0 {
		t.Fatal("HTTP experiment had failed requests", errors)
	}
}
