package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"across/backend/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const sessionPassword = "Lab-Only-Strong!123"

type sessionFixture struct {
	ID           string `json:"id"`
	Role         string `json:"role"`
	Email        string `json:"email"`
	Token        string `json:"token"`
	Peer         string `json:"peer_id"`
	Conversation string `json:"conversation"`
	Room         string `json:"room"`
	HotRoom      string `json:"hot_room"`
	Listing      string `json:"listing"`
	Product      string `json:"product"`
	Ticket       string `json:"ticket"`
}

func requireSessionLab(ctx context.Context, db *pgxpool.Pool) error {
	var name, marker string
	if err := db.QueryRow(ctx, `SELECT current_database(),marker FROM capacity_lab_identity`).Scan(&name, &marker); err != nil || name != "capacity_lab" || marker != "disposable-compose-only" {
		return fmt.Errorf("session operations require the disposable capacity_lab identity")
	}
	return nil
}

// This operates only on the internal Compose database. No production credentials
// or arbitrary target URLs are accepted by this executable.
func prepareSessions(ctx context.Context, db *pgxpool.Pool) error {
	if err := requireSessionLab(ctx, db); err != nil {
		return err
	}
	plan, err := scenarioPlan(os.Getenv("LAB_SCENARIO"))
	if err != nil {
		return err
	}
	if _, err = checkFixtures(ctx, db, plan); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(sessionPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// A repeated preparation fails rather than changing run identities under load.
	_, err = tx.Exec(ctx, `CREATE TABLE capacity_lab_session_run(run_id uuid PRIMARY KEY,admin_id uuid NOT NULL,created_at timestamptz DEFAULT now())`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO capacity_lab_session_run(run_id,admin_id) VALUES($1,$2)`, uuid.NewString(), uuid.NewString())
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE users SET password_hash=$1 WHERE id IN(SELECT buyer FROM capacity_lab_actors UNION ALL SELECT provider_user FROM capacity_lab_actors)`, string(hash))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO admins(id,email,full_name,password_hash,role) SELECT admin_id,'admin@example.invalid','Synthetic administrator',$1,'super_admin' FROM capacity_lab_session_run`, string(hash))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO user_push_tokens(user_id,expo_push_token,platform) SELECT id,'ExpoPushToken['||id::text||']','android' FROM users WHERE id IN(SELECT buyer FROM capacity_lab_actors UNION ALL SELECT provider_user FROM capacity_lab_actors)`)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func exportSessions(ctx context.Context, db *pgxpool.Pool, out io.Writer) error {
	if err := requireSessionLab(ctx, db); err != nil {
		return err
	}
	var runID, admin string
	var created time.Time
	if err := db.QueryRow(ctx, `SELECT run_id::text,admin_id::text,created_at FROM capacity_lab_session_run`).Scan(&runID, &admin, &created); err != nil {
		return err
	}
	rows, err := db.Query(ctx, `SELECT buyer::text,provider_user::text,listing::text,conversation::text,room::text,ticket::text,COALESCE(hot_room::text,''),(SELECT id::text FROM products p WHERE p.provider_id=a.provider ORDER BY p.created_at,p.id LIMIT 1) FROM capacity_lab_actors a ORDER BY slot`)
	if err != nil {
		return err
	}
	defer rows.Close()
	sessions := []sessionFixture{}
	for rows.Next() {
		var buyer, provider, listing, conversation, room, ticket, hot, product string
		if err = rows.Scan(&buyer, &provider, &listing, &conversation, &room, &ticket, &hot, &product); err != nil {
			return err
		}
		for _, side := range []struct{ id, peer, role string }{{buyer, provider, "buyer"}, {provider, buyer, "provider"}} {
			token, _, e := auth.Sign(side.id, secret, 6*time.Hour)
			if e != nil {
				return e
			}
			sessions = append(sessions, sessionFixture{side.id, side.role, side.id + "@example.invalid", token, side.peer, conversation, room, hot, listing, product, ticket})
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(sessions) == 0 {
		return fmt.Errorf("no synthetic sessions")
	}
	expired, _, err := auth.Sign(sessions[0].ID, secret, -time.Hour)
	if err != nil {
		return err
	}
	adminToken, _, err := auth.Sign(admin, secret, 6*time.Hour)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(map[string]any{"schema": 1, "marker": "disposable-compose-only", "origin": origin, "run_id": runID, "created_at": created, "expires_at": time.Now().Add(5 * time.Hour), "password": sessionPassword, "expired_token": expired, "admin_token": adminToken, "sessions": sessions, "scope": "synthetic HTTP polling; no external delivery or media transfers"})
}

func verifySessions(ctx context.Context, db *pgxpool.Pool, out io.Writer) error {
	if err := requireSessionLab(ctx, db); err != nil {
		return err
	}
	var runID string
	if err := db.QueryRow(ctx, `SELECT run_id::text FROM capacity_lab_session_run`).Scan(&runID); err != nil {
		return err
	}
	// Summary is generated by k6 even after thresholds fail. Missing evidence is a
	// failure, never interpreted as zero successful writes.
	raw, err := os.ReadFile("/exchange/summary.json")
	if err != nil {
		return fmt.Errorf("session evidence missing: %w", err)
	}
	var summary struct {
		RunID   string `json:"run_id"`
		Metrics map[string]struct {
			Values map[string]float64 `json:"values"`
		} `json:"metrics"`
	}
	if json.Unmarshal(raw, &summary) != nil || summary.RunID != runID {
		return fmt.Errorf("summary run identity mismatch")
	}
	writes, ok := summary.Metrics["confirmed_writes"]
	if !ok {
		return fmt.Errorf("confirmed write counter missing")
	}
	confirmed := int64(writes.Values["count"])
	var market, hall, offers, duplicate, receipts int64
	prefix := "sessions-" + runID + ":%"
	err = db.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM provider_conversation_messages WHERE body LIKE $1),
 (SELECT count(*) FROM chat_messages WHERE body LIKE $1),
 (SELECT count(*) FROM town_hall_offers WHERE terms LIKE $1),
 (SELECT count(*) FROM (SELECT conversation_id,body,count(*) FROM provider_conversation_messages WHERE body LIKE $1 GROUP BY conversation_id,body HAVING count(*)>1) d),
 (SELECT count(*) FROM message_receipts r JOIN provider_conversation_messages m ON m.id=r.message_id AND r.channel='business' WHERE m.body LIKE $1)`, prefix).Scan(&market, &hall, &offers, &duplicate, &receipts)
	if err != nil {
		return err
	}
	passed := confirmed == market+hall+offers && duplicate == 0
	if err = json.NewEncoder(out).Encode(map[string]any{"run_id": runID, "confirmed_writes": confirmed, "stored_marketplace_messages": market, "stored_town_hall_messages": hall, "stored_offers": offers, "duplicate_messages": duplicate, "delivery_receipt_rows": receipts, "write_reconciliation_passed": passed, "external_delivery_tested": false}); err != nil {
		return err
	}
	if !passed {
		return fmt.Errorf("confirmed/stored writes differ or duplicated messages")
	}
	if err = verifyRecipientVisibility(ctx, db, prefix, out); err != nil {
		return err
	}
	// Queue aggregates include generated work, without sending external messages.
	for _, table := range []string{"email_outbox", "notification_push_deliveries", "web_push_deliveries"} {
		var n int64
		if err = db.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			return err
		}
		if err = json.NewEncoder(out).Encode(map[string]any{"queue": table, "rows": n, "worker_drain_tested": false}); err != nil {
			return err
		}
	}
	return nil
}
