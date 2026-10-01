package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"across/backend/internal/config"
	"across/backend/internal/db"
	"across/backend/internal/workers"
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
	healthServer := &http.Server{Addr: cfg.WorkerHealthAddr, ReadHeaderTimeout: 3 * time.Second}
	http.HandleFunc("/api/v1/health", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]bool{"ok": true})
	})
	http.HandleFunc("/api/v1/ready", func(response http.ResponseWriter, request *http.Request) {
		ready := store.PG.Ping(request.Context()) == nil
		if store.Redis == nil && !cfg.RedisOptional {
			ready = false
		} else if store.Redis != nil && store.Redis.Ping(request.Context()).Err() != nil {
			ready = false
		}
		response.Header().Set("Content-Type", "application/json")
		if !ready {
			response.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(response).Encode(map[string]bool{"ok": ready})
	})
	go func() {
		if err := healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("worker health server error: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = healthServer.Shutdown(shutdownCtx)
	}()
	log.Println("Atlantic Express background worker started")
	workers.Run(ctx, store.PG, cfg)
	log.Println("Atlantic Express background worker stopped")
}
