package main

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"across/backend/internal/config"
	"across/backend/internal/controllers"
	"across/backend/internal/db"
	"across/backend/internal/migrations"
	"across/backend/internal/routes"
	"across/backend/internal/services"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx := context.Background()
	cfg := config.Load()
	store, err := db.New(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	if err := migrations.Run(ctx, store.PG); err != nil {
		log.Fatal(err)
	}

	app := fiber.New(fiber.Config{
		AppName:      "Across API",
		ServerHeader: "Across",
	})
	app.Use(requestid.New())
	app.Use(recover.New())
	app.Use(compress.New(compress.Config{Level: compress.LevelBestSpeed}))
	app.Use(logger.New(logger.Config{
		Format: "${time} ${status} ${latency} ${method} ${path} request_id=${locals:requestid} error=${error}\n",
	}))
	app.Use(cors.New(cors.Config{
		AllowOrigins: cfg.AllowedOrigins,
		AllowHeaders: "Origin, Content-Type, Accept, Authorization, X-Admin-Token, X-Client-Country-Code",
		AllowMethods: "GET,POST,PUT,PATCH,DELETE,OPTIONS",
	}))
	routes.Register(app, store.PG, cfg)

	// Start background cron workers
	go startAutoConfirmWorker(ctx, store.PG)
	go startBatchClosureWorker(ctx, store.PG)
	go startPushNotificationWorker(ctx, store.PG)
	go startEmailDeliveryWorker(ctx, store.PG, cfg)

	log.Printf("service configuration: privy_app_id_set=%t privy_app_secret_set=%t s3_region_set=%t s3_bucket_set=%t smtp_set=%t ses_feedback_set=%t",
		strings.TrimSpace(cfg.PrivyAppID) != "", strings.TrimSpace(cfg.PrivyAppSecret) != "",
		strings.TrimSpace(cfg.AWSRegion) != "", strings.TrimSpace(cfg.S3BucketName) != "",
		strings.TrimSpace(cfg.SMTPHost) != "" && strings.TrimSpace(cfg.SMTPUsername) != "" && strings.TrimSpace(cfg.SMTPPassword) != "",
		strings.TrimSpace(cfg.SESSNSTopicARN) != "")
	log.Fatal(app.Listen(cfg.HTTPAddr))
}

func startEmailDeliveryWorker(ctx context.Context, db *pgxpool.Pool, cfg config.Config) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	sender := services.NewEmailService(cfg)
	runEmailDelivery(ctx, db, sender)
	for {
		select {
		case <-ticker.C:
			runEmailDelivery(ctx, db, sender)
		case <-ctx.Done():
			log.Println("email delivery worker stopped")
			return
		}
	}
}

func runEmailDelivery(ctx context.Context, db *pgxpool.Pool, sender *services.EmailService) {
	count, err := services.RunEmailDeliveryBatch(ctx, db, sender)
	if err != nil {
		log.Printf("email delivery worker error: %v", err)
		return
	}
	if count > 0 {
		log.Printf("email delivery worker: processed %d queued emails", count)
	}
}

func startPushNotificationWorker(ctx context.Context, db *pgxpool.Pool) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	client := &http.Client{Timeout: 15 * time.Second}
	runPushNotifications(ctx, db, client)
	for {
		select {
		case <-ticker.C:
			runPushNotifications(ctx, db, client)
		case <-ctx.Done():
			log.Println("push notification worker stopped")
			return
		}
	}
}

func runPushNotifications(ctx context.Context, db *pgxpool.Pool, client *http.Client) {
	count, err := services.RunPushDeliveryBatch(ctx, db, client)
	if err != nil {
		log.Printf("push notification worker error: %v", err)
		return
	}
	if count > 0 {
		log.Printf("push notification worker: submitted %d deliveries", count)
	}
	checked, err := services.RunPushReceiptBatch(ctx, db, client)
	if err != nil {
		log.Printf("push receipt worker error: %v", err)
		return
	}
	if checked > 0 {
		log.Printf("push receipt worker: checked %d receipts", checked)
	}
}

// startAutoConfirmWorker runs every hour to auto-confirm deliveries older than 3 days
func startAutoConfirmWorker(ctx context.Context, db *pgxpool.Pool) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	// Run once on startup
	runAutoConfirm(ctx, db)

	for {
		select {
		case <-ticker.C:
			runAutoConfirm(ctx, db)
		case <-ctx.Done():
			log.Println("auto-confirm worker stopped")
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

// startBatchClosureWorker closes operational-day batches shortly after their
// country-specific midnight. Conditional updates make this replica-safe.
func startBatchClosureWorker(ctx context.Context, db *pgxpool.Pool) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	runBatchClosure(ctx, db)
	for {
		select {
		case <-ticker.C:
			runBatchClosure(ctx, db)
		case <-ctx.Done():
			log.Println("batch closure worker stopped")
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
