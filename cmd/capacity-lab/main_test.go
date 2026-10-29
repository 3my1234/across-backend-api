package main

import (
	"across/backend/internal/auth"
	"across/backend/internal/migrations"
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMetricsDoNotCountOverloadOrFastFailuresAsSuccess(t *testing.T) {
	m := &metric{}
	m.add(sample{Status: 200, Valid: true, Elapsed: 300 * time.Millisecond})
	m.add(sample{Status: 503, Elapsed: time.Millisecond})
	m.add(sample{Status: 500, Elapsed: time.Millisecond})
	m.add(sample{Status: 200, Elapsed: time.Millisecond}) // Invalid response body.
	m.finish()
	if m.Requests != 4 || m.Success != 1 || m.Busy != 1 || m.Errors != 2 || m.P95MS != 300 {
		t.Fatalf("incorrect success accounting: %+v", m)
	}
}

func TestComparisonPercentilesAndAcknowledgementKeys(t *testing.T) {
	m := &metric{}
	for i := 1; i <= 100; i++ {
		m.add(sample{Status: 200, Valid: true, Elapsed: time.Duration(i) * time.Millisecond})
	}
	m.finish()
	if m.P50MS != 50 || m.P95MS != 95 || m.P99MS != 99 {
		t.Fatalf("wrong percentile ranks: %+v", m)
	}
	for _, v := range []struct{ body, want string }{{`{"message":"synthetic"}`, "synthetic"}, {`{"terms":"offer"}`, "offer"}, {`invalid`, ""}} {
		if got := operationWriteKey(operation{Body: v.body}); got != v.want {
			t.Fatalf("write key got %q want %q", got, v.want)
		}
	}
}

// Creates only a randomly named disposable database on an explicitly supplied
// loopback PostgreSQL, never migrates the caller's database or loads .env.
func TestFreshLabMigrationsAndFixtures(t *testing.T) {
	dsn := os.Getenv("CAPACITY_LAB_TEST_DB")
	if dsn == "" {
		t.Skip("requires opt-in disposable loopback PostgreSQL")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		t.Fatal("loopback PostgreSQL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := "capacity_lab_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+identifier); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+identifier); err != nil {
			t.Error(err)
		}
	}()
	u.Path = "/" + name
	db, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(filepath.Join("..", "..")); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	if err = migrations.Run(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = migrations.Run(ctx, db); err != nil {
		t.Fatal("migration replay:", err)
	}
	if err = seed(ctx, db); err != nil {
		t.Fatal("fixtures:", err)
	}
	plan, err := scenarioPlan(os.Getenv("LAB_SCENARIO"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = checkFixtures(ctx, db, plan); err != nil {
		t.Fatal(err)
	}
	// Import optimization must restore both live effects before the API starts.
	probe, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Rollback(ctx)
	_, err = probe.Exec(ctx, `INSERT INTO provider_conversation_messages(conversation_id,sender_type,sender_user_id,body,created_at) SELECT conversation,'buyer',buyer,'Synthetic live-trigger probe',clock_timestamp()+interval '1 minute' FROM capacity_lab_actors ORDER BY slot LIMIT 1`)
	if err != nil {
		t.Fatal(err)
	}
	var liveEffects bool
	err = probe.QueryRow(ctx, `SELECT c.history_revision=$1+1 AND c.last_message_at=(SELECT max(created_at) FROM provider_conversation_messages WHERE conversation_id=c.id) FROM provider_conversations c JOIN capacity_lab_actors a ON a.conversation=c.id ORDER BY a.slot LIMIT 1`, plan.ConversationHistory).Scan(&liveEffects)
	if err != nil || !liveEffects {
		t.Fatalf("live revision/thread timestamp effects restored=%v error=%v", liveEffects, err)
	}
	if err = probe.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// Large fixtures need twenty combined posts and two simultaneous rooms.
	// Use a real tier allowance; never disable the new quota triggers to seed.
	var entitlementsReady bool
	if err = db.QueryRow(ctx, `SELECT count(*)=$2 AND bool_and((provider_current_benefits(provider)->>'post_limit')::int >= $1 AND (provider_current_benefits(provider)->>'town_hall_room_limit')::int >= 2) FROM capacity_lab_actors`, 2*(1+plan.ExtraListings), plan.Pairs).Scan(&entitlementsReady); err != nil || !entitlementsReady {
		t.Fatalf("fixture entitlements ready=%v error=%v", entitlementsReady, err)
	}
	var actors int
	if err = db.QueryRow(ctx, "SELECT count(*) FROM capacity_lab_actors").Scan(&actors); err != nil || actors != plan.Pairs {
		t.Fatalf("actors=%d error=%v", actors, err)
	}
	if os.Getenv("CAPACITY_LAB_TEST_EXPANDED") == "true" {
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if err = prepareExpandedJourneys(ctx, tx); err != nil {
			t.Fatal(err)
		}
		var eligible int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM capacity_lab_actors a JOIN provider_listings l ON l.id=a.listing JOIN provider_public_identity i ON i.provider_id=a.provider
   WHERE l.status='approved' AND i.verified AND i.paid_active AND driver_documents_approved(a.provider) AND l.listing_type='artisan'`).Scan(&eligible); err != nil || eligible != plan.Pairs {
			t.Fatal("expanded transport fixtures ineligible", eligible, err)
		}
		var messageOnly int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM town_hall_requests WHERE room_id IN(SELECT room FROM capacity_lab_actors UNION SELECT hot_room FROM capacity_lab_actors) AND (message_only OR unit<>'job')`).Scan(&messageOnly); err != nil || messageOnly != 0 {
			t.Fatal("bid room fixtures invalid", messageOnly, err)
		}
	}
}

func TestScenarioSelectionRejectsArbitrarySeedInput(t *testing.T) {
	for _, name := range []string{"standard", "large", "busy", "large-busy"} {
		if _, err := scenarioPlan(name); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"huge", "large';DROP TABLE users;--", "1000000"} {
		if _, err := scenarioPlan(name); err == nil {
			t.Fatalf("unbounded seed input accepted: %q", name)
		}
	}
}

func TestDatabaseCeilingRemainsBounded(t *testing.T) {
	for _, value := range []string{"", "1", "2", "3"} {
		if _, err := databaseCPU(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"0", "4", "200", "NaN"} {
		if _, err := databaseCPU(value); err == nil {
			t.Fatalf("unbounded database CPU accepted: %q", value)
		}
	}
}

func TestSessionFixtureBounds(t *testing.T) {
	for _, value := range []string{"", "10", "100", "6000"} {
		if _, err := actorPairCount(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"0", "9", "11", "6001", "1000000", "NaN", "10;DROP TABLE users"} {
		if _, err := actorPairCount(value); err == nil {
			t.Fatalf("unsafe fixture bound accepted: %s", value)
		}
	}
}

func TestBusyRoomOperationsUseAdmittedProviderIdentity(t *testing.T) {
	a := actor{Buyer: uuid.NewString(), ProviderUser: uuid.NewString(), Room: uuid.NewString(), HotRoom: uuid.NewString()}
	want := map[string]bool{"busy_room_history": false, "busy_room_send": false, "town_hall_offer": false}
	for _, op := range operations(a, 0, "synthetic:") {
		if _, ok := want[op.Name]; !ok {
			continue
		}
		want[op.Name] = true
		if !strings.Contains(op.Path, a.HotRoom) {
			t.Errorf("%s did not target shared room", op.Name)
		}
		claims, err := auth.Verify(op.Token, secret)
		if err != nil || claims.UserID != a.ProviderUser {
			t.Errorf("%s did not use admitted provider session", op.Name)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("missing %s", name)
		}
	}
}
func TestLabHTTPClientDoesNotFollowRedirects(t *testing.T) {
	reached := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer source.Close()
	c := client()
	defer c.CloseIdleConnections()
	res, err := c.Get(source.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 302 || reached {
		t.Fatal("lab client followed a redirect outside its target")
	}
}

func TestSeededReadsMustContainBoundedHistory(t *testing.T) {
	op := operation{Name: "support_history", Want: 200}
	if validPayload(op, []byte(`{"messages":[]}`)) || validPayload(op, []byte(`{"error":"failure"}`)) {
		t.Fatal("empty/error response passed as real history")
	}
	if !validPayload(op, []byte(`{"messages":[{"id":"synthetic"}],"has_more":true}`)) {
		t.Fatal("valid bounded history rejected")
	}
	if validPayload(op, []byte(`{"messages":[`+strings.Repeat(`{},`, 40)+`{}]}`)) {
		t.Fatal("unbounded page accepted")
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestPublicBrowsingDoesNotBypassSharedCacheWithEmptyBearer(t *testing.T) {
	c := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if _, exists := r.Header["Authorization"]; exists {
			t.Error("public browse unexpectedly includes authorization")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"products":[{}]}`))}, nil
	})}
	if !request(c, operation{Name: "catalogue", Path: "/api/v1/products", Want: 200}, 0).Valid {
		t.Fatal("public request failed")
	}
}
