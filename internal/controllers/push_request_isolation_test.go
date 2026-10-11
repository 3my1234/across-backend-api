package controllers

import (
	"across/backend/internal/services"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPush400IsolationAndRecovery(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE notifications(id uuid PRIMARY KEY,user_id uuid,order_id uuid,title text,body text,data jsonb,is_read boolean DEFAULT false,created_at timestamptz DEFAULT now());
 CREATE TABLE user_push_tokens(id uuid PRIMARY KEY,user_id uuid,expo_push_token text,disabled_at timestamptz,sound_enabled boolean DEFAULT true,notification_channel text DEFAULT 'orders',expo_project_id uuid);
 CREATE TABLE notification_push_deliveries(notification_id uuid,push_token_id uuid,status text DEFAULT 'pending',attempts int DEFAULT 0,next_attempt_at timestamptz DEFAULT now(),expo_ticket_id text,last_error text DEFAULT '',sent_at timestamptz,receipt_checked_at timestamptz,updated_at timestamptz DEFAULT now(),PRIMARY KEY(notification_id,push_token_id));`)
	if err != nil {
		t.Fatal(err)
	}
	user, note := uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO notifications(id,user_id,title,body,data) VALUES($1,$2,'Support reply','A real reply','{}')`, note, user); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"old-a", "new-b", "bad", "old-c"} {
		id := uuid.NewString()
		if _, err = db.Exec(ctx, `INSERT INTO user_push_tokens(id,user_id,expo_push_token) VALUES($1,$2,$3)`, id, user, "ExpoPushToken["+name+"]"); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, `INSERT INTO notification_push_deliveries(notification_id,push_token_id,status,attempts,last_error) VALUES($1,$2,'failed',1,'expo push HTTP 400')`, note, id); err != nil {
			t.Fatal(err)
		}
	}
	// Run the actual one-time recovery migration, not a manually reset queue.
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "070_push_project_isolation.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	// Simulate an old worker reclaiming and rejecting jobs after the migration.
	// The new worker must recover these without rerunning a one-time migration.
	if _, err = db.Exec(ctx, `UPDATE notification_push_deliveries SET status='failed',attempts=1,last_error='expo push HTTP 400'`); err != nil {
		t.Fatal(err)
	}
	accepted := map[string]int{}
	rejections := 0
	client := &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		var messages []map[string]any
		if err := json.NewDecoder(req.Body).Decode(&messages); err != nil {
			t.Fatal(err)
		}
		projects := map[string]bool{}
		bad := false
		for _, m := range messages {
			token := m["to"].(string)
			project := "old"
			if strings.Contains(token, "new-") {
				project = "new"
			}
			projects[project] = true
			bad = bad || strings.Contains(token, "[bad]")
		}
		code := ""
		if len(projects) > 1 {
			code = "PUSH_TOO_MANY_EXPERIENCE_IDS"
		} else if bad {
			code = "VALIDATION_ERROR"
		}
		if code != "" {
			rejections++
			return &http.Response{StatusCode: 400, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"errors":[{"code":"` + code + `","message":"secret ExpoPushToken[bad] details"}]}`))}, nil
		}
		tickets := []map[string]string{}
		for _, m := range messages {
			token := m["to"].(string)
			accepted[token]++
			tickets = append(tickets, map[string]string{"status": "ok", "id": uuid.NewString()})
		}
		body, _ := json.Marshal(map[string]any{"data": tickets})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	count, err := services.RunPushDeliveryBatch(ctx, db, client)
	if err != nil || count != 3 || len(accepted) != 3 || rejections == 0 {
		t.Fatalf("count=%d accepted=%v rejections=%d err=%v", count, accepted, rejections, err)
	}
	for token, count := range accepted {
		if count != 1 {
			t.Fatalf("duplicate accepted delivery %s: %d", token, count)
		}
	}
	var status, lastError string
	if err = db.QueryRow(ctx, `SELECT status,last_error FROM notification_push_deliveries d JOIN user_push_tokens p ON p.id=d.push_token_id WHERE p.expo_push_token='ExpoPushToken[bad]'`).Scan(&status, &lastError); err != nil || status != "failed" || !strings.Contains(lastError, "VALIDATION_ERROR") || strings.Contains(lastError, "ExpoPushToken") {
		t.Fatalf("unsafe/missing isolated error: %s %s %v", status, lastError, err)
	}
	// Known project metadata groups clean tokens before reaching Expo.
	if _, err = db.Exec(ctx, `UPDATE user_push_tokens SET disabled_at=now() WHERE expo_push_token='ExpoPushToken[bad]'; UPDATE notification_push_deliveries SET status='pending',attempts=0,last_error='';`); err != nil {
		t.Fatal(err)
	}
	project := uuid.NewString()
	if _, err = db.Exec(ctx, `UPDATE user_push_tokens SET expo_project_id=$1 WHERE expo_push_token='ExpoPushToken[new-b]'`, project); err != nil {
		t.Fatal(err)
	}
	accepted = map[string]int{}
	rejections = 0
	count, err = services.RunPushDeliveryBatch(ctx, db, client)
	if err != nil || count != 3 || rejections != 0 {
		t.Fatalf("metadata grouping failed: count=%d rejected=%d err=%v", count, rejections, err)
	}
}
