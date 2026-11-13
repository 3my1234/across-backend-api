package controllers

import (
	"across/backend/internal/config"
	"across/backend/internal/storage"
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func driverSafetyDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	db := communityTestDB(t)
	_, err := db.Exec(context.Background(), `ALTER TABLE provider_organizations ADD COLUMN country_code text DEFAULT 'NG',ADD COLUMN state text DEFAULT 'Abuja';
 CREATE TABLE provider_verification_documents(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),provider_id uuid,document_type text,document_url text DEFAULT '',status text,review_notes text DEFAULT '',reviewed_by uuid,reviewed_at timestamptz,created_at timestamptz DEFAULT clock_timestamp());`)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../migrations/096_driver_documents.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(context.Background(), string(raw)); err != nil {
		t.Fatal(err)
	}
	return db
}
func safetyExec(t *testing.T, db *pgxpool.Pool, q string, args ...any) {
	t.Helper()
	if _, e := db.Exec(context.Background(), q, args...); e != nil {
		t.Fatal(e)
	}
}
func safetyMigration(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	raw, e := os.ReadFile("../../migrations/115_driver_safety_identity.sql")
	if e != nil {
		t.Fatal(e)
	}
	safetyExec(t, db, string(raw))
}
func safetyApproved(t *testing.T, db *pgxpool.Pool, p string) bool {
	t.Helper()
	var ok bool
	if e := db.QueryRow(context.Background(), `SELECT driver_documents_approved($1::uuid)`, p).Scan(&ok); e != nil {
		t.Fatal(e)
	}
	return ok
}
func safetySeed(t *testing.T, db *pgxpool.Pool, p string, s3 *storage.S3) {
	t.Helper()
	for _, kind := range []string{"driver_face", "driver_vehicle", "driver_plate", "drivers_license", "vehicle_registration", "vehicle_insurance", "roadworthiness"} {
		safetyExec(t, db, `INSERT INTO provider_verification_documents(provider_id,document_type,document_url,status,expires_on)VALUES($1,$2,$3,'approved',CURRENT_DATE+30)`, p, kind, s3.ObjectURL("private/providers/"+p+"/verification/evidence/"+kind+".jpg"))
	}
}
func TestDriverSafetyMigrationExpiryLatestAndLocalPermits(t *testing.T) {
	db := driverSafetyDB(t)
	p, owner, listing := uuid.NewString(), uuid.NewString(), uuid.NewString()
	safetyExec(t, db, `INSERT INTO users(id,full_name)VALUES($1,'Actual driver')`, owner)
	safetyExec(t, db, `INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status,is_driver)VALUES($1,$2,'Transport','pending',true)`, p, owner)
	safetyExec(t, db, `INSERT INTO provider_verification_documents(provider_id,document_type,status,created_at)SELECT $1,kind,'approved',now()-interval '1 day' FROM unnest(ARRAY['drivers_license','driver_vehicle','driver_plate'])kind`, p)
	safetyExec(t, db, `UPDATE provider_organizations SET verification_status='approved' WHERE id=$1`, p)
	safetyExec(t, db, `INSERT INTO provider_listings(id,provider_id,title,category,status)VALUES($1,$2,'Motorcycle courier','Courier','approved')`, listing, p)
	foodOwner, foodProvider, foodListing := uuid.NewString(), uuid.NewString(), uuid.NewString()
	safetyExec(t, db, `INSERT INTO users(id,full_name)VALUES($1,'Restaurant owner')`, foodOwner)
	safetyExec(t, db, `INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status,is_driver)VALUES($1,$2,'Food','approved',false)`, foodProvider, foodOwner)
	safetyExec(t, db, `INSERT INTO provider_listings(id,provider_id,title,category,status)VALUES($1,$2,'Food delivery','Catering','approved')`, foodListing, foodProvider)
	safetyMigration(t, db)
	var status string
	db.QueryRow(context.Background(), `SELECT status FROM provider_listings WHERE id=$1`, listing).Scan(&status)
	if status != "suspended" || safetyApproved(t, db, p) {
		t.Fatal("legacy transport evidence bypassed new requirements")
	}
	if err := db.QueryRow(context.Background(), `SELECT status FROM provider_listings WHERE id=$1`, foodListing).Scan(&status); err != nil || status != "approved" {
		t.Fatal("ordinary non-driver delivery business was incorrectly suspended")
	}
	s3 := storage.NewS3(config.Config{AWSRegion: "us-east-1", S3BucketName: "synthetic", AWSAccessKeyID: "synthetic", AWSSecretAccessKey: "synthetic"})
	safetySeed(t, db, p, s3)
	if !safetyApproved(t, db, p) {
		t.Fatal("complete current evidence not accepted")
	}
	safetyExec(t, db, `UPDATE provider_listings SET status='approved' WHERE id=$1`, listing)
	safetyExec(t, db, `INSERT INTO provider_verification_documents(provider_id,document_type,status,created_at)VALUES($1,'driver_face','pending',now()+interval '1 second')`, p)
	if safetyApproved(t, db, p) {
		t.Fatal("old approved face bypassed latest pending replacement")
	}
	safetyExec(t, db, `UPDATE provider_verification_documents SET status='approved' WHERE provider_id=$1 AND document_type='driver_face'`, p)
	safetyExec(t, db, `UPDATE provider_verification_documents SET expires_on=CURRENT_DATE-1 WHERE provider_id=$1 AND document_type='vehicle_insurance'`, p)
	if safetyApproved(t, db, p) {
		t.Fatal("expired insurance accepted")
	}
	safetyExec(t, db, `UPDATE provider_verification_documents SET expires_on=CURRENT_DATE WHERE provider_id=$1 AND document_type='vehicle_insurance'`, p)
	if !safetyApproved(t, db, p) {
		t.Fatal("document valid through today was rejected")
	}
	safetyExec(t, db, `UPDATE provider_organizations SET state='Lagos' WHERE id=$1`, p)
	if safetyApproved(t, db, p) {
		t.Fatal("Lagos permits missing")
	}
	safetyExec(t, db, `INSERT INTO provider_verification_documents(provider_id,document_type,status)SELECT $1,kind,'approved' FROM unnest(ARRAY['driver_lasrra','driver_lasdri'])kind`, p)
	if !safetyApproved(t, db, p) {
		t.Fatal("complete Lagos evidence rejected")
	}
	safetyExec(t, db, `DELETE FROM provider_verification_documents WHERE provider_id=$1 AND document_type='driver_vehicle'`, p)
	if safetyApproved(t, db, p) {
		t.Fatal("deleted vehicle evidence accepted")
	}
}

func TestDriverSafetyProfileOnlyApprovedPhotoProjection(t *testing.T) {
	db := driverSafetyDB(t)
	safetyMigration(t, db)
	p, owner, listing, plan, sub := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	s3 := storage.NewS3(config.Config{AWSRegion: "us-east-1", S3BucketName: "synthetic", AWSAccessKeyID: "synthetic", AWSSecretAccessKey: "synthetic"})
	safetyExec(t, db, `INSERT INTO users(id,full_name)VALUES($1,'Actual driver')`, owner)
	safetyExec(t, db, `INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status,is_driver)VALUES($1,$2,'Transport','pending',true)`, p, owner)
	safetySeed(t, db, p, s3)
	safetyExec(t, db, `UPDATE provider_organizations SET verification_status='approved' WHERE id=$1`, p)
	safetyExec(t, db, `INSERT INTO provider_subscription_plans(id,code,listing_limit)VALUES($1,'provider-gold',20)`, plan)
	safetyExec(t, db, `INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,starts_at,current_period_end)VALUES($1,$2,$3,'active',now()-interval '1 day',now()+interval '1 month')`, sub, p, plan)
	safetyExec(t, db, `INSERT INTO provider_subscription_payments VALUES($1,500,'NGN')`, sub)
	safetyExec(t, db, `INSERT INTO provider_listings(id,provider_id,title,category,status)VALUES($1,$2,'Driver','Driver','approved')`, listing, p)
	market := &ProviderMarketplaceController{db: db, s3: s3}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		if c.Get("Actor") != "" {
			c.Locals("user_id", c.Get("Actor"))
		}
		return c.Next()
	})
	app.Get("/:listing_id", market.DriverSafetyProfile)
	read := func(actor string, want int) map[string]any {
		t.Helper()
		req := httptest.NewRequest("GET", "/"+listing, nil)
		req.Header.Set("Actor", actor)
		r, e := app.Test(req)
		if e != nil {
			t.Fatal(e)
		}
		defer r.Body.Close()
		if r.StatusCode != want {
			t.Fatalf("got %d want %d", r.StatusCode, want)
		}
		if r.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatal("private response became cacheable")
		}
		body := map[string]any{}
		json.NewDecoder(r.Body).Decode(&body)
		return body
	}
	read("", 401)
	body := read("customer", 200)
	encoded, _ := json.Marshal(body)
	if body["driver_name"] != "Actual driver" || len(body) != 5 || strings.Contains(string(encoded), "drivers_license") || strings.Contains(string(encoded), "vehicle_insurance") {
		t.Fatal("identity/document projection leaked", string(encoded))
	}
	for _, field := range []string{"face_photo_url", "vehicle_photo_url", "plate_photo_url"} {
		if !strings.Contains(body[field].(string), "X-Amz-Expires=300") {
			t.Fatal("photo link is not short-lived")
		}
	}
	safetyExec(t, db, `UPDATE provider_verification_documents SET status='rejected' WHERE provider_id=$1 AND document_type='driver_face'`, p)
	read("customer", 404)
}

func TestDriverSafetyExpiryKinds(t *testing.T) {
	for _, kind := range []string{"drivers_license", "vehicle_insurance", "roadworthiness"} {
		if !driverDocument(kind) || !driverDocumentNeedsExpiry(kind) {
			t.Fatal(kind)
		}
	}
	for _, kind := range []string{"driver_face", "driver_vehicle", "driver_plate", "vehicle_registration", "driver_lasrra", "driver_lasdri"} {
		if !driverDocument(kind) || driverDocumentNeedsExpiry(kind) {
			t.Fatal(kind)
		}
	}
	if driverDocument("unknown") {
		t.Fatal("unknown driver evidence type")
	}
}

func TestDriverSafetySubmissionExpiryBeforeStorage(t *testing.T) {
	db := driverSafetyDB(t)
	safetyMigration(t, db)
	p, owner := uuid.NewString(), uuid.NewString()
	safetyExec(t, db, `INSERT INTO users(id,full_name)VALUES($1,'Driver')`, owner)
	safetyExec(t, db, `INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status,is_driver)VALUES($1,$2,'Transport','pending',true)`, p, owner)
	safetyExec(t, db, `INSERT INTO provider_members VALUES($1,$2,'owner',true)`, p, owner)
	market := &ProviderMarketplaceController{db: db}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", owner); return c.Next() })
	app.Post("/documents", market.AddVerificationDocument)
	for _, expiry := range []string{"", "2020-01-01", "2026-02-30", "not-a-date"} {
		body, _ := json.Marshal(map[string]string{"document_type": "drivers_license", "document_url": "https://synthetic.test/owned.jpg", "expires_on": expiry})
		req := httptest.NewRequest("POST", "/documents", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 422 {
			t.Fatal("invalid/missing expiry reached document storage")
		}
	}
}

func TestDriverSafetyAdminConfirmationAndExpiry(t *testing.T) {
	db := driverSafetyDB(t)
	safetyMigration(t, db)
	p, owner, doc, admin := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	safetyExec(t, db, `INSERT INTO users(id,full_name)VALUES($1,'Driver')`, owner)
	safetyExec(t, db, `INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status)VALUES($1,$2,'Transport','pending')`, p, owner)
	safetyExec(t, db, `INSERT INTO provider_verification_documents(id,provider_id,document_type,status,expires_on)VALUES($1,$2,'drivers_license','pending',CURRENT_DATE+30)`, doc, p)
	market := &ProviderMarketplaceController{db: db}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("admin_id", admin); return c.Next() })
	app.Patch("/:provider_id/:document_id", market.AdminReviewVerificationDocument)
	review := func(confirmed bool, want int) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"status": "approved", "evidence_confirmed": confirmed})
		req := httptest.NewRequest("PATCH", "/"+p+"/"+doc, strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		r, e := app.Test(req)
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode != want {
			t.Fatalf("admin confirmation: got %d want %d", r.StatusCode, want)
		}
	}
	review(false, 422)
	review(true, 204)
	safetyExec(t, db, `UPDATE provider_verification_documents SET expires_on=CURRENT_DATE-1 WHERE id=$1`, doc)
	review(true, 422)
	safetyExec(t, db, `UPDATE provider_verification_documents SET document_type='government_id',expires_on=NULL WHERE id=$1`, doc)
	review(false, 204)
}
