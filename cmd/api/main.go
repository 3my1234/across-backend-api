package main

import (
	"context"
	"log"
	"strings"

	"across/backend/internal/config"
	"across/backend/internal/db"
	"across/backend/internal/migrations"
	"across/backend/internal/routes"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
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
	app.Use(logger.New(logger.Config{
		Format: "${time} ${status} ${latency} ${method} ${path} request_id=${locals:requestid} error=${error}\n",
	}))
	app.Use(cors.New(cors.Config{
		AllowOrigins: cfg.AllowedOrigins,
		AllowHeaders: "Origin, Content-Type, Accept, Authorization, X-Admin-Token, X-Client-Country-Code",
		AllowMethods: "GET,POST,PUT,PATCH,DELETE,OPTIONS",
	}))
	routes.Register(app, store.PG, cfg)

	log.Printf("service configuration: privy_app_id_set=%t privy_app_secret_set=%t s3_region_set=%t s3_bucket_set=%t",
		strings.TrimSpace(cfg.PrivyAppID) != "", strings.TrimSpace(cfg.PrivyAppSecret) != "",
		strings.TrimSpace(cfg.AWSRegion) != "", strings.TrimSpace(cfg.S3BucketName) != "")
	log.Fatal(app.Listen(cfg.HTTPAddr))
}
