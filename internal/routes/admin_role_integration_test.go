package routes

import (
	"across/backend/internal/auth"
	"across/backend/internal/config"
	"context"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Retain the real registered authentication/authorization chain, replacing only
// final business handlers. Exercise live database role changes with the same JWT.
func TestAdminRoleRoutesAndImmediateRevocation(t *testing.T) {
	if os.Getenv("SETTLEMENT_TEST_LOCAL") != "true" {
		t.Skip("requires isolated loopback PostgreSQL")
	}
	target := os.Getenv("DATABASE_URL")
	parsed, err := url.Parse(target)
	if err != nil || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost") {
		t.Fatal("loopback database required")
	}
	ctx := context.Background()
	owner, err := pgxpool.New(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	schema := "admin_routes_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = owner.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer owner.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	pc, err := pgxpool.ParseConfig(target)
	if err != nil {
		t.Fatal(err)
	}
	pc.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(ctx, "CREATE TABLE admins(id uuid PRIMARY KEY,role text,is_active bool)"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: "isolated-route-test-secret"}
	app := fiber.New()
	Register(app, db, db, nil, cfg)
	for _, stack := range app.Stack() {
		for _, route := range stack {
			if strings.HasPrefix(route.Path, "/api/v1/admin/") {
				route.Handlers[len(route.Handlers)-1] = func(c *fiber.Ctx) error { return c.SendStatus(204) }
			}
		}
	}
	roles := []string{"super_admin", "catalog_admin", "support_admin", "moderation_admin", "finance_admin", "ads_admin"}
	cases := []struct {
		method, path string
		allowed      string
	}{
		{"GET", "/session", "super_admin catalog_admin support_admin moderation_admin finance_admin ads_admin"},
		{"GET", "/admins", "super_admin"}, {"POST", "/admins", "super_admin"}, {"PATCH", "/admins/11111111-1111-4111-8111-111111111111", "super_admin"},
		{"GET", "/admin-access-events", "super_admin"}, {"GET", "/support/tickets", "super_admin catalog_admin support_admin"},
		{"POST", "/support/tickets/11111111-1111-4111-8111-111111111111/reply", "super_admin catalog_admin support_admin"},
		{"GET", "/providers", "super_admin catalog_admin moderation_admin"},
		{"PATCH", "/provider-listings/11111111-1111-4111-8111-111111111111/moderation", "super_admin catalog_admin moderation_admin"},
		{"GET", "/xp/withdrawals", "super_admin finance_admin"}, {"GET", "/transactions", "super_admin catalog_admin finance_admin"},
		{"GET", "/discovery/summary", "super_admin catalog_admin ads_admin"}, {"GET", "/overview", "super_admin catalog_admin"},
		{"GET", "/provider-subscription-plans", "super_admin"}, {"GET", "/ops/runtime", "super_admin"},
	}
	id := uuid.NewString()
	if _, err = db.Exec(ctx, "INSERT INTO admins VALUES($1,'super_admin',true)", id); err != nil {
		t.Fatal(err)
	}
	token, _, err := auth.Sign(id, cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	check := func(method, path string, want int) {
		t.Helper()
		req := httptest.NewRequest(method, "/api/v1/admin"+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, e := app.Test(req)
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("%s %s: got %d want %d", method, path, resp.StatusCode, want)
		}
	}
	for _, role := range roles {
		if _, err = db.Exec(ctx, "UPDATE admins SET role=$1 WHERE id=$2", role, id); err != nil {
			t.Fatal(err)
		}
		for _, test := range cases {
			want := 403
			for _, allowed := range strings.Fields(test.allowed) {
				if allowed == role {
					want = 204
				}
			}
			t.Run(role+test.method+test.path, func(t *testing.T) { check(test.method, test.path, want) })
		}
	}
	if _, err = db.Exec(ctx, "UPDATE admins SET is_active=false WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	check("GET", "/session", 401)
	if _, err = db.Exec(ctx, "UPDATE admins SET is_active=true,role='unknown_role' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	check("GET", "/session", 403)
}
