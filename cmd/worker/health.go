package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"across/backend/internal/workers"
)

func workerHealthHandler(monitor *workers.Monitor, dependencies func(context.Context) bool) http.Handler {
	mux := http.NewServeMux()
	live := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/api/v1/health" {
			http.NotFound(w, r)
			return
		}
		healthy, checks := monitor.Snapshot()
		w.Header().Set("Content-Type", "application/json")
		if !healthy {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": healthy, "service": "worker", "loops": checks})
	}
	mux.HandleFunc("/", live)
	mux.HandleFunc("/api/v1/health", live)
	mux.HandleFunc("/api/v1/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		healthy, checks := monitor.Snapshot()
		ready := dependencies(ctx) && healthy
		w.Header().Set("Content-Type", "application/json")
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": ready, "service": "worker", "loops": checks})
	})
	return mux
}
