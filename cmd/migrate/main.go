package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"across/backend/internal/config"
	"across/backend/internal/db"
	"across/backend/internal/migrations"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	cfg := config.Load()
	pool, err := db.NewPostgresPool(ctx, cfg.MigrationDatabaseURL, 2, 1)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := migrations.Run(ctx, pool); err != nil {
		log.Fatal(err)
	}
	log.Println("Atlantic Express database migrations completed")
}
