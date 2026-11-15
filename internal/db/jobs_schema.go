package db

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Jobs must never start against a schema that cannot enforce its shared allowance.
// Read-only: the normal, separately authorized migrator owns schema changes.
func VerifyJobsSchema(ctx context.Context, pool *pgxpool.Pool) error {
	var ready bool
	err := pool.QueryRow(ctx, `SELECT to_regclass('provider_jobs') IS NOT NULL
 AND to_regclass('provider_job_applications') IS NOT NULL AND to_regclass('provider_job_events') IS NOT NULL
 AND to_regclass('provider_job_reports') IS NOT NULL AND to_regclass('provider_job_moderation') IS NOT NULL
 AND EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('provider_conversations') AND attname='job_id' AND atttypid='uuid'::regtype AND NOT attisdropped)
 AND EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('provider_jobs') AND tgname='provider_job_slot' AND tgenabled IN('O','A') AND NOT tgisinternal)
 AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('provider_conversations') AND conname='conversation_subject_check' AND convalidated)`).Scan(&ready)
	if err != nil || !ready {
		return errors.New("Jobs schema unavailable; apply reviewed migration117 before starting this API")
	}
	return nil
}
