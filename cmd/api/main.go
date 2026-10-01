package main

import (
	"context"
	"log"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"across/backend/internal/config"
	"across/backend/internal/db"
	appmiddleware "across/backend/internal/middleware"
	"across/backend/internal/migrations"
	"across/backend/internal/routes"
	"across/backend/internal/workers"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
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
	app.Use(appmiddleware.DistributedRateLimit(store.Redis, "api", cfg.RateLimitPerMinute, time.Minute))
	app.Use(appmiddleware.SharedPublicCache(store.Redis, time.Duration(cfg.PublicCacheTTLSeconds)*time.Second))
	app.Use(compress.New(compress.Config{Level: compress.LevelBestSpeed}))
	app.Use(logger.New(logger.Config{
		Format: "${time} ${status} ${latency} ${method} ${path} request_id=${locals:requestid} error=${error}\n",
	}))
	app.Use(cors.New(cors.Config{
		AllowOrigins: cfg.AllowedOrigins,
		AllowHeaders: "Origin, Content-Type, Accept, Authorization, X-Admin-Token, X-Client-Country-Code",
		AllowMethods: "GET,POST,PUT,PATCH,DELETE,OPTIONS",
	}))
	routes.Register(app, store.PG, store.ReadPG, store.Redis, cfg)

	// Backward-compatible single-resource mode. At scale, set this false and
	// run one or more /app/across-worker services independently.
	if cfg.RunInlineWorkers {
		go workers.Run(ctx, store.PG, cfg)
	}

	log.Printf("service configuration: privy_app_id_set=%t privy_app_secret_set=%t s3_region_set=%t s3_bucket_set=%t smtp_set=%t ses_feedback_set=%t",
		strings.TrimSpace(cfg.PrivyAppID) != "", strings.TrimSpace(cfg.PrivyAppSecret) != "",
		strings.TrimSpace(cfg.AWSRegion) != "", strings.TrimSpace(cfg.S3BucketName) != "",
		strings.TrimSpace(cfg.SMTPHost) != "" && strings.TrimSpace(cfg.SMTPUsername) != "" && strings.TrimSpace(cfg.SMTPPassword) != "",
		strings.TrimSpace(cfg.SESSNSTopicARN) != "")
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- app.Listen(cfg.HTTPAddr) }()
	select {
	case err := <-serverErrors:
		if err != nil {
			log.Fatal(err)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := app.ShutdownWithContext(shutdownCtx); err != nil {
			log.Printf("graceful shutdown error: %v", err)
		}
	}
}
