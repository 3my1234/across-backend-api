package controllers

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestVerifyPrivyAccessToken(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	verificationKey := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))
	claims := jwt.RegisteredClaims{
		Subject:   "did:privy:test-user",
		Issuer:    "privy.io",
		Audience:  jwt.ClaimStrings{"test-app"},
		IssuedAt:  jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodES256, claims).SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}

	userID, err := verifyPrivyAccessToken(token, verificationKey, "test-app")
	if err != nil {
		t.Fatalf("expected valid token: %v", err)
	}
	if userID != claims.Subject {
		t.Fatalf("expected %q, got %q", claims.Subject, userID)
	}

	if _, err := verifyPrivyAccessToken(token, verificationKey, "wrong-app"); err == nil {
		t.Fatal("expected audience validation failure")
	}
}

func TestParsePrivyVerificationKeyFormats(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pemValue := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))
	x := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.X.Bytes())
	y := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.Y.Bytes())
	jwkBytes, err := json.Marshal(map[string]string{"kty": "EC", "crv": "P-256", "x": x, "y": y})
	if err != nil {
		t.Fatal(err)
	}

	formats := map[string]string{
		"pem":                pemValue,
		"escaped pem":        strings.ReplaceAll(pemValue, "\n", `\n`),
		"double escaped pem": strings.ReplaceAll(pemValue, "\n", `\\n`),
		"base64 pem":         base64.StdEncoding.EncodeToString([]byte(pemValue)),
		"base64 der":         base64.StdEncoding.EncodeToString(publicDER),
		"jwk":                string(jwkBytes),
	}
	for name, value := range formats {
		t.Run(name, func(t *testing.T) {
			parsed, err := parsePrivyVerificationKey(value)
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			if parsed.X.Cmp(privateKey.PublicKey.X) != 0 || parsed.Y.Cmp(privateKey.PublicKey.Y) != 0 {
				t.Fatal("parsed public key does not match")
			}
		})
	}
}
