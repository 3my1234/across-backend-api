package controllers

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
)

// Chat/review tests deliberately use small schemas. Badge lifecycle tests use
// the actual migrations; this fixture supplies public identity for unrelated tests.
func installPublicIdentityFixture(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	var existing *string
	if err := db.QueryRow(ctx, `SELECT to_regclass('provider_organizations')::text`).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	if existing == nil {
		if _, err := db.Exec(ctx, `CREATE TABLE provider_organizations(id uuid PRIMARY KEY,business_name text DEFAULT 'Provider',verification_status text DEFAULT 'approved',is_active boolean DEFAULT true);
   INSERT INTO provider_organizations(id) SELECT DISTINCT (to_jsonb(l)->>'provider_id')::uuid FROM provider_listings l WHERE to_jsonb(l)->>'provider_id' IS NOT NULL ON CONFLICT DO NOTHING;`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `CREATE VIEW provider_public_identity AS SELECT p.id AS provider_id,
 COALESCE(to_jsonb(p)->>'business_name','Provider') AS name,COALESCE(to_jsonb(p)->>'logo_url','') AS avatar_url,
 COALESCE(to_jsonb(p)->>'verification_status','approved')='approved' AND COALESCE((to_jsonb(p)->>'is_active')::boolean,true) AS verified,
 false AS paid_active,''::text AS badge_tier,1 AS listing_limit FROM provider_organizations p`); err != nil {
		t.Fatal(err)
	}
}
