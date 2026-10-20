package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"across/backend/internal/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Post-run completeness check, distinct from in-workload delivery latency.
// Every generated marketplace message must be readable by its intended peer.
// This does not assert device display, phone push or Town Hall fanout.
func verifyRecipientVisibility(ctx context.Context, db *pgxpool.Pool, prefix string, out io.Writer) error {
	type recipient struct {
		user, conversation, role string
		expected                 map[string]string
	}
	groups := map[string]*recipient{}
	rows, err := db.Query(ctx, `SELECT m.id::text,m.body,a.conversation::text,
 CASE WHEN m.sender_type='buyer' THEN a.provider_user ELSE a.buyer END::text,
 CASE WHEN m.sender_type='buyer' THEN 'provider' ELSE 'buyer' END
 FROM provider_conversation_messages m JOIN capacity_lab_actors a ON a.conversation=m.conversation_id WHERE m.body LIKE $1`, prefix)
	if err != nil {
		return err
	}
	total := 0
	for rows.Next() {
		var id, body, conversation, user, role string
		if err = rows.Scan(&id, &body, &conversation, &user, &role); err != nil {
			rows.Close()
			return err
		}
		key := user + ":" + conversation
		if groups[key] == nil {
			groups[key] = &recipient{user, conversation, role, map[string]string{}}
		}
		groups[key].expected[id] = body
		total++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if total == 0 || total > 10000 {
		return fmt.Errorf("post-run recipient check requires 1..10000 messages; larger runs need reviewed sharded verification")
	}
	c := client()
	defer c.CloseIdleConnections()
	observed, pages := 0, 0
	for _, group := range groups {
		token, _, err := auth.Sign(group.user, secret, time.Hour)
		if err != nil {
			return err
		}
		base := "/api/v1/marketplace/conversations/"
		if group.role == "provider" {
			base = "/api/v1/providers/me/conversations/"
		}
		cursor := ""
		seen := map[string]bool{}
		for page := 0; page < 1000; page++ {
			req, err := http.NewRequestWithContext(ctx, "GET", origin+base+group.conversation+"/messages?limit=100&mark_read=false&cursor="+url.QueryEscape(cursor), nil)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+token)
			res, err := c.Do(req)
			if err != nil {
				return fmt.Errorf("recipient history transport failed")
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
				return fmt.Errorf("recipient history status=%d or invalid JSON", res.StatusCode)
			}
			for _, message := range data.Items {
				if body, ok := group.expected[message.ID]; ok {
					if message.Body != body {
						return fmt.Errorf("recipient message body differs from stored acknowledgement")
					}
					if !seen[message.ID] {
						seen[message.ID] = true
						observed++
					}
				}
			}
			if len(seen) == len(group.expected) {
				break
			}
			if data.Next == "" || data.Next == cursor {
				return fmt.Errorf("recipient history is missing confirmed messages")
			}
			cursor = data.Next
		}
		if len(seen) != len(group.expected) {
			return fmt.Errorf("recipient paging bound reached before completeness")
		}
	}
	return json.NewEncoder(out).Encode(map[string]any{"post_run_recipient_visibility_passed": observed == total, "expected_marketplace_messages": total, "visible_to_intended_peer": observed, "history_pages": pages, "in_workload_latency_check": false, "device_or_push_delivery_tested": false})
}
