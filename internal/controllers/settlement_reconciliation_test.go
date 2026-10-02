package controllers

import "testing"

func TestNormalizeSettlementStatus(t *testing.T) {
	tests := map[string]string{
		"successful": "settled",
		"completed":  "settled",
		"On hold":    "on_hold",
		"flagged":    "on_hold",
		"pending":    "pending",
		"failed":     "failed",
		"reversed":   "reversed",
	}
	for input, expected := range tests {
		if actual := normalizeSettlementStatus(input); actual != expected {
			t.Fatalf("normalizeSettlementStatus(%q) = %q; want %q", input, actual, expected)
		}
	}
}
