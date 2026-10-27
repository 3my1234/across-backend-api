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
	_, err := db.Exec(ctx, `CREATE TABLE capacity_lab_mock_control(id int PRIMARY KEY CHECK(id=1),outage_until timestamptz NOT NULL DEFAULT 'epoch',delay_ms int NOT NULL DEFAULT 0,ambiguous_email int NOT NULL DEFAULT 0,ambiguous_push int NOT NULL DEFAULT 0);
 INSERT INTO capacity_lab_mock_control(id) VALUES(1);
 CREATE TABLE capacity_lab_mock_delivery(channel text NOT NULL,key text NOT NULL,ticket text,attempts int NOT NULL DEFAULT 0,acceptances int NOT NULL DEFAULT 0,acknowledgements int NOT NULL DEFAULT 0,receipts int NOT NULL DEFAULT 0,first_accepted_at timestamptz,PRIMARY KEY(channel,key));
 CREATE INDEX ON capacity_lab_mock_delivery(ticket);
 CREATE TABLE capacity_lab_killed_jobs(channel text,key text,claimed_at timestamptz,PRIMARY KEY(channel,key));`)
	if err != nil {
		return err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "isolated notification lab"}, DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	raw, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		return err
	}
	if err = os.WriteFile("/exchange/mock-cert.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw}), 0600); err != nil {
		return err
	}
	return os.WriteFile("/exchange/mock-key.pem", pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600)
}

type notificationMock struct{ db *pgxpool.Pool }

func (m *notificationMock) control(ctx context.Context) (bool, error) {
	var outage bool
	var delay int
	if err := m.db.QueryRow(ctx, `SELECT outage_until>now(),delay_ms FROM capacity_lab_mock_control WHERE id=1`).Scan(&outage, &delay); err != nil {
		return false, err
	}
	if delay > 0 {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(time.Duration(delay) * time.Millisecond):
		}
	}
	return outage, nil
}
func (m *notificationMock) attempt(ctx context.Context, channel, key, ticket string) error {
	_, err := m.db.Exec(ctx, `INSERT INTO capacity_lab_mock_delivery(channel,key,ticket,attempts) VALUES($1,$2,$3,1) ON CONFLICT(channel,key) DO UPDATE SET attempts=capacity_lab_mock_delivery.attempts+1`, channel, key, ticket)
	return err
}
func (m *notificationMock) accept(ctx context.Context, channel, key string) error {
	_, err := m.db.Exec(ctx, `UPDATE capacity_lab_mock_delivery SET acceptances=acceptances+1,first_accepted_at=COALESCE(first_accepted_at,now()) WHERE channel=$1 AND key=$2`, channel, key)
	return err
}
func (m *notificationMock) ack(ctx context.Context, channel, key string) error {
	_, err := m.db.Exec(ctx, `UPDATE capacity_lab_mock_delivery SET acknowledgements=acknowledgements+1 WHERE channel=$1 AND key=$2`, channel, key)
	return err
}
func (m *notificationMock) ambiguous(ctx context.Context, channel string) bool {
	column := "ambiguous_email"
	if channel == "push" {
		column = "ambiguous_push"
	}
	var n int
	return m.db.QueryRow(ctx, "UPDATE capacity_lab_mock_control SET "+column+"="+column+"-1 WHERE id=1 AND "+column+">0 RETURNING 1").Scan(&n) == nil
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
			tag, err := m.db.Exec(req.Context(), `UPDATE capacity_lab_mock_delivery SET receipts=receipts+1 WHERE ticket=$1 AND acknowledgements>0`, id)
			if err != nil {
				return nil, err
			}
			if tag.RowsAffected() != 1 {
				return nil, fmt.Errorf("unknown mock receipt")
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
func runNotificationWorker(ctx context.Context, db *pgxpool.Pool) error {
	if err := requireSessionLab(ctx, db); err != nil {
		return err
	}
	if err := os.Setenv("SSL_CERT_FILE", "/exchange/mock-cert.pem"); err != nil {
		return err
	}
	certificate, err := tls.LoadX509KeyPair("/exchange/mock-cert.pem", "/exchange/mock-key.pem")
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:2525")
	if err != nil {
		return err
	}
	defer listener.Close()
	mock := &notificationMock{db: db}
	http.DefaultTransport = mock // no external network dialer
	go func() { <-ctx.Done(); _ = listener.Close() }()
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go mock.smtp(ctx, connection, certificate)
		}
	}()
	cfg := config.Config{WorkerRoles: "email,push,push_receipts", SMTPHost: "localhost", SMTPPort: "2525", SMTPUsername: "lab", SMTPPassword: "lab-disposable-only", SMTPFromEmail: "lab@example.invalid", SMTPFromName: "Isolated lab"}
	workers.Run(ctx, db, cfg)
	return nil
}

func verifyNotificationLab(ctx context.Context, db *pgxpool.Pool, out io.Writer) error {
	if err := requireSessionLab(ctx, db); err != nil {
		return err
	}
	var report []byte
	err := db.QueryRow(ctx, `SELECT json_build_object('time',now(),'channels',
 (SELECT json_agg(q) FROM(SELECT channel,count(*) AS unique_provider_effect_keys,sum(attempts) AS transport_attempts,sum(acceptances) AS provider_acceptances,sum(acknowledgements) AS transport_acknowledgements,sum(receipts) AS mocked_receipt_acknowledgements,count(*) FILTER(WHERE acceptances>1) AS repeated_provider_acceptance_keys FROM capacity_lab_mock_delivery GROUP BY channel)q),
 'email_status',(SELECT json_object_agg(status,n) FROM(SELECT status,count(*) n FROM email_outbox GROUP BY status)x),
 'push_status',(SELECT json_object_agg(status,n) FROM(SELECT status,count(*) n FROM notification_push_deliveries GROUP BY status)x),
 'due_receipt_backlog',(SELECT count(*) FROM notification_push_deliveries WHERE status='sent' AND receipt_checked_at IS NULL AND sent_at<=now()-interval '15 minutes'),
 'email_sent_without_mock_ack',(SELECT count(*) FROM email_outbox e LEFT JOIN capacity_lab_mock_delivery m ON m.channel='email' AND m.key=e.id::text WHERE e.status='sent' AND COALESCE(m.acknowledgements,0)=0),
 'push_sent_without_mock_ack',(SELECT count(*) FROM notification_push_deliveries d JOIN user_push_tokens t ON t.id=d.push_token_id LEFT JOIN capacity_lab_mock_delivery m ON m.channel='push' AND m.key=encode(digest(d.notification_id::text||':'||t.expo_push_token,'sha256'),'hex') WHERE d.status IN('sent','delivered') AND COALESCE(m.acknowledgements,0)=0),
 'killed_jobs',(SELECT count(*) FROM capacity_lab_killed_jobs),
 'killed_jobs_recovered',(SELECT count(*) FROM capacity_lab_killed_jobs k WHERE CASE WHEN k.channel='email' THEN EXISTS(SELECT 1 FROM email_outbox e WHERE e.id::text=k.key AND e.status='sent') ELSE EXISTS(SELECT 1 FROM notification_push_deliveries d WHERE d.notification_id::text||':'||d.push_token_id::text=k.key AND d.status IN('sent','delivered')) END))`).Scan(&report)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(report))
	return err
}
