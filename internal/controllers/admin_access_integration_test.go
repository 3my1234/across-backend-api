package controllers

import (
	"across/backend/internal/config"
	"context"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"os"
	"strings"
	"testing"
)

func TestAdminAccessRolesRevocationAndAudit(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`CREATE TABLE admins(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),email text UNIQUE,full_name text,password_hash text,role text,is_active bool DEFAULT true,created_at timestamptz DEFAULT now(),updated_at timestamptz DEFAULT now());`)
	raw, e := os.ReadFile("../../migrations/106_admin_role_workspaces.sql")
	if e != nil {
		t.Fatal(e)
	}
	exec(string(raw))
	owner, peer := uuid.NewString(), uuid.NewString()
	exec("INSERT INTO admins(id,email,role,is_active)VALUES($1,'owner@fixture.test','super_admin',true),($2,'peer@fixture.test','super_admin',true)", owner, peer)
	a := NewAdminController(db, config.Config{})
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("admin_id", c.Get("Actor")); return c.Next() })
	app.Post("/admins", a.CreateAdmin)
	app.Post("/login", a.Login)
	app.Patch("/admins/:admin_id", a.UpdateAdminAccess)
	app.Delete("/admins/:admin_id", a.DeleteAdmin)
	app.Patch("/admins/:admin_id/password", a.ResetAdminPassword)
	app.Get("/audit", a.AdminAccessEvents)
	ids := []string{}
	for _, role := range []string{"support_admin", "moderation_admin", "finance_admin", "ads_admin", "catalog_admin"} {
		out := communityCall(t, app, "POST", "/admins", owner, fmt.Sprintf(`{"email":"%s@fixture.test","full_name":"Staff","password":"fixture-password-not-real","role":"%s"}`, role, role), 201)
		ids = append(ids, out["id"].(string))
		signedIn := communityCall(t, app, "POST", "/login", "", fmt.Sprintf(`{"email":"%s@fixture.test","password":"fixture-password-not-real"}`, role), 200)
		if signedIn["role"] != role || signedIn["access_token"] == "" {
			t.Fatal("staff login lost assigned role", signedIn["role"])
		}
		var hash, got string
		db.QueryRow(ctx, "SELECT password_hash,role FROM admins WHERE id=$1::uuid", out["id"]).Scan(&hash, &got)
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte("fixture-password-not-real")) != nil || got != role {
			t.Fatal("password/role not persisted")
		}
	}
	communityCall(t, app, "POST", "/admins", owner, `{"email":"wrong@fixture.test","full_name":"Wrong","password":"fixture-password","role":"everything"}`, 400)
	communityCall(t, app, "POST", "/admins", ids[0], `{"email":"attack@fixture.test","full_name":"Wrong","password":"fixture-password","role":"super_admin"}`, 403)
	communityCall(t, app, "PATCH", "/admins/"+ids[0], owner, `{"role":"moderation_admin"}`, 200)
	communityCall(t, app, "PATCH", "/admins/"+ids[0], owner, `{"is_active":false}`, 200)
	communityCall(t, app, "PATCH", "/admins/"+ids[0], owner, `{"is_active":true}`, 200)
	communityCall(t, app, "PATCH", "/admins/"+owner, owner, `{"is_active":false}`, 409)
	communityCall(t, app, "PATCH", "/admins/"+owner, owner, `{"role":"support_admin"}`, 409)
	communityCall(t, app, "PATCH", "/admins/"+ids[0]+"/password", owner, `{"password":"replacement-fixture-pass"}`, 204)
	communityCall(t, app, "DELETE", "/admins/"+ids[1], owner, "", 200)
	var active bool
	db.QueryRow(ctx, "SELECT is_active FROM admins WHERE id=$1::uuid", ids[1]).Scan(&active)
	if active {
		t.Fatal("deactivation did not revoke access")
	}
	communityCall(t, app, "PATCH", "/admins/"+peer, owner, `{"role":"support_admin"}`, 200)
	communityCall(t, app, "PATCH", "/admins/"+owner, peer, `{"is_active":false}`, 403)
	report := communityCall(t, app, "GET", "/audit?limit=100", owner, "", 200)
	encoded := fmt.Sprint(report)
	if strings.Contains(encoded, "fixture-password") || strings.Contains(encoded, "password_hash") || strings.Contains(encoded, "replacement-fixture") {
		t.Fatal("secret leaked into audit")
	}
	if len(report["items"].([]any)) != 11 {
		t.Fatal("expected atomic audit for each access change", report)
	}
	var supers int
	db.QueryRow(ctx, "SELECT count(*) FROM admins WHERE role='super_admin' AND is_active").Scan(&supers)
	if supers != 1 {
		t.Fatal("owner access not preserved")
	}
}
