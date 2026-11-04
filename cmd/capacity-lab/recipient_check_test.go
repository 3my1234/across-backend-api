package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"across/backend/internal/auth"
)

type recipientRoundTripFunc func(*http.Request) (*http.Response, error)

func (f recipientRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRecipientShardsAuditRequestsArePaced(t *testing.T) {
	var started []time.Time
	base := recipientRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		started = append(started, time.Now())
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})
	transport := &recipientPacedTransport{base: base, interval: 2 * time.Millisecond}
	req, _ := http.NewRequest("GET", "http://gateway:8080", nil)
	for range 3 {
		if _, err := transport.RoundTrip(req); err != nil {
			t.Fatal(err)
		}
	}
	if started[2].Sub(started[0]) < 4*time.Millisecond {
		t.Fatal("audit requests exceeded configured rate")
	}
}

func TestRecipientShardsAuditPacingRespectsContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	transport := &recipientPacedTransport{base: http.DefaultTransport, interval: time.Second, next: time.Now().Add(time.Second)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	start := time.Now()
	if _, err := transport.RoundTrip(req); err == nil {
		t.Fatal("canceled audit request passed")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("canceled audit waited for pacing deadline")
	}
}

func TestRecipientShardsCheckEveryMessageBeyondFormerGlobalBound(t *testing.T) {
	const count = 10001
	records := make([]recipientRecord, count)
	for i := range records {
		records[i] = recipientRecord{fmt.Sprint(i), fmt.Sprint("body-", i), "conversation", "00000000-0000-0000-0000-000000000001", "buyer"}
	}
	var pages atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || r.URL.Query().Get("limit") != "100" {
			t.Error("recipient authorization/pagination missing")
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
		end := min(offset+100, count)
		items := make([]map[string]string, 0, end-offset)
		for _, row := range records[offset:end] {
			items = append(items, map[string]string{"id": row.id, "body": row.body})
		}
		next := ""
		if end < count {
			next = strconv.Itoa(end)
		}
		pages.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"items": items, "next_cursor": next})
	}))
	defer server.Close()
	index := 0
	next := func() (recipientRecord, bool, error) {
		if index == count {
			return recipientRecord{}, false, nil
		}
		row := records[index]
		index++
		return row, true, nil
	}
	var out bytes.Buffer
	if err := verifyRecipientStream(context.Background(), next, server.Client(), server.URL, &out); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["visible_to_intended_peer"] != float64(count) || result["recipient_shards"] != float64(2) || result["max_messages_per_shard"] != float64(10000) || pages.Load() <= 100 {
		t.Fatalf("incomplete sharded verification: %s", out.String())
	}
}

func TestRecipientShardsBindEachRecipientsOwnIdentity(t *testing.T) {
	rows := []recipientRecord{{"one", "first", "conv-a", "buyer-a", "buyer"}, {"two", "second", "conv-b", "provider-b", "provider"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		row := rows[0]
		if strings.Contains(r.URL.Path, "providers/me") {
			row = rows[1]
		}
		claims, err := auth.Verify(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), secret)
		if err != nil || claims.UserID != row.user {
			w.WriteHeader(403)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"items": []map[string]string{{"id": row.id, "body": row.body}}, "next_cursor": ""})
	}))
	defer server.Close()
	index := 0
	next := func() (recipientRecord, bool, error) {
		if index == len(rows) {
			return recipientRecord{}, false, nil
		}
		row := rows[index]
		index++
		return row, true, nil
	}
	if err := verifyRecipientStream(context.Background(), next, server.Client(), server.URL, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}

func TestRecipientShardsRejectMissingChangedAndHTTPFailedMessages(t *testing.T) {
	for _, mode := range []string{"missing", "changed", "http"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "http" {
					w.WriteHeader(503)
					return
				}
				body := "expected"
				if mode == "changed" {
					body = "wrong"
				}
				id := "one"
				if mode == "missing" {
					id = "another"
				}
				json.NewEncoder(w).Encode(map[string]any{"items": []map[string]string{{"id": id, "body": body}}, "next_cursor": ""})
			}))
			defer server.Close()
			emitted := false
			next := func() (recipientRecord, bool, error) {
				if emitted {
					return recipientRecord{}, false, nil
				}
				emitted = true
				return recipientRecord{"one", "expected", "conversation", "00000000-0000-0000-0000-000000000001", "buyer"}, true, nil
			}
			if err := verifyRecipientStream(context.Background(), next, server.Client(), server.URL, &bytes.Buffer{}); err == nil {
				t.Fatal("incorrect recipient result passed")
			}
		})
	}
}
