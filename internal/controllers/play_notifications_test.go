package controllers

import (
	"across/backend/internal/playbilling"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type playPushTransport func(*http.Request) (*http.Response, error)

func (f playPushTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPlayWebhookAuthenticatesBeforeDurableHashOnlyEnqueue(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE play_provider_events(message_id text PRIMARY KEY,token_hash text,encrypted_token bytea)`); err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	aud := "https://fixture.example.invalid/api/v1/webhooks/google-play"
	email := "push@synthetic-project.iam.gserviceaccount.com"
	subscription := "projects/synthetic-project/subscriptions/play-fixture"
	public, _ := json.Marshal(map[string]any{"keys": []any{map[string]any{"kid": "fixture", "kty": "RSA", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	validator, err := playbilling.NewPushVerifier(aud, email, subscription, &http.Client{Transport: playPushTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Cache-Control": []string{"public,max-age=3600"}}, Body: io.NopCloser(strings.NewReader(string(public)))}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	signed := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "https://accounts.google.com", "aud": aud, "sub": "synthetic-service", "email": email, "email_verified": true, "exp": time.Now().Add(time.Hour).Unix()})
	signed.Header["kid"] = "fixture"
	bearer, err := signed.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := playbilling.NewVault(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	controller := &PlayBillingController{db: db, store: &playbilling.Store{DB: db, Vault: vault}, push: validator}
	app := fiber.New()
	app.Post("/webhook", controller.Notification)
	rawToken := "sensitive-synthetic-purchase-token"
	notice, _ := json.Marshal(map[string]any{"packageName": playbilling.PackageName, "subscriptionNotification": map[string]any{"notificationType": 2, "purchaseToken": rawToken}})
	for _, test := range []struct {
		auth, subscription string
		status             int
	}{{"Bearer forged.value.here", subscription, 401}, {"Bearer " + bearer, "projects/other/subscriptions/other", 400}, {"Bearer " + bearer, subscription, 204}, {"Bearer " + bearer, subscription, 204}} {
		body, _ := json.Marshal(map[string]any{"subscription": test.subscription, "message": map[string]any{"messageId": "duplicate-fixture-event", "data": base64.StdEncoding.EncodeToString(notice)}})
		request := httptest.NewRequest("POST", "/webhook", strings.NewReader(string(body)))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", test.auth)
		response, e := app.Test(request)
		if e != nil {
			t.Fatal(e)
		}
		response.Body.Close()
		if response.StatusCode != test.status {
			t.Fatalf("Webhook boundary: %d expected %d", response.StatusCode, test.status)
		}
	}
	var count int
	var hash string
	if err = db.QueryRow(ctx, `SELECT count(*),min(token_hash) FROM play_provider_events`).Scan(&count, &hash); err != nil || count != 1 || hash != playbilling.TokenHash(rawToken) || strings.Contains(hash, rawToken) {
		t.Fatal("Webhook duplicated or stored an unverified/plain token", err)
	}
}
