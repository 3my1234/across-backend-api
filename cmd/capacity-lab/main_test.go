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
	var actors int
	if err = db.QueryRow(ctx, "SELECT count(*) FROM capacity_lab_actors").Scan(&actors); err != nil || actors != 100 {
		t.Fatalf("actors=%d error=%v", actors, err)
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
