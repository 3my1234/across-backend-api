package auth

import (
	"testing"
	"time"
)

func TestSignWithVersionRoundTrip(t *testing.T) {
	token, _, err := SignWithVersion("user-id", "test-secret", time.Minute, 7)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := Verify(token, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != "user-id" || claims.SessionVersion != 7 {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestSignKeepsLegacySessionVersionZero(t *testing.T) {
	token, _, err := Sign("user-id", "test-secret", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := Verify(token, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	if claims.SessionVersion != 0 {
		t.Fatalf("expected session version 0, got %d", claims.SessionVersion)
	}
}
