package main

import "testing"

func TestSessionJournalAbortAndIntegrity(t *testing.T) {
	cases := []struct {
		name                                string
		offered, ack, stored                map[string]int
		counter                             int64
		completed, ackOK, journalOK, normal bool
		uncertain                           int
	}{
		{"complete", map[string]int{"a": 1}, map[string]int{"a": 1}, map[string]int{"a": 1}, 1, true, true, true, true, 0},
		{"abort commits unconfirmed offer", map[string]int{"a": 1, "b": 1}, map[string]int{"a": 1}, map[string]int{"a": 1, "b": 1}, 1, false, true, true, false, 1},
		{"missing acknowledged write", map[string]int{"a": 1}, map[string]int{"a": 1}, map[string]int{}, 1, false, false, true, false, 0},
		{"duplicated stored write", map[string]int{"a": 1}, map[string]int{"a": 1}, map[string]int{"a": 2}, 1, true, false, false, false, 0},
		{"unknown stored write", map[string]int{"a": 1}, map[string]int{"a": 1}, map[string]int{"a": 1, "unknown": 1}, 1, false, true, false, false, 1},
		{"ack counter interrupted", map[string]int{"a": 1}, map[string]int{"a": 1}, map[string]int{"a": 1}, 0, false, true, false, false, 0},
		{"duplicate offer", map[string]int{"a": 2}, map[string]int{"a": 1}, map[string]int{"a": 1}, 1, true, true, false, false, 0},
		{"complete cannot ignore unconfirmed commit", map[string]int{"a": 1, "b": 1}, map[string]int{"a": 1}, map[string]int{"a": 1, "b": 1}, 1, true, true, true, false, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := reconcileSessionJournal(c.offered, c.ack, c.stored, c.counter, c.completed)
			if got.AcknowledgementsPreserved != c.ackOK || got.JournalConsistent != c.journalOK || got.NormalInventoryMatched != c.normal || got.UnconfirmedCommitted != c.uncertain {
				t.Fatalf("unexpected accounting: %+v", got)
			}
		})
	}
}

func TestSessionJournalParserIsRunScoped(t *testing.T) {
	run := "00000000-0000-4000-8000-000000000001"
	key := "sessions-" + run + ":123:00000000-0000-4000-8000-000000000002:00000000-0000-4000-8000-000000000003"
	offered, ack := parseSessionJournal("console OFFER:"+key+"\nconsole ACK:"+key+"\nACK:sessions-unrelated:123", run)
	if offered[key] != 1 || ack[key] != 1 || len(offered) != 1 || len(ack) != 1 {
		t.Fatal("run journal was not isolated")
	}
}
