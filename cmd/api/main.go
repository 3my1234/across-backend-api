package main

import (
	"context"
	"log"
	"strings"
	"time"

	"across/backend/internal/config"
	"across/backend/internal/db"
	"across/backend/internal/migrations"
	"across/backend/internal/routes"

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

	log.Printf("service configuration: privy_app_id_set=%t privy_app_secret_set=%t s3_region_set=%t s3_bucket_set=%t",
		strings.TrimSpace(cfg.PrivyAppID) != "", strings.TrimSpace(cfg.PrivyAppSecret) != "",
		strings.TrimSpace(cfg.AWSRegion) != "", strings.TrimSpace(cfg.S3BucketName) != "")
	log.Fatal(app.Listen(cfg.HTTPAddr))
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
	tag, err := db.Exec(ctx, `
		UPDATE orders
		SET order_status = 'Completed',
			delivery_confirmed = true,
			confirmed_at = now(),
			updated_at = now()
		WHERE current_tracking_stage = 'Delivered'::tracking_stage
			AND delivery_confirmed = false
			AND delivered_at < now() - interval '3 days'
	`)
	if err != nil {
		log.Printf("auto-confirm worker error: %v", err)
		return
	}
	if count := tag.RowsAffected(); count > 0 {
		log.Printf("auto-confirm worker: auto-confirmed %d deliveries", count)
	}
}
