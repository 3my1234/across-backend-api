package workers

import (
	"context"
	"log"
	"time"

	"across/backend/internal/services"
	"github.com/jackc/pgx/v5/pgxpool"
)

func runMessageFanoutLoop(ctx context.Context, db *pgxpool.Pool) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastPrune := time.Time{}
	for {
		// A bounded page budget per tick prevents this role consuming the whole
		// shared worker connection budget while a backlog is draining.
		for page := 0; page < 4; page++ {
			worked, err := services.RunMessageFanoutBatch(ctx, db)
			if err != nil {
				log.Print("message notification fan-out batch failed; durable retry retained")
				break
			}
			if !worked {
				break
			}
		}
		if time.Since(lastPrune) >= time.Minute {
			if err := services.PruneCompletedMessageFanout(ctx, db); err != nil {
				log.Print("message notification completed-job cleanup failed")
			}
			lastPrune = time.Now()
		}
		heartbeat(ctx, "message_fanout")
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
