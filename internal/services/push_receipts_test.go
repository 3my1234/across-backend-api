package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type receiptTransport func(*http.Request) (*http.Response, error)

func (f receiptTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func receiptTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("RECEIPT_TEST_LOCAL") != "true" {
		t.Skip("isolated receipt PostgreSQL tests require RECEIPT_TEST_LOCAL=true")
	}
	dsn := os.Getenv("DATABASE_URL")
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1") {
		t.Fatal("receipt tests require an explicitly supplied loopback database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "receipt_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 8
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		_, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
		if err != nil {
			t.Error(err)
		}
	})
	_, err = db.Exec(ctx, `CREATE TABLE user_push_tokens(id uuid PRIMARY KEY,disabled_at timestamptz,updated_at timestamptz);
	CREATE TABLE notification_push_deliveries(notification_id uuid,push_token_id uuid,status text NOT NULL DEFAULT 'sent',
	expo_ticket_id text,sent_at timestamptz DEFAULT now()-interval '16 minutes',receipt_checked_at timestamptz,last_error text DEFAULT '',updated_at timestamptz DEFAULT now(),PRIMARY KEY(notification_id,push_token_id));`)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "108_push_receipt_claims.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedReceipts(t *testing.T, db *pgxpool.Pool, n int) {
	t.Helper()
	_, err := db.Exec(context.Background(), `WITH token AS (INSERT INTO user_push_tokens(id) VALUES(gen_random_uuid()) RETURNING id)
	INSERT INTO notification_push_deliveries(notification_id,push_token_id,expo_ticket_id)
	SELECT gen_random_uuid(),token.id,'ticket-'||generate_series(1,$1)::text FROM token`, n)
	if err != nil {
		t.Fatal(err)
	}
}

func successfulReceipts(t *testing.T, calls *atomic.Int32) *http.Client {
	return &http.Client{Transport: receiptTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != expoPushReceiptsURL {
			t.Error("unexpected external target")
			return nil, fmt.Errorf("blocked")
		}
		var payload struct {
			IDs []string `json:"ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, err
		}
		if len(payload.IDs) > 100 {
			t.Error("receipt request exceeded historical batch limit")
		}
		calls.Add(1)
		data := map[string]expoTicket{}
		for _, id := range payload.IDs {
			data[id] = expoTicket{Status: "ok"}
		}
		body, _ := json.Marshal(map[string]any{"data": data})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
	})}
}

func TestReceiptConcurrentClaimCrashRecoveryAndFencing(t *testing.T) {
	db := receiptTestDB(t)
	seedReceipts(t, db, 2)
	ctx := context.Background()
	owner, other := uuid.NewString(), uuid.NewString()
	items, err := claimPushReceipts(ctx, db, owner)
	if err != nil || len(items) != 2 {
		t.Fatalf("claim: %d %v", len(items), err)
	}
	// An actual competing connection cannot claim or contact the provider.
	var calls atomic.Int32
	if n, err := RunPushReceiptBatch(ctx, db, successfulReceipts(t, &calls)); err != nil || n != 0 || calls.Load() != 0 {
		t.Fatal("overlapping dispatcher", n, err, calls.Load())
	}
	if ok, err := finishPushReceipt(ctx, db, other, items[0], expoTicket{Status: "ok"}); err != nil || ok {
		t.Fatal("foreign completion", ok, err)
	}
	// Simulated killed owner: no release/cleanup. Expire durable deadlines without
	// altering receipt eligibility, then prove a new worker can recover the jobs.
	if _, err := db.Exec(ctx, `UPDATE notification_push_deliveries SET receipt_lease_until=now()-interval '1 second';
	UPDATE push_receipt_dispatch SET lease_until=now()-interval '1 second',next_attempt_at=now()-interval '1 second';`); err != nil {
		t.Fatal(err)
	}
	recovered, err := claimPushReceipts(ctx, db, other)
	if err != nil || len(recovered) != 2 {
		t.Fatal("recovery", len(recovered), err)
	}
	if ok, err := finishPushReceipt(ctx, db, owner, items[0], expoTicket{Status: "ok"}); err != nil || ok {
		t.Fatal("stale owner committed", ok, err)
	}
	for _, item := range recovered {
		if ok, err := finishPushReceipt(ctx, db, other, item, expoTicket{Status: "ok"}); err != nil || !ok {
			t.Fatal("recovered completion", ok, err)
		}
		if ok, err := finishPushReceipt(ctx, db, other, item, expoTicket{Status: "ok"}); err != nil || ok {
			t.Fatal("duplicate completion", ok, err)
		}
	}
}

func TestReceiptEligibilityDrainAndBoundedRequests(t *testing.T) {
	db := receiptTestDB(t)
	seedReceipts(t, db, 620)
	ctx := context.Background()
	_, err := db.Exec(ctx, `UPDATE notification_push_deliveries SET sent_at=now() WHERE expo_ticket_id IN('ticket-619','ticket-620')`)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	start := time.Now()
	n, err := RunPushReceiptDrain(ctx, db, successfulReceipts(t, &calls))
	if err != nil || n != 500 || calls.Load() != 5 {
		t.Fatalf("bounded drain n=%d calls=%d err=%v", n, calls.Load(), err)
	}
	if time.Since(start) < 4*time.Second {
		t.Fatal("global one-request/second spacing bypassed")
	}
	var fresh, remaining int
	if err := db.QueryRow(ctx, `SELECT count(*) FILTER(WHERE receipt_checked_at IS NOT NULL AND sent_at>now()-interval '15 minutes'),count(*) FILTER(WHERE status='sent') FROM notification_push_deliveries`).Scan(&fresh, &remaining); err != nil || fresh != 0 || remaining != 120 {
		t.Fatal("eligibility/budget", fresh, remaining, err)
	}
}

func TestReceiptMissingAndProviderBackoff(t *testing.T) {
	db := receiptTestDB(t)
	seedReceipts(t, db, 101)
	ctx := context.Background()
	missing := &http.Client{Transport: receiptTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":{}}`))}, nil
	})}
	if n, err := RunPushReceiptBatch(ctx, db, missing); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	var backedOff int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM notification_push_deliveries WHERE receipt_next_attempt_at>now() AND receipt_claim IS NULL`).Scan(&backedOff); err != nil || backedOff != 100 {
		t.Fatal("missing receipt backoff", backedOff, err)
	}
	// Move only global pacing clock, leaving per-row retry deadlines untouched.
	if _, err := db.Exec(ctx, `UPDATE push_receipt_dispatch SET next_attempt_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	if n, err := RunPushReceiptBatch(ctx, db, successfulReceipts(t, &calls)); err != nil || n != 1 {
		t.Fatal("missing oldest receipts starved healthy work", n, err)
	}
	if _, err := db.Exec(ctx, `UPDATE push_receipt_dispatch SET next_attempt_at=now()-interval '1 second'; UPDATE notification_push_deliveries SET receipt_next_attempt_at=now()-interval '1 second' WHERE status='sent'`); err != nil {
		t.Fatal(err)
	}
	busy := &http.Client{Transport: receiptTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"120"}}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})}
	if _, err := RunPushReceiptBatch(ctx, db, busy); err == nil {
		t.Fatal("provider failure hidden")
	}
	var seconds float64
	if err := db.QueryRow(ctx, `SELECT extract(epoch FROM next_attempt_at-now()) FROM push_receipt_dispatch`).Scan(&seconds); err != nil || seconds < 119 {
		t.Fatal("Retry-After not respected", seconds, err)
	}
	if n, err := RunPushReceiptBatch(ctx, db, successfulReceipts(t, &calls)); err != nil || n != 0 || calls.Load() != 1 {
		t.Fatal("global cooldown bypassed", n, err)
	}
}

func TestReceiptCancellationAndDeviceRejection(t *testing.T) {
	db := receiptTestDB(t)
	seedReceipts(t, db, 1)
	ctx := context.Background()
	deadline, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	slow := &http.Client{Transport: receiptTransport(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })}
	if _, err := RunPushReceiptBatch(deadline, db, slow); err == nil {
		t.Fatal("cancelled request accepted")
	}
	var claims int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM notification_push_deliveries WHERE receipt_claim IS NOT NULL`).Scan(&claims); err != nil || claims != 0 {
		t.Fatal("cancel cleanup", claims, err)
	}
	if _, err := db.Exec(ctx, `UPDATE push_receipt_dispatch SET next_attempt_at=now()-interval '1 second'; UPDATE notification_push_deliveries SET receipt_next_attempt_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	owner := uuid.NewString()
	items, err := claimPushReceipts(ctx, db, owner)
	if err != nil || len(items) != 1 {
		t.Fatal(len(items), err)
	}
	rejected := expoTicket{Status: "error"}
	rejected.Details.Error = "DeviceNotRegistered"
	if ok, err := finishPushReceipt(ctx, db, owner, items[0], rejected); err != nil || !ok {
		t.Fatal(ok, err)
	}
	var disabled bool
	if err := db.QueryRow(ctx, `SELECT disabled_at IS NOT NULL FROM user_push_tokens`).Scan(&disabled); err != nil || !disabled {
		t.Fatal("device token not disabled", disabled, err)
	}
}

func TestReceiptRetryAfterParsing(t *testing.T) {
	now := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	for input, want := range map[string]time.Duration{"120": 120 * time.Second, "bad": 0, "-1": 0, now.Add(time.Minute).Format(http.TimeFormat): time.Minute} {
		if got := receiptRetryAfter(input, now); got != want {
			t.Fatalf("%q: %v != %v", input, got, want)
		}
	}
}

// Deliberately separate from the regressions: actual 15-second scheduling over
// identical rows in ABBA order. This measures receipt policy capacity, not HTTP
// session capacity, vendor delivery, or maximum backend throughput.
func TestReceiptMatchedPolicyComparison(t *testing.T) {
	if os.Getenv("RECEIPT_POLICY_COMPARE") != "true" {
		t.Skip("explicitly enable the two-minute isolated policy comparison")
	}
	template := receiptTestDB(t)
	seedReceipts(t, template, 1000)
	schema := template.Config().ConnConfig.RuntimeParams["search_path"]
	for arm, candidate := range []bool{false, true, true, false} {
		db := receiptTestDB(t)
		ctx := context.Background()
		_, err := db.Exec(ctx, "INSERT INTO user_push_tokens SELECT * FROM "+schema+".user_push_tokens; INSERT INTO notification_push_deliveries SELECT * FROM "+schema+".notification_push_deliveries")
		if err != nil {
			t.Fatal(err)
		}
		var calls atomic.Int32
		client := successfulReceipts(t, &calls)
		start := time.Now()
		total := 0
		for tick := 0; tick < 2; tick++ {
			var n int
			if candidate {
				n, err = RunPushReceiptDrain(ctx, db, client)
			} else {
				n, err = runLegacyReceiptBatch(ctx, db, client)
			}
			if err != nil {
				t.Fatal(err)
			}
			total += n
			time.Sleep(time.Until(start.Add(time.Duration(tick+1) * 15 * time.Second)))
		}
		var stored int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM notification_push_deliveries WHERE status='delivered' AND receipt_checked_at IS NOT NULL`).Scan(&stored); err != nil || stored != total {
			t.Fatal("comparison reconciliation", stored, total, err)
		}
		want := 200
		if candidate {
			want = 1000
		}
		if stored != want {
			t.Fatalf("arm %d candidate=%v: stored=%d want=%d", arm, candidate, stored, want)
		}
		elapsed := time.Since(start).Seconds()
		t.Logf("RECEIPT_POLICY_RESULT arm=%d candidate=%v rows=%d seconds=%.3f checks_per_second=%.3f requests=%d", arm+1, candidate, stored, elapsed, float64(stored)/elapsed, calls.Load())
	}
}
