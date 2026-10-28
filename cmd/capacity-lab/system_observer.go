package main

import (
	"across/backend/internal/auth"
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"time"
)

// Synthetic lab only. No query text, coordinates, identities or bearer tokens
// enter the output. Unlike the slow queue observer, retain short-burst samples.
func observeSystemBurst(ctx context.Context, db *pgxpool.Pool, out io.Writer) error {
	if err := requireSessionLab(ctx, db); err != nil {
		return err
	}
	var admin string
	if err := db.QueryRow(ctx, "SELECT admin_id::text FROM capacity_lab_session_run").Scan(&admin); err != nil {
		return err
	}
	token, _, err := auth.Sign(admin, secret, time.Hour)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	nextHTTP := time.Time{}
	encode := json.NewEncoder(out)
	for ctx.Err() == nil {
		began := time.Now()
		sample := map[string]any{"time": began.UTC()}
		var activity json.RawMessage
		err = db.QueryRow(ctx, `SELECT COALESCE(json_agg(s),'[]') FROM (
 SELECT application_name,usename,state,wait_event_type,wait_event,query_id::text,count(*) AS connections,
 max(extract(epoch FROM clock_timestamp()-xact_start)*1000) AS oldest_transaction_ms
 FROM pg_stat_activity WHERE datname=current_database() AND backend_type='client backend'
 AND application_name<>'lab-observe-system'
 GROUP BY application_name,usename,state,wait_event_type,wait_event,query_id ORDER BY application_name,query_id LIMIT 64)s`).Scan(&activity)
		if err != nil {
			sample["activity_error"] = true
		} else {
			sample["database_activity"] = activity
		}
		sample["activity_query_ms"] = time.Since(began).Seconds() * 1000
		if !began.Before(nextHTTP) {
			for _, backend := range []string{"api1", "api2"} {
				req, e := http.NewRequestWithContext(ctx, "GET", "http://"+backend+":8080/api/v1/admin/ops/runtime", nil)
				if e != nil {
					return e
				}
				req.Header.Set("Authorization", "Bearer "+token)
				res, e := client.Do(req)
				if e != nil {
					sample[backend] = map[string]any{"observer_http_error": true}
					continue
				}
				var payload struct {
					Pool     map[string]any `json:"database_pool"`
					HTTP     map[string]any `json:"http"`
					Password map[string]any `json:"password_admission"`
				}
				decodeErr := json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&payload)
				res.Body.Close()
				entry := map[string]any{"observer_http_status": res.StatusCode}
				if decodeErr == nil && res.StatusCode == 200 {
					entry["pool"] = payload.Pool
					entry["http"] = payload.HTTP
					entry["password"] = payload.Password
				}
				sample[backend] = entry
			}
			nextHTTP = began.Add(500 * time.Millisecond)
		}
		sample["observer_pool_acquired"] = db.Stat().AcquiredConns()
		if err = encode.Encode(sample); err != nil {
			return err
		}
		remaining := 250*time.Millisecond - time.Since(began)
		if remaining < 0 {
			remaining = 0
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(remaining):
		}
	}
	return nil
}
