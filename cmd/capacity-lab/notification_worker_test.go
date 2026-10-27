package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestProviderJournalWithoutDatabase(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "provider-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	mock := &notificationMock{journal: file, entries: map[string]*mockEntry{}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := mock.attempt(context.Background(), "email", "same", ""); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if mock.entries["email:same"].Attempts != 8 {
		t.Fatal("lost concurrent attempt")
	}
	req, _ := http.NewRequest("POST", "https://exp.host/--/api/v2/push/send", strings.NewReader(`[{"to":"synthetic","data":{"notification_id":"fixture"}}]`))
	response, err := mock.RoundTrip(req)
	if err != nil || response.StatusCode != 200 {
		t.Fatal(err)
	}
	response.Body.Close()
	var ticket string
	for _, e := range mock.entries {
		if e.Channel == "push" {
			ticket = e.Ticket
			if e.Attempts != 1 || e.Acceptances != 1 || e.Acknowledgements != 1 {
				t.Fatal(e)
			}
		}
	}
	if err := mock.receipt(ticket); err != nil {
		t.Fatal(err)
	}
	if err := mock.receipt("unknown"); err == nil {
		t.Fatal("unknown ticket accepted")
	}
	mock.ambiguousEmail = 1
	if !mock.ambiguous(context.Background(), "email") || mock.ambiguous(context.Background(), "email") {
		t.Fatal("ambiguity budget not bounded")
	}
	bad, _ := http.NewRequest("POST", "https://example.com/real", nil)
	if _, err := mock.RoundTrip(bad); err == nil {
		t.Fatal("external target accepted")
	}
	if _, err := (notificationProxy{}).RoundTrip(bad); err == nil {
		t.Fatal("external proxy target accepted")
	}
	raw, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var event map[string]any
		if json.Unmarshal([]byte(line), &event) != nil || event["event"] == nil {
			t.Fatal("invalid journal")
		}
	}
	if mock.events != 12 {
		t.Fatalf("journal count=%d", mock.events)
	}
}
