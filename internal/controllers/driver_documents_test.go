package controllers

import (
	"context"
	"github.com/google/uuid"
	"os"
	"testing"
)

func TestDriverDocumentsRequiredAndRevocation(t *testing.T) {
	db := communityTestDB(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`CREATE TABLE provider_verification_documents(provider_id uuid,document_type text,status text)`)
	raw, e := os.ReadFile("../../migrations/096_driver_documents.sql")
	if e != nil {
		t.Fatal(e)
	}
	exec(string(raw))
	owner, p, l := uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO users(id,full_name) VALUES($1,'Driver')`, owner)
	exec(`INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status,is_driver) VALUES($1,$2,'Driver','pending',true)`, p, owner)
	if _, e = db.Exec(ctx, `UPDATE provider_organizations SET verification_status='approved' WHERE id=$1`, p); e == nil {
		t.Fatal("driver approved without evidence")
	}
	exec(`INSERT INTO provider_verification_documents VALUES($1,'drivers_license','approved'),($1,'driver_vehicle','approved'),($1,'driver_plate','pending')`, p)
	if _, e = db.Exec(ctx, `UPDATE provider_organizations SET verification_status='approved' WHERE id=$1`, p); e == nil {
		t.Fatal("driver approved without plate approval")
	}
	exec(`UPDATE provider_verification_documents SET status='approved' WHERE provider_id=$1 AND document_type='driver_plate'`, p)
	exec(`UPDATE provider_organizations SET verification_status='approved' WHERE id=$1`, p)
	exec(`INSERT INTO provider_listings(id,provider_id,title,category,status,media_urls) VALUES($1,$2,'Bolt driver','Driver','approved',ARRAY['https://example.test/car.jpg'])`, l, p)
	exec(`UPDATE provider_verification_documents SET status='rejected' WHERE provider_id=$1 AND document_type='drivers_license'`, p)
	var status string
	if e = db.QueryRow(ctx, `SELECT status FROM provider_listings WHERE id=$1`, l).Scan(&status); e != nil {
		t.Fatal(e)
	}
	if status != "suspended" {
		t.Fatal("revoked driver evidence did not suspend driving listing")
	}
	if _, e = db.Exec(ctx, `UPDATE provider_listings SET status='approved' WHERE id=$1`, l); e == nil {
		t.Fatal("driver listing reapproved with revoked licence")
	}
}
