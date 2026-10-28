// Synthetic target for measuring the unchanged k6 generator, never application capacity.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type actor struct {
	ID           string `json:"id"`
	PeerID       string `json:"peer_id"`
	Role         string `json:"role"`
	Email        string `json:"email"`
	Token        string `json:"token"`
	Conversation string `json:"conversation"`
	Room         string `json:"room"`
	Product      string `json:"product"`
	Listing      string `json:"listing"`
	Ticket       string `json:"ticket"`
	ProviderID   string `json:"provider_id"`
	Slot         int    `json:"slot"`
}
type message struct {
	ID         string    `json:"id"`
	Body       string    `json:"body"`
	SenderType string    `json:"sender_type"`
	CreatedAt  time.Time `json:"created_at"`
}

func uuid(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
func population() []actor {
	out := make([]actor, 500)
	for i := range out {
		pair := i / 2
		role := "buyer"
		if i%2 == 1 {
			role = "provider"
		}
		id := uuid(i + 1)
		out[i] = actor{ID: id, PeerID: uuid((i ^ 1) + 1), Role: role, Email: fmt.Sprintf("generator-%d@example.invalid", i), Token: "synthetic:" + id, Conversation: uuid(10000 + pair), Room: uuid(20000 + pair), Product: uuid(30000 + pair), Listing: uuid(40000 + pair), Ticket: uuid(50000 + pair), ProviderID: uuid(60000 + pair), Slot: pair + 1}
	}
	return out
}
func writeJSON(path string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	if err = os.WriteFile(path, b, 0600); err != nil {
		panic(err)
	}
}
func prepare(dir string) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		panic(err)
	}
	base := map[string]any{"schema": 1, "marker": "disposable-compose-only", "origin": "http://gateway:8080", "run_id": uuid(999999), "expires_at": time.Now().Add(time.Hour).UTC(), "password": "synthetic-generator-only", "expired_token": "synthetic:expired", "admin_token": "synthetic:admin", "expanded_journeys": true}
	writeJSON(filepath.Join(dir, "run.json"), base)
	base["sessions"] = population()
	writeJSON(filepath.Join(dir, "fixtures.json"), base)
	writeJSON(filepath.Join(dir, "approval.json"), map[string]any{"authorized": true, "environment": "isolated-staging", "profile": "stage", "max_sessions": 500, "max_duration_seconds": 180, "spending_cap_usd": 0, "approved_at": time.Now().UTC(), "source": "User-authorized independent generator qualification; synthetic HTTP only, no application/database/production"})
}

type target struct {
	mu            sync.Mutex
	actors        map[string]actor
	emails        map[string]actor
	messages      map[string][]message
	replay        map[string]message
	presence      map[string]bool
	first         map[string]time.Time
	begun         time.Time
	requests      atomic.Uint64
	responseBytes atomic.Uint64
}

func newTarget() *target {
	s := &target{actors: map[string]actor{}, emails: map[string]actor{}, messages: map[string][]message{}, replay: map[string]message{}, presence: map[string]bool{}, first: map[string]time.Time{}}
	for _, a := range population() {
		s.actors[a.Token] = a
		s.emails[a.Email] = a
		if _, ok := s.messages[a.Conversation]; !ok {
			for j := 0; j < 40; j++ {
				s.messages[a.Conversation] = append(s.messages[a.Conversation], message{uuid(70000 + a.Slot*100 + j), strings.Repeat("synthetic historical content ", 12), "buyer", time.Now().Add(time.Duration(j-100) * time.Second).UTC()})
			}
		}
	}
	return s
}
func (s *target) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/generator-stats" {
		s.mu.Lock()
		defer s.mu.Unlock()
		last := s.begun
		for _, v := range s.first {
			if v.After(last) {
				last = v
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"requests": s.requests.Load(), "response_bytes": s.responseBytes.Load(), "active_identities": len(s.first), "first_activity_spread_seconds": last.Sub(s.begun).Seconds(), "synthetic_only": true, "baseline_messages_per_page": 40, "historical_body_bytes": len(strings.Repeat("synthetic historical content ", 12))})
		return
	}
	seq := s.requests.Add(1)
	w.Header().Set("X-Lab-Backend", fmt.Sprintf("api%d", seq%2+1))
	w.Header().Set("X-Atl-Capacity-Lab", "disposable-compose-only")
	w.Header().Set("Content-Type", "application/json")
	// Five milliseconds bounds the response floor; this target has no SQL or external I/O.
	time.Sleep(5 * time.Millisecond)
	code, body := s.response(r)
	raw, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	s.responseBytes.Add(uint64(len(raw)))
	w.WriteHeader(code)
	if code != 204 {
		_, _ = w.Write(raw)
	}
}
func (s *target) response(r *http.Request) (int, any) {
	p := strings.TrimPrefix(r.URL.Path, "/api/v1")
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	var input map[string]any
	if r.Body != nil {
		defer r.Body.Close()
		_ = json.NewDecoder(http.MaxBytesReader(nil, r.Body, 65536)).Decode(&input)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, known := s.actors[token]
	if p == "/ready" {
		return 200, map[string]any{"ok": true}
	}
	if p == "/auth/login" {
		email, _ := input["email"].(string)
		user, ok := s.emails[email]
		if !ok {
			return 401, map[string]any{"error": "synthetic identity missing"}
		}
		return 200, map[string]any{"user_id": user.ID, "access_token": user.Token}
	}
	if p == "/auth/session" {
		if !known {
			return 401, map[string]any{"error": "invalid synthetic token"}
		}
		return 200, map[string]any{"user_id": a.ID}
	}
	outsider := strings.Contains(p, "/conversations/") && !strings.Contains(p, a.Conversation)
	if known && !outsider {
		if _, ok := s.first[a.ID]; !ok {
			now := time.Now()
			s.first[a.ID] = now
			if s.begun.IsZero() {
				s.begun = now
			}
		}
	}
	if strings.HasPrefix(p, "/marketplace/conversations/") || strings.HasPrefix(p, "/providers/me/conversations/") {
		parts := strings.Split(p, "/")
		conv := parts[len(parts)-2]
		if !known || conv != a.Conversation {
			return 404, map[string]any{"error": "not found"}
		}
		if r.Method == "GET" {
			all := s.messages[conv]
			items := append([]message(nil), all...)
			sort.SliceStable(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
			if len(items) > 40 {
				items = items[len(items)-40:]
			}
			return 200, map[string]any{"items": items}
		}
		key, _ := input["client_message_id"].(string)
		if m, ok := s.replay[conv+key]; ok {
			return 201, m
		}
		text, _ := input["message"].(string)
		m := message{uuid(1000000 + int(s.requests.Load())), text, a.Role, time.Now().UTC()}
		s.messages[conv] = append(s.messages[conv], m)
		s.replay[conv+key] = m
		return 201, m
	}
	if p == "/providers/me/driver-availability" {
		if !known {
			return 401, map[string]any{"error": "no actor"}
		}
		if r.Method == "DELETE" {
			s.presence[a.ID] = false
			return 204, nil
		}
		if r.Method == "PUT" {
			s.presence[a.ID] = true
		}
		return 200, map[string]any{"online": s.presence[a.ID], "listing_id": a.Listing}
	}
	if p == "/marketplace/drivers/nearby" {
		kind := r.URL.Query().Get("kind")
		kinds := []string{kind}
		if kind == "all" {
			kinds = []string{"passenger", "courier"}
		}
		items := []any{}
		for i, k := range kinds {
			items = append(items, map[string]any{"id": uuid(900000 + i), "nearby_driver": true, "contact_available": true, "distance_km": float64(i + 1), "transport_kind": k, "fresh_for_seconds": 20, "title": "Synthetic driver", "description": strings.Repeat("Synthetic ", 30)})
		}
		return 200, map[string]any{"items": items, "approximate_distances": true}
	}
	if p == "/messaging/receipts" || strings.HasSuffix(p, "/offers") {
		return 204, nil
	}
	if strings.HasSuffix(p, "/messages") {
		if r.Method == "POST" {
			return 201, map[string]any{"id": uuid(2000000 + int(s.requests.Load()))}
		}
		items := s.messages[a.Conversation]
		if len(items) > 40 {
			items = items[len(items)-40:]
		}
		return 200, map[string]any{"items": items}
	}
	if p == "/products" || p == "/marketplace/listings" || p == "/chat/inbox" {
		items := []any{}
		for i := 0; i < 25; i++ {
			items = append(items, map[string]any{"id": uuid(800000 + i), "title": "Synthetic listing", "description": strings.Repeat("synthetic description ", 24), "image_url": "https://images.example.invalid/synthetic.jpg", "price": 1000})
		}
		return 200, map[string]any{"items": items}
	}
	return 200, map[string]any{"id": a.ID, "title": "Synthetic response", "description": strings.Repeat("Synthetic description ", 30)}
}
func main() {
	if os.Getenv("GENERATOR_LAB") != "synthetic-only" {
		log.Fatal("isolated synthetic generator target only")
	}
	if len(os.Args) == 3 && os.Args[1] == "prepare" {
		prepare(os.Args[2])
		return
	}
	log.Fatal(http.ListenAndServe(":8080", newTarget()))
}
