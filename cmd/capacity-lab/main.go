// capacity-lab is deliberately fixed to the disposable Compose lab. It accepts
// no database URL, bearer token, target hostname or production configuration.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
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

type actor struct{ Buyer, ProviderUser, Provider, Listing, Conversation, Room, Ticket, HotRoom string }

type fixturePlan struct {
	Name                                           string
	Pairs                                          int
	OlderRooms, ConversationHistory, ExtraListings int
	Busy                                           bool
}

func scenarioPlan(name string) (fixturePlan, error) {
	if name == "" {
		name = "standard"
	}
	pairs, err := actorPairCount(os.Getenv("LAB_ACTOR_PAIRS"))
	if err != nil {
		return fixturePlan{}, err
	}
	p := fixturePlan{Name: name, Pairs: pairs, OlderRooms: 19, ConversationHistory: 100}
	switch name {
	case "standard":
	case "busy":
		p.Busy = true
	case "large":
		p.OlderRooms = 199
		p.ConversationHistory = 1000
		p.ExtraListings = 9
	case "large-busy":
		p.OlderRooms = 199
		p.ConversationHistory = 1000
		p.ExtraListings = 9
		p.Busy = true
	default:
		return p, fmt.Errorf("LAB_SCENARIO must be standard, busy, large or large-busy")
	}
	return p, nil
}

func actorPairCount(value string) (int, error) {
	if value == "" {
		return 100, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 10 || n > 6000 || n%10 != 0 {
		return 0, fmt.Errorf("LAB_ACTOR_PAIRS must be a multiple of 10 between 10 and 6000")
	}
	return n, nil
}

func databaseCPU(value string) (string, error) {
	if value == "" {
		return "1", nil
	}
	if value != "1" && value != "2" {
		return "", fmt.Errorf("LAB_DB_CPUS must be 1 or 2")
	}
	return value, nil
}

func main() {
	if os.Getenv("CAPACITY_LAB") != "disposable-compose-only" || len(os.Args) != 2 {
		log.Fatal("Use only the isolated capacity Compose workflow")
	}
	_, cpuError := databaseCPU(os.Getenv("LAB_DB_CPUS"))
	must(cpuError)
	limit := 12 * time.Minute
	if os.Args[1] == "observe-sessions" {
		limit = 40 * time.Minute
	}
	// A 30-minute workload needs at least another 15 minutes for its final
	// receipts to become eligible. Keep mocks/worker bounded but alive through
	// that drain window; the workload observer retains its original lifetime.
	if os.Args[1] == "notification-worker" || os.Args[1] == "notification-provider" {
		limit = 65 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	if os.Args[1] == "notification-provider" {
		must(runNotificationProvider(ctx))
		return
	}
	cfg, err := pgxpool.ParseConfig(database)
	must(err)
	if os.Args[1] == "notification-worker" {
		cfg.ConnConfig.User = "lab_worker"
		cfg.ConnConfig.Password = "lab-worker-disposable"
	}
	cfg.MaxConns = 4
	cfg.ConnConfig.RuntimeParams["application_name"] = "lab-" + os.Args[1]
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	must(err)
	defer db.Close()
	switch os.Args[1] {
	case "seed":
		must(migrations.Run(ctx, db))
		must(seed(ctx, db))
	case "run":
		must(run(ctx, db))
	case "comparison-prepare":
		must(prepareComparison(ctx, db))
	case "release-check":
		must(releaseJourneys(ctx, db))
	case "export":
		must(exportSessions(ctx, db, os.Stdout))
	case "prepare-sessions":
		must(prepareSessions(ctx, db))
		must(exportSessions(ctx, db, os.Stdout))
	case "signup-bursts":
		must(signupBursts(ctx, db, os.Stdout))
	case "login-bursts":
		must(loginBursts(ctx, db, os.Stdout))
	case "observe-sessions":
		must(observeSessions(ctx, db, os.Stdout))
	case "verify-sessions":
		must(verifySessions(ctx, db, os.Stdout))
	case "lifecycle":
		must(sessionLifecycle(ctx, db, os.Stdout))
	case "notification-prepare":
		must(prepareNotificationLab(ctx, db))
	case "notification-worker":
		must(runNotificationWorker(ctx, db))
	case "notification-verify":
		must(verifyNotificationLab(ctx, db, os.Stdout))
	case "queue-check":
		must(sessionQueueCheck(ctx, db, os.Stdout))
	case "media-check":
		must(sessionMediaCheck(ctx, db, os.Stdout))
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
	plan, err := scenarioPlan(os.Getenv("LAB_SCENARIO"))
	if err != nil {
		return err
	}
	// No conflict updates or truncate: a repeat must fail rather than mutate a
	// database that isn't the fresh lab. No production data or secrets are copied.
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, fmt.Sprintf(`CREATE TABLE capacity_lab_identity (marker text PRIMARY KEY,scenario text NOT NULL);
 INSERT INTO capacity_lab_identity VALUES ('disposable-compose-only','%s');
 CREATE TABLE capacity_lab_actors (buyer uuid,provider_user uuid,provider uuid,listing uuid,conversation uuid,room uuid,ticket uuid,slot int UNIQUE,hot_room uuid);
 INSERT INTO capacity_lab_actors SELECT gen_random_uuid(),gen_random_uuid(),gen_random_uuid(),gen_random_uuid(),gen_random_uuid(),gen_random_uuid(),gen_random_uuid(),i,NULL FROM generate_series(1,%d) i;
 INSERT INTO countries_config(country_code,currency_code) VALUES ('NG','NGN') ON CONFLICT(country_code) DO NOTHING;
 INSERT INTO users(id,country_id,email,password_hash,full_name,email_verified)
 SELECT id,(SELECT id FROM countries_config WHERE country_code='NG'),id::text||'@example.invalid','unusable','Synthetic lab actor',true
 FROM (SELECT buyer id FROM capacity_lab_actors UNION ALL SELECT provider_user FROM capacity_lab_actors) u;
 INSERT INTO provider_organizations(id,owner_user_id,business_name,slug,contact_email,contact_phone,verification_status)
 SELECT provider,provider_user,'Synthetic lab provider',provider::text,'lab@example.invalid','lab','approved' FROM capacity_lab_actors;
 INSERT INTO provider_members(provider_id,user_id,role) SELECT provider,provider_user,'owner' FROM capacity_lab_actors;
 INSERT INTO provider_subscription_plans(code,name,amount_ngn,listing_limit,badge_tier) VALUES ('capacity-lab-paid','Synthetic Gold plan',5000,60,'gold');
 INSERT INTO provider_subscriptions(provider_id,plan_id,status,tx_ref,customer_email,starts_at,current_period_end,expected_amount_ngn)
 SELECT provider,(SELECT id FROM provider_subscription_plans WHERE code='capacity-lab-paid'),'active',provider::text,'lab@example.invalid',now()-interval '1 day',now()+interval '30 days',5000 FROM capacity_lab_actors;
 INSERT INTO provider_subscription_payments(subscription_id,flutterwave_transaction_id,tx_ref,amount,currency_code)
 SELECT id,id::text,tx_ref,5000,'NGN' FROM provider_subscriptions WHERE customer_email='lab@example.invalid';
 INSERT INTO provider_listings(id,provider_id,listing_type,title,slug,description,category,status,published_at,city)
 SELECT listing,provider,'hotel','Synthetic hotel',listing::text,'Synthetic hotel listing','hotel','approved',now(),'Abuja' FROM capacity_lab_actors;
 INSERT INTO provider_listings(provider_id,listing_type,title,slug,description,category,status,published_at,city)
 SELECT provider,'hotel','Synthetic extra hotel',provider::text||'-'||i,'Synthetic hotel listing','hotel','approved',now(),'Abuja' FROM capacity_lab_actors CROSS JOIN generate_series(1,%d) i;
 INSERT INTO products(provider_id,sku,title,description,category_path,cost_price_rmb,local_selling_price,exchange_rate_snapshot,inventory_count,moderation_status,published_at,fulfillment_mode)
 SELECT provider,provider::text,'Synthetic trousers','Synthetic catalogue item',ARRAY['Fashion','Trousers'],1,1000,1,100,'approved',now(),'merchant_local' FROM capacity_lab_actors;
 INSERT INTO products(provider_id,sku,title,description,category_path,cost_price_rmb,local_selling_price,exchange_rate_snapshot,inventory_count,moderation_status,published_at,fulfillment_mode)
 SELECT provider,provider::text||'-'||i,'Synthetic extra trousers','Synthetic catalogue item',ARRAY['Fashion','Trousers'],1,1000,1,100,'approved',now(),'merchant_local' FROM capacity_lab_actors CROSS JOIN generate_series(1,%d) i;
 INSERT INTO product_delivery_areas(product_id,country_code,delivered_price,currency_code)
 SELECT id,'NG',1000,'NGN' FROM products WHERE provider_id IN (SELECT provider FROM capacity_lab_actors);
 INSERT INTO provider_conversations(id,listing_id,provider_id,user_id) SELECT conversation,listing,provider,buyer FROM capacity_lab_actors;
 -- Historical import only: avoid repeatedly updating the same revision row
 -- thousands of times in a single seed transaction. Restore the equivalent
 -- revision and re-enable the named trigger before any API starts.
 ALTER TABLE provider_conversation_messages DISABLE TRIGGER provider_chat_revision;
 INSERT INTO provider_conversation_messages(conversation_id,sender_type,sender_user_id,body)
 SELECT conversation,'buyer',buyer,'Synthetic prior message' FROM capacity_lab_actors CROSS JOIN generate_series(1,%d);
 UPDATE provider_conversations c SET history_revision=h.total FROM (SELECT conversation_id,count(*) AS total FROM provider_conversation_messages GROUP BY conversation_id) h WHERE c.id=h.conversation_id;
 ALTER TABLE provider_conversation_messages ENABLE TRIGGER provider_chat_revision;
 INSERT INTO chat_rooms(id,kind,owner_user_id,title) SELECT room,'town_hall',buyer,'Synthetic request' FROM capacity_lab_actors;
 INSERT INTO chat_rooms(kind,owner_user_id,title,created_at) SELECT 'town_hall',buyer,'Synthetic older request',now()-i*interval '1 second' FROM capacity_lab_actors CROSS JOIN generate_series(1,%d) i;
 INSERT INTO town_hall_requests(room_id,description,location,unit,message_only,first_provider_engaged_at)
 SELECT id,'Synthetic request','','',true,now() FROM chat_rooms;
 INSERT INTO chat_participants(room_id,user_id,role) SELECT id,owner_user_id,'buyer' FROM chat_rooms;
 INSERT INTO chat_participants(room_id,user_id,provider_id,role) SELECT room,provider_user,provider,'provider' FROM capacity_lab_actors;
 INSERT INTO chat_messages(room_id,sender_user_id,body,client_message_id) SELECT id,owner_user_id,'Synthetic prior message',gen_random_uuid() FROM chat_rooms CROSS JOIN generate_series(1,10);
 INSERT INTO town_hall_offers(room_id,provider_id,sender_user_id,unit_price,terms) SELECT room,provider,provider_user,1000,'Synthetic prior offer' FROM capacity_lab_actors;
 INSERT INTO support_tickets(id,user_id,subject,message) SELECT ticket,buyer,'Synthetic support','Synthetic support' FROM capacity_lab_actors;
 INSERT INTO support_messages(ticket_id,sender_type,sender_id,message) SELECT ticket,'user',buyer,'Synthetic prior reply' FROM capacity_lab_actors CROSS JOIN generate_series(1,20);`, plan.Name, plan.Pairs, plan.ExtraListings, plan.ExtraListings, plan.ConversationHistory, plan.OlderRooms))
	if err != nil {
		return err
	}
	if plan.Busy {
		_, err = tx.Exec(ctx, `CREATE TABLE capacity_lab_hotspots AS SELECT (slot-1)/10 AS group_id,gen_random_uuid() AS room,min(buyer::text)::uuid AS owner FROM capacity_lab_actors GROUP BY (slot-1)/10;
 UPDATE capacity_lab_actors a SET hot_room=h.room FROM capacity_lab_hotspots h WHERE (a.slot-1)/10=h.group_id;
 INSERT INTO chat_rooms(id,kind,owner_user_id,title) SELECT room,'town_hall',owner,'Synthetic busy room' FROM capacity_lab_hotspots;
 INSERT INTO town_hall_requests(room_id,description,location,unit,message_only,first_provider_engaged_at) SELECT room,'Synthetic busy request','','',true,now() FROM capacity_lab_hotspots;
 INSERT INTO chat_participants(room_id,user_id,role) SELECT room,owner,'buyer' FROM capacity_lab_hotspots;
 INSERT INTO chat_participants(room_id,user_id,provider_id,role) SELECT hot_room,provider_user,provider,'provider' FROM capacity_lab_actors;
 INSERT INTO town_hall_offers(room_id,provider_id,sender_user_id,unit_price,terms) SELECT hot_room,provider,provider_user,1000,'Synthetic prior offer' FROM capacity_lab_actors;
 INSERT INTO chat_messages(room_id,sender_user_id,body,client_message_id) SELECT room,owner,'Synthetic prior busy message',gen_random_uuid() FROM capacity_lab_hotspots CROSS JOIN generate_series(1,500);`)
		if err != nil {
			return err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	_, err = db.Exec(ctx, "ANALYZE")
	return err
}

func checkFixtures(ctx context.Context, db *pgxpool.Pool, plan fixturePlan) (map[string]int, error) {
	counts := map[string]int{}
	var products, listings, rooms, hallMessages, marketMessages, busyRooms int
	err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM products),(SELECT count(*) FROM provider_listings),(SELECT count(*) FROM chat_rooms),(SELECT count(*) FROM chat_messages),(SELECT count(*) FROM provider_conversation_messages),(SELECT count(*) FROM (SELECT room_id FROM chat_participants WHERE role='provider' AND state='active' GROUP BY room_id HAVING count(*)=10) rooms)`).Scan(&products, &listings, &rooms, &hallMessages, &marketMessages, &busyRooms)
	if err != nil {
		return counts, err
	}
	var revisionsReady bool
	err = db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='provider_conversation_messages'::regclass AND tgname='provider_chat_revision' AND tgenabled='O') AND NOT EXISTS(SELECT 1 FROM provider_conversations WHERE history_revision<>$1)`, plan.ConversationHistory).Scan(&revisionsReady)
	if err != nil {
		return counts, err
	}
	if !revisionsReady {
		return counts, fmt.Errorf("history revisions or live trigger not ready")
	}
	counts = map[string]int{"products": products, "listings": listings, "rooms": rooms, "hall_messages": hallMessages, "marketplace_messages": marketMessages, "busy_rooms": busyRooms}
	extraRooms, extraMessages, wantBusy := 0, 0, 0
	if plan.Busy {
		extraRooms = plan.Pairs / 10
		extraMessages = extraRooms * 500
		wantBusy = extraRooms
	}
	if products != plan.Pairs*(1+plan.ExtraListings) || listings != products || rooms != plan.Pairs*(1+plan.OlderRooms)+extraRooms || hallMessages != plan.Pairs*(1+plan.OlderRooms)*10+extraMessages || marketMessages != plan.Pairs*plan.ConversationHistory || busyRooms != wantBusy {
		return counts, fmt.Errorf("synthetic fixture shape mismatch: %v", counts)
	}
	return counts, nil
}

type sample struct {
	Name               string
	Status             int
	Elapsed            time.Duration
	Write              bool
	Backend            string
	Valid              bool
	Controlled         bool // JSON overload response with a positive Retry-After.
	AdmissionRejected  bool
	AcknowledgementKey string
}
type metric struct {
	Requests int         `json:"requests"`
	Success  int         `json:"success"`
	Busy     int         `json:"busy"`
	Errors   int         `json:"errors"`
	P95MS    float64     `json:"successful_p95_ms"`
	P50MS    float64     `json:"successful_p50_ms"`
	P99MS    float64     `json:"successful_p99_ms"`
	Statuses map[int]int `json:"status_counts"`
	times    []time.Duration
}

func (m *metric) add(s sample) {
	if m.Statuses == nil {
		m.Statuses = map[int]int{}
	}
	m.Statuses[s.Status]++
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
		m.P50MS = float64(m.times[(len(m.times)*50+99)/100-1].Microseconds()) / 1000
		m.P95MS = float64(m.times[(len(m.times)*95+99)/100-1].Microseconds()) / 1000
		m.P99MS = float64(m.times[(len(m.times)*99+99)/100-1].Microseconds()) / 1000
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
		{Name: "catalogue", Path: "/api/v1/products", Want: 200},
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
		room := a.Room
		if a.HotRoom != "" {
			room = a.HotRoom
		}
		ops = append(ops, operation{Name: "town_hall_offer", Path: "/api/v1/chat/town-hall/" + room + "/offers", Token: provider, Body: string(b), Want: 204, Write: true})
	}
	if a.HotRoom != "" {
		path := "/api/v1/chat/conversations/" + a.HotRoom + "/messages"
		ops = append(ops, operation{Name: "busy_room_history", Path: path + "?limit=40&mark_read=false", Token: provider, Want: 200}, operation{Name: "busy_room_send", Path: path, Token: provider, Body: body(), Want: 201, Write: true})
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

func validPayload(op operation, raw []byte) bool {
	if op.Want == 204 {
		return true
	}
	if !json.Valid(raw) {
		return false
	}
	if op.Write {
		return true
	}
	key, limit := "items", 40
	switch op.Name {
	case "catalogue":
		key, limit = "products", 80 // Existing public API bound.
	case "services":
		limit = 20
	case "town_hall_feed":
		limit = 25
	case "support_history":
		key = "messages"
	}
	// Other envelope fields need not be arrays.
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil {
		return false
	}
	var items []json.RawMessage
	if json.Unmarshal(envelope[key], &items) != nil {
		return false
	}
	return len(items) > 0 && len(items) <= limit
}
func request(c *http.Client, op operation, index int) sample {
	method := "GET"
	if op.Body != "" {
		method = "POST"
	}
	req, _ := http.NewRequest(method, origin+op.Path, bytes.NewBufferString(op.Body))
	if op.Token != "" {
		req.Header.Set("Authorization", "Bearer "+op.Token)
	}
	req.Header.Set("Content-Type", "application/json")
	// Synthetic distinct clients through the lab-only proxy; real Redis rate
	// limits remain enabled. Production proxy trust is not modified here.
	req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.18.%d.%d", (index/254)%256, index%254+1))
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
	s.Valid = err == nil && res.StatusCode == op.Want && validPayload(op, raw)
	if s.Valid && op.Write {
		s.AcknowledgementKey = operationWriteKey(op)
	}
	s.Controlled = err == nil && controlledOverload(res.StatusCode, res.Header.Get("Retry-After"), raw)
	var overload struct {
		Error string `json:"error"`
	}
	if s.Controlled && json.Unmarshal(raw, &overload) == nil {
		s.AdmissionRejected = s.Status == 503 && overload.Error == "Service busy. Please retry shortly."
	}
	return s
}

func run(ctx context.Context, db *pgxpool.Pool) (runError error) {
	dbCPUs, err := databaseCPU(os.Getenv("LAB_DB_CPUS"))
	if err != nil {
		return err
	}
	plan, err := scenarioPlan(os.Getenv("LAB_SCENARIO"))
	if err != nil {
		return err
	}
	var marker, seedScenario string
	if err := db.QueryRow(ctx, "SELECT marker,scenario FROM capacity_lab_identity").Scan(&marker, &seedScenario); err != nil || marker != "disposable-compose-only" || seedScenario != plan.Name {
		return fmt.Errorf("missing isolated lab identity")
	}
	rows, err := db.Query(ctx, "SELECT buyer::text,provider_user::text,provider::text,listing::text,conversation::text,room::text,ticket::text,COALESCE(hot_room::text,'') FROM capacity_lab_actors ORDER BY slot")
	if err != nil {
		return err
	}
	actors := []actor{}
	for rows.Next() {
		var a actor
		if err = rows.Scan(&a.Buyer, &a.ProviderUser, &a.Provider, &a.Listing, &a.Conversation, &a.Room, &a.Ticket, &a.HotRoom); err != nil {
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
	counts, err := checkFixtures(ctx, db, plan)
	if err != nil {
		return err
	}
	must(json.NewEncoder(os.Stdout).Encode(map[string]any{"scenario": plan.Name, "fixture_counts": counts}))
	duration := 20 * time.Second
	if os.Getenv("LAB_PROFILE") == "baseline" {
		duration = 90 * time.Second
	} else if os.Getenv("LAB_PROFILE") != "smoke" && os.Getenv("LAB_PROFILE") != "burst" {
		return fmt.Errorf("LAB_PROFILE must be smoke, baseline or burst")
	}
	c := client()
	defer c.CloseIdleConnections()
	if plan.Busy {
		token, _, _ := auth.Sign(actors[10].Buyer, secret, time.Hour)
		req, _ := http.NewRequest("GET", origin+"/api/v1/chat/conversations/"+actors[0].HotRoom+"/messages", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := c.Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != 403 {
			return fmt.Errorf("busy-room privacy check status=%d", res.StatusCode)
		}
	}
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
	if os.Getenv("LAB_PROFILE") == "burst" {
		return runBurst(ctx, db, actors, prefix, plan.Name, dbCPUs, c)
	}
	var confirmed int
	acknowledged := map[string]int{}
	if os.Getenv("LAB_COMPARISON") == "true" {
		defer func() {
			verifyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			runError = errors.Join(runError, reconcileAcknowledged(verifyCtx, db, prefix, acknowledged))
		}()
	}
	var stageError error
	for _, users := range []int{20, 50, 100} {
		var poolBefore any
		if os.Getenv("LAB_COMPARISON") == "true" {
			if err := comparisonHTTPChecks(ctx, db, c, actors[0], actors[1]); err != nil {
				return err
			}
			poolBefore, err = comparisonPools(c)
			if err != nil {
				return err
			}
			if _, err = db.Exec(ctx, "SELECT pg_stat_statements_reset()"); err != nil {
				return err
			}
		}
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
				acknowledged[s.AcknowledgementKey]++
			}
			if !s.Valid {
				if s.Status == 429 || s.Status == 503 {
					busy++
				} else {
					errors++
				}
			}
		}
		latencyPassed := true
		for _, m := range metrics {
			m.finish()
			if m.Success == 0 || m.P95MS > 500 {
				latencyPassed = false
			}
		}
		ended := time.Now()
		succeeded := 0
		for _, m := range metrics {
			succeeded += m.Success
		}
		report := map[string]any{"scenario": plan.Name, "profile": os.Getenv("LAB_PROFILE"), "simulated_active_pairs": users, "stage_started_at": started.UTC(), "stage_ended_at": ended.UTC(), "elapsed_seconds": ended.Sub(started).Seconds(), "requests_per_second": float64(total) / ended.Sub(started).Seconds(), "successful_requests_per_second": float64(succeeded) / ended.Sub(started).Seconds(), "operations": metrics, "backends": backends, "unexpected_errors": errors, "busy": busy, "latency_target_ms": 500, "latency_target_passed": latencyPassed, "scope": "isolated Docker host; no internet/media/worker delivery capacity claim"}
		if os.Getenv("LAB_COMPARISON") == "true" {
			poolAfter, e := comparisonPools(c)
			if e != nil {
				return e
			}
			report["pool_before"] = poolBefore
			report["pool_after"] = poolAfter
			statements, e := comparisonStatements(ctx, db)
			if e != nil {
				return e
			}
			report["query_execution"] = statements
		}
		report["database_cpus"] = dbCPUs
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
	if err := reconcileWrites(ctx, db, prefix, confirmed); err != nil {
		return err
	}
	return stageError
}

func reconcileWrites(ctx context.Context, db *pgxpool.Pool, prefix string, confirmed int) error {
	var stored int
	err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM provider_conversation_messages WHERE body LIKE $1)+(SELECT count(*) FROM chat_messages WHERE body LIKE $1 OR body LIKE $2)+(SELECT count(*) FROM support_messages WHERE message LIKE $1)+(SELECT count(*) FROM chat_rooms WHERE title LIKE $1)`, prefix+"%", "Offer:%"+prefix+"%").Scan(&stored)
	if err != nil {
		return err
	}
	must(json.NewEncoder(os.Stdout).Encode(map[string]any{"confirmed_writes": confirmed, "stored_writes": stored, "write_reconciliation_passed": stored == confirmed}))
	if stored != confirmed {
		return fmt.Errorf("confirmed and stored writes differ")
	}
	return nil
}
