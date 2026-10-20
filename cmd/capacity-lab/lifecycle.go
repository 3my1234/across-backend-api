package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Exercise real auth/onboarding routes against the fixed disposable origin.
// Verification tokens stay inside the synthetic database/process, never logs.
func sessionLifecycle(ctx context.Context, db *pgxpool.Pool, out io.Writer) error {
	if err := requireSessionLab(ctx, db); err != nil {
		return err
	}
	c := client()
	defer c.CloseIdleConnections()
	call := func(name, method, path, token string, body any, status int) (map[string]any, error) {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, method, origin+"/api/v1"+path, bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Real-IP", "10.250.1.1")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		began := time.Now()
		res, err := c.Do(req)
		if err != nil {
			return nil, fmt.Errorf("lifecycle %s transport failed", name)
		}
		defer res.Body.Close()
		payload, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		if err != nil {
			return nil, fmt.Errorf("lifecycle %s read failed", name)
		}
		if res.StatusCode != status {
			return nil, fmt.Errorf("lifecycle %s status=%d expected=%d", name, res.StatusCode, status)
		}
		data := map[string]any{}
		_ = json.Unmarshal(payload, &data)
		if err = json.NewEncoder(out).Encode(map[string]any{"lifecycle_step": name, "status": status, "elapsed_ms": float64(time.Since(began).Microseconds()) / 1000}); err != nil {
			return nil, err
		}
		return data, nil
	}
	for _, kind := range []string{"buyer", "product_merchant", "service_professional", "mixed"} {
		email := "lifecycle-" + uuid.NewString() + "@example.invalid"
		signup, err := call("signup_"+kind, "POST", "/auth/signup", "", map[string]string{"full_name": "Synthetic lifecycle actor", "email": email, "password": sessionPassword}, 202)
		if err != nil {
			return err
		}
		id, _ := signup["user_id"].(string)
		if id == "" {
			return fmt.Errorf("signup did not return synthetic user id")
		}
		var verification string
		if err = db.QueryRow(ctx, `SELECT verification_token FROM users WHERE id=$1::uuid AND email=$2 AND NOT email_verified`, id, email).Scan(&verification); err != nil {
			return err
		}
		if _, err = call("unverified_login_"+kind, "POST", "/auth/login", "", map[string]string{"email": email, "password": sessionPassword}, 403); err != nil {
			return err
		}
		if _, err = call("verify_"+kind, "GET", "/auth/verify-email?token="+url.QueryEscape(verification), "", nil, 200); err != nil {
			return err
		}
		login, err := call("login_"+kind, "POST", "/auth/login", "", map[string]string{"email": email, "password": sessionPassword}, 200)
		if err != nil {
			return err
		}
		token, _ := login["access_token"].(string)
		if token == "" || login["user_id"] != id {
			return fmt.Errorf("lifecycle identity mismatch")
		}
		if _, err = call("profile_"+kind, "PUT", "/profile", token, map[string]string{"full_name": "Synthetic updated lifecycle actor", "city": "Abuja"}, 200); err != nil {
			return err
		}
		if kind != "buyer" {
			provider, err := call("onboarding_"+kind, "POST", "/providers/onboarding", token, map[string]string{"business_name": "Synthetic " + kind, "provider_type": kind, "contact_email": email, "contact_phone": "lab-" + uuid.NewString()[:8], "city": "Abuja", "country_code": "NG"}, 201)
			if err != nil {
				return err
			}
			if provider["provider_type"] != kind {
				return fmt.Errorf("provider role was not retained")
			}
			if _, err = call("provider_profile_"+kind, "GET", "/providers/me", token, nil, 200); err != nil {
				return err
			}
		}
		var active, verified bool
		if err = db.QueryRow(ctx, `SELECT is_active,email_verified FROM users WHERE id=$1::uuid`, id).Scan(&active, &verified); err != nil || !active || !verified {
			return fmt.Errorf("lifecycle verification not persisted")
		}
	}
	return json.NewEncoder(out).Encode(map[string]any{"auth_lifecycle_passed": true, "roles": []string{"buyer", "product_merchant", "service_professional", "mixed"}, "verification_transport": "synthetic database token; external email suppressed", "refresh_route_available": false, "server_logout_route_available": false})
}
