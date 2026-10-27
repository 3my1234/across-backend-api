package main

// Lab-only adapter around the actual workers.Run loops and delivery services.
// No configurable production target, external dialer, real SMTP or push vendor.
import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/mail"
	"net/textproto"
	"os"
	"strings"
	"sync"
	"time"

	"across/backend/internal/config"
	"across/backend/internal/workers"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func prepareNotificationLab(ctx context.Context, db *pgxpool.Pool) error {
	if err := requireSessionLab(ctx, db); err != nil {
		return err
	}
	_, err := db.Exec(ctx, `CREATE ROLE lab_worker LOGIN PASSWORD 'lab-worker-disposable'; GRANT lab TO lab_worker; CREATE TABLE capacity_lab_killed_jobs(channel text,key text,claimed_at timestamptz,PRIMARY KEY(channel,key));`)
	if err != nil {
		return err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "isolated notification lab"}, DNSNames: []string{"localhost", "notification-provider"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	raw, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		return err
	}
	if err = os.WriteFile("/exchange/mock-cert.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw}), 0600); err != nil {
		return err
	}
	return os.WriteFile("/exchange/mock-key.pem", pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600)
}

type mockEntry struct {
	Channel          string `json:"channel"`
	Key              string `json:"key"`
	Ticket           string `json:"ticket"`
	Attempts         int    `json:"attempts"`
	Acceptances      int    `json:"acceptances"`
	Acknowledgements int    `json:"acknowledgements"`
	Receipts         int    `json:"receipts"`
}
type notificationMock struct {
	mu                            sync.Mutex
	entries                       map[string]*mockEntry
	journal                       *os.File
	outageUntil                   time.Time
	delayMS                       int
	ambiguousEmail, ambiguousPush int
	journalNanos                  int64
	events                        int64
}

func (m *notificationMock) event(channel, key, ticket, event string) error {
	started := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	// Append-only provider journal on its own filesystem, never application SQL.
	raw, err := json.Marshal(map[string]any{"time": time.Now().UTC(), "channel": channel, "key": key, "ticket": ticket, "event": event})
	if err != nil {
		return err
	}
	if _, err = m.journal.Write(append(raw, '\n')); err != nil {
		return err
	}
	if err = m.journal.Sync(); err != nil {
		return err
	}
	k := channel + ":" + key
	e := m.entries[k]
	if e == nil {
		e = &mockEntry{Channel: channel, Key: key, Ticket: ticket}
		m.entries[k] = e
	}
	switch event {
	case "attempt":
		e.Attempts++
	case "accept":
		e.Acceptances++
	case "ack":
		e.Acknowledgements++
	case "receipt":
		e.Receipts++
	}
	m.journalNanos += time.Since(started).Nanoseconds()
	m.events++
	return nil
}
func (m *notificationMock) control(ctx context.Context) (bool, error) {
	m.mu.Lock()
	outage := time.Now().Before(m.outageUntil)
	delay := m.delayMS
	m.mu.Unlock()
	if delay > 0 {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(time.Duration(delay) * time.Millisecond):
		}
	}
	return outage, nil
}
func (m *notificationMock) attempt(_ context.Context, channel, key, ticket string) error {
	return m.event(channel, key, ticket, "attempt")
}
func (m *notificationMock) accept(_ context.Context, channel, key string) error {
	return m.event(channel, key, "", "accept")
}
func (m *notificationMock) ack(_ context.Context, channel, key string) error {
	return m.event(channel, key, "", "ack")
}
func (m *notificationMock) ambiguous(_ context.Context, channel string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	budget := &m.ambiguousEmail
	if channel == "push" {
		budget = &m.ambiguousPush
	}
	if *budget <= 0 {
		return false
	}
	*budget--
	return true
}
func (m *notificationMock) receipt(ticket string) error {
	m.mu.Lock()
	var key string
	for _, e := range m.entries {
		if e.Ticket == ticket && e.Acknowledgements > 0 {
			key = e.Key
			break
		}
	}
	m.mu.Unlock()
	if key == "" {
		return fmt.Errorf("unknown mock receipt")
	}
	return m.event("push", key, ticket, "receipt")
}
func mockResponse(body any, status int) *http.Response {
	raw, _ := json.Marshal(body)
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw))}
}
func (m *notificationMock) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.Host != "exp.host" || req.Method != "POST" {
		return nil, fmt.Errorf("mock rejected non-Expo request")
	}
	if req.URL.Path == "/--/api/v2/push/getReceipts" {
		var payload struct {
			IDs []string `json:"ids"`
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			return nil, err
		}
		data := map[string]any{}
		for _, id := range payload.IDs {
			if err := m.receipt(id); err != nil {
				return nil, err
			}
			data[id] = map[string]string{"status": "ok"}
		}
		return mockResponse(map[string]any{"data": data}, 200), nil
	}
	if req.URL.Path != "/--/api/v2/push/send" {
		return nil, fmt.Errorf("mock rejected unexpected path")
	}
	var messages []struct {
		To   string `json:"to"`
		Data struct {
			ID string `json:"notification_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&messages); err != nil {
		return nil, err
	}
	outage, err := m.control(req.Context())
	if err != nil {
		return nil, err
	}
	keys := []string{}
	tickets := []map[string]string{}
	lost := false
	for _, message := range messages {
		digest := sha256.Sum256([]byte(message.Data.ID + ":" + message.To))
		key := hex.EncodeToString(digest[:])
		ticket := uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String()
		if err = m.attempt(req.Context(), "push", key, ticket); err != nil {
			return nil, err
		}
		keys = append(keys, key)
		if !outage {
			if err = m.accept(req.Context(), "push", key); err != nil {
				return nil, err
			}
			lost = m.ambiguous(req.Context(), "push") || lost
		}
		tickets = append(tickets, map[string]string{"status": "ok", "id": ticket})
	}
	if outage {
		return mockResponse(map[string]string{"error": "mock outage"}, 503), nil
	}
	if lost {
		return nil, fmt.Errorf("mock accepted batch but acknowledgement lost")
	}
	for _, key := range keys {
		if err = m.ack(req.Context(), "push", key); err != nil {
			return nil, err
		}
	}
	return mockResponse(map[string]any{"data": tickets}, 200), nil
}

func (m *notificationMock) smtp(ctx context.Context, connection net.Conn, certificate tls.Certificate) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(45 * time.Second))
	reader := bufio.NewReader(connection)
	writer := bufio.NewWriter(connection)
	reply := func(line string) { fmt.Fprint(writer, line+"\r\n"); _ = writer.Flush() }
	reply("220 localhost mock SMTP")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "EHLO"):
			reply("250-localhost\r\n250-STARTTLS\r\n250 AUTH PLAIN")
		case line == "STARTTLS":
			reply("220 ready")
			secured := tls.Server(connection, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
			if secured.Handshake() != nil {
				return
			}
			connection = secured
			reader = bufio.NewReader(connection)
			writer = bufio.NewWriter(connection)
		case strings.HasPrefix(line, "AUTH PLAIN"):
			reply("235 authenticated")
		case strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
			reply("250 OK")
		case line == "DATA":
			reply("354 data")
			message, err := mail.ReadMessage(textproto.NewReader(reader).DotReader())
			if err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, message.Body)
			key := strings.Split(strings.Trim(message.Header.Get("Message-ID"), "<>"), "@")[0]
			if _, err = uuid.Parse(key); err != nil {
				return
			}
			if m.attempt(ctx, "email", key, "") != nil {
				return
			}
			outage, err := m.control(ctx)
			if err != nil {
				return
			}
			if outage {
				reply("451 mock temporary failure")
				continue
			}
			if m.accept(ctx, "email", key) != nil {
				return
			}
			if m.ambiguous(ctx, "email") {
				return
			}
			if m.ack(ctx, "email", key) != nil {
				return
			}
			reply("250 accepted by mock")
		case line == "QUIT":
			reply("221 bye")
			return
		default:
			reply("500 unsupported")
		}
	}
}

// Provider is a separate bounded process, with no database connection at all.
func runNotificationProvider(ctx context.Context) error {
	file, err := os.OpenFile("/exchange/provider-journal.jsonl", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	mock := &notificationMock{entries: map[string]*mockEntry{}, journal: file}
	cert, err := tls.LoadX509KeyPair("/exchange/mock-cert.pem", "/exchange/mock-key.pem")
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", ":2525")
	if err != nil {
		return err
	}
	defer listener.Close()
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go mock.smtp(ctx, connection, cert)
		}
	}()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") })
	mux.HandleFunc("/ledger", func(w http.ResponseWriter, r *http.Request) {
		mock.mu.Lock()
		defer mock.mu.Unlock()
		json.NewEncoder(w).Encode(mock.entries)
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		mock.mu.Lock()
		defer mock.mu.Unlock()
		channels := map[string]map[string]int{}
		for _, e := range mock.entries {
			c := channels[e.Channel]
			if c == nil {
				c = map[string]int{}
				channels[e.Channel] = c
			}
			c["keys"]++
			c["attempts"] += e.Attempts
			c["acceptances"] += e.Acceptances
			c["acknowledgements"] += e.Acknowledgements
			c["receipts"] += e.Receipts
			if e.Acceptances > 1 {
				c["repeated_acceptance_keys"]++
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"channels": channels, "journal_events": mock.events, "journal_nanoseconds": mock.journalNanos})
	})
	mux.HandleFunc("/control", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST required", 405)
			return
		}
		var c struct {
			OutageSeconds  int `json:"outage_seconds"`
			DelayMS        int `json:"delay_ms"`
			AmbiguousEmail int `json:"ambiguous_email"`
			AmbiguousPush  int `json:"ambiguous_push"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&c) != nil || c.OutageSeconds < 0 || c.OutageSeconds > 60 || c.DelayMS < 0 || c.DelayMS > 40000 {
			http.Error(w, "invalid control", 400)
			return
		}
		mock.mu.Lock()
		mock.outageUntil = time.Now().Add(time.Duration(c.OutageSeconds) * time.Second)
		mock.delayMS = c.DelayMS
		mock.ambiguousEmail = c.AmbiguousEmail
		mock.ambiguousPush = c.AmbiguousPush
		mock.mu.Unlock()
		fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("/--/api/v2/push/", func(w http.ResponseWriter, r *http.Request) {
		r.URL.Scheme = "https"
		r.URL.Host = "exp.host"
		response, err := mock.RoundTrip(r)
		if err != nil {
			http.Error(w, "mock interrupted acknowledgement", 502)
			return
		}
		defer response.Body.Close()
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
	})
	server := &http.Server{Addr: ":8081", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); server.Close(); listener.Close() }()
	err = server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

type notificationProxy struct{ transport *http.Transport }

func (p notificationProxy) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.Host != "exp.host" || req.Method != "POST" || (req.URL.Path != "/--/api/v2/push/send" && req.URL.Path != "/--/api/v2/push/getReceipts") {
		return nil, fmt.Errorf("non-mock target rejected")
	}
	target := *req.URL
	target.Scheme = "http"
	target.Host = "notification-provider:8081"
	clone := req.Clone(req.Context())
	clone.URL = &target
	clone.Host = target.Host
	return p.transport.RoundTrip(clone)
}
func runNotificationWorker(ctx context.Context, db *pgxpool.Pool) error {
	if err := requireSessionLab(ctx, db); err != nil {
		return err
	}
	if err := os.Setenv("SSL_CERT_FILE", "/exchange/mock-cert.pem"); err != nil {
		return err
	}
	http.DefaultTransport = notificationProxy{transport: &http.Transport{Proxy: nil}}
	cfg := config.Config{WorkerRoles: "email,push,push_receipts", SMTPHost: "notification-provider", SMTPPort: "2525", SMTPUsername: "lab", SMTPPassword: "lab-disposable-only", SMTPFromEmail: "lab@example.invalid", SMTPFromName: "Isolated lab"}
	workers.Run(ctx, db, cfg)
	return nil
}
func providerJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://notification-provider:8081/"+path, nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("mock provider status %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 32<<20)).Decode(out)
}
func verifyNotificationLab(ctx context.Context, db *pgxpool.Pool, out io.Writer) error {
	if err := requireSessionLab(ctx, db); err != nil {
		return err
	}
	ledger := map[string]mockEntry{}
	if err := providerJSON(ctx, "ledger", &ledger); err != nil {
		return err
	}
	var raw []byte
	if err := db.QueryRow(ctx, `SELECT json_build_object('time',now(),
 'email_status',(SELECT json_object_agg(status,n) FROM(SELECT status,count(*) n FROM email_outbox GROUP BY status)x),
 'push_status',(SELECT json_object_agg(status,n) FROM(SELECT status,count(*) n FROM notification_push_deliveries GROUP BY status)x),
 'due_receipt_backlog',(SELECT count(*) FROM notification_push_deliveries WHERE status='sent' AND receipt_checked_at IS NULL AND sent_at<=now()-interval '15 minutes'),
 'killed_jobs',(SELECT count(*) FROM capacity_lab_killed_jobs),
 'killed_jobs_recovered',(SELECT count(*) FROM capacity_lab_killed_jobs k WHERE CASE WHEN k.channel='email' THEN EXISTS(SELECT 1 FROM email_outbox e WHERE e.id::text=k.key AND e.status='sent') ELSE EXISTS(SELECT 1 FROM notification_push_deliveries d WHERE d.notification_id::text||':'||d.push_token_id::text=k.key AND d.status IN('sent','delivered')) END))`).Scan(&raw); err != nil {
		return err
	}
	report := map[string]any{}
	if err := json.Unmarshal(raw, &report); err != nil {
		return err
	}
	// Snapshot durable application completions, then refresh provider ACK ledger:
	// a live worker may acknowledge more jobs between the two observations.
	rows, err := db.Query(ctx, `SELECT 'email',id::text FROM email_outbox WHERE status='sent' UNION ALL SELECT 'push',encode(digest(d.notification_id::text||':'||t.expo_push_token,'sha256'),'hex') FROM notification_push_deliveries d JOIN user_push_tokens t ON t.id=d.push_token_id WHERE d.status IN('sent','delivered')`)
	if err != nil {
		return err
	}
	keys := [][2]string{}
	for rows.Next() {
		var channel, key string
		if err = rows.Scan(&channel, &key); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, [2]string{channel, key})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if err = providerJSON(ctx, "ledger", &ledger); err != nil {
		return err
	}
	missing := map[string]int{"email": 0, "push": 0}
	for _, k := range keys {
		if ledger[k[0]+":"+k[1]].Acknowledgements == 0 {
			missing[k[0]]++
		}
	}
	report["sent_without_mock_ack"] = missing
	var stats any
	if err = providerJSON(ctx, "stats", &stats); err != nil {
		return err
	}
	report["provider"] = stats
	return json.NewEncoder(out).Encode(report)
}
