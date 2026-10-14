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
