package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"across/backend/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type signupAttempt struct {
	Burst         int       `json:"burst"`
	Client        int       `json:"synthetic_client"`
	Status        int       `json:"status"`
	ValidAccepted bool      `json:"valid_accepted"`
	ValidOverload bool      `json:"valid_overload"`
	RetryAfter    int       `json:"retry_after_seconds"`
	RateRemaining int       `json:"rate_limit_remaining"`
	DurationMS    float64   `json:"duration_ms"`
	Started       time.Time `json:"started"`
	Finished      time.Time `json:"finished"`
	email, id     string    // Private synthetic lookup keys; never serialized.
}

func labSignup(c *http.Client, prefix string, burst, index int) signupAttempt {
	result := signupAttempt{Burst: burst, Client: index, Started: time.Now().UTC(), email: fmt.Sprintf("%s%d@example.invalid", prefix, index)}
	body, _ := json.Marshal(map[string]string{"full_name": "Synthetic signup actor", "email": result.email, "password": sessionPassword, "registration_context": "buyer"})
	req, _ := http.NewRequest("POST", origin+"/api/v1/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Client-Country-Code", "NG")
	req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.1.0.%d", index+1))
	response, err := c.Do(req)
	if err == nil {
		result.Status = response.StatusCode
		var payload struct {
			ID           string `json:"user_id"`
			Verification bool   `json:"requires_email_verification"`
			Pending      bool   `json:"verification_delivery_pending"`
			Error        string `json:"error"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&payload)
		response.Body.Close()
		result.RetryAfter, _ = strconv.Atoi(response.Header.Get("Retry-After"))
		result.RateRemaining, _ = strconv.Atoi(response.Header.Get("X-RateLimit-Remaining"))
		result.id = payload.ID
		_, idErr := uuid.Parse(payload.ID)
		result.ValidAccepted = decodeErr == nil && result.Status == 202 && idErr == nil && payload.Verification && payload.Pending
		result.ValidOverload = decodeErr == nil && result.Status == 503 && payload.Error != "" && result.RetryAfter > 0
	}
	result.Finished = time.Now().UTC()
	result.DurationMS = float64(result.Finished.Sub(result.Started)) / float64(time.Millisecond)
	return result
}

func verifySignupOutcomes(ctx context.Context, db *pgxpool.Pool, results []signupAttempt, out io.Writer) error {
	accepted, stored, queued := 0, 0, 0
	for _, result := range results {
		var rows int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM users WHERE email=$1`, result.email).Scan(&rows); err != nil {
			return err
		}
		stored += rows
		if result.ValidAccepted {
			accepted++
			var id, hash string
			var state bool
			if err := db.QueryRow(ctx, `SELECT id::text,password_hash,NOT is_active AND NOT email_verified AND verification_token IS NOT NULL AND verification_token_expires_at>now() AND registration_context='buyer' FROM users WHERE email=$1`, result.email).Scan(&id, &hash, &state); err != nil {
				return err
			}
			cost, err := bcrypt.Cost([]byte(hash))
			if err != nil || cost != bcrypt.DefaultCost || rows != 1 || id != result.id || !state {
				return fmt.Errorf("accepted signup state/hash/identity invariant failed")
			}
			var emails int
			if err := db.QueryRow(ctx, `SELECT count(*) FROM email_outbox WHERE user_id=$1::uuid AND template_type='verification' AND status='pending' AND attempts=0`, id).Scan(&emails); err != nil {
				return err
			}
			if emails != 1 {
				return fmt.Errorf("accepted signup verification outbox invariant failed")
			}
			queued += emails
		} else if !result.ValidOverload || rows != 0 {
			return fmt.Errorf("unexpected/rejected signup persistence outcome")
		}
	}
	if err := json.NewEncoder(out).Encode(map[string]any{"event": "signup_reconciliation", "offered": len(results), "accepted": accepted, "stored_users": stored, "pending_verification_emails": queued, "reconciliation_passed": accepted == stored && stored == queued, "password_cost": bcrypt.DefaultCost, "activation_requires_verification": true, "email_delivery_tested": false}); err != nil {
		return err
	}
	if accepted != stored || stored != queued {
		return fmt.Errorf("signup write inventory mismatch")
	}
	return nil
}

func signupBursts(ctx context.Context, db *pgxpool.Pool, out io.Writer) error {
	if err := requireSessionLab(ctx, db); err != nil {
		return err
	}
	var admin string
	if err := db.QueryRow(ctx, `SELECT admin_id::text FROM capacity_lab_session_run`).Scan(&admin); err != nil {
		return err
	}
	token, _, err := auth.Sign(admin, secret, time.Hour)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 12 * time.Second, Transport: &http.Transport{MaxIdleConns: 256, MaxIdleConnsPerHost: 256, MaxConnsPerHost: 256}}
	defer client.CloseIdleConnections()
	encoder := json.NewEncoder(out)
	for _, size := range []int{20, 50, 100} {
		prefix := "signup-" + uuid.NewString() + "-"
		before, err := passwordSnapshot(client, token)
		if err != nil {
			return err
		}
		if err = encoder.Encode(map[string]any{"event": "burst_start", "operation": "signup", "size": size, "snapshot": before, "retry_model": "none: registration mutations are not automatically replayed"}); err != nil {
			return err
		}
		stop, done := make(chan struct{}), make(chan struct{})
		var samples []map[string]any
		var sampleErr error
		go func() {
			defer close(done)
			for {
				select {
				case <-stop:
					return
				case <-time.After(200 * time.Millisecond):
					sample, e := passwordSnapshot(client, token)
					if e != nil {
						sampleErr = e
						return
					}
					samples = append(samples, sample)
				}
			}
		}()
		results := make([]signupAttempt, size)
		gate := make(chan struct{})
		var wave sync.WaitGroup
		for i := 0; i < size; i++ {
			wave.Add(1)
			go func(index int) { defer wave.Done(); <-gate; results[index] = labSignup(client, prefix, size, index) }(i)
		}
		close(gate)
		wave.Wait()
		close(stop)
		<-done
		for _, result := range results {
			if err = encoder.Encode(map[string]any{"event": "signup_attempt", "data": result}); err != nil {
				return err
			}
		}
		after, err := passwordSnapshot(client, token)
		if err != nil {
			return err
		}
		if sampleErr != nil {
			return sampleErr
		}
		if err = encoder.Encode(map[string]any{"event": "burst_end", "operation": "signup", "size": size, "snapshot": after, "password_samples": samples}); err != nil {
			return err
		}
		if size == 20 {
			for _, result := range results {
				if result.RateRemaining != 19 {
					return fmt.Errorf("distinct-IP signup accounting failed")
				}
			}
		}
		if err = verifySignupOutcomes(ctx, db, results, out); err != nil {
			return err
		}
		time.Sleep(2 * time.Second)
		recovery := labSignup(client, "recovery-"+uuid.NewString()+"-", size, 200)
		if err = encoder.Encode(map[string]any{"event": "recovery_signup", "size": size, "data": recovery}); err != nil {
			return err
		}
		if !recovery.ValidAccepted {
			return fmt.Errorf("single signup did not recover")
		}
		if err = verifySignupOutcomes(ctx, db, []signupAttempt{recovery}, out); err != nil {
			return err
		}
	}
	return nil
}
