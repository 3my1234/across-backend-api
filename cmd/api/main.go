package main

import (
	"context"
	"log"

	"across/backend/internal/config"
	"across/backend/internal/db"
	"across/backend/internal/routes"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

func main() {
	ctx := context.Background()
	cfg := config.Load()
	store, err := db.New(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	app := fiber.New(fiber.Config{
		AppName:      "Across API",
		ServerHeader: "Across",
	})
	app.Use(cors.New(cors.Config{
		AllowOrigins: "http://localhost:5173,http://127.0.0.1:5173",
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
		AllowMethods: "GET,POST,PUT,PATCH,DELETE,OPTIONS",
	}))
	routes.Register(app, store.PG, cfg)

	log.Fatal(app.Listen(cfg.HTTPAddr))
}
