package workers

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"log"
	"time"
)

func runDriverExpiryLoop(ctx context.Context, db *pgxpool.Pool) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		check, cancel := context.WithTimeout(ctx, 10*time.Second)
		var count int
		err := db.QueryRow(check, `SELECT purge_expired_driver_presence(1000)`).Scan(&count)
		cancel()
		heartbeat(ctx, "driver_expiry")
		if err != nil {
			log.Printf("driver availability cleanup failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
