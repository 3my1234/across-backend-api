package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"across/backend/internal/middleware"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Isolated loopback PostgreSQL only. This checks real controller/SQL behavior
// under simultaneous work; it is NOT production hardware/network capacity.
func TestCommunityLaunchMixedLoad(t *testing.T) {
	db := communityTestDB(t)
	ctx := context.Background()
	buyer, stranger := uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(ctx, `INSERT INTO users(id,full_name) VALUES($1,'Synthetic buyer'),($2,'Synthetic outsider')`, buyer, stranger); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO chat_rooms(kind,owner_user_id,title,created_at)
 SELECT 'town_hall',$1::uuid,'Synthetic request',now()-i*interval '1 second' FROM generate_series(1,2000) i;
 `, buyer); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO town_hall_requests(room_id,description,location,unit,message_only,first_provider_engaged_at)
 SELECT id,'Synthetic request','','',true,now() FROM chat_rooms;
 INSERT INTO chat_participants(room_id,user_id,role) SELECT id,owner_user_id,'buyer' FROM chat_rooms;
 INSERT INTO chat_messages(room_id,sender_user_id,body,client_message_id)
 SELECT r.id,r.owner_user_id,'Synthetic history',gen_random_uuid() FROM chat_rooms r CROSS JOIN generate_series(1,10);`); err != nil {
		t.Fatal(err)
	}
	indexSQL, err := os.ReadFile("../../migrations/092_town_hall_feed_indexes.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(indexSQL)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `ANALYZE chat_rooms; ANALYZE town_hall_requests; ANALYZE chat_participants; ANALYZE chat_messages`); err != nil {
		t.Fatal(err)
	}
	var plan string
	if err = db.QueryRow(ctx, `EXPLAIN (FORMAT JSON) SELECT r.id FROM chat_rooms r JOIN town_hall_requests h ON h.room_id=r.id
 WHERE r.kind='town_hall' AND r.state='open' AND (h.first_provider_engaged_at IS NOT NULL OR h.engagement_deadline>clock_timestamp())
 ORDER BY r.created_at DESC,r.id DESC LIMIT 25`).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "town_hall_feed_created") {
		t.Fatalf("feed query did not select creation-order index: %s", plan)
	}
	var rooms []string
	rows, err := db.Query(ctx, `SELECT id::text FROM chat_rooms ORDER BY created_at DESC LIMIT 25`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		rooms = append(rooms, id)
	}
	rows.Close()
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	poolConfig := db.Config()
	poolConfig.MaxConns, poolConfig.MinConns = 8, 0
	bounded, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer bounded.Close()
	controller := &CommunityController{db: bounded, market: &ProviderMarketplaceController{db: bounded}}
	app := fiber.New()
	gate := middleware.NewAdmission(128)
	app.Use(gate.Handler)
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("Actor")); return c.Next() })
	app.Get("/hall", controller.HallList)
	app.Get("/rooms/:conversation_id/messages", controller.Messages)
	app.Post("/rooms/:conversation_id/messages", controller.Send)
	var acceptedWrites atomic.Int64
	for _, concurrency := range []int{20, 100, 400} {
		var group sync.WaitGroup
		start := make(chan struct{})
		latencies := make([]time.Duration, concurrency)
		acceptedFlags := make([]bool, concurrency)
		var accepted, rejected atomic.Int64
		for i := 0; i < concurrency; i++ {
			group.Add(1)
			go func(i int) {
				defer group.Done()
				<-start
				path, method, body := "/hall?limit=25", "GET", ""
				if i%3 != 0 {
					path = "/rooms/" + rooms[i%len(rooms)] + "/messages?limit=40&mark_read=false"
				}
				if i%5 == 0 {
					method = "POST"
					path = "/rooms/" + rooms[i%len(rooms)] + "/messages"
					body = fmt.Sprintf(`{"message":"Launch capacity write","client_message_id":%q}`, uuid.NewString())
				}
				req := httptest.NewRequest(method, path, strings.NewReader(body))
				req.Header.Set("Actor", buyer)
				req.Header.Set("Content-Type", "application/json")
				began := time.Now()
				response, err := app.Test(req, 15000)
				latencies[i] = time.Since(began)
				if err != nil {
					t.Error(err)
					return
				}
				defer response.Body.Close()
				if response.StatusCode == 503 {
					rejected.Add(1)
					if response.Header.Get("Retry-After") == "" {
						t.Error("overload omitted retry hint")
					}
					return
				}
				want := 200
				if method == "POST" {
					want = 201
				}
				if response.StatusCode != want {
					raw, _ := io.ReadAll(response.Body)
					t.Errorf("%s status=%d body=%s", method, response.StatusCode, raw)
					return
				}
				accepted.Add(1)
				acceptedFlags[i] = true
				if method == "POST" {
					acceptedWrites.Add(1)
				} else {
					var data struct {
						Items []json.RawMessage `json:"items"`
					}
					if err := json.NewDecoder(response.Body).Decode(&data); err != nil {
						t.Error(err)
					}
					limit := 40
					if strings.HasPrefix(path, "/hall") {
						limit = 25
					}
					if len(data.Items) > limit {
						t.Error("unbounded response page")
					}
				}
			}(i)
		}
		close(start)
		group.Wait()
		completed := []time.Duration{}
		for i, elapsed := range latencies {
			if acceptedFlags[i] {
				completed = append(completed, elapsed)
			}
		}
		sort.Slice(completed, func(i, j int) bool { return completed[i] < completed[j] })
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		var acceptedP95 time.Duration
		if len(completed) > 0 {
			acceptedP95 = completed[(len(completed)-1)*95/100]
		}
		t.Logf("isolated mixed request burst=%d accepted=%d busy=%d p95_accepted=%s p95_all=%s admission=128 db_pool=8 seed=2000rooms/20000messages (NOT production capacity)", concurrency, accepted.Load(), rejected.Load(), acceptedP95, latencies[(concurrency*95/100)-1])
		if accepted.Load()+rejected.Load() != int64(concurrency) {
			t.Fatal("requests failed outside controlled overload")
		}
		if concurrency == 20 && rejected.Load() != 0 {
			t.Fatal("below admission ceiling unexpectedly rejected work")
		}
		if concurrency == 400 && rejected.Load() == 0 {
			t.Fatal("overload scenario did not exercise admission")
		}
	}
	var committed int64
	if err := db.QueryRow(ctx, `SELECT count(*) FROM chat_messages WHERE body='Launch capacity write'`).Scan(&committed); err != nil {
		t.Fatal(err)
	}
	if committed != acceptedWrites.Load() {
		t.Fatalf("committed=%d confirmed=%d: rejected writes must have no side effect", committed, acceptedWrites.Load())
	}
	denied := httptest.NewRequest("GET", "/rooms/"+rooms[0]+"/messages", nil)
	denied.Header.Set("Actor", stranger)
	response, err := app.Test(denied, 5000)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("outsider gained access to private bargaining")
	}
}
