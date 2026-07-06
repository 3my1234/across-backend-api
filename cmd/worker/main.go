package main

import (
	"context"
	"log"
	"log/slog"
	"os"

	"across/backend/internal/config"
	"across/backend/internal/db"
	"across/backend/internal/workers"
)

func main() {
	ctx := context.Background()
	cfg := config.Load()
	store, err := db.New(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	if store.Redis == nil {
		log.Fatal("redis is required for the escrow worker")
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	worker := workers.NewEscrowWorker(store.PG, store.Redis, logger)
	if err := worker.Run(ctx); err != nil && err != context.Canceled {
		log.Fatal(err)
	}
}
