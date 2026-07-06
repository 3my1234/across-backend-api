package workers

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type EscrowWorker struct {
	db     *pgxpool.Pool
	redis  *redis.Client
	logger *slog.Logger
}

func NewEscrowWorker(db *pgxpool.Pool, redis *redis.Client, logger *slog.Logger) *EscrowWorker {
	return &EscrowWorker{db: db, redis: redis, logger: logger}
}

func (w *EscrowWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		if err := w.ScanDueEscrows(ctx); err != nil {
			w.logger.Error("escrow scan failed", "error", err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *EscrowWorker) ScanDueEscrows(ctx context.Context) error {
	tx, err := w.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT order_id
		FROM escrow_ledger
		WHERE escrow_lock_expiry <= now()
		  AND dispute_status = 'none'
		  AND escrow_status = 'held_in_escrow'
		FOR UPDATE SKIP LOCKED
		LIMIT 500
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	orderIDs := make([]string, 0, 500)
	for rows.Next() {
		var orderID string
		if err := rows.Scan(&orderID); err != nil {
			return err
		}
		orderIDs = append(orderIDs, orderID)
	}
	if rows.Err() != nil {
		return rows.Err()
	}
	if len(orderIDs) == 0 {
		return tx.Commit(ctx)
	}

	for _, orderID := range orderIDs {
		_, err = tx.Exec(ctx, `
			UPDATE orders
			SET ready_for_manual_settlement = true, updated_at = now()
			WHERE id = $1
		`, orderID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO admin_audit_logs(action, entity_type, entity_id, priority, metadata)
			VALUES ('escrow_ready_for_manual_settlement', 'order', $1, 'high',
				jsonb_build_object('reason', 'escrow_lock_expired_no_dispute'))
		`, orderID)
		if err != nil {
			return err
		}
		_ = w.redis.LPush(ctx, "ops:alerts:high", orderID).Err()
	}

	return tx.Commit(ctx)
}
