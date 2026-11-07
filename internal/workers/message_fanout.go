package workers

import (
	"context"
	"log"
	"time"

	"across/backend/internal/services"
	"github.com/jackc/pgx/v5/pgxpool"
)

func runMessageFanoutLoop(ctx context.Context, db *pgxpool.Pool) {
	lastObservation := time.Time{}
	lastPrune := time.Time{}
	lastLagWarning := time.Time{}
	for {
		// Bound active work by both elapsed time and pages, then explicitly yield.
		// Four jobs/second was below the observed ~8 writes/second at100 users.
		if err := drainMessageFanoutQuantum(ctx, func(ctx context.Context) (bool, error) {
			return services.RunMessageFanoutBatch(ctx, db)
		}, time.Now); err != nil {
			log.Print("message notification fan-out batch failed; durable job retained for recovery")
		}
		if time.Since(lastPrune) >= time.Minute {
			if err := services.PruneCompletedMessageFanout(ctx, db); err != nil {
				log.Print("message notification completed-job cleanup failed")
			}
			lastPrune = time.Now()
		}
		if time.Since(lastObservation) >= time.Second {
			probe, cancel := context.WithTimeout(ctx, time.Second)
			lag, err := services.MessageFanoutLag(probe, db)
			failed, failedErr := services.MessageFanoutHasFailures(probe, db)
			cancel()
			if (err != nil || failedErr != nil || lag >= 5 || failed) && time.Since(lastLagWarning) >= time.Minute {
				log.Printf("message notification backlog oldest_seconds=%.1f failed_jobs_present=%t observation_ok=%t; check consumer throughput and retries", lag, failed, err == nil && failedErr == nil)
				lastLagWarning = time.Now()
			}
			lastObservation = time.Now()
		}
		heartbeat(ctx, "message_fanout")
		pause := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			pause.Stop()
			return
		case <-pause.C:
		}
	}
}

// One slow page may run up to its own service deadline, but no further page
// starts after this quantum expires. No extra connection or goroutine is added.
func drainMessageFanoutQuantum(ctx context.Context, run func(context.Context) (bool, error), now func() time.Time) error {
	deadline := now().Add(100 * time.Millisecond)
	for page := 0; page < 32 && now().Before(deadline); page++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		worked, err := run(ctx)
		if err != nil {
			return err
		}
		if !worked {
			return nil
		}
	}
	return nil
}
