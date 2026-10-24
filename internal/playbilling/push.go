package playbilling

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"google.golang.org/api/idtoken"
	"google.golang.org/api/option"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

type PushVerifier struct {
	validator       *idtoken.Validator
	audience, email string
	Subscription    string
}

func PushFromEnvironment() (*PushVerifier, error) {
	audience, email, subscription := os.Getenv("PLAY_RTDN_AUDIENCE"), os.Getenv("PLAY_RTDN_SERVICE_ACCOUNT_EMAIL"), os.Getenv("PLAY_RTDN_SUBSCRIPTION")
	if audience == "" && email == "" && subscription == "" {
		return nil, nil
	}
	return NewPushVerifier(audience, email, subscription, nil)
}
func NewPushVerifier(audience, email, subscription string, client *http.Client) (*PushVerifier, error) {
	target, err := url.Parse(audience)
	if err != nil || target.Scheme != "https" || target.Hostname() == "" || target.User != nil || target.RawQuery != "" || target.Fragment != "" || target.Path != "/api/v1/webhooks/google-play" || !regexp.MustCompile(`^[a-zA-Z0-9._-]+@[a-zA-Z0-9-]+\.iam\.gserviceaccount\.com$`).MatchString(email) || !regexp.MustCompile(`^projects/[a-zA-Z0-9-]{1,100}/subscriptions/[a-zA-Z0-9._~-]{1,200}$`).MatchString(subscription) {
		return nil, errors.New("Play push authentication configuration is invalid")
	}
	if client == nil {
		client = &http.Client{}
	}
	copy := *client
	copy.Timeout = 5 * time.Second
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	validator, err := idtoken.NewValidator(context.Background(), option.WithHTTPClient(&copy))
	if err != nil {
		return nil, errors.New("Play push validator is unavailable")
	}
	return &PushVerifier{validator: validator, audience: audience, email: email, Subscription: subscription}, nil
}
func (v *PushVerifier) Validate(ctx context.Context, bearer string) error {
	denied := errors.New("Play push identity is not verified")
	if v == nil || len(bearer) > 8192 || !strings.HasPrefix(bearer, "Bearer ") {
		return denied
	}
	parts := strings.Split(strings.TrimPrefix(bearer, "Bearer "), ".")
	if len(parts) != 3 || len(parts[0]) > 1024 {
		return denied
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return denied
	}
	var header struct {
		Algorithm string `json:"alg"`
	}
	if json.Unmarshal(raw, &header) != nil || header.Algorithm != "RS256" {
		return denied
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	payload, err := v.validator.Validate(ctx, strings.TrimPrefix(bearer, "Bearer "), v.audience)
	if err != nil || payload == nil || (payload.Issuer != "https://accounts.google.com" && payload.Issuer != "accounts.google.com") || payload.Subject == "" || payload.Claims["email"] != v.email || payload.Claims["email_verified"] != true {
		return denied
	}
	return nil
}
