package workers

import (
	"across/backend/internal/services"
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"log"
	"time"
)

// Graph work stays off the feed request. Each walk has three levels, at most
// twenty neighbours per node, a two-second statement budget and 200 cached paths.
// Cached paths are private worker data, never returned to clients.
func runSocialPromotionLoop(ctx context.Context, db *pgxpool.Pool, cache *redis.Client) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		heartbeat(ctx, "social_promotions")
		for i := 0; i < 5; i++ {
			if cache == nil {
				break
			}
			if err := buildSocialPromotions(ctx, db, cache); err != nil {
				if err != pgx.ErrNoRows {
					log.Printf("social promotion worker: %v", err)
				}
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func buildSocialPromotions(ctx context.Context, db *pgxpool.Pool, cache *redis.Client) (err error) {
	task, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := db.Begin(task)
	if err != nil {
		return err
	}
	defer tx.Rollback(task)
	var actor string
	err = tx.QueryRow(task, `SELECT user_id::text FROM social_promotion_jobs WHERE next_attempt_at<=now() ORDER BY next_attempt_at,requested_at,user_id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&actor)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			// Release the claim before recording a retry. One expensive graph or
			// transient cache failure must not indefinitely occupy the queue head.
			_ = tx.Rollback(ctx)
			retry, cancelRetry := context.WithTimeout(ctx, time.Second)
			defer cancelRetry()
			_, _ = db.Exec(retry, `UPDATE social_promotion_jobs SET attempts=attempts+1,next_attempt_at=clock_timestamp()+make_interval(secs=>LEAST(3600,30*power(2,LEAST(attempts,7)))::double precision) WHERE user_id=$1::uuid`, actor)
		}
	}()
	_, err = tx.Exec(task, `SET LOCAL statement_timeout='2s'`)
	if err != nil {
		return err
	}
	paths, err := services.PromotionPaths(task, tx, actor)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(paths)
	if err != nil {
		return err
	}
	if err = cache.Set(task, "atl:social:promotions:"+actor, raw, 15*time.Minute).Err(); err != nil {
		return err
	}
	_, err = tx.Exec(task, `DELETE FROM social_promotion_jobs WHERE user_id=$1::uuid`, actor)
	if err != nil {
		return err
	}
	return tx.Commit(task)
}
