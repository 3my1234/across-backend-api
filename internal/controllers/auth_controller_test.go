package controllers

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
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
