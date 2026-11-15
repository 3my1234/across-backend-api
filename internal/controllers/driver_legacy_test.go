package controllers

import (
	"across/backend/internal/config"
	"across/backend/internal/storage"
	"context"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"os"
	"testing"
	"time"
)

func TestDriverLegacyEvidenceEligibilityAndNearby(t *testing.T) {
	db := driverSafetyDB(t)
	safetyExec(t, db, `ALTER TABLE provider_listings ADD COLUMN moderated_at timestamptz,ADD COLUMN description text DEFAULT '',ADD COLUMN price numeric,ADD COLUMN currency_code text DEFAULT 'NGN',ADD COLUMN pricing_unit text DEFAULT 'ride';CREATE TABLE admins(id uuid PRIMARY KEY,role text,is_active bool);CREATE TABLE schema_migrations(name text PRIMARY KEY,applied_at timestamptz);INSERT INTO schema_migrations VALUES('115_driver_safety_identity.sql',now());`)
	p, owner, listing, admin, plan, sub := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	safetyExec(t, db, `INSERT INTO admins VALUES($1,'super_admin',true);`, admin)
	s3 := storage.NewS3(config.Config{AWSRegion: "us-east-1", S3BucketName: "synthetic", AWSAccessKeyID: "synthetic", AWSSecretAccessKey: "synthetic"})
	seed := func() (string, string, string) {
		q, u, l := uuid.NewString(), uuid.NewString(), uuid.NewString()
		safetyExec(t, db, `INSERT INTO users(id,full_name)VALUES($1,'Synthetic driver')`, u)
		safetyExec(t, db, `INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status,is_driver)VALUES($1,$2,'Transport','pending',true)`, q, u)
		for _, kind := range []string{"drivers_license", "driver_vehicle", "driver_plate"} {
			safetyExec(t, db, `INSERT INTO provider_verification_documents(provider_id,document_type,document_url,status)VALUES($1,$2,$3,'approved')`, q, kind, s3.ObjectURL("private/providers/"+q+"/verification/evidence/"+kind+".jpg"))
		}
		safetyExec(t, db, `UPDATE provider_organizations SET verification_status='approved' WHERE id=$1`, q)
		safetyExec(t, db, `INSERT INTO provider_listings(id,provider_id,title,category,status,moderated_at)VALUES($1,$2,'Driver','Driver','approved',now()-interval '2 hours')`, l, q)
		return q, u, l
	}
	p, owner, listing = seed()
	newProvider, _, _ := seed()
	safetyExec(t, db, `INSERT INTO provider_members VALUES($1,$2,'owner',true)`, p, owner)
	safetyExec(t, db, `INSERT INTO provider_subscription_plans VALUES($1,'provider-gold',20)`, plan)
	safetyExec(t, db, `INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,starts_at,current_period_end)VALUES($1,$2,$3,'active',now()-interval '1 day',now()+interval '1 month')`, sub, p, plan)
	safetyExec(t, db, `INSERT INTO provider_subscription_payments VALUES($1,500,'NGN')`, sub)
	safetyMigration(t, db)
	checklistMigration(t, db)
	raw, err := os.ReadFile("../../migrations/118_legacy_driver_screening.sql")
	if err != nil {
		t.Fatal(err)
	}
	safetyExec(t, db, string(raw))
	var n int
	db.QueryRow(context.Background(), `SELECT count(*) FROM provider_driver_legacy_screening`).Scan(&n)
	if n != 0 || safetyApproved(t, db, p) {
		t.Fatal("migration automatically enrolled an old account")
	}
	enroll := `INSERT INTO provider_driver_legacy_screening(provider_id,source_listing_id,source_approved_at,rollout_at,backup_taken_at,backup_sha256,evidence_reference,enrolled_by) SELECT $1,$2,now()-interval '2 hours',applied_at,now()-interval '1 hour',repeat('a',64),'synthetic reviewed before-rollout backup',$3 FROM schema_migrations WHERE name='115_driver_safety_identity.sql'`
	safetyExec(t, db, enroll, p, listing, admin)
	if _, err := db.Exec(context.Background(), enroll, newProvider, listing, admin); err == nil {
		t.Fatal("another provider reused reviewed listing evidence")
	}
	var status string
	db.QueryRow(context.Background(), `SELECT status FROM provider_listings WHERE id=$1`, listing).Scan(&status)
	if status != "suspended" || !safetyApproved(t, db, p) || safetyApproved(t, db, newProvider) {
		t.Fatal("enrollment restored listing or weakened new applicant requirements", status)
	}
	safetyExec(t, db, `UPDATE provider_listings SET status='approved' WHERE id=$1`, listing)
	market := &ProviderMarketplaceController{db: db, s3: s3}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("Actor")); return c.Next() })
	app.Put("/availability", market.DriverAvailability)
	app.Get("/nearby", market.NearbyDrivers)
	app.Get("/safety/:listing_id", market.DriverSafetyProfile)
	payload := func() string {
		return fmt.Sprintf(`{"listing_id":%q,"latitude":9.08,"longitude":7.45,"accuracy_m":20,"captured_at":%q}`, listing, time.Now().Format(time.RFC3339Nano))
	}
	nearby := func(want int) {
		t.Helper()
		b := communityCall(t, app, "GET", "/nearby?latitude=9.081&longitude=7.451", "customer", "", 200)
		if len(b["items"].([]any)) != want {
			t.Fatal("nearby eligibility incorrect", b)
		}
	}
	communityCall(t, app, "PUT", "/availability", owner, payload(), 200)
	nearby(1)
	profile := communityCall(t, app, "GET", "/safety/"+listing, "customer", "", 200)
	if profile["legacy_screening"] != true || profile["face_photo_url"] != "" || profile["screening_notice"] == nil {
		t.Fatal("legacy missing face falsely presented as reviewed", profile)
	}
	// Even an optional legacy face must never expose an unapproved replacement.
	safetyExec(t, db, `INSERT INTO provider_verification_documents(provider_id,document_type,document_url,status,created_at)VALUES($1,'driver_face',$2,'approved',now()-interval '1 day'),($1,'driver_face',$3,'rejected',now())`, p, s3.ObjectURL("private/providers/"+p+"/verification/evidence/old-face.jpg"), s3.ObjectURL("private/providers/"+p+"/verification/evidence/rejected-face.jpg"))
	profile = communityCall(t, app, "GET", "/safety/"+listing, "customer", "", 200)
	if profile["face_photo_url"] != "" {
		t.Fatal("unapproved or superseded optional face exposed")
	}
	safetyExec(t, db, `UPDATE provider_subscriptions SET current_period_end=now()-interval '1 second' WHERE id=$1`, sub)
	nearby(0)
	safetyExec(t, db, `UPDATE provider_subscriptions SET current_period_end=now()+interval '1 month' WHERE id=$1`, sub)
	safetyExec(t, db, `UPDATE provider_listings SET status='suspended' WHERE id=$1`, listing)
	nearby(0)
	// A separate safety suspension is never undone by legacy enrollment.
	if !safetyApproved(t, db, p) {
		t.Fatal("unexpected document mutation")
	}
	safetyExec(t, db, `UPDATE provider_listings SET status='approved' WHERE id=$1`, listing)
	safetyExec(t, db, `UPDATE provider_verification_documents SET expires_on=CURRENT_DATE-1 WHERE provider_id=$1 AND document_type='drivers_license'`, p)
	if safetyApproved(t, db, p) {
		t.Fatal("legacy exception accepted explicitly expired licence")
	}
	nearby(0)
	safetyExec(t, db, `UPDATE provider_verification_documents SET expires_on=CURRENT_DATE+1,status='rejected' WHERE provider_id=$1 AND document_type='drivers_license'`, p)
	if safetyApproved(t, db, p) {
		t.Fatal("legacy exception accepted rejected licence")
	}
	safetyExec(t, db, `UPDATE provider_verification_documents SET status='approved' WHERE provider_id=$1 AND document_type='drivers_license'`, p)
	safetyExec(t, db, `UPDATE provider_listings SET status='approved' WHERE id=$1`, listing)
	safetyExec(t, db, `UPDATE provider_driver_legacy_screening SET revoked_at=now() WHERE provider_id=$1`, p)
	if safetyApproved(t, db, p) {
		t.Fatal("revoked exception retained original checklist")
	}
	nearby(0)
	if _, err := db.Exec(context.Background(), `UPDATE provider_driver_legacy_screening SET revoked_at=NULL WHERE provider_id=$1`, p); err == nil {
		t.Fatal("revoked evidence reinstated")
	}
	if _, err := db.Exec(context.Background(), `UPDATE provider_driver_legacy_screening SET evidence_reference='changed evidence' WHERE provider_id=$1`, p); err == nil {
		t.Fatal("evidence silently replaced")
	}
}
