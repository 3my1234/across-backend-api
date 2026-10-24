package workers

import (
	"across/backend/internal/playbilling"
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"log"
	"time"
)

func runPlayBillingLoop(ctx context.Context, db *pgxpool.Pool) {
	store, err := playbilling.FromEnvironment(db)
	if err != nil {
		log.Print("Play billing configuration is unavailable; purchase activation remains disabled")
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if store != nil {
			batch, done := context.WithTimeout(ctx, 4*time.Minute)
			err = store.Reconcile(batch)
			done()
			if err != nil {
				log.Print("Play subscription reconciliation deferred; no entitlement extension on failed verification")
			}
		}
		heartbeat(ctx, "play_billing")
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
