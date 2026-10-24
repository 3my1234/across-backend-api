package playbilling

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"github.com/golang-jwt/jwt/v5"
	"io"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGooglePushSignatureAudienceIssuerAndServiceIdentity(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	aud := "https://fixture.example.invalid/api/v1/webhooks/google-play"
	email := "push@synthetic-project.iam.gserviceaccount.com"
	jwks, _ := json.Marshal(map[string]any{"keys": []any{map[string]any{"kid": "synthetic-key", "kty": "RSA", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	verifier, err := NewPushVerifier(aud, email, "projects/synthetic-project/subscriptions/play-fixture", &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || !strings.HasSuffix(r.URL.Host, "googleapis.com") {
			t.Fatal("Validator fetched untrusted certificate origin")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Cache-Control": []string{"public,max-age=3600"}}, Body: io.NopCloser(strings.NewReader(string(jwks)))}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	base := jwt.MapClaims{"iss": "https://accounts.google.com", "aud": aud, "sub": "synthetic-service-identity", "email": email, "email_verified": true, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()}
	for _, test := range []struct {
		field   string
		value   any
		allowed bool
	}{{"", nil, true}, {"aud", "https://wrong.example.invalid", false}, {"iss", "https://attacker.example.invalid", false}, {"email", "another@synthetic-project.iam.gserviceaccount.com", false}, {"email_verified", false, false}, {"exp", time.Now().Add(-time.Hour).Unix(), false}} {
		claims := jwt.MapClaims{}
		for k, v := range base {
			claims[k] = v
		}
		if test.field != "" {
			claims[test.field] = test.value
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		token.Header["kid"] = "synthetic-key"
		signed, e := token.SignedString(key)
		if e != nil {
			t.Fatal(e)
		}
		e = verifier.Validate(context.Background(), "Bearer "+signed)
		if (e == nil) != test.allowed {
			t.Fatalf("Push verification boundary failed %s: %v", test.field, e)
		}
	}
	if verifier.Validate(context.Background(), "Bearer unsigned.fake.value") == nil {
		t.Fatal("Unsigned notification accepted")
	}
}
