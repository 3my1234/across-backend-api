package controllers

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

// Rank participates in the cursor, so paging from paid listings into newer
// free listings does not silently skip the free group.
func encodePromotedCursor(created time.Time, id string, paid bool) string {
	raw, _ := json.Marshal(struct {
		CreatedAt time.Time `json:"created_at"`
		ID        string    `json:"id"`
		Paid      bool      `json:"paid"`
	}{created, id, paid})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func promotedCursorPriority(raw string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return false
	}
	var cursor struct {
		Paid bool `json:"paid"`
	}
	if json.Unmarshal(decoded, &cursor) != nil {
		return false
	}
	return cursor.Paid
}
func encodeRankedCursor(created time.Time, id string, rank int) string {
	raw, _ := json.Marshal(struct {
		CreatedAt time.Time `json:"created_at"`
		ID        string    `json:"id"`
		Rank      int       `json:"rank"`
		Paid      bool      `json:"paid"`
	}{created, id, rank, rank > 0})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func promotedCursorRank(raw string) int {
	decoded, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil {
		return 0
	}
	var cursor struct {
		Rank *int `json:"rank"`
		Paid bool `json:"paid"`
	}
	if json.Unmarshal(decoded, &cursor) != nil {
		return 0
	}
	if cursor.Rank != nil && *cursor.Rank >= 0 && *cursor.Rank <= 3 {
		return *cursor.Rank
	}
	if cursor.Paid {
		return 1
	}
	return 0
}
