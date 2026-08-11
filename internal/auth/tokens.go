package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type Claims struct {
	UserID         string `json:"sub"`
	ExpiresAt      int64  `json:"exp"`
	SessionVersion int    `json:"ver,omitempty"`
}

func Sign(userID, secret string, ttl time.Duration) (string, int64, error) {
	return SignWithVersion(userID, secret, ttl, 0)
}

func SignWithVersion(userID, secret string, ttl time.Duration, sessionVersion int) (string, int64, error) {
	expiresAt := time.Now().Add(ttl).Unix()
	claims := Claims{UserID: userID, ExpiresAt: expiresAt, SessionVersion: sessionVersion}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", 0, err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	sig := signature(body, secret)
	return body + "." + sig, expiresAt, nil
}

func Verify(token, secret string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return Claims{}, errors.New("invalid token")
	}
	if !hmac.Equal([]byte(parts[1]), []byte(signature(parts[0], secret))) {
		return Claims{}, errors.New("invalid token signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Claims{}, err
	}
	var claims Claims
	if err := json.Unmarshal(raw, &claims); err != nil {
		return Claims{}, err
	}
	if claims.UserID == "" || time.Now().Unix() >= claims.ExpiresAt {
		return Claims{}, errors.New("expired token")
	}
	return claims, nil
}

func signature(body, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
