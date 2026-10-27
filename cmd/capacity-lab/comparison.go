package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"across/backend/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const comparisonAdmin = "00000000-0000-4000-8000-000000000099"

func prepareComparison(ctx context.Context, db *pgxpool.Pool) error {
	if err := requireSessionLab(ctx, db); err != nil {
		return err
	}
	_, err := db.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
 INSERT INTO admins(id,email,full_name,password_hash,role) VALUES('00000000-0000-4000-8000-000000000099','capacity-observer@example.invalid','Synthetic observer','no-password-login','super_admin');`)
	return err
}

func operationWriteKey(op operation) string {
	var body struct {
		Message string `json:"message"`
		Terms   string `json:"terms"`
	}
	if json.Unmarshal([]byte(op.Body), &body) != nil {
		return ""
	}
	if body.Message != "" {
		return body.Message
	}
	return body.Terms
}

// Capture only bounded private pool counters, never the synthetic bearer token.
func comparisonPools(c *http.Client) (map[string]any, error) {
	token, _, err := auth.Sign(comparisonAdmin, secret, time.Hour)
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	for _, backend := range []string{"api1", "api2"} {
		req, _ := http.NewRequest("GET", "http://"+backend+":8080/api/v1/admin/ops/runtime", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, e := c.Do(req)
		if e != nil {
			return nil, e
		}
		var data struct {
			Pool map[string]any `json:"database_pool"`
		}
		e = json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&data)
		res.Body.Close()
		if e != nil || res.StatusCode != 200 || data.Pool == nil {
			return nil, fmt.Errorf("private synthetic pool snapshot failed on %s", backend)
		}
		result[backend] = data.Pool
	}
	return result, nil
}

func comparisonStatements(ctx context.Context, db *pgxpool.Pool) (json.RawMessage, error) {
	var raw json.RawMessage
	err := db.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(s)),'[]'::jsonb) FROM (
 SELECT queryid::text,calls,total_exec_time,mean_exec_time,max_exec_time,stddev_exec_time,rows,shared_blks_hit,shared_blks_read,temp_blks_written,
 CASE WHEN query LIKE 'SELECT msg.id::text%' THEN 'business_history'
 WHEN query LIKE '%history_revision%' THEN 'history_revision'
 WHEN query LIKE '%support_messages%' THEN 'support'
 WHEN query LIKE '%chat_messages%' THEN 'shared_chat'
 WHEN query LIKE '%provider_subscriptions%' OR query LIKE '%provider_current_benefits%' THEN 'entitlements'
 WHEN query LIKE '%provider_conversations%' THEN 'business_thread'
 ELSE 'other' END AS query_group
 FROM pg_stat_statements WHERE dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) ORDER BY total_exec_time DESC LIMIT 30
 ) s`).Scan(&raw)
	return raw, err
}

func reconcileAcknowledged(ctx context.Context, db *pgxpool.Pool, prefix string, ack map[string]int) error {
	if _, bad := ack[""]; bad {
		return fmt.Errorf("acknowledged write missing its synthetic key")
	}
	rows, err := db.Query(ctx, `SELECT body FROM provider_conversation_messages WHERE body LIKE $1
 UNION ALL SELECT body FROM chat_messages WHERE body LIKE $1 OR body LIKE $2
 UNION ALL SELECT message FROM support_messages WHERE message LIKE $1
	UNION ALL SELECT h.description FROM chat_rooms r JOIN town_hall_requests h ON h.room_id=r.id WHERE r.title LIKE $1`, prefix+"%", "Offer:%"+prefix+"%")
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
		pos := strings.Index(body, prefix)
		if pos < 0 || len(body) < pos+len(prefix)+36 {
			return fmt.Errorf("invalid stored synthetic key")
		}
		key := body[pos : pos+len(prefix)+36]
		if _, err = uuid.Parse(key[len(prefix):]); err != nil {
			return fmt.Errorf("invalid stored write suffix")
		}
		stored[key]++
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(stored) != len(ack) {
		return fmt.Errorf("acknowledged key inventory differs from stored writes")
	}
	for key, count := range ack {
		if count != 1 || stored[key] != 1 {
			return fmt.Errorf("acknowledged write missing or duplicated")
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"exact_acknowledged_write_keys": len(ack), "all_acknowledged_keys_stored_once": true})
}

type comparisonMessage struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}
type comparisonPage struct {
	Items []comparisonMessage `json:"items"`
	Next  string              `json:"next_cursor"`
}

func comparisonHTTPChecks(ctx context.Context, db *pgxpool.Pool, c *http.Client, a, other actor) error {
	buyer, _, _ := auth.Sign(a.Buyer, secret, time.Hour)
	outsider, _, _ := auth.Sign(other.Buyer, secret, time.Hour)
	path := "/api/v1/marketplace/conversations/" + a.Conversation + "/messages"
	call := func(path, token, body string, want int) ([]byte, error) {
		method := "GET"
		if body != "" {
			method = "POST"
		}
		req, _ := http.NewRequest(""+method, origin+path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res, e := c.Do(req)
		if e != nil {
			return nil, e
		}
		raw, e := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		if e != nil || res.StatusCode != want {
			return nil, fmt.Errorf("comparison correctness HTTP status=%d want=%d", res.StatusCode, want)
		}
		return raw, nil
	}
	if _, err := call(path+"?mark_read=false&limit=17", outsider, "", 404); err != nil {
		return err
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(a.Conversation+":comparison-idempotency")).String()
	body, _ := json.Marshal(map[string]string{"message": "comparison-idempotency-check", "client_message_id": id})
	var saved string
	for i := 0; i < 2; i++ {
		raw, err := call(path, buyer, string(body), 201)
		if err != nil {
			return err
		}
		var out struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &out) != nil || out.ID == "" {
			return fmt.Errorf("idempotency response missing ID")
		}
		if i == 0 {
			saved = out.ID
		} else if out.ID != saved {
			return fmt.Errorf("message replay changed ID")
		}
	}
	var stored int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM provider_conversation_messages WHERE sender_user_id=$1::uuid AND client_message_id=$2::uuid`, a.Buyer, id).Scan(&stored); err != nil || stored != 1 {
		return fmt.Errorf("idempotent message did not persist exactly once")
	}
	seen := map[string]bool{}
	var previous *comparisonMessage
	for page := 0; page < 3; page++ {
		separator := "?"
		if strings.Contains(path, "?") {
			separator = "&"
		}
		raw, err := call(path+separator+"mark_read=false&limit=17", buyer, "", 200)
		if err != nil {
			return err
		}
		var out comparisonPage
		if json.Unmarshal(raw, &out) != nil || len(out.Items) != 17 {
			return fmt.Errorf("invalid comparison history page")
		}
		for i, msg := range out.Items {
			if seen[msg.ID] {
				return fmt.Errorf("pagination repeated a message")
			}
			seen[msg.ID] = true
			if i > 0 {
				older := out.Items[i-1]
				if msg.CreatedAt.Before(older.CreatedAt) || (msg.CreatedAt.Equal(older.CreatedAt) && msg.ID <= older.ID) {
					return fmt.Errorf("history order changed")
				}
			}
			if previous != nil {
				if msg.CreatedAt.After(previous.CreatedAt) || (msg.CreatedAt.Equal(previous.CreatedAt) && msg.ID >= previous.ID) {
					return fmt.Errorf("older page crossed cursor")
				}
			}
		}
		first := out.Items[0]
		previous = &first
		if out.Next == "" {
			return fmt.Errorf("older history cursor missing")
		}
		path = "/api/v1/marketplace/conversations/" + a.Conversation + "/messages?cursor=" + url.QueryEscape(out.Next)
	}
	return nil
}
