package controllers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func checklistMigration(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	raw, err := os.ReadFile("../../migrations/116_provider_verification_checklist.sql")
	if err != nil {
		t.Fatal(err)
	}
	safetyExec(t, db, string(raw))
}

func TestDriverSafetyChecklistMigrationPreservesHistory(t *testing.T) {
	db := driverSafetyDB(t)
	safetyMigration(t, db)
	ctx := context.Background()
	p, owner, listing := uuid.NewString(), uuid.NewString(), uuid.NewString()
	safetyExec(t, db, `INSERT INTO users(id,full_name)VALUES($1,'Driver')`, owner)
	safetyExec(t, db, `INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status,is_driver,state)VALUES($1,$2,'Driver','pending',true,'Lagos')`, p, owner)
	safetyExec(t, db, `INSERT INTO provider_verification_documents(provider_id,document_type,status,expires_on) SELECT $1,kind,'approved',CURRENT_DATE+30 FROM unnest(ARRAY['drivers_license','driver_face','driver_vehicle','driver_plate','vehicle_registration','vehicle_insurance','roadworthiness','driver_lasrra','driver_lasdri'])kind`, p)
	safetyExec(t, db, `UPDATE provider_organizations SET verification_status='approved' WHERE id=$1`, p)
	safetyExec(t, db, `INSERT INTO provider_listings(id,provider_id,title,category,status)VALUES($1,$2,'Driver','Driver','approved')`, listing, p)
	checklistMigration(t, db)
	var count int
	var status string
	if err := db.QueryRow(ctx, `SELECT count(*) FROM driver_required_documents($1)`, p).Scan(&count); err != nil || count != 5 {
		t.Fatalf("required count %d: %v", count, err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM provider_verification_documents WHERE provider_id=$1`, p).Scan(&count); err != nil || count != 9 {
		t.Fatal("history altered", count, err)
	}
	if err := db.QueryRow(ctx, `SELECT status FROM provider_listings WHERE id=$1`, listing).Scan(&status); err != nil || status != "approved" {
		t.Fatal("listing approval changed", status, err)
	}
	// Removed documents can expire, be rejected or deleted without blocking the
	// five required records. Migration does not require duplicate government ID.
	safetyExec(t, db, `UPDATE provider_verification_documents SET status='rejected',expires_on=CURRENT_DATE-1 WHERE provider_id=$1 AND document_type IN('vehicle_insurance','roadworthiness','driver_lasrra','driver_lasdri')`, p)
	if !safetyApproved(t, db, p) {
		t.Fatal("retired requirements still block driver")
	}
	safetyExec(t, db, `DELETE FROM provider_verification_documents WHERE provider_id=$1 AND document_type IN('vehicle_insurance','roadworthiness','driver_lasrra','driver_lasdri')`, p)
	if !safetyApproved(t, db, p) {
		t.Fatal("five documents not sufficient")
	}
	safetyExec(t, db, `INSERT INTO provider_verification_documents(provider_id,document_type,status)VALUES($1,'driver_face','pending')`, p)
	if safetyApproved(t, db, p) {
		t.Fatal("pending replacement bypassed review")
	}
	db.QueryRow(ctx, `SELECT status FROM provider_listings WHERE id=$1`, listing).Scan(&status)
	if status != "suspended" {
		t.Fatal("invalid evidence did not suspend driver listing")
	}
	safetyExec(t, db, `UPDATE provider_verification_documents SET status='approved' WHERE provider_id=$1 AND document_type='driver_face'`, p)
	safetyExec(t, db, `UPDATE provider_verification_documents SET expires_on=CURRENT_DATE-1 WHERE provider_id=$1 AND document_type='drivers_license'`, p)
	if safetyApproved(t, db, p) {
		t.Fatal("expired licence accepted as driver ID")
	}
	safetyExec(t, db, `UPDATE provider_verification_documents SET expires_on=CURRENT_DATE+30 WHERE provider_id=$1 AND document_type='drivers_license'`, p)
	if !safetyApproved(t, db, p) {
		t.Fatal("current licence not accepted")
	}
	db.QueryRow(ctx, `SELECT status FROM provider_listings WHERE id=$1`, listing).Scan(&status)
	if status != "suspended" {
		t.Fatal("migration or document approval silently reapproved a listing")
	}
}

func TestDriverSafetyChecklistProviderIDAlternatives(t *testing.T) {
	db := driverSafetyDB(t)
	safetyMigration(t, db)
	checklistMigration(t, db)
	safetyExec(t, db, `ALTER TABLE provider_organizations ADD COLUMN verification_notes text DEFAULT '',ADD COLUMN verified_at timestamptz,ADD COLUMN verified_by uuid,ADD COLUMN updated_at timestamptz DEFAULT now()`)
	p, owner := uuid.NewString(), uuid.NewString()
	safetyExec(t, db, `INSERT INTO users(id,full_name)VALUES($1,'Provider')`, owner)
	safetyExec(t, db, `INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status,is_driver)VALUES($1,$2,'Ordinary provider','pending',false)`, p, owner)
	market := &ProviderMarketplaceController{db: db}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("admin_id", owner); return c.Next() })
	app.Patch("/:provider_id", market.AdminVerifyProvider)
	approve := func(want int) {
		t.Helper()
		req := httptest.NewRequest("PATCH", "/"+p, strings.NewReader(`{"status":"approved"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("approval got %d want %d", resp.StatusCode, want)
		}
	}
	approve(409)
	safetyExec(t, db, `INSERT INTO provider_verification_documents(provider_id,document_type,status)VALUES($1,'business_registration','approved')`, p)
	approve(409)
	safetyExec(t, db, `INSERT INTO provider_verification_documents(provider_id,document_type,status)VALUES($1,'government_id','approved')`, p)
	approve(204)
	safetyExec(t, db, `INSERT INTO provider_verification_documents(provider_id,document_type,status)VALUES($1,'government_id','pending')`, p)
	approve(409)
	safetyExec(t, db, `INSERT INTO provider_verification_documents(provider_id,document_type,status,expires_on)VALUES($1,'drivers_license','approved',CURRENT_DATE+30)`, p)
	approve(204)
	safetyExec(t, db, `UPDATE provider_verification_documents SET expires_on=CURRENT_DATE-1 WHERE provider_id=$1 AND document_type='drivers_license'`, p)
	approve(409)
	app.Get("/:provider_id/documents", market.AdminListVerificationDocuments)
	resp, err := app.Test(httptest.NewRequest("GET", "/"+p+"/documents", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Required []string `json:"required_documents"`
		Driver   bool     `json:"is_driver"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || len(body.Required) != 1 || body.Required[0] != "government_id" || body.Driver {
		t.Fatal("ordinary provider checklist", body, err)
	}
}
