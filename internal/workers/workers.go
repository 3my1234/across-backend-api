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
	group.Add(6)
	go func() { defer group.Done(); runEmailLoop(ctx, db, cfg) }()
	go func() { defer group.Done(); runPushLoop(ctx, db) }()
	go func() { defer group.Done(); runPushReceiptLoop(ctx, db) }()
	go func() { defer group.Done(); runAutoConfirmLoop(ctx, db) }()
	go func() { defer group.Done(); runBatchClosureLoop(ctx, db) }()
	go func() { defer group.Done(); runSettlementReconciliationLoop(ctx, db, cfg) }()
	group.Wait()
}

func runSettlementReconciliationLoop(ctx context.Context, db *pgxpool.Pool, cfg config.Config) {
	// Durable jobs schedule full checks every 30 minutes and page continuations
	// after one minute, independently of the number of sellers or replicas.
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	client := &http.Client{Timeout: 20 * time.Second}
	runSettlementReconciliation(ctx, db, cfg, client)
	for {
		select {
		case <-ticker.C:
			runSettlementReconciliation(ctx, db, cfg, client)
		case <-ctx.Done():
			return
		}
	}
}

func runSettlementReconciliation(ctx context.Context, db *pgxpool.Pool, cfg config.Config, client *http.Client) {
	defer heartbeat(ctx, "settlements")
	count, err := controllers.ReconcileFlutterwaveSettlements(ctx, db, cfg, client)
	if err != nil {
		log.Printf("settlement reconciliation worker error: %v", err)
		return
	}
	if count > 0 {
		log.Printf("settlement reconciliation worker: updated %d seller payouts", count)
	}
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
	defer heartbeat(ctx, "email")
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
	defer heartbeat(ctx, "push")
	count, err := services.RunPushDeliveryBatch(ctx, db, client)
	if err != nil {
		log.Printf("push notification worker error: %v", err)
		return
	}
	if count > 0 {
		log.Printf("push notification worker: submitted %d deliveries", count)
	}
}

// Receipt lookups must not block sending new order updates when Expo is slow.
func runPushReceiptLoop(ctx context.Context, db *pgxpool.Pool) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	client := &http.Client{Timeout: 15 * time.Second}
	for {
		select {
		case <-ticker.C:
			if _, err := services.RunPushReceiptBatch(ctx, db, client); err != nil {
				log.Printf("push receipt worker error: %v", err)
			}
			heartbeat(ctx, "push_receipts")
		case <-ctx.Done():
			return
		}
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
	defer heartbeat(ctx, "auto_confirm")
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
	defer heartbeat(ctx, "batch_closure")
	count, err := controllers.CloseExpiredBatches(ctx, db)
	if err != nil {
		log.Printf("batch closure worker error: %v", err)
		return
	}
	if count > 0 {
		log.Printf("batch closure worker: closed %d operational-day batches", count)
	}
}
