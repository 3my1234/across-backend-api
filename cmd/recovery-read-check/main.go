// recovery-read-check exercises real read handlers inside the isolated restore
// container. It has no host/URI argument, loads no environment configuration,
// starts no listener/worker, and never emits returned application data.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http/httptest"
	"os"
	"time"

	"across/backend/internal/config"
	"across/backend/internal/controllers"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type outcome struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	HTTP   int    `json:"http_status,omitempty"`
}

func main() {
	log.SetOutput(io.Discard)
	results, err := run()
	if err != nil {
		// Do not print database errors, actor IDs, paths or handler responses.
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"application_reads_passed": false, "checks": results})
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"application_reads_passed": true, "checks": results,
		"scope": "sampled real read handlers; not live integrations, login middleware, all records or device behavior"})
}

func run() ([]outcome, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, item := range interfaces {
		if item.Flags&net.FlagLoopback == 0 {
			return nil, errors.New("network isolation required")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	// Fixed Unix socket inside the Docker network-none recovery target only.
	cfg, err := pgxpool.ParseConfig("host=/var/run/postgresql user=recovery_reader dbname=recovery_drill sslmode=disable connect_timeout=3")
	if err != nil {
		return nil, err
	}
	cfg.MaxConns, cfg.MinConns = 2, 0
	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "1000"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	var safe bool
	err = pool.QueryRow(ctx, `SELECT current_database()='recovery_drill' AND current_user='recovery_reader'
	 AND current_setting('transaction_read_only')='on' AND NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole
	 FROM pg_roles WHERE rolname=current_user`).Scan(&safe)
	if err != nil || !safe {
		return nil, errors.New("read-only target required")
	}
	// Empty credentials, no .env/config.Load, no external client invocation.
	settings := config.Config{AppEnv: "test"}
	market := controllers.NewProviderMarketplaceController(pool, settings)
	community := controllers.NewCommunityController(pool, settings)
	admin := controllers.NewAdminController(pool, settings)
	results := []outcome{}
	_, writeErr := pool.Exec(ctx, `UPDATE users SET full_name=full_name WHERE false`)
	var writePG *pgconn.PgError
	if !errors.As(writeErr, &writePG) || (writePG.Code != "25006" && writePG.Code != "42501") {
		return results, errors.New("write permission unexpectedly available")
	}
	results = append(results, outcome{Name: "write_denied", Status: "pass"})
	check := func(name, actor, path string, handler fiber.Handler, expected ...int) error {
		app := fiber.New(fiber.Config{DisableStartupMessage: true, ErrorHandler: func(c *fiber.Ctx, e error) error {
			code := 500
			var fe *fiber.Error
			if errors.As(e, &fe) {
				code = fe.Code
			}
			return c.SendStatus(code)
		}})
		app.Use(func(c *fiber.Ctx) error {
			defer func() {
				if recover() != nil {
					_ = c.SendStatus(500)
				}
			}()
			c.Locals("user_id", actor)
			c.SetUserContext(ctx)
			return c.Next()
		})
		app.Get("/read/:conversation_id?", handler)
		response, callErr := app.Test(httptest.NewRequest("GET", path, nil), 7000)
		result := outcome{Name: name, Status: "fail"}
		if callErr == nil {
			result.HTTP = response.StatusCode
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
			response.Body.Close()
			for _, code := range expected {
				if response.StatusCode == code && readErr == nil && len(body) <= 2*1024*1024 && (code != 200 || json.Valid(body)) {
					result.Status = "pass"
				}
			}
		}
		results = append(results, result)
		if result.Status != "pass" {
			return errors.New("read handler failed")
		}
		return nil
	}
	unknown := uuid.NewString()
	for _, item := range []struct {
		name    string
		handler fiber.Handler
	}{
		{"subscription_plans", market.ListPlans}, {"business_overview", admin.BusinessOverview},
		{"paid_subscribers", admin.Subscribers}, {"stored_transactions", admin.ListTransactions}, {"town_hall_feed", community.HallList},
	} {
		if err := check(item.name, unknown, "/read?limit=5", item.handler, 200); err != nil {
			return results, err
		}
	}
	var owner string
	err = pool.QueryRow(ctx, `SELECT p.owner_user_id::text FROM provider_organizations p
	 JOIN provider_members m ON m.provider_id=p.id AND m.user_id=p.owner_user_id AND m.is_active
	 WHERE p.is_active ORDER BY p.id LIMIT 1`).Scan(&owner)
	if err == pgx.ErrNoRows {
		results = append(results, outcome{Name: "provider_reads", Status: "not_exercised"})
	} else if err != nil {
		return results, err
	} else {
		for _, item := range []struct {
			name    string
			handler fiber.Handler
		}{
			{"provider_profile", market.MyProvider}, {"verification_documents", market.ListVerificationDocuments},
			{"renewal_preferences", market.ProviderRenewalStatus}, {"provider_inbox", market.ListProviderConversations},
			{"unified_inbox", community.Inbox},
		} {
			if err := check(item.name, owner, "/read?limit=5", item.handler, 200); err != nil {
				return results, err
			}
		}
	}
	var conversation, buyer, providerOwner string
	err = pool.QueryRow(ctx, `SELECT c.id::text,c.user_id::text,p.owner_user_id::text
	 FROM provider_conversations c JOIN provider_organizations p ON p.id=c.provider_id
	 JOIN provider_members m ON m.provider_id=p.id AND m.user_id=p.owner_user_id AND m.is_active
	 WHERE p.is_active ORDER BY c.id LIMIT 1`).Scan(&conversation, &buyer, &providerOwner)
	if err == pgx.ErrNoRows {
		results = append(results, outcome{Name: "ordinary_history", Status: "not_exercised"})
	} else if err != nil {
		return results, err
	} else {
		path := "/read/" + conversation + "?limit=5&mark_read=false"
		if err = check("buyer_history", buyer, path, market.BuyerConversationMessages, 200); err != nil {
			return results, err
		}
		if err = check("provider_history", providerOwner, path, market.ProviderConversationMessages, 200); err != nil {
			return results, err
		}
		if err = check("ordinary_history_denies_stranger", unknown, path, market.BuyerConversationMessages, 403, 404); err != nil {
			return results, err
		}
	}
	var room, member string
	err = pool.QueryRow(ctx, `SELECT r.id::text,p.user_id::text FROM chat_rooms r
	 JOIN chat_participants p ON p.room_id=r.id AND p.state='active'
	 WHERE r.kind='direct' ORDER BY r.id,p.user_id LIMIT 1`).Scan(&room, &member)
	if err == pgx.ErrNoRows {
		results = append(results, outcome{Name: "shared_history", Status: "not_exercised"})
	} else if err != nil {
		return results, err
	} else {
		path := "/read/" + room + "?limit=5&mark_read=false"
		if err = check("shared_history", member, path, community.Messages, 200); err != nil {
			return results, err
		}
		if err = check("shared_history_denies_stranger", unknown, path, community.Messages, 403, 404); err != nil {
			return results, err
		}
	}
	var hall, hallOwner string
	var hallStatus int
	err = pool.QueryRow(ctx, `SELECT r.id::text,r.owner_user_id::text,
	 CASE WHEN r.state='open' AND h.engagement_deadline>clock_timestamp() THEN 200 ELSE 410 END
	 FROM chat_rooms r JOIN town_hall_requests h ON h.room_id=r.id
	 JOIN chat_participants p ON p.room_id=r.id AND p.user_id=r.owner_user_id AND p.state='active'
	 ORDER BY r.id LIMIT 1`).Scan(&hall, &hallOwner, &hallStatus)
	if err == pgx.ErrNoRows {
		results = append(results, outcome{Name: "town_hall_history", Status: "not_exercised"})
	} else if err != nil {
		return results, err
	} else {
		path := "/read/" + hall + "?limit=5&mark_read=false"
		if err = check("town_hall_history", hallOwner, path, community.Messages, hallStatus); err != nil {
			return results, err
		}
		if err = check("town_hall_history_denies_stranger", unknown, path, community.Messages, 403, 404); err != nil {
			return results, err
		}
	}
	return results, nil
}
