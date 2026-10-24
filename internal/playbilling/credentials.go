package playbilling

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const publisherScope = "https://www.googleapis.com/auth/androidpublisher"
const oauthEndpoint = "https://oauth2.googleapis.com/token"

// ServiceAccountSource prepares a server-only, fixed-scope Google credential.
// It is offline until the returned function is used. Never pass request data.
func ServiceAccountSource(raw []byte) (AccessToken, error) {
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return serviceAccountSource(raw, client)
}

func serviceAccountSource(raw []byte, client *http.Client) (AccessToken, error) {
	invalid := errors.New("Play service-account configuration is invalid")
	if len(raw) > 65536 {
		return nil, invalid
	}
	var fields struct {
		Type     string `json:"type"`
		Email    string `json:"client_email"`
		Key      string `json:"private_key"`
		TokenURL string `json:"token_uri"`
	}
	if json.Unmarshal(raw, &fields) != nil || fields.Type != "service_account" || fields.TokenURL != oauthEndpoint || !strings.HasSuffix(fields.Email, ".iam.gserviceaccount.com") || strings.ContainsAny(fields.Email, "\r\n \t") {
		return nil, invalid
	}
	block, rest := pem.Decode([]byte(fields.Key))
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, invalid
	}
	var key *rsa.PrivateKey
	if parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		key, _ = parsed.(*rsa.PrivateKey)
	}
	if key == nil {
		key, _ = x509.ParsePKCS1PrivateKey(block.Bytes)
	}
	if key == nil || key.N.BitLen() < 2048 {
		return nil, invalid
	}
	configuration, err := google.JWTConfigFromJSON(raw, publisherScope)
	if err != nil || configuration.TokenURL != oauthEndpoint {
		return nil, invalid
	}
	gate := make(chan struct{}, 1)
	var cached *oauth2.Token
	return func(ctx context.Context) (string, error) {
		unavailable := errors.New("Play authentication is unavailable")
		select {
		case gate <- struct{}{}:
		case <-ctx.Done():
			return "", unavailable
		}
		defer func() { <-gate }()
		if ctx.Err() != nil {
			return "", unavailable
		}
		if cached != nil && cached.AccessToken != "" && cached.Expiry.After(time.Now().Add(time.Minute)) {
			return cached.AccessToken, nil
		}
		// Use the caller's cancellation for a refresh, retaining only the token,
		// not a token-source context that could be cancelled after the first call.
		requestContext := context.WithValue(ctx, oauth2.HTTPClient, client)
		issued, err := configuration.TokenSource(requestContext).Token()
		if err != nil || issued == nil || issued.AccessToken == "" || len(issued.AccessToken) > 8192 || strings.ContainsAny(issued.AccessToken, "\r\n") || !strings.EqualFold(issued.TokenType, "Bearer") || !issued.Expiry.After(time.Now().Add(time.Minute)) {
			return "", unavailable
		}
		cached = issued
		return issued.AccessToken, nil
	}, nil
}
