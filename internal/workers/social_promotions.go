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
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		heartbeat(ctx, "social_promotions")
		for i := 0; i < 2; i++ {
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
func buildSocialPromotions(ctx context.Context, db *pgxpool.Pool, cache *redis.Client) error {
	task, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := db.Begin(task)
	if err != nil {
		return err
	}
	defer tx.Rollback(task)
	var actor string
	err = tx.QueryRow(task, `SELECT user_id::text FROM social_promotion_jobs ORDER BY requested_at,user_id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&actor)
	if err != nil {
		return err
	}
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
