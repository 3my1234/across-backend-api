package workers

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"log"
	"time"
)

func runTownHallExpiryLoop(ctx context.Context, db *pgxpool.Pool) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		check, cancel := context.WithTimeout(ctx, 10*time.Second)
		var count int
		err := db.QueryRow(check, `SELECT expire_unengaged_town_hall_requests(250)`).Scan(&count)
		cancel()
		heartbeat(ctx, "town_hall_expiry")
		if err != nil {
			log.Printf("town hall expiry worker error: %v", err)
		} else if count > 0 {
			log.Printf("town hall expiry worker: closed %d unanswered requests", count)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
