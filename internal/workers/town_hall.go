package workers

import (
	"across/backend/internal/controllers"
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"log"
	"time"
)

func runTownHallExpiryLoop(ctx context.Context, db *pgxpool.Pool) {
	lastDiscoveryPurge := time.Time{}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		check, cancel := context.WithTimeout(ctx, 10*time.Second)
		var count int
		err := db.QueryRow(check, `SELECT purge_ended_town_hall_requests(250)`).Scan(&count)
		if time.Since(lastDiscoveryPurge) > time.Minute {
			if purgeErr := controllers.PurgeDiscoveryMetrics(check, db); purgeErr != nil {
				log.Printf("discovery retention unavailable: %v", purgeErr)
			}
			lastDiscoveryPurge = time.Now()
		}
		cancel()
		heartbeat(ctx, "town_hall_expiry")
		if err != nil {
			log.Printf("town hall expiry worker error: %v", err)
		} else if count > 0 {
			log.Printf("town hall expiry worker: purged %d ended requests", count)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
