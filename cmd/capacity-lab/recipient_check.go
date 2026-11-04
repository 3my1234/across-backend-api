package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"across/backend/internal/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

type recipientRecord struct{ id, body, conversation, user, role string }
type recipientShard struct {
	user, conversation, role string
	expected                 map[string]string
}

// Audit traffic is not user load. Keep it below the unchanged600/minute
// API limit instead of making a large successful run fail its own audit.
type recipientPacedTransport struct {
	base     http.RoundTripper
	interval time.Duration
	mu       sync.Mutex
	next     time.Time
}

func (p *recipientPacedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	p.mu.Lock()
	start := time.Now()
	if p.next.After(start) {
		start = p.next
	}
	p.next = start.Add(p.interval)
	p.mu.Unlock()
	if wait := time.Until(start); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-timer.C:
		}
	}
	return p.base.RoundTrip(r)
}

func (p *recipientPacedTransport) CloseIdleConnections() {
	if closer, ok := p.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// Every generated marketplace message is checked. The former10,000-message
// global admission bound is now the in-memory shard bound, not a sample size.
// A1,000,000-row total bound and the unchanged12-minute context remain fail-closed.
func verifyRecipientVisibility(ctx context.Context, db *pgxpool.Pool, prefix string, out io.Writer) error {
	rows, err := db.Query(ctx, `SELECT m.id::text,m.body,a.conversation::text,
 CASE WHEN m.sender_type='buyer' THEN a.provider_user ELSE a.buyer END::text,
 CASE WHEN m.sender_type='buyer' THEN 'provider' ELSE 'buyer' END
 FROM provider_conversation_messages m JOIN capacity_lab_actors a ON a.conversation=m.conversation_id
 WHERE m.body LIKE $1 ORDER BY 3,4,m.id`, prefix)
	if err != nil {
		return err
	}
	defer rows.Close()
	next := func() (recipientRecord, bool, error) {
		var r recipientRecord
		if !rows.Next() {
			return r, false, rows.Err()
		}
		err := rows.Scan(&r.id, &r.body, &r.conversation, &r.user, &r.role)
		return r, err == nil, err
	}
	c := client()
	c.Transport = &recipientPacedTransport{base: c.Transport, interval: 125 * time.Millisecond}
	defer c.CloseIdleConnections()
	return verifyRecipientStream(ctx, next, c, origin, out)
}

func verifyRecipientStream(ctx context.Context, next func() (recipientRecord, bool, error), c *http.Client, baseOrigin string, out io.Writer) error {
	var current *recipientShard
	var previous recipientRecord
	hasPrevious := false
	total, observed, pages, shards := 0, 0, 0, 0
	flush := func() error {
		if current == nil || len(current.expected) == 0 {
			return nil
		}
		count, read, err := readRecipientShard(ctx, c, baseOrigin, current)
		if err != nil {
			return err
		}
		observed += count
		pages += read
		shards++
		current.expected = map[string]string{}
		return nil
	}
	for {
		row, ok, err := next()
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		if total >= 1000000 {
			return fmt.Errorf("recipient inventory exceeds1,000,000-message reviewed bound")
		}
		// SQL orders IDs within recipient/conversation; this also catches a
		// duplicate exactly at a shard boundary without retaining a global map.
		if hasPrevious && previous.user == row.user && previous.conversation == row.conversation && previous.id == row.id {
			return fmt.Errorf("duplicate recipient inventory ID across shard boundary")
		}
		previous, hasPrevious = row, true
		if current == nil || current.user != row.user || current.conversation != row.conversation || len(current.expected) >= 10000 {
			if err = flush(); err != nil {
				return err
			}
			current = &recipientShard{row.user, row.conversation, row.role, map[string]string{}}
		}
		if _, duplicate := current.expected[row.id]; duplicate {
			return fmt.Errorf("duplicate recipient inventory ID")
		}
		current.expected[row.id] = row.body
		total++
	}
	if total == 0 {
		return fmt.Errorf("recipient inventory is empty")
	}
	if err := flush(); err != nil {
		return err
	}
	if observed != total {
		return fmt.Errorf("recipient visibility count mismatch")
	}
	return json.NewEncoder(out).Encode(map[string]any{"post_run_recipient_visibility_passed": true, "expected_marketplace_messages": total, "visible_to_intended_peer": observed, "history_pages": pages, "recipient_shards": shards, "max_messages_per_shard": 10000, "in_workload_latency_check": false, "device_or_push_delivery_tested": false})
}

func readRecipientShard(ctx context.Context, c *http.Client, baseOrigin string, group *recipientShard) (int, int, error) {
	token, _, err := auth.Sign(group.user, secret, time.Hour)
	if err != nil {
		return 0, 0, err
	}
	base := "/api/v1/marketplace/conversations/"
	if group.role == "provider" {
		base = "/api/v1/providers/me/conversations/"
	}
	cursor := ""
	seen := map[string]bool{}
	pages := 0
	for page := 0; page < 1000; page++ {
		req, err := http.NewRequestWithContext(ctx, "GET", baseOrigin+base+group.conversation+"/messages?limit=100&mark_read=false&cursor="+url.QueryEscape(cursor), nil)
		if err != nil {
			return 0, pages, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := c.Do(req)
		if err != nil {
			return 0, pages, fmt.Errorf("recipient history transport failed")
		}
		var data struct {
			Items []struct {
				ID   string `json:"id"`
				Body string `json:"body"`
			} `json:"items"`
			Next string `json:"next_cursor"`
		}
		err = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&data)
		res.Body.Close()
		pages++
		if res.StatusCode != 200 || err != nil {
			return 0, pages, fmt.Errorf("recipient history status=%d or invalid JSON", res.StatusCode)
		}
		for _, message := range data.Items {
			if body, ok := group.expected[message.ID]; ok {
				if message.Body != body {
					return 0, pages, fmt.Errorf("recipient message body differs from stored acknowledgement")
				}
				seen[message.ID] = true
			}
		}
		if len(seen) == len(group.expected) {
			return len(seen), pages, nil
		}
		if data.Next == "" || data.Next == cursor {
			return 0, pages, fmt.Errorf("recipient history is missing confirmed messages")
		}
		cursor = data.Next
	}
	return 0, pages, fmt.Errorf("recipient paging bound reached before completeness")
}
