package main

import (
	"context"
	"github.com/jackc/pgx/v5"
)

// Called only inside guarded one-shot preparation of synthetic capacity_lab.
// Verification evidence is synthetic; no files, credentials or production rows.
func prepareExpandedJourneys(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `CREATE TABLE capacity_lab_expanded_identity(marker text PRIMARY KEY CHECK(marker='synthetic-expanded-v1'));
	INSERT INTO capacity_lab_expanded_identity VALUES('synthetic-expanded-v1');
	INSERT INTO provider_verification_documents(provider_id,document_type,document_url,status,reviewed_at)
	SELECT a.provider,kind,'https://media.example.invalid/synthetic-document','approved',now()
	FROM capacity_lab_actors a CROSS JOIN unnest(ARRAY['drivers_license','driver_vehicle','driver_plate']) kind;
	UPDATE provider_organizations SET is_driver=true WHERE id IN(SELECT provider FROM capacity_lab_actors);
	UPDATE provider_listings l SET listing_type='artisan',
	 category=CASE WHEN a.slot%2=0 THEN 'Motorcycle courier' ELSE 'Independent driver' END,
	 title=CASE WHEN a.slot%2=0 THEN 'Synthetic motorcycle courier' ELSE 'Synthetic passenger driver' END
	FROM capacity_lab_actors a WHERE l.id=a.listing;
	UPDATE town_hall_requests SET message_only=false,unit='job'
	WHERE room_id IN(SELECT room FROM capacity_lab_actors UNION SELECT hot_room FROM capacity_lab_actors WHERE hot_room IS NOT NULL);`)
	return err
}
