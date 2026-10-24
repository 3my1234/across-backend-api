package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"across/backend/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A bounded functional journey, not the independent 100-pair capacity workload.
// It uses the same fixed, identity-checked synthetic database and HTTP origin.
func releaseJourneys(ctx context.Context, db *pgxpool.Pool) error {
	if err := requireSessionLab(ctx, db); err != nil {
		return err
	}
	var a actor
	if err := db.QueryRow(ctx, `SELECT buyer::text,provider_user::text,provider::text,listing::text,conversation::text,room::text,ticket::text FROM capacity_lab_actors ORDER BY slot LIMIT 1`).Scan(&a.Buyer, &a.ProviderUser, &a.Provider, &a.Listing, &a.Conversation, &a.Room, &a.Ticket); err != nil {
		return err
	}
	c := &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	defer c.CloseIdleConnections()
	backends := map[string]bool{}
	count := 0
	prefix := "release-check-" + uuid.NewString() + ":"
	for n := 0; n < 3; n++ {
		for _, op := range operations(a, n, prefix) {
			result := request(c, op, 0)
			if !result.Valid {
				return fmt.Errorf("release journey %s status=%d", result.Name, result.Status)
			}
			backends[result.Backend] = true
			count++
		}
	}
	var administrator string
	if err := db.QueryRow(ctx, `SELECT admin_id::text FROM capacity_lab_session_run`).Scan(&administrator); err != nil {
		return err
	}
	token, _, err := auth.Sign(administrator, secret, time.Hour)
	if err != nil {
		return err
	}
	op := operation{Name: "admin_subscribers", Path: "/api/v1/admin/subscribers?limit=25&status=active", Token: token, Want: 200}
	if result := request(c, op, 0); !result.Valid {
		return fmt.Errorf("authenticated administrator release read status=%d", result.Status)
	}
	if !backends["one"] || !backends["two"] {
		return fmt.Errorf("release journeys did not traverse both API copies")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"release_journeys_passed": true, "bounded_operations": count + 1, "backends": backends, "external_credentials": "none"})
}
