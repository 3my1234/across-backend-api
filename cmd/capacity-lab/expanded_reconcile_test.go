package main

import (
	"strings"
	"testing"
)

func TestPresenceJournalRejectsMissingDuplicateAndForeignAcknowledgements(t *testing.T) {
	provider := "00000000-0000-4000-8000-000000000001"
	key := provider + ":0:on:9.081:7.451"
	valid := "PRESENCE_OFFER:" + key + "\nPRESENCE_ACK:" + key
	states, n, err := parsePresenceJournal(valid)
	if err != nil || n != 1 || !states[provider].Online {
		t.Fatal(states, n, err)
	}
	for _, bad := range []string{"", strings.Split(valid, "\n")[0], strings.Split(valid, "\n")[1], valid + "\nPRESENCE_ACK:" + key, valid + "\nPRESENCE_OFFER:" + key} {
		if _, _, err := parsePresenceJournal(bad); err == nil {
			t.Fatal("invalid journal accepted")
		}
	}
	last := provider + ":1:off:9.082:7.451"
	states, n, err = parsePresenceJournal(valid + "\nPRESENCE_OFFER:" + last + "\nPRESENCE_ACK:" + last)
	if err != nil || n != 2 || states[provider].Online {
		t.Fatal(states, n, err)
	}
}
