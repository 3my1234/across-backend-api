package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"across/backend/internal/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Journal every logical offer before HTTP, and every observed acknowledgement.
// Aborted runs retain per-key outcomes but can never qualify a completed stage.
func verifySessionAcknowledgements(ctx context.Context, db *pgxpool.Pool, run string, count int64, out io.Writer) (sessionJournalResult, error) {
	raw, err := os.ReadFile("/exchange/k6.log")
	if err != nil {
		return sessionJournalResult{}, err
	}
	offered, ack := parseSessionJournal(string(raw), run)
	exitRaw, err := os.ReadFile("/exchange/k6-exit-code.txt")
	if err != nil {
		return sessionJournalResult{}, err
	}
	completed := strings.TrimSpace(string(exitRaw)) == "0"
	rows, err := db.Query(ctx, `SELECT body FROM provider_conversation_messages WHERE body LIKE $1 UNION ALL SELECT body FROM chat_messages WHERE body LIKE $1`, "sessions-"+run+":%")
	if err != nil {
		return sessionJournalResult{}, err
	}
	defer rows.Close()
	stored := map[string]int{}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			return sessionJournalResult{}, err
		}
		stored[body]++
	}
	if err = rows.Err(); err != nil {
		return sessionJournalResult{}, err
	}
	result := reconcileSessionJournal(offered, ack, stored, count, completed)
	if err = json.NewEncoder(out).Encode(result); err != nil {
		return result, err
	}
	if !result.AcknowledgementsPreserved || !result.JournalConsistent {
		return result, fmt.Errorf("session write-key journal or acknowledged persistence failed")
	}
	return result, nil
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
				Pool     map[string]any `json:"database_pool"`
				Password map[string]any `json:"password_admission"`
			}
			err = json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&data)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 || data.Pool == nil {
				return fmt.Errorf("pool observer %s invalid response", backend)
			}
			sample[backend] = data.Pool
			sample[backend+"_password"] = data.Password
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
