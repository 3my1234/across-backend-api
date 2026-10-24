package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"time"

	"across/backend/internal/config"
	"across/backend/internal/db"
	"across/backend/internal/migrations"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cfg := config.Load()
	if err := cfg.Validate("migrate"); err != nil {
		log.Fatal(err)
	}
	pool, err := db.NewMigrationPool(ctx, cfg.MigrationDatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := migrations.Run(ctx, pool); err != nil {
		log.Fatal(err)
	}
	log.Println("Atlantic Express database migrations completed")
}
