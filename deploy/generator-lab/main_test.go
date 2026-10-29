package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSyntheticTargetBranches(t *testing.T) {
	s := newTarget()
	a := population()[0]
	peer := population()[2]
	call := func(method, path, token, body string) (int, map[string]any) {
		r := httptest.NewRequest(method, "http://gateway:8080/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		var d map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &d)
		return w.Code, d
	}
	path := "/marketplace/conversations/" + a.Conversation + "/messages"
	if c, _ := call("GET", path, peer.Token, ""); c != 404 {
		t.Fatal(c)
	}
	if len(s.first) != 0 {
		t.Fatal("outsider preflight incorrectly starts measured window")
	}
	if c, d := call("GET", path, a.Token, ""); c != 200 || len(d["items"].([]any)) != 40 {
		t.Fatal(c, d)
	}
	body := `{"message":"synthetic write","client_message_id":"replay-key"}`
	_, first := call("POST", path, a.Token, body)
	_, again := call("POST", path, a.Token, body)
	if first["id"] != again["id"] || len(s.messages[a.Conversation]) != 41 {
		t.Fatal("replay did not preserve same stored message")
	}
	if c, d := call("GET", path, a.Token, ""); c != 200 || len(d["items"].([]any)) != 40 {
		t.Fatal("page bound")
	}
	if c, _ := call("GET", "/auth/session", "synthetic:expired", ""); c != 401 {
		t.Fatal(c)
	}
}

func TestCompletePopulationAndPayloadEnvelope(t *testing.T) {
	population := population()
	if len(population) != 500 {
		t.Fatal("expected500 actors")
	}
	seen := map[string]bool{}
	for _, a := range population {
		if seen[a.ID] {
			t.Fatal("duplicate identity")
		}
		seen[a.ID] = true
	}
	s := newTarget()
	a := population[1]
	call := func(method, path, body string) (int, map[string]any) {
		r := httptest.NewRequest(method, "http://gateway:8080/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+a.Token)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		var d map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &d)
		return w.Code, d
	}
	if c, _ := call("PUT", "/providers/me/driver-availability", `{}`); c != 200 {
		t.Fatal(c)
	}
	if _, d := call("GET", "/providers/me/driver-availability", ""); d["online"] != true || d["listing_id"] != a.Listing {
		t.Fatal(d)
	}
	if c, _ := call("DELETE", "/providers/me/driver-availability", ""); c != 204 {
		t.Fatal(c)
	}
	if _, d := call("GET", "/providers/me/driver-availability", ""); d["online"] != false {
		t.Fatal(d)
	}
	if _, d := call("GET", "/products", ""); len(d["items"].([]any)) != 25 {
		t.Fatal("payload list size")
	}
	if _, d := call("GET", "/marketplace/drivers/nearby?kind=courier", ""); d["items"].([]any)[0].(map[string]any)["transport_kind"] != "courier" {
		t.Fatal(d)
	}
}

func TestReviewedPreparationProfiles(t *testing.T) {
	for _, profile := range []string{"stage", "sustained"} {
		t.Run(profile, func(t *testing.T) {
			t.Setenv("GENERATOR_SESSION_PROFILE", profile)
			path := t.TempDir()
			prepare(path)
			raw, err := os.ReadFile(filepath.Join(path, "approval.json"))
			if err != nil {
				t.Fatal(err)
			}
			var approval map[string]any
			if err = json.Unmarshal(raw, &approval); err != nil {
				t.Fatal(err)
			}
			want := float64(180)
			if profile == "sustained" {
				want = 1800
			}
			if approval["profile"] != profile || approval["max_duration_seconds"] != want || approval["max_sessions"] != float64(500) {
				t.Fatal(approval)
			}
		})
	}
	t.Setenv("GENERATOR_SESSION_PROFILE", "unreviewed")
	defer func() {
		if recover() == nil {
			t.Fatal("unreviewed profile accepted")
		}
	}()
	generatorProfile()
}

func TestConcurrentPeerWritesHaveDistinctMessageIDs(t *testing.T) {
	s := newTarget()
	actors := population()
	a := actors[0]
	peer := actors[1]
	outputs := make(chan map[string]any, 2)
	s.mu.Lock()
	for i, current := range []actor{a, peer} {
		go func(i int, a actor) {
			payload := fmt.Sprintf(`{"message":"concurrent synthetic","client_message_id":"key-%d"}`, i)
			r := httptest.NewRequest("POST", "http://gateway:8080/api/v1/marketplace/conversations/"+a.Conversation+"/messages", strings.NewReader(payload))
			r.Header.Set("Authorization", "Bearer "+a.Token)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			var body map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			outputs <- body
		}(i, current)
	}
	deadline := time.Now().Add(time.Second)
	for s.requests.Load() != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	observed := s.requests.Load()
	s.mu.Unlock()
	if observed != 2 {
		t.Fatal("both synthetic HTTP requests did not reach the response barrier")
	}
	first, second := <-outputs, <-outputs
	if first["id"] == nil || second["id"] == nil || first["id"] == second["id"] {
		t.Fatal("distinct committed synthetic messages shared an ID", first, second)
	}
}
