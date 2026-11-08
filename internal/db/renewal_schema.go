package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The new API must not begin serving against the pre-lease billing schema.
// This is read-only; only the separately approved migrator may apply migration114.
func VerifyProviderRenewalSchema(ctx context.Context, pool *pgxpool.Pool) error {
	var ready bool
	err := pool.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('provider_renewal_preferences') AND attname='operation_token' AND atttypid='uuid'::regtype AND NOT attisdropped)
 AND EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('provider_renewal_preferences') AND attname='operation_expires_at' AND atttypid='timestamptz'::regtype AND NOT attisdropped)
 AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('provider_renewal_preferences') AND conname='provider_renewal_operation_pair' AND convalidated)
 AND EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('provider_renewal_preferences') AND tgname='provider_renewal_operation_fence' AND tgenabled IN('O','A') AND NOT tgisinternal)`).Scan(&ready)
	if err != nil || !ready {
		return errors.New("provider renewal schema unavailable; apply reviewed migration114 before starting this API")
	}
	return nil
}
