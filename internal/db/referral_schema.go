package db

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Read-only startup gate: a separate release migration owns schema changes.
func VerifyReferralCodeSchema(ctx context.Context, pool *pgxpool.Pool) error {
	var ready bool
	err := pool.QueryRow(ctx, `SELECT to_regclass('referral_code_names') IS NOT NULL
 AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('referral_code_names') AND conname='referral_code_names_pkey' AND convalidated)
 AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('referral_code_names') AND conname='referral_code_names_user_id_kind_key' AND convalidated)
 AND EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('referral_codes') AND tgname='register_original_referral_code' AND tgenabled IN('O','A') AND NOT tgisinternal)`).Scan(&ready)
	if err != nil || !ready {
		return errors.New("referral code schema unavailable; apply reviewed migration122 before starting this API")
	}
	return nil
}
