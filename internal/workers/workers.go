package workers

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"across/backend/internal/config"
	"across/backend/internal/controllers"
	"across/backend/internal/services"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Run starts all durable background consumers. Queue claims use SKIP LOCKED
// and idempotent database keys, so multiple worker replicas can run safely.
func Run(ctx context.Context, db *pgxpool.Pool, cfg config.Config) {
	var group sync.WaitGroup
	group.Add(4)
	go func() { defer group.Done(); runEmailLoop(ctx, db, cfg) }()
	go func() { defer group.Done(); runPushLoop(ctx, db) }()
	go func() { defer group.Done(); runAutoConfirmLoop(ctx, db) }()
	go func() { defer group.Done(); runBatchClosureLoop(ctx, db) }()
	group.Wait()
}

func runEmailLoop(ctx context.Context, db *pgxpool.Pool, cfg config.Config) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	sender := services.NewEmailService(cfg)
	runEmail(ctx, db, sender)
	for {
		select {
		case <-ticker.C:
			runEmail(ctx, db, sender)
		case <-ctx.Done():
			return
		}
	}
}

func runEmail(ctx context.Context, db *pgxpool.Pool, sender *services.EmailService) {
	count, err := services.RunEmailDeliveryBatch(ctx, db, sender)
	if err != nil {
		log.Printf("email delivery worker error: %v", err)
		return
	}
	if count > 0 {
		log.Printf("email delivery worker: processed %d queued emails", count)
	}
}

func runPushLoop(ctx context.Context, db *pgxpool.Pool) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	client := &http.Client{Timeout: 15 * time.Second}
	runPush(ctx, db, client)
	for {
		select {
		case <-ticker.C:
			runPush(ctx, db, client)
		case <-ctx.Done():
			return
		}
	}
}

func runPush(ctx context.Context, db *pgxpool.Pool, client *http.Client) {
	count, err := services.RunPushDeliveryBatch(ctx, db, client)
	if err != nil {
		log.Printf("push notification worker error: %v", err)
		return
	}
	if count > 0 {
		log.Printf("push notification worker: submitted %d deliveries", count)
	}
	if _, err = services.RunPushReceiptBatch(ctx, db, client); err != nil {
		log.Printf("push receipt worker error: %v", err)
	}
}

func runAutoConfirmLoop(ctx context.Context, db *pgxpool.Pool) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	runAutoConfirm(ctx, db)
	for {
		select {
		case <-ticker.C:
			runAutoConfirm(ctx, db)
		case <-ctx.Done():
			return
		}
	}
}

func runAutoConfirm(ctx context.Context, db *pgxpool.Pool) {
	count, err := controllers.AutoConfirmExpiredDeliveries(ctx, db)
	if err != nil {
		log.Printf("auto-confirm worker error: %v", err)
		return
	}
	if count > 0 {
		log.Printf("auto-confirm worker: auto-confirmed %d deliveries", count)
	}
}

func runBatchClosureLoop(ctx context.Context, db *pgxpool.Pool) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	runBatchClosure(ctx, db)
	for {
		select {
		case <-ticker.C:
			runBatchClosure(ctx, db)
		case <-ctx.Done():
			return
		}
	}
}

func runBatchClosure(ctx context.Context, db *pgxpool.Pool) {
	count, err := controllers.CloseExpiredBatches(ctx, db)
	if err != nil {
		log.Printf("batch closure worker error: %v", err)
		return
	}
	if count > 0 {
		log.Printf("batch closure worker: closed %d operational-day batches", count)
	}
}
