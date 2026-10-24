package playbilling

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Aggregate-only operational evidence; no token/hash, user or receipt details.
func QueueHealth(ctx context.Context, db *pgxpool.Pool) (map[string]any, error) {
	var ready bool
	if err := db.QueryRow(ctx, `SELECT to_regclass('play_provider_events') IS NOT NULL`).Scan(&ready); err != nil {
		return nil, errors.New("Play queue metadata is unavailable")
	}
	if !ready {
		return map[string]any{"schema_ready": false}, nil
	}
	var due, ack, refunds, events int64
	var oldest, oldestEvent float64
	if err := db.QueryRow(ctx, `SELECT count(*),COALESCE(MAX(GREATEST(EXTRACT(EPOCH FROM now()-CASE WHEN next_check_at='-infinity'::timestamptz THEN checked_at ELSE next_check_at END),0)),0) FROM play_provider_purchases WHERE next_check_at<=now()`).Scan(&due, &oldest); err != nil {
		return nil, errors.New("Play purchase queue is unavailable")
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM play_provider_purchases WHERE needs_acknowledgement`).Scan(&ack); err != nil {
		return nil, errors.New("Play acknowledgement queue is unavailable")
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM play_provider_refund_jobs WHERE status='requested'`).Scan(&refunds); err != nil {
		return nil, errors.New("Play refund queue is unavailable")
	}
	if err := db.QueryRow(ctx, `SELECT count(*),COALESCE(MAX(EXTRACT(EPOCH FROM now()-received_at)),0) FROM play_provider_events WHERE processed_at IS NULL`).Scan(&events, &oldestEvent); err != nil {
		return nil, errors.New("Play notification queue is unavailable")
	}
	return map[string]any{"schema_ready": true, "due": due, "oldest_due_seconds": oldest, "needs_acknowledgement": ack, "refunds_pending": refunds, "events_pending": events, "oldest_event_seconds": oldestEvent}, nil
}
