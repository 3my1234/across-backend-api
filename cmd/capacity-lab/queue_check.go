package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"across/backend/internal/services"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type isolatedPushTransport struct {
	fail     atomic.Bool
	requests atomic.Int64
}

func (t *isolatedPushTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "exp.host" || r.URL.Path != "/--/api/v2/push/send" {
		return nil, fmt.Errorf("unexpected push transport request rejected")
	}
	t.requests.Add(1)
	if t.fail.Swap(false) {
		return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(`{"error":"synthetic upstream interruption"}`))}, nil
	}
	var messages []map[string]any
	if err := json.NewDecoder(r.Body).Decode(&messages); err != nil {
		return nil, err
	}
	tickets := []map[string]string{}
	for range messages {
		tickets = append(tickets, map[string]string{"status": "ok", "id": uuid.NewString()})
	}
	data, _ := json.Marshal(map[string]any{"data": tickets})
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}, nil
}

// Real durable native-push selection/lease/retry code, with a transport that has
// no network dialer. This proves bounded mechanics, not vendor/device delivery.
func sessionQueueCheck(ctx context.Context, db *pgxpool.Pool, out io.Writer) error {
	if err := requireSessionLab(ctx, db); err != nil {
		return err
	}
	var total int64
	if err := db.QueryRow(ctx, `SELECT count(*) FROM notification_push_deliveries`).Scan(&total); err != nil {
		return err
	}
	if total == 0 {
		return fmt.Errorf("no workload push jobs were generated")
	}
	transport := &isolatedPushTransport{}
	transport.fail.Store(true)
	client := &http.Client{Transport: transport}
	began := time.Now()
	if _, err := services.RunPushDeliveryBatch(ctx, db, client); err != nil {
		return err
	}
	var retry int64
	if err := db.QueryRow(ctx, `SELECT count(*) FROM notification_push_deliveries WHERE status='retry'`).Scan(&retry); err != nil || retry == 0 {
		return fmt.Errorf("upstream outage did not create durable retry")
	}
	// Simulate jobs left in sending by a killed worker and advance test retry time.
	if _, err := db.Exec(ctx, `UPDATE notification_push_deliveries SET status='sending',updated_at=now()-interval '6 minutes' WHERE status='retry'`); err != nil {
		return err
	}
	for pass := 0; pass < 100; pass++ {
		if _, err := services.RunPushDeliveryBatch(ctx, db, client); err != nil {
			return err
		}
		var pending int64
		if err := db.QueryRow(ctx, `SELECT count(*) FROM notification_push_deliveries WHERE status IN('pending','retry','sending')`).Scan(&pending); err != nil {
			return err
		}
		if pending == 0 {
			break
		}
		if pass == 99 {
			return fmt.Errorf("mock queue did not drain within 100 batches")
		}
	}
	var submitted, failed, duplicates int64
	if err := db.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='sent'),count(*) FILTER(WHERE status='failed'),count(*)-count(DISTINCT(notification_id,push_token_id)) FROM notification_push_deliveries`).Scan(&submitted, &failed, &duplicates); err != nil {
		return err
	}
	passed := submitted == total && failed == 0 && duplicates == 0
	if err := json.NewEncoder(out).Encode(map[string]any{"native_queue_recovery_passed": passed, "queued": total, "submitted_to_mock": submitted, "duplicate_job_rows": duplicates, "failed": failed, "retry_jobs_after_interruption": retry, "mock_transport_requests": transport.requests.Load(), "elapsed_ms": float64(time.Since(began).Microseconds()) / 1000, "actual_worker_process_restart_tested": false, "phone_or_vendor_delivery_tested": false}); err != nil {
		return err
	}
	if !passed {
		return fmt.Errorf("native queue recovery check failed")
	}
	return nil
}
