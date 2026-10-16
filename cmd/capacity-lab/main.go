// capacity-lab is deliberately fixed to the disposable Compose lab. It accepts
// no database URL, bearer token, target hostname or production configuration.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"across/backend/internal/auth"
	"across/backend/internal/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const database = "postgres://lab:lab-disposable-only@postgres:5432/capacity_lab?sslmode=disable"
const secret = "capacity-lab-not-a-production-secret"
const origin = "http://gateway:8080"

type actor struct{ Buyer, ProviderUser, Provider, Listing, Conversation, Room, Ticket string }

func main() {
	if os.Getenv("CAPACITY_LAB") != "disposable-compose-only" || len(os.Args) != 2 {
		log.Fatal("Use only the isolated capacity Compose workflow")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(database)
	must(err)
	cfg.MaxConns = 4
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	must(err)
	defer db.Close()
	switch os.Args[1] {
	case "seed":
		must(migrations.Run(ctx, db))
		must(seed(ctx, db))
	case "run":
		must(run(ctx, db))
	default:
		log.Fatal("Expected seed or run")
	}
}
func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func seed(ctx context.Context, db *pgxpool.Pool) error {
	// No conflict updates or truncate: a repeat must fail rather than mutate a
	// database that isn't the fresh lab. No production data or secrets are copied.
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `CREATE TABLE capacity_lab_identity (marker text PRIMARY KEY);
 INSERT INTO capacity_lab_identity VALUES ('disposable-compose-only');
 CREATE TABLE capacity_lab_actors (buyer uuid,provider_user uuid,provider uuid,listing uuid,conversation uuid,room uuid,ticket uuid);
 INSERT INTO capacity_lab_actors SELECT gen_random_uuid(),gen_random_uuid(),gen_random_uuid(),gen_random_uuid(),gen_random_uuid(),gen_random_uuid(),gen_random_uuid() FROM generate_series(1,100);
 INSERT INTO countries_config(country_code,currency_code) VALUES ('NG','NGN') ON CONFLICT(country_code) DO NOTHING;
 INSERT INTO users(id,country_id,email,password_hash,full_name,email_verified)
 SELECT id,(SELECT id FROM countries_config WHERE country_code='NG'),id::text||'@example.invalid','unusable','Synthetic lab actor',true
 FROM (SELECT buyer id FROM capacity_lab_actors UNION ALL SELECT provider_user FROM capacity_lab_actors) u;
 INSERT INTO provider_organizations(id,owner_user_id,business_name,slug,contact_email,contact_phone,verification_status)
 SELECT provider,provider_user,'Synthetic lab provider',provider::text,'lab@example.invalid','lab','approved' FROM capacity_lab_actors;
 INSERT INTO provider_members(provider_id,user_id,role) SELECT provider,provider_user,'owner' FROM capacity_lab_actors;
 INSERT INTO provider_subscription_plans(code,name,amount_ngn,listing_limit,badge_tier) VALUES ('capacity-lab-paid','Synthetic paid plan',500,5,'bronze');
 INSERT INTO provider_subscriptions(provider_id,plan_id,status,tx_ref,customer_email,starts_at,current_period_end,expected_amount_ngn)
 SELECT provider,(SELECT id FROM provider_subscription_plans WHERE code='capacity-lab-paid'),'active',provider::text,'lab@example.invalid',now()-interval '1 day',now()+interval '30 days',500 FROM capacity_lab_actors;
 INSERT INTO provider_subscription_payments(subscription_id,flutterwave_transaction_id,tx_ref,amount,currency_code)
 SELECT id,id::text,tx_ref,500,'NGN' FROM provider_subscriptions WHERE customer_email='lab@example.invalid';
 INSERT INTO provider_listings(id,provider_id,listing_type,title,slug,description,category,status,published_at,city)
 SELECT listing,provider,'hotel','Synthetic hotel',listing::text,'Synthetic hotel listing','hotel','approved',now(),'Abuja' FROM capacity_lab_actors;
 INSERT INTO products(provider_id,sku,title,description,category_path,cost_price_rmb,local_selling_price,exchange_rate_snapshot,inventory_count,moderation_status,published_at,fulfillment_mode)
 SELECT provider,provider::text,'Synthetic trousers','Synthetic catalogue item',ARRAY['Fashion','Trousers'],1,1000,1,100,'approved',now(),'merchant_local' FROM capacity_lab_actors;
 INSERT INTO provider_conversations(id,listing_id,provider_id,user_id) SELECT conversation,listing,provider,buyer FROM capacity_lab_actors;
 INSERT INTO provider_conversation_messages(conversation_id,sender_type,sender_user_id,body)
 SELECT conversation,'buyer',buyer,'Synthetic prior message' FROM capacity_lab_actors CROSS JOIN generate_series(1,100);
 INSERT INTO chat_rooms(id,kind,owner_user_id,title) SELECT room,'town_hall',buyer,'Synthetic request' FROM capacity_lab_actors;
 INSERT INTO chat_rooms(kind,owner_user_id,title,created_at) SELECT 'town_hall',buyer,'Synthetic older request',now()-i*interval '1 second' FROM capacity_lab_actors CROSS JOIN generate_series(1,19) i;
 INSERT INTO town_hall_requests(room_id,description,location,unit,message_only,first_provider_engaged_at)
 SELECT id,'Synthetic request','','',true,now() FROM chat_rooms;
 INSERT INTO chat_participants(room_id,user_id,role) SELECT id,owner_user_id,'buyer' FROM chat_rooms;
 INSERT INTO chat_participants(room_id,user_id,provider_id,role) SELECT room,provider_user,provider,'provider' FROM capacity_lab_actors;
 INSERT INTO chat_messages(room_id,sender_user_id,body,client_message_id) SELECT id,owner_user_id,'Synthetic prior message',gen_random_uuid() FROM chat_rooms CROSS JOIN generate_series(1,10);
 INSERT INTO town_hall_offers(room_id,provider_id,sender_user_id,unit_price,terms) SELECT room,provider,provider_user,1000,'Synthetic prior offer' FROM capacity_lab_actors;
 INSERT INTO support_tickets(id,user_id,subject,message) SELECT ticket,buyer,'Synthetic support','Synthetic support' FROM capacity_lab_actors;
 INSERT INTO support_messages(ticket_id,sender_type,sender_id,message) SELECT ticket,'user',buyer,'Synthetic prior reply' FROM capacity_lab_actors CROSS JOIN generate_series(1,20);`)
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	_, err = db.Exec(ctx, "ANALYZE")
	return err
}

type sample struct {
	Name    string
	Status  int
	Elapsed time.Duration
	Write   bool
	Backend string
	Valid   bool
}
type metric struct {
	Requests int     `json:"requests"`
	Success  int     `json:"success"`
	Busy     int     `json:"busy"`
	Errors   int     `json:"errors"`
	P95MS    float64 `json:"successful_p95_ms"`
	times    []time.Duration
}

func (m *metric) add(s sample) {
	m.Requests++
	if s.Valid {
		m.Success++
		m.times = append(m.times, s.Elapsed)
	} else if s.Status == 429 || s.Status == 503 {
		m.Busy++
	} else {
		m.Errors++
	}
}
func (m *metric) finish() {
	sort.Slice(m.times, func(i, j int) bool { return m.times[i] < m.times[j] })
	if len(m.times) > 0 {
		m.P95MS = float64(m.times[(len(m.times)*95+99)/100-1].Microseconds()) / 1000
	}
}

type operation struct {
	Name, Path, Body, Token string
	Want                    int
	Write                   bool
}

func operations(a actor, n int, prefix string) []operation {
	buyer, _, _ := auth.Sign(a.Buyer, secret, time.Hour)
	provider, _, _ := auth.Sign(a.ProviderUser, secret, time.Hour)
	body := func() string {
		b, _ := json.Marshal(map[string]string{"message": prefix + uuid.NewString(), "client_message_id": uuid.NewString()})
		return string(b)
	}
	ops := []operation{
		{Name: "catalogue", Path: "/api/v1/products?limit=20", Want: 200},
		{Name: "services", Path: "/api/v1/marketplace/listings?limit=20", Want: 200},
		{Name: "town_hall_feed", Path: "/api/v1/chat/town-hall?limit=25", Token: buyer, Want: 200},
		{Name: "buyer_history", Path: "/api/v1/marketplace/conversations/" + a.Conversation + "/messages?limit=40&mark_read=false", Token: buyer, Want: 200},
		{Name: "provider_history", Path: "/api/v1/providers/me/conversations/" + a.Conversation + "/messages?limit=40&mark_read=false", Token: provider, Want: 200},
		{Name: "support_history", Path: "/api/v1/support/tickets/" + a.Ticket + "/messages?limit=40", Token: buyer, Want: 200},
	}
	if n%3 == 0 {
		ops = append(ops, operation{Name: "buyer_send", Path: "/api/v1/marketplace/conversations/" + a.Conversation + "/messages", Token: buyer, Body: body(), Want: 201, Write: true})
	}
	if n%3 == 1 {
		ops = append(ops, operation{Name: "provider_send", Path: "/api/v1/providers/me/conversations/" + a.Conversation + "/messages", Token: provider, Body: body(), Want: 201, Write: true})
	}
	if n%3 == 2 {
		ops = append(ops, operation{Name: "bargaining_send", Path: "/api/v1/chat/conversations/" + a.Room + "/messages", Token: buyer, Body: body(), Want: 201, Write: true})
	}
	if n%5 == 0 {
		b, _ := json.Marshal(map[string]any{"unit_price": 1000, "terms": prefix + uuid.NewString(), "client_message_id": uuid.NewString()})
		ops = append(ops, operation{Name: "town_hall_offer", Path: "/api/v1/chat/town-hall/" + a.Room + "/offers", Token: provider, Body: string(b), Want: 204, Write: true})
	}
	if n%7 == 0 {
		ops = append(ops, operation{Name: "support_reply", Path: "/api/v1/support/tickets/" + a.Ticket + "/reply", Token: buyer, Body: body(), Want: 201, Write: true})
	}
	if n%11 == 0 {
		b, _ := json.Marshal(map[string]string{"message": prefix + uuid.NewString(), "client_request_id": uuid.NewString()})
		ops = append(ops, operation{Name: "town_hall_create", Path: "/api/v1/chat/town-hall", Token: buyer, Body: string(b), Want: 201, Write: true})
	}
	return ops
}

func client() *http.Client {
	return &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{MaxIdleConns: 400, MaxIdleConnsPerHost: 200}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func request(c *http.Client, op operation, index int) sample {
	method := "GET"
	if op.Body != "" {
		method = "POST"
	}
	req, _ := http.NewRequest(method, origin+op.Path, bytes.NewBufferString(op.Body))
	req.Header.Set("Authorization", "Bearer "+op.Token)
	req.Header.Set("Content-Type", "application/json")
	// Synthetic distinct clients through the lab-only proxy; real Redis rate
	// limits remain enabled. Production proxy trust is not modified here.
	req.Header.Set("X-Forwarded-For", fmt.Sprintf("192.0.2.%d", index+1))
	began := time.Now()
	res, err := c.Do(req)
	s := sample{Name: op.Name, Write: op.Write}
	if err != nil {
		s.Elapsed = time.Since(began)
		return s
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	s.Elapsed = time.Since(began)
	s.Status = res.StatusCode
	s.Backend = res.Header.Get("X-Lab-Backend")
	s.Valid = err == nil && res.StatusCode == op.Want && (op.Want == 204 || json.Valid(raw))
	return s
}

func run(ctx context.Context, db *pgxpool.Pool) error {
	var marker string
	if err := db.QueryRow(ctx, "SELECT marker FROM capacity_lab_identity").Scan(&marker); err != nil || marker != "disposable-compose-only" {
		return fmt.Errorf("missing isolated lab identity")
	}
	rows, err := db.Query(ctx, "SELECT buyer::text,provider_user::text,provider::text,listing::text,conversation::text,room::text,ticket::text FROM capacity_lab_actors")
	if err != nil {
		return err
	}
	actors := []actor{}
	for rows.Next() {
		var a actor
		if err = rows.Scan(&a.Buyer, &a.ProviderUser, &a.Provider, &a.Listing, &a.Conversation, &a.Room, &a.Ticket); err != nil {
			return err
		}
		actors = append(actors, a)
	}
	rows.Close()
	if rows.Err() != nil {
		return rows.Err()
	}
	if len(actors) != 100 {
		return fmt.Errorf("expected 100 synthetic pairs")
	}
	duration := 20 * time.Second
	if os.Getenv("LAB_PROFILE") == "baseline" {
		duration = 90 * time.Second
	} else if os.Getenv("LAB_PROFILE") != "smoke" {
		return fmt.Errorf("LAB_PROFILE must be smoke or baseline")
	}
	c := client()
	defer c.CloseIdleConnections()
	prefix := "lab-run-" + uuid.NewString() + ":"
	// Fail fast on bad fixtures/routes instead of interpreting fast errors as capacity.
	for n := 0; n < 3; n++ {
		for _, op := range operations(actors[0], n, "preflight:") {
			s := request(c, op, 0)
			if !s.Valid {
				return fmt.Errorf("preflight %s status=%d", s.Name, s.Status)
			}
			if n == 0 {
				must(json.NewEncoder(os.Stdout).Encode(map[string]any{"preflight_operation": s.Name, "elapsed_ms": float64(s.Elapsed.Microseconds()) / 1000}))
			}
		}
	}
	var confirmed int
	var stageError error
	for _, users := range []int{20, 50, 100} {
		started := time.Now()
		deadline := started.Add(duration)
		samples := make(chan sample, 1000)
		var wg sync.WaitGroup
		for i := 0; i < users; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				for n := 0; time.Now().Before(deadline) && ctx.Err() == nil; n++ {
					for _, op := range operations(actors[i], n, prefix) {
						samples <- request(c, op, i)
					}
					time.Sleep(2 * time.Second)
				}
			}(i)
		}
		go func() { wg.Wait(); close(samples) }()
		metrics := map[string]*metric{}
		backends := map[string]int{}
		errors, busy, total := 0, 0, 0
		for s := range samples {
			if metrics[s.Name] == nil {
				metrics[s.Name] = &metric{}
			}
			metrics[s.Name].add(s)
			total++
			if s.Backend != "" {
				backends[s.Backend]++
			}
			if s.Write && s.Valid {
				confirmed++
			}
			if !s.Valid {
				if s.Status == 429 || s.Status == 503 {
					busy++
				} else {
					errors++
				}
			}
		}
		for _, m := range metrics {
			m.finish()
		}
		report := map[string]any{"profile": os.Getenv("LAB_PROFILE"), "simulated_active_pairs": users, "elapsed_seconds": time.Since(started).Seconds(), "requests_per_second": float64(total) / time.Since(started).Seconds(), "operations": metrics, "backends": backends, "unexpected_errors": errors, "busy": busy, "scope": "isolated Docker host; no internet/media/worker delivery capacity claim"}
		must(json.NewEncoder(os.Stdout).Encode(report))
		if errors > 0 || busy > 0 {
			stageError = fmt.Errorf("stage did not pass: errors=%d busy=%d; stop increasing load", errors, busy)
			break
		}
		if len(backends) != 2 {
			stageError = fmt.Errorf("both API replicas were not observed")
			break
		}
	}
	var stored int
	err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM provider_conversation_messages WHERE body LIKE $1)+(SELECT count(*) FROM chat_messages WHERE body LIKE $1 OR body LIKE $2)+(SELECT count(*) FROM support_messages WHERE message LIKE $1)+(SELECT count(*) FROM chat_rooms WHERE title LIKE $1)`, prefix+"%", "Offer:%"+prefix+"%").Scan(&stored)
	if err != nil {
		return err
	}
	must(json.NewEncoder(os.Stdout).Encode(map[string]any{"confirmed_writes": confirmed, "stored_writes": stored, "write_reconciliation_passed": stored == confirmed}))
	if stored != confirmed {
		return fmt.Errorf("confirmed and stored writes differ")
	}
	return stageError
}
