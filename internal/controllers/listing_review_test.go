package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListingApprovedBaselineAndStaleModeration(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE provider_listings(id uuid PRIMARY KEY,provider_id uuid,title text,description text,price numeric,attributes jsonb,status text,moderation_notes text,moderated_by uuid,moderated_at timestamptz,published_at timestamptz,updated_at timestamptz);
 CREATE TABLE provider_marketplace_events(provider_id uuid,listing_id uuid,event_type text,metadata jsonb);`)
	if err != nil {
		t.Fatal(err)
	}
	id, provider, admin, legacy := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO provider_listings(id,provider_id,title,description,price,attributes,status,published_at) VALUES($1,$2,'Old title','Old description',100,'{"price_mode":"fixed"}','approved',now()),($3,$2,'Already edited','Legacy content',100,'{}','pending',now())`, id, provider, legacy); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "075_listing_review_baseline.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	var snapshot []byte
	var revision int64
	var status string
	check := func(wantTitle string, wantRevision int64, wantStatus string) {
		t.Helper()
		var baseline map[string]any
		if err = db.QueryRow(ctx, `SELECT approved_snapshot,content_revision,status FROM provider_listings WHERE id=$1`, id).Scan(&snapshot, &revision, &status); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(snapshot, &baseline); err != nil {
			t.Fatal(err)
		}
		if baseline["title"] != wantTitle || revision != wantRevision || status != wantStatus {
			t.Fatal(baseline, revision, status)
		}
	}
	check("Old title", 0, "approved")
	emptyID := uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO provider_listings(id,provider_id,title,attributes,status) VALUES($1,$2,'No extra details','{}','approved')`, emptyID, provider); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE provider_listings SET attributes='null',status='pending' WHERE id=$1`, emptyID); err != nil {
		t.Fatal(err)
	}
	var emptyStatus string
	var emptyRevision int64
	if err = db.QueryRow(ctx, `SELECT status,content_revision FROM provider_listings WHERE id=$1`, emptyID).Scan(&emptyStatus, &emptyRevision); err != nil || emptyStatus != "approved" || emptyRevision != 0 {
		t.Fatal("equivalent empty details must not withdraw approval", err, emptyStatus, emptyRevision)
	}
	if _, err = db.Exec(ctx, `UPDATE provider_listings SET status='pending',moderation_notes='' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	check("Old title", 0, "approved")
	if _, err = db.Exec(ctx, `UPDATE provider_listings SET title='Edited title',price=200,status='pending' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	check("Old title", 1, "pending")
	if _, err = db.Exec(ctx, `UPDATE provider_listings SET description='Second edit',attributes='{"price_mode":"quote"}' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	check("Old title", 2, "pending")
	var current []byte
	if err = db.QueryRow(ctx, `SELECT to_jsonb(l) FROM provider_listings l WHERE id=$1`, id).Scan(&current); err != nil {
		t.Fatal(err)
	}
	edits := listingEdits(snapshot, current)
	if len(edits) != 4 {
		t.Fatal("all changes since approval must remain visible", edits)
	}
	var legacyBaseline []byte
	if err = db.QueryRow(ctx, `SELECT approved_snapshot FROM provider_listings WHERE id=$1`, legacy).Scan(&legacyBaseline); err != nil || len(legacyBaseline) != 0 {
		t.Fatal("cannot reconstruct old pending content", err, string(legacyBaseline))
	}
	controller := &ProviderMarketplaceController{db: db}
	app := fiber.New()
	app.Patch("/listing/:listing_id", func(c *fiber.Ctx) error { c.Locals("admin_id", admin); return controller.AdminModerateListing(c) })
	for _, tc := range []struct {
		revision int
		want     int
	}{{1, 409}, {2, 200}} {
		req := httptest.NewRequest("PATCH", "/listing/"+id, strings.NewReader(fmt.Sprintf(`{"status":"approved","expected_content_revision":%d}`, tc.revision)))
		req.Header.Set("Content-Type", "application/json")
		res, e := app.Test(req)
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		if res.StatusCode != tc.want {
			t.Fatal(res.StatusCode, tc.want)
		}
	}
	check("Edited title", 2, "approved")
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE provider_listings SET title='Rolled back' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	tx.Rollback(ctx)
	check("Edited title", 2, "approved")
}
