package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"time"

	"across/backend/internal/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Only synthetic acknowledgement keys are logged; no tokens or coordinates.
func verifySessionAcknowledgements(ctx context.Context, db *pgxpool.Pool, run string, count int64, out io.Writer) error {
	raw, err := os.ReadFile("/exchange/k6.log")
	if err != nil {
		return err
	}
	expression := regexp.MustCompile(`ACK:(sessions-` + regexp.QuoteMeta(run) + `:[0-9]+:[a-f0-9-]{36}:[a-f0-9-]{36})`)
	ack := map[string]int{}
	for _, match := range expression.FindAllStringSubmatch(string(raw), -1) {
		ack[match[1]]++
	}
	if int64(len(ack)) != count {
		return fmt.Errorf("acknowledgement inventory incomplete")
	}
	rows, err := db.Query(ctx, `SELECT body FROM provider_conversation_messages WHERE body LIKE $1 UNION ALL SELECT body FROM chat_messages WHERE body LIKE $1`, "sessions-"+run+":%")
	if err != nil {
		return err
	}
	defer rows.Close()
	stored := map[string]int{}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			return err
		}
		stored[body]++
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(stored) != len(ack) {
		return fmt.Errorf("stored key inventory differs")
	}
	for key, n := range ack {
		if n != 1 || stored[key] != 1 {
			return fmt.Errorf("acknowledged write missing or duplicated")
		}
	}
	return json.NewEncoder(out).Encode(map[string]any{"exact_acknowledged_keys": len(ack), "all_acknowledged_keys_stored_once": true})
}

func observeSessions(ctx context.Context, db *pgxpool.Pool, out io.Writer) error {
	if err := requireSessionLab(ctx, db); err != nil {
		return err
	}
	var admin string
	if err := db.QueryRow(ctx, `SELECT admin_id::text FROM capacity_lab_session_run`).Scan(&admin); err != nil {
		return err
	}
	token, _, err := auth.Sign(admin, secret, time.Hour)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	for {
		sample := map[string]any{"time": time.Now().UTC()}
		for _, backend := range []string{"api1", "api2"} {
			req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+backend+":8080/api/v1/admin/ops/runtime", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			response, err := client.Do(req)
			if err != nil {
				return fmt.Errorf("pool observer %s failed", backend)
			}
			var data struct {
				Pool map[string]any `json:"database_pool"`
			}
			err = json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&data)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 || data.Pool == nil {
				return fmt.Errorf("pool observer %s invalid response", backend)
			}
			sample[backend] = data.Pool
		}
		if err := json.NewEncoder(out).Encode(sample); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
}
