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
	rows, err := db.Query(ctx, `SELECT body FROM provider_conversation_messages WHERE body LIKE $1
 UNION ALL SELECT body FROM chat_messages WHERE body LIKE $1
 UNION ALL SELECT substring(body FROM $2) FROM chat_messages WHERE body LIKE 'Offer: %' AND body LIKE '%'||$1`, "sessions-"+run+":%", `(sessions-`+run+`:[0-9]+:[a-f0-9-]{36}:[a-f0-9-]{36})$`)
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
		// Synthetic lab only: observe backlog without inspecting recipients/payloads
		// or starting delivery workers. Native delivery has no created_at column;
		// use its parent notification's creation time and label that basis.
		queues, err := db.Query(ctx, `
		 SELECT 'email' AS queue, status, count(*),
		        greatest(0, extract(epoch FROM (now()-min(created_at))))::float8 AS oldest_age_seconds
		 FROM email_outbox GROUP BY status
		 UNION ALL
		 SELECT 'native_push', d.status, count(*),
		        greatest(0, extract(epoch FROM (now()-min(n.created_at))))::float8
		 FROM notification_push_deliveries d JOIN notifications n ON n.id=d.notification_id GROUP BY d.status
         UNION ALL
         SELECT 'native_receipts','eligible',count(*),
                greatest(0,extract(epoch FROM (now()-min(sent_at)))-900)::float8
         FROM notification_push_deliveries
         WHERE status='sent' AND receipt_checked_at IS NULL AND sent_at<=now()-interval '15 minutes'
		 UNION ALL
		 SELECT 'web_push', status, count(*),
		        greatest(0, extract(epoch FROM (now()-min(created_at))))::float8
		 FROM web_push_deliveries GROUP BY status`)
		if err != nil {
			return fmt.Errorf("queue observer failed: %w", err)
		}
		queueSamples := []map[string]any{}
		for queues.Next() {
			var name, status string
			var count int64
			var age float64
			if err := queues.Scan(&name, &status, &count, &age); err != nil {
				queues.Close()
				return err
			}
			queueSamples = append(queueSamples, map[string]any{"queue": name, "status": status, "count": count, "oldest_age_seconds": age})
		}
		err = queues.Err()
		queues.Close()
		if err != nil {
			return err
		}
		sample["queues"] = queueSamples
		sample["workers_enabled"] = os.Getenv("LAB_NOTIFICATION_WORKERS") == "1"
		if os.Getenv("LAB_NOTIFICATION_WORKERS") == "1" {
			var stats any
			if err := providerJSON(ctx, "stats", &stats); err != nil {
				return err
			}
			sample["mock"] = stats
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
