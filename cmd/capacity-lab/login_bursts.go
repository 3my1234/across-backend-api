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
	"github.com/jackc/pgx/v5/pgxpool"
)

type loginAttempt struct {
	Burst              int       `json:"burst"`
	Client             int       `json:"synthetic_client"`
	Attempt            int       `json:"attempt"`
	Status             int       `json:"status"`
	ValidIdentity      bool      `json:"valid_identity"`
	ValidOverload      bool      `json:"valid_overload"`
	RateLimitRemaining int       `json:"rate_limit_remaining"`
	RateLimitPresent   bool      `json:"rate_limit_header_present"`
	RetryAfter         int       `json:"retry_after_seconds"`
	WaitMS             float64   `json:"wait_since_previous_response_ms"`
	DurationMS         float64   `json:"duration_ms"`
	Started            time.Time `json:"started"`
	Finished           time.Time `json:"finished"`
}

// This intentionally records only an index, never login credentials or tokens.
func labPasswordLogin(c *http.Client, actor sessionFixture, burst, index, attempt int, wait float64) loginAttempt {
	result := loginAttempt{Burst: burst, Client: index, Attempt: attempt, WaitMS: wait, Started: time.Now().UTC()}
	body, _ := json.Marshal(map[string]string{"email": actor.Email, "password": sessionPassword})
	req, _ := http.NewRequest("POST", origin+"/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.0.0.%d", index+1))
	response, err := c.Do(req)
	if err == nil {
		result.Status = response.StatusCode
		var payload struct {
			ID    string `json:"user_id"`
			Token string `json:"access_token"`
			Error string `json:"error"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&payload)
		response.Body.Close()
		result.RetryAfter, _ = strconv.Atoi(response.Header.Get("Retry-After"))
		remaining, headerErr := strconv.Atoi(response.Header.Get("X-RateLimit-Remaining"))
		result.RateLimitRemaining = remaining
		result.RateLimitPresent = headerErr == nil
		result.ValidIdentity = decodeErr == nil && result.Status == 200 && payload.ID == actor.ID && payload.Token != ""
		result.ValidOverload = decodeErr == nil && result.Status == 503 && payload.Error != "" && result.RetryAfter > 0
	}
	result.Finished = time.Now().UTC()
	result.DurationMS = float64(result.Finished.Sub(result.Started)) / float64(time.Millisecond)
	return result
}

func passwordSnapshot(c *http.Client, token string) (map[string]any, error) {
	result := map[string]any{"time": time.Now().UTC()}
	for _, backend := range []string{"api1", "api2"} {
		req, _ := http.NewRequest("GET", "http://"+backend+":8080/api/v1/admin/ops/runtime", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := c.Do(req)
		if err != nil {
			return nil, fmt.Errorf("password observer transport failed")
		}
		var payload struct {
			Password map[string]any `json:"password_admission"`
		}
		err = json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&payload)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || payload.Password == nil {
			return nil, fmt.Errorf("password observer response invalid")
		}
		result[backend] = payload.Password
	}
	return result, nil
}

func loginBursts(ctx context.Context, db *pgxpool.Pool, out io.Writer) error {
	if err := requireSessionLab(ctx, db); err != nil {
		return err
	}
	var buffer bytes.Buffer
	if err := exportSessions(ctx, db, &buffer); err != nil {
		return err
	}
	var fixture struct {
		Sessions []sessionFixture `json:"sessions"`
	}
	if err := json.Unmarshal(buffer.Bytes(), &fixture); err != nil || len(fixture.Sessions) < 151 {
		return fmt.Errorf("100 synthetic clients required")
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
	// Existing app has no automatic password-login retry. First-wave results
	// therefore represent one attempt only. Subsequent attempts are explicitly
	// a hypothetical bounded Retry-After+jitter client, not released app behavior.
	for _, size := range []int{20, 50, 100} {
		before, err := passwordSnapshot(client, token)
		if err != nil {
			return err
		}
		if err = encoder.Encode(map[string]any{"event": "burst_start", "size": size, "snapshot": before, "retry_model": "at most6total attempts; wait Retry-After plus deterministic0..1second jitter; actual mobile has no automatic retry"}); err != nil {
			return err
		}
		stop := make(chan struct{})
		done := make(chan struct{})
		var samples []map[string]any
		var sampleErr error
		go func() {
			defer close(done)
			for {
				select {
				case <-stop:
					return
				case <-time.After(20 * time.Millisecond):
					sample, e := passwordSnapshot(client, token)
					if e != nil {
						sampleErr = e
						return
					}
					samples = append(samples, sample)
				}
			}
		}()
		first := make([]loginAttempt, size)
		gate := make(chan struct{})
		var wave sync.WaitGroup
		for i := 0; i < size; i++ {
			wave.Add(1)
			go func(index int) {
				defer wave.Done()
				<-gate
				first[index] = labPasswordLogin(client, fixture.Sessions[index], size, index, 1, 0)
			}(i)
		}
		close(gate)
		wave.Wait()
		for _, attempt := range first {
			if err = encoder.Encode(map[string]any{"event": "login_attempt", "data": attempt}); err != nil {
				return err
			}
		}
		for _, attempt := range first {
			if !attempt.ValidIdentity && !attempt.ValidOverload {
				close(stop)
				<-done
				return fmt.Errorf("unexpected first-wave login outcome; stop")
			}
		}
		if size == 20 {
			for _, attempt := range first {
				if !attempt.RateLimitPresent || attempt.RateLimitRemaining != 19 {
					close(stop)
					<-done
					return fmt.Errorf("distinct-IP first-wave rate accounting failed; stop before retries")
				}
			}
		}
		attempts := make([][]loginAttempt, size)
		for i, result := range first {
			if !result.ValidOverload {
				continue
			}
			wave.Add(1)
			go func(index int, previous loginAttempt) {
				defer wave.Done()
				for n := 2; n <= 6; n++ {
					delay := time.Duration(previous.RetryAfter)*time.Second + time.Duration((index*137+n*71)%1000)*time.Millisecond
					time.Sleep(delay)
					wait := float64(time.Since(previous.Finished)) / float64(time.Millisecond)
					current := labPasswordLogin(client, fixture.Sessions[index], size, index, n, wait)
					attempts[index] = append(attempts[index], current)
					if !current.ValidOverload {
						return
					}
					previous = current
				}
			}(i, result)
		}
		wave.Wait()
		close(stop)
		<-done
		for _, series := range attempts {
			for _, attempt := range series {
				if err = encoder.Encode(map[string]any{"event": "login_attempt", "data": attempt}); err != nil {
					return err
				}
			}
		}
		for _, series := range attempts {
			for _, attempt := range series {
				if !attempt.ValidIdentity && !attempt.ValidOverload {
					return fmt.Errorf("unexpected modeled-retry outcome; stop")
				}
			}
		}
		after, err := passwordSnapshot(client, token)
		if err != nil {
			return err
		}
		if sampleErr != nil {
			return sampleErr
		}
		if err = encoder.Encode(map[string]any{"event": "burst_end", "size": size, "snapshot": after, "password_samples": samples}); err != nil {
			return err
		}
		// Readiness and a single fresh login must recover before another burst.
		time.Sleep(2 * time.Second)
		req, _ := http.NewRequest("GET", origin+"/api/v1/ready", nil)
		response, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("burst recovery readiness failed")
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			return fmt.Errorf("burst recovery readiness not200")
		}
		recovered := labPasswordLogin(client, fixture.Sessions[150], size, 150, 1, 0)
		if err = encoder.Encode(map[string]any{"event": "recovery_login", "size": size, "data": recovered}); err != nil {
			return err
		}
		if !recovered.ValidIdentity {
			return fmt.Errorf("single login did not recover after burst")
		}
	}
	// No raw credentials have been written; stdout is safe synthetic metadata.
	return nil
}
