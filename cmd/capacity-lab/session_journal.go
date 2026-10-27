package main

import "regexp"

type sessionJournalResult struct {
	Offered                   int  `json:"offered_write_keys"`
	Acknowledged              int  `json:"exact_acknowledged_keys"`
	Stored                    int  `json:"stored_write_keys"`
	MissingAcknowledged       int  `json:"missing_acknowledged_keys"`
	DuplicateStored           int  `json:"duplicate_stored_keys"`
	UnknownStored             int  `json:"unknown_stored_keys"`
	UnconfirmedCommitted      int  `json:"unconfirmed_committed_keys"`
	OfferedNotStored          int  `json:"offered_keys_not_stored"`
	LoadCompleted             bool `json:"load_process_completed"`
	AcknowledgementsPreserved bool `json:"all_acknowledged_keys_stored_once"`
	JournalConsistent         bool `json:"journal_consistent"`
	NormalInventoryMatched    bool `json:"normal_completion_inventory_matched"`
}

func parseSessionJournal(raw, run string) (map[string]int, map[string]int) {
	offered, ack := map[string]int{}, map[string]int{}
	expression := regexp.MustCompile(`(OFFER|ACK):(sessions-` + regexp.QuoteMeta(run) + `:[0-9]+:[a-f0-9-]{36}:[a-f0-9-]{36})`)
	for _, match := range expression.FindAllStringSubmatch(raw, -1) {
		if match[1] == "OFFER" {
			offered[match[2]]++
		} else {
			ack[match[2]]++
		}
	}
	return offered, ack
}

func reconcileSessionJournal(offered, ack, stored map[string]int, counter int64, completed bool) sessionJournalResult {
	result := sessionJournalResult{Offered: len(offered), Acknowledged: len(ack), Stored: len(stored), LoadCompleted: completed, AcknowledgementsPreserved: true, JournalConsistent: int64(len(ack)) == counter}
	for key, count := range offered {
		if count != 1 {
			result.JournalConsistent = false
		}
		if stored[key] == 0 {
			result.OfferedNotStored++
		}
	}
	for key, count := range ack {
		if offered[key] != 1 || count != 1 {
			result.JournalConsistent = false
		}
		if stored[key] == 0 {
			result.MissingAcknowledged++
		}
		if stored[key] != 1 {
			result.AcknowledgementsPreserved = false
		}
	}
	for key, count := range stored {
		if count != 1 {
			result.DuplicateStored++
		}
		if offered[key] != 1 {
			result.UnknownStored++
		}
		if ack[key] == 0 {
			result.UnconfirmedCommitted++
		}
	}
	if result.DuplicateStored > 0 || result.UnknownStored > 0 {
		result.JournalConsistent = false
	}
	result.NormalInventoryMatched = completed && result.JournalConsistent && result.AcknowledgementsPreserved && len(offered) == len(ack) && len(ack) == len(stored) && result.UnconfirmedCommitted == 0 && result.OfferedNotStored == 0
	return result
}
