package main

import (
	"context"
	"log"
	"net"
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
	monitor := workers.NewMonitor()
	ctx = workers.WithMonitor(ctx, monitor)
	healthServer := &http.Server{Addr: cfg.WorkerHealthAddr, ReadHeaderTimeout: 3 * time.Second,
		Handler: workerHealthHandler(monitor, func(checkCtx context.Context) bool {
			ready := store.PG.Ping(checkCtx) == nil
			if store.Redis == nil && !cfg.RedisOptional {
				ready = false
			} else if store.Redis != nil && store.Redis.Ping(checkCtx).Err() != nil {
				ready = false
			}
			return ready
		})}
	listener, err := net.Listen("tcp", cfg.WorkerHealthAddr)
	if err != nil {
		log.Fatalf("worker health server could not start: %v", err)
	}
	go func() {
		if err := healthServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Fatalf("worker health server error: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = healthServer.Shutdown(shutdownCtx)
	}()
	// Exit a process with a stuck consumer so the container restart policy can
	// recover it. PostgreSQL leases preserve in-flight jobs for safe retry.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if healthy, checks := monitor.Snapshot(); !healthy {
					log.Fatalf("worker polling stalled; exiting for restart: %v", checks)
				}
			}
		}
	}()
	log.Println("Atlantic Express background worker started")
	workers.Run(ctx, store.PG, cfg)
	log.Println("Atlantic Express background worker stopped")
}
