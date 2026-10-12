package services

import (
	"across/backend/internal/config"
	"context"
	"crypto/elliptic"
	"encoding/base64"
	"fmt"
	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

func WebPushConfigured(cfg config.Config) bool {
	public, err := base64.RawURLEncoding.DecodeString(cfg.WebPushPublicKey)
	if err != nil || len(public) != 65 {
		return false
	}
	private, err := base64.RawURLEncoding.DecodeString(cfg.WebPushPrivateKey)
	if err != nil || len(private) != 32 {
		return false
	}
	scalar := new(big.Int).SetBytes(private)
	if scalar.Sign() <= 0 || scalar.Cmp(elliptic.P256().Params().N) >= 0 {
		return false
	}
	x, y := elliptic.P256().ScalarBaseMult(private)
	if string(elliptic.Marshal(elliptic.P256(), x, y)) != string(public) {
		return false
	}
	subject, err := url.Parse(cfg.WebPushSubject)
	return err == nil && (subject.Scheme == "mailto" && subject.Opaque != "" || subject.Scheme == "https" && subject.Hostname() != "")
}
func ValidWebPushEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Port() != "" || len(endpoint) > 2048 {
		return false
	}
	switch u.Hostname() {
	case "fcm.googleapis.com", "updates.push.services.mozilla.com", "web.push.apple.com", "wns2-db5p.notify.windows.com":
		return u.Path != ""
	}
	return strings.HasSuffix(u.Hostname(), ".notify.windows.com") && u.Path != ""
}
func ValidWebPushKeys(public, auth string) bool {
	p, e := base64.RawURLEncoding.DecodeString(public)
	if e != nil {
		return false
	}
	a, e := base64.RawURLEncoding.DecodeString(auth)
	if e != nil || len(a) != 16 {
		return false
	}
	x, _ := elliptic.Unmarshal(elliptic.P256(), p)
	return x != nil
}

type webPushJob struct {
	id, endpoint, public, auth, vapid string
	payload                           []byte
	attempts                          int
	subscription                      string
}

func RunWebPushBatch(ctx context.Context, db *pgxpool.Pool, cfg config.Config, client *http.Client) (int, error) {
	if !WebPushConfigured(cfg) {
		return 0, nil
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	_, err := db.Exec(ctx, `UPDATE web_push_deliveries SET status='expired',last_error='Notification expired',updated_at=now() WHERE status IN ('pending','retry','sending') AND created_at<now()-interval '24 hours';
 UPDATE web_push_deliveries SET status='retry',next_attempt_at=now(),updated_at=now() WHERE status='sending' AND updated_at<now()-interval '5 minutes'`)
	if err != nil {
		return 0, err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT d.id::text,s.id::text,s.endpoint,s.p256dh,s.auth,s.vapid_public_key,d.payload,d.attempts FROM web_push_deliveries d JOIN web_push_subscriptions s ON s.id=d.subscription_id AND s.disabled_at IS NULL WHERE d.status IN ('pending','retry') AND d.next_attempt_at<=now() ORDER BY d.next_attempt_at,d.id FOR UPDATE OF d SKIP LOCKED LIMIT 20`)
	if err != nil {
		return 0, err
	}
	jobs := []webPushJob{}
	for rows.Next() {
		var j webPushJob
		if err = rows.Scan(&j.id, &j.subscription, &j.endpoint, &j.public, &j.auth, &j.vapid, &j.payload, &j.attempts); err != nil {
			rows.Close()
			return 0, err
		}
		jobs = append(jobs, j)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	for _, j := range jobs {
		if _, err = tx.Exec(ctx, `UPDATE web_push_deliveries SET status='sending',attempts=attempts+1,updated_at=now() WHERE id=$1::uuid`, j.id); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	var mutex sync.Mutex
	var firstErr error
	count := 0
	slots := make(chan struct{}, 5)
	var group sync.WaitGroup
	for _, j := range jobs {
		group.Add(1)
		slots <- struct{}{}
		go func(j webPushJob) {
			defer group.Done()
			defer func() { <-slots }()
			status, message := "failed", "Invalid browser push subscription"
			code := 0
			if ValidWebPushEndpoint(j.endpoint) && ValidWebPushKeys(j.public, j.auth) && j.vapid == cfg.WebPushPublicKey {
				sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				response, sendErr := webpush.SendNotificationWithContext(sendCtx, j.payload, &webpush.Subscription{Endpoint: j.endpoint, Keys: webpush.Keys{P256dh: j.public, Auth: j.auth}}, &webpush.Options{HTTPClient: client, Subscriber: cfg.WebPushSubject, VAPIDPublicKey: cfg.WebPushPublicKey, VAPIDPrivateKey: cfg.WebPushPrivateKey, TTL: 3600, Urgency: webpush.UrgencyHigh})
				cancel()
				if sendErr != nil {
					status, message = "retry", "Browser push request failed"
				} else {
					code = response.StatusCode
					io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
					response.Body.Close()
					message = fmt.Sprintf("web push HTTP %d", code)
					if code >= 200 && code < 300 {
						status, message = "sent", ""
					} else if code == 429 || code >= 500 {
						status = "retry"
					}
				}
			}
			if j.attempts+1 >= 6 && status == "retry" {
				status = "failed"
			}
			delay := time.Duration(30*(1<<min(j.attempts, 7))) * time.Second
			_, saveErr := db.Exec(ctx, `UPDATE web_push_deliveries SET status=$2,last_error=$3,next_attempt_at=$4,updated_at=now(),sent_at=CASE WHEN $2='sent' THEN now() ELSE sent_at END WHERE id=$1::uuid`, j.id, status, message, time.Now().Add(delay))
			if code == 404 || code == 410 {
				_, disableErr := db.Exec(ctx, `UPDATE web_push_subscriptions SET disabled_at=now(),updated_at=now() WHERE id=$1::uuid`, j.subscription)
				if saveErr == nil {
					saveErr = disableErr
				}
			}
			mutex.Lock()
			defer mutex.Unlock()
			if status == "sent" {
				count++
			}
			if firstErr == nil && saveErr != nil {
				firstErr = saveErr
			}
		}(j)
	}
	group.Wait()
	return count, firstErr
}
