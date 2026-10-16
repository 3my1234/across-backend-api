package main

import (
	"across/backend/internal/migrations"
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	var actors int
	if err = db.QueryRow(ctx, "SELECT count(*) FROM capacity_lab_actors").Scan(&actors); err != nil || actors != 100 {
		t.Fatalf("actors=%d error=%v", actors, err)
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
