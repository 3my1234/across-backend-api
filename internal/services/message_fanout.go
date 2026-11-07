package services

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const MessageFanoutBatchSize = 64

// RunMessageFanoutBatch commits notifications, their existing durable delivery
// triggers and the recipient cursor together. A crash rolls the entire page
// back. SKIP LOCKED lets replicas claim other jobs without waiting on this one.
// No chat/room row is locked and no network delivery runs in this transaction.
func RunMessageFanoutBatch(ctx context.Context, db *pgxpool.Pool) (bool, error) {
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var key string
	var cursor int
	err = tx.QueryRow(ctx, `SELECT event_key,next_recipient
FROM message_notification_jobs WHERE completed_at IS NULL AND available_at<=now()
ORDER BY available_at,created_at,event_key FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&key, &cursor)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO notifications(user_id,type,title,body,data,event_key,created_at)
SELECT u.id,j.type,j.title,j.body,j.data,j.event_key||':'||u.id::text,j.created_at
FROM message_notification_jobs j CROSS JOIN LATERAL unnest(j.recipient_ids[($2::int)+1:($2::int)+($3::int)]) recipient(id)
JOIN users u ON u.id=recipient.id WHERE j.event_key=$1
ON CONFLICT(event_key) WHERE event_key IS NOT NULL DO NOTHING`, key, cursor, MessageFanoutBatchSize)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE message_notification_jobs SET next_recipient=least(($2::int)+($3::int),cardinality(recipient_ids)),
completed_at=CASE WHEN ($2::int)+($3::int)>=cardinality(recipient_ids) THEN now() END,available_at=clock_timestamp(),last_error=NULL
WHERE event_key=$1`, key, cursor, MessageFanoutBatchSize)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err == nil {
		return true, nil
	}
	// Release the failed page before scheduling its retry. A poisoned job must
	// not permanently occupy the front of the queue or block unrelated rooms.
	cleanup, stopCleanup := context.WithTimeout(context.Background(), 2*time.Second)
	_ = tx.Rollback(cleanup)
	stopCleanup()
	if parent.Err() == nil {
		retry, stopRetry := context.WithTimeout(parent, time.Second)
		defer stopRetry()
		_, _ = db.Exec(retry, `UPDATE message_notification_jobs SET attempts=attempts+1,
available_at=now()+least(60,attempts+1)*interval '1 second',last_error='notification page failed'
WHERE event_key=$1 AND next_recipient=$2 AND completed_at IS NULL`, key, cursor)
	}
	return true, err
}

// Completed jobs are an audit/retry aid, not an unbounded second message store.
// Pending jobs are never removed, including repeatedly failing jobs.
func PruneCompletedMessageFanout(ctx context.Context, db *pgxpool.Pool) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := db.Exec(ctx, `DELETE FROM message_notification_jobs WHERE event_key IN (
 SELECT event_key FROM message_notification_jobs WHERE completed_at<now()-interval '7 days'
 ORDER BY completed_at,event_key FOR UPDATE SKIP LOCKED LIMIT 256)`)
	return err
}
