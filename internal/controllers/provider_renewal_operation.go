package controllers

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

func renewalOperationBusy() error {
	return fiber.NewError(409, "A renewal change is already in progress. Check its status before retrying.")
}

// Lease release never turns an uncertain gateway result into stopped renewal.
// Fencing keeps an old request from releasing a replacement operation's lease.
func releaseRenewalOperation(db *pgxpool.Pool, provider, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tx, err := db.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT set_config('app.renewal_operation_token',$1,true)`, token); err != nil {
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE provider_renewal_preferences SET operation_token=NULL,operation_expires_at=NULL WHERE provider_id=$1::uuid AND operation_token=$2::uuid`, provider, token); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}

// All final local state changes commit together after confirmed gateway absence.
// No network call is made inside this transaction.
func completeRenewalOperation(ctx context.Context, db *pgxpool.Pool, provider, user, token string, next, effective any) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT set_config('app.renewal_operation_token',$1,true)`, token); err != nil {
		return fiber.ErrInternalServerError
	}
	var current bool
	if err = tx.QueryRow(ctx, `SELECT operation_token=$2::uuid AND operation_expires_at>clock_timestamp() FROM provider_renewal_preferences WHERE provider_id=$1::uuid FOR UPDATE`, provider, token).Scan(&current); err != nil || !current {
		return renewalOperationBusy()
	}
	var owner bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_members WHERE provider_id=$1::uuid AND user_id=$2::uuid AND is_active AND role='owner')`, provider, user).Scan(&owner); err != nil {
		return fiber.ErrInternalServerError
	}
	if !owner {
		return fiber.ErrForbidden
	}
	if _, err = tx.Exec(ctx, `UPDATE provider_renewal_cancellations SET status='confirmed',updated_at=now() WHERE provider_id=$1::uuid AND status<>'confirmed'`, provider); err != nil {
		return fiber.ErrInternalServerError
	}
	if _, err = tx.Exec(ctx, `UPDATE provider_renewal_preferences SET renewal_status='stopped',next_plan_id=$2::uuid,change_at=$3,updated_at=now(),operation_token=NULL,operation_expires_at=NULL WHERE provider_id=$1::uuid`, provider, next, effective); err != nil {
		return fiber.ErrInternalServerError
	}
	if err = tx.Commit(ctx); err != nil {
		return fiber.ErrInternalServerError
	}
	return nil
}
