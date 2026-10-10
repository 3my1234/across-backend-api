package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"across/backend/internal/workers"
)

func TestWorkerHealthRoutes(t *testing.T) {
	dependencyReady := true
	handler := workerHealthHandler(workers.NewMonitor(), func(ctx context.Context) bool {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("dependency checks must be bounded")
		}
		return dependencyReady
	})
	for _, path := range []string{"/", "/api/v1/health", "/api/v1/ready"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		var body struct {
			OK      bool   `json:"ok"`
			Service string `json:"service"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if response.Code != 200 || !body.OK || body.Service != "worker" {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
	}
	dependencyReady = false
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/ready", nil))
	if response.Code != 503 {
		t.Fatalf("failed dependencies must not be ready: %d", response.Code)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/unrecognised", nil))
	if response.Code != 404 {
		t.Fatalf("unknown paths must not masquerade as health checks: %d", response.Code)
	}
}
