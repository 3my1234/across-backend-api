package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"sync"
	"testing"
)

func tierEntitlementDB(t *testing.T, before func(*pgxpool.Pool)) *pgxpool.Pool {
	t.Helper()
	db := communityTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `ALTER TABLE provider_subscription_plans ADD COLUMN name text,ADD COLUMN description text,ADD COLUMN updated_at timestamptz DEFAULT now(),ADD COLUMN amount_ngn numeric,ADD COLUMN features jsonb DEFAULT '{}';ALTER TABLE products ADD COLUMN factory_details jsonb DEFAULT '{}';ALTER TABLE provider_listings ADD COLUMN attributes jsonb DEFAULT '{}';`); err != nil {
		t.Fatal(err)
	}
	for i, code := range []string{"provider-monthly", "provider-growth", "provider-business", "provider-diamond"} {
		if _, err := db.Exec(ctx, `INSERT INTO provider_subscription_plans(id,code,badge_tier,listing_limit,amount_ngn)VALUES($1,$2,$3,99,$4)`, uuid.NewString(), code, []string{"bronze", "silver", "gold", "diamond"}[i], []int{500, 1000, 2000, 5000}[i]); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"095_provider_post_rules.sql", "097_provider_post_visibility_slots.sql"} {
		raw, err := os.ReadFile("../../migrations/" + file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if before != nil {
		before(db)
	}
	raw, err := os.ReadFile("../../migrations/107_provider_tier_entitlements.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	return db
}
func tierProvider(t *testing.T, db *pgxpool.Pool, code string) (string, string, string) {
	t.Helper()
	ctx := context.Background()
	user, provider, sub := uuid.NewString(), uuid.NewString(), uuid.NewString()
	queries := []struct {
		q string
		a []any
	}{
		{`INSERT INTO users(id,full_name)VALUES($1,'Synthetic tier owner')`, []any{user}},
		{`INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status)VALUES($1,$2,'Synthetic tier business','approved')`, []any{provider, user}},
		{`INSERT INTO provider_members(provider_id,user_id,role)VALUES($1,$2,'owner')`, []any{provider, user}},
	}
	for _, q := range queries {
		if _, err := db.Exec(ctx, q.q, q.a...); err != nil {
			t.Fatal(err)
		}
	}
	if code != "" {
		if _, err := db.Exec(ctx, `INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,starts_at,current_period_end)SELECT $1,$2,id,'active',now(),now()+interval '1 month' FROM provider_subscription_plans WHERE code=$3`, sub, provider, code); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO provider_subscription_payments VALUES($1,500,'NGN')`, sub); err != nil {
			t.Fatal(err)
		}
	}
	return user, provider, sub
}
func TestTierEntitlementsMatrixCombinedMediaAndPaymentState(t *testing.T) {
	db := tierEntitlementDB(t, nil)
	ctx := context.Background()
	for _, tc := range []struct {
		code, tier                          string
		posts, images, rooms, weight, price int
	}{{"provider-monthly", "bronze", 3, 3, 3, 1, 500}, {"provider-growth", "silver", 10, 5, 5, 2, 1000}, {"provider-business", "diamond", 25, 8, 8, 3, 2000}, {"provider-diamond", "gold", 60, 12, 10, 5, 5000}} {
		t.Run(tc.tier, func(t *testing.T) {
			_, provider, sub := tierProvider(t, db, tc.code)
			var raw []byte
			if err := db.QueryRow(ctx, `SELECT provider_current_benefits($1)`, provider).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var actual providerPostPolicy
			if err := json.Unmarshal(raw, &actual); err != nil {
				t.Fatal(err)
			}
			if actual.Posts != tc.posts || actual.Images != tc.images || actual.Rooms != tc.rooms || actual.Weight != tc.weight || actual != postPolicy(tc.tier) {
				t.Fatalf("wrong verified tier policy %+v", actual)
			}
			var price int
			var name string
			db.QueryRow(ctx, `SELECT amount_ngn::int,name FROM provider_subscription_plans WHERE code=$1`, tc.code).Scan(&price, &name)
			if price != tc.price || name != providerTierLabel(tc.tier) {
				t.Fatal("price/identity mapping changed", price, name)
			}
			media := make([]string, tc.images)
			for i := range media {
				media[i] = fmt.Sprintf("https://fixture.test/%d.jpg", i)
			}
			product := uuid.NewString()
			if _, err := db.Exec(ctx, `INSERT INTO products(id,provider_id,moderation_status,image_urls)VALUES($1,$2,'draft',$3)`, product, provider, media); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `UPDATE products SET image_urls=$2 WHERE id=$1`, product, append(media, "https://fixture.test/excess.jpg")); err == nil {
				t.Fatal("image allowance bypassed")
			}
			for i := 1; i < tc.posts; i++ {
				if _, err := db.Exec(ctx, `INSERT INTO provider_listings(id,provider_id,status,media_urls)VALUES($1,$2,'draft',ARRAY['https://fixture.test/a.jpg'])`, uuid.NewString(), provider); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Exec(ctx, `INSERT INTO products(id,provider_id,moderation_status,image_urls)VALUES($1,$2,'draft',$3)`, uuid.NewString(), provider, media); err == nil {
				t.Fatal("combined quota bypass")
			}
			for _, state := range []string{"future", "unpaid", "expired", "pending", "cancelled", "renewed"} {
				switch state {
				case "future":
					db.Exec(ctx, `UPDATE provider_subscriptions SET starts_at=now()+interval '1 day' WHERE id=$1`, sub)
				case "unpaid":
					db.Exec(ctx, `UPDATE provider_subscriptions SET starts_at=now() WHERE id=$1;`, sub)
					db.Exec(ctx, `DELETE FROM provider_subscription_payments WHERE subscription_id=$1`, sub)
				case "expired":
					db.Exec(ctx, `INSERT INTO provider_subscription_payments VALUES($1,500,'NGN')`, sub)
					db.Exec(ctx, `UPDATE provider_subscriptions SET current_period_end=now()-interval '1 second' WHERE id=$1`, sub)
				case "pending", "cancelled":
					if _, err := db.Exec(ctx, `UPDATE provider_subscriptions SET status=$2,current_period_end=now()+interval '1 month' WHERE id=$1`, sub, state); err != nil {
						t.Fatal(err)
					}
				case "renewed":
					db.Exec(ctx, `UPDATE provider_subscriptions SET status='active',current_period_end=now()+interval '1 month' WHERE id=$1`, sub)
				}
				var cap int
				db.QueryRow(ctx, `SELECT (provider_current_benefits($1)->>'post_limit')::int`, provider).Scan(&cap)
				want := 1
				if state == "renewed" {
					want = tc.posts
				}
				if cap != want {
					t.Fatalf("%s granted wrong benefits: %d", state, cap)
				}
			}
		})
	}
}
func TestTierEntitlementsConcurrentRoomQuotaAndCapacityRelease(t *testing.T) {
	db := tierEntitlementDB(t, nil)
	ctx := context.Background()
	_, provider, _ := tierProvider(t, db, "provider-monthly")
	rooms := []string{}
	members := []string{}
	for i := 0; i < 8; i++ {
		owner, member, room := uuid.NewString(), uuid.NewString(), uuid.NewString()
		members = append(members, member)
		rooms = append(rooms, room)
		if _, err := db.Exec(ctx, `INSERT INTO users(id,full_name)VALUES($1,'Requester'),($2,'Staff')`, owner, member); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO chat_rooms(id,kind,owner_user_id,title)VALUES($1,'town_hall',$2,'Quota request')`, room, owner); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO town_hall_requests(room_id,description,location,unit,message_only)VALUES($1,'Synthetic','Abuja','request',true)`, room); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan bool, len(rooms))
	for i := range rooms {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := db.Exec(ctx, `INSERT INTO chat_participants(room_id,user_id,provider_id,role,state)VALUES($1,$2,$3,'provider','active')`, rooms[i], members[i], provider)
			results <- err == nil
		}(i)
	}
	wg.Wait()
	close(results)
	joined := 0
	for success := range results {
		if success {
			joined++
		}
	}
	if joined != 3 {
		t.Fatalf("concurrent joins exceeded or underfilled3: %d", joined)
	}
	var activeRoom string
	db.QueryRow(ctx, `SELECT room_id::text FROM chat_participants WHERE provider_id=$1 LIMIT 1`, provider).Scan(&activeRoom)
	if _, err := db.Exec(ctx, `UPDATE chat_rooms SET state='closed' WHERE id=$1`, activeRoom); err != nil {
		t.Fatal(err)
	}
	var candidate, staff string
	db.QueryRow(ctx, `SELECT r.id::text,r.owner_user_id::text FROM chat_rooms r WHERE NOT EXISTS(SELECT 1 FROM chat_participants p WHERE p.room_id=r.id AND p.provider_id=$1) LIMIT 1`, provider).Scan(&candidate, &staff)
	if _, err := db.Exec(ctx, `INSERT INTO chat_participants(room_id,user_id,provider_id,role,state)VALUES($1,$2,$3,'provider','active')`, candidate, staff, provider); err != nil {
		t.Fatal("closed room did not release capacity", err)
	}
}

func TestTierEntitlementsDowngradeRetainsPublishedMediaAndUsage(t *testing.T) {
	db := tierEntitlementDB(t, nil)
	ctx := context.Background()
	_, provider, sub := tierProvider(t, db, "provider-diamond")
	ids := []string{}
	images := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"}
	for i := 0; i < 6; i++ {
		id := uuid.NewString()
		ids = append(ids, id)
		if _, err := db.Exec(ctx, `INSERT INTO products(id,provider_id,moderation_status,published_at,image_urls)VALUES($1,$2,'approved',now(),$3)`, id, provider, images); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `UPDATE provider_subscriptions SET plan_id=(SELECT id FROM provider_subscription_plans WHERE code='provider-monthly') WHERE id=$1`, sub); err != nil {
		t.Fatal(err)
	}
	var visible, quota int
	if err := db.QueryRow(ctx, `SELECT listing_limit,(provider_current_benefits(provider_id)->>'post_limit')::int FROM provider_public_identity WHERE provider_id=$1`, provider).Scan(&visible, &quota); err != nil || visible != 6 || quota != 3 {
		t.Fatal("paid publications hidden or visibility used as quota", visible, quota, err)
	}
	if _, err := db.Exec(ctx, `UPDATE products SET image_urls=$2 WHERE id=$1`, ids[0], images[:10]); err != nil {
		t.Fatal("removing grandfathered media blocked", err)
	}
	if _, err := db.Exec(ctx, `UPDATE products SET image_urls=$2 WHERE id=$1`, ids[0], append(images[:10], "new")); err == nil {
		t.Fatal("extra over-limit media accepted")
	}
	if _, err := db.Exec(ctx, `UPDATE products SET moderation_status='pending' WHERE id=$1`, ids[0]); err != nil {
		t.Fatal("existing published edit blocked", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO provider_listings(id,provider_id,status)VALUES($1,$2,'draft')`, uuid.NewString(), provider); err == nil {
		t.Fatal("over-limit new listing accepted")
	}
	m := &ProviderMarketplaceController{db: db}
	usage, err := m.providerEntitlements(ctx, provider)
	if err != nil || usage["active_listings_used"] != 6 || usage["remaining_active_listings"] != 0 || usage["listing_over_limit"] != true {
		t.Fatal("wrong downgrade usage", usage, err)
	}
	// Expiry keeps records, while retaining the existing one-listing Free visibility.
	if _, err := db.Exec(ctx, `UPDATE provider_subscriptions SET current_period_end=now()-interval '1 second' WHERE id=$1`, sub); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT listing_limit FROM provider_public_identity WHERE provider_id=$1`, provider).Scan(&visible); err != nil || visible != 1 {
		t.Fatal("expiry changed Free behavior", visible, err)
	}
	var stored int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM products WHERE provider_id=$1`, provider).Scan(&stored); err != nil || stored != 6 {
		t.Fatal("stored publications lost", stored, err)
	}
}
func tierRoom(t *testing.T, db *pgxpool.Pool) (string, string) {
	t.Helper()
	ctx := context.Background()
	user, room := uuid.NewString(), uuid.NewString()
	for _, q := range []struct {
		sql  string
		args []any
	}{{`INSERT INTO users(id,full_name)VALUES($1,'Synthetic requester')`, []any{user}}, {`INSERT INTO chat_rooms(id,kind,owner_user_id,title)VALUES($1,'town_hall',$2,'Synthetic room')`, []any{room, user}}, {`INSERT INTO town_hall_requests(room_id,description,location,unit,message_only)VALUES($1,'Synthetic','Abuja','request',true)`, []any{room}}} {
		if _, err := db.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	return room, user
}
func TestTierEntitlementsEachRoomCapDowngradeAndExit(t *testing.T) {
	db := tierEntitlementDB(t, nil)
	ctx := context.Background()
	for _, tc := range []struct {
		code string
		cap  int
	}{{"", 0}, {"provider-monthly", 3}, {"provider-growth", 5}, {"provider-business", 8}, {"provider-diamond", 10}} {
		t.Run(tc.code, func(t *testing.T) {
			_, provider, sub := tierProvider(t, db, tc.code)
			var joinedRoom, joinedUser string
			for i := 0; i <= tc.cap; i++ {
				room, user := tierRoom(t, db)
				_, err := db.Exec(ctx, `INSERT INTO chat_participants(room_id,user_id,provider_id,role,state)VALUES($1,$2,$3,'provider','active')`, room, user, provider)
				if (i < tc.cap) != (err == nil) {
					t.Fatalf("room %d cap%d: %v", i, tc.cap, err)
				}
				if i == 0 {
					joinedRoom = room
					joinedUser = user
				}
			}
			if tc.cap == 0 {
				return
			}
			// A second staff member in the same room consumes no additional business slot.
			extra := uuid.NewString()
			if _, err := db.Exec(ctx, `INSERT INTO users(id,full_name)VALUES($1,'Synthetic staff')`, extra); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `INSERT INTO chat_participants(room_id,user_id,provider_id,role,state)VALUES($1,$2,$3,'provider','active')`, joinedRoom, extra, provider); err == nil {
				t.Fatal("duplicate business seat accepted")
			}
			if tc.cap > 3 {
				if _, err := db.Exec(ctx, `UPDATE provider_subscriptions SET plan_id=(SELECT id FROM provider_subscription_plans WHERE code='provider-monthly')WHERE id=$1`, sub); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(ctx, `UPDATE chat_participants SET state='active' WHERE provider_id=$1`, provider); err != nil {
					t.Fatal("downgrade ejected existing members", err)
				}
				room, user := tierRoom(t, db)
				if _, err := db.Exec(ctx, `INSERT INTO chat_participants(room_id,user_id,provider_id,role,state)VALUES($1,$2,$3,'provider','active')`, room, user, provider); err == nil {
					t.Fatal("over-limit downgraded join accepted")
				}
			} else {
				if _, err := db.Exec(ctx, `UPDATE chat_participants SET state='removed' WHERE room_id=$1 AND provider_id=$2`, joinedRoom, provider); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(ctx, `UPDATE chat_participants SET state='active' WHERE room_id=$1 AND user_id=$2`, joinedRoom, joinedUser); err != nil {
					t.Fatal("exited room did not release capacity", err)
				}
				if _, err := db.Exec(ctx, `UPDATE town_hall_requests SET engagement_deadline=clock_timestamp()-interval '1 second',first_provider_engaged_at=clock_timestamp() WHERE room_id=$1`, joinedRoom); err != nil {
					t.Fatal(err)
				}
				room, user := tierRoom(t, db)
				if _, err := db.Exec(ctx, `INSERT INTO chat_participants(room_id,user_id,provider_id,role,state)VALUES($1,$2,$3,'provider','active')`, room, user, provider); err != nil {
					t.Fatal("engaged but expired room did not release capacity", err)
				}

			}
		})
	}
}
func TestTierEntitlementsConcurrentMixedListingCreation(t *testing.T) {
	db := tierEntitlementDB(t, nil)
	ctx := context.Background()
	_, provider, _ := tierProvider(t, db, "provider-monthly")
	var wg sync.WaitGroup
	results := make(chan bool, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			q := `INSERT INTO products(id,provider_id,moderation_status)VALUES($1,$2,'draft')`
			if i%2 == 0 {
				q = `INSERT INTO provider_listings(id,provider_id,status)VALUES($1,$2,'draft')`
			}
			_, err := db.Exec(ctx, q, uuid.NewString(), provider)
			results <- err == nil
		}(i)
	}
	wg.Wait()
	close(results)
	success := 0
	for ok := range results {
		if ok {
			success++
		}
	}
	if success != 3 {
		t.Fatalf("concurrent mixed quota: %d", success)
	}
}

func TestTierEntitlementsUploadOwnershipAndArchivedReactivation(t *testing.T) {
	db := tierEntitlementDB(t, nil)
	ctx := context.Background()
	_, provider, sub := tierProvider(t, db, "provider-diamond")
	_, other, _ := tierProvider(t, db, "provider-monthly")
	id := uuid.NewString()
	if _, err := db.Exec(ctx, `INSERT INTO products(id,provider_id,moderation_status,image_urls)VALUES($1,$2,'draft',ARRAY['a','b','c','d'])`, id, provider); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE provider_subscriptions SET plan_id=(SELECT id FROM provider_subscription_plans WHERE code='provider-monthly') WHERE id=$1`, sub); err != nil {
		t.Fatal(err)
	}
	m := &ProviderMarketplaceController{db: db}
	if err := m.checkPostUpload(ctx, provider, id, "products", "listing"); err == nil {
		t.Fatal("over-limit upload preflight accepted")
	}
	if err := m.checkPostUpload(ctx, other, id, "products", "listing"); err == nil {
		t.Fatal("other business upload target accepted")
	}
	if _, err := db.Exec(ctx, `UPDATE products SET image_urls=ARRAY['a','b','c'] WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := m.checkPostUpload(ctx, provider, id, "products", "listing"); err != nil {
		t.Fatal("compliant media edit blocked", err)
	}
	if _, err := db.Exec(ctx, `UPDATE products SET moderation_status='archived' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := db.Exec(ctx, `INSERT INTO provider_listings(id,provider_id,status)VALUES($1,$2,'draft')`, uuid.NewString(), provider); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `UPDATE products SET moderation_status='pending' WHERE id=$1`, id); err == nil {
		t.Fatal("archived reactivation bypassed mixed capacity")
	}
	if _, err := db.Exec(ctx, `UPDATE products SET provider_id=$2 WHERE id=$1`, id, other); err == nil {
		t.Fatal("listing owner changed")
	}
}
func TestTierEntitlementsWeightedPromotion(t *testing.T) {
	for _, tc := range []struct {
		tier     string
		min, max int
	}{{"bronze", 400, 600}, {"silver", 580, 760}, {"diamond", 670, 830}, {"gold", 760, 920}} {
		count := 0
		for i := 0; i < 1000; i++ {
			items := []discoveryItem{{Kind: "product", ID: "candidate", Provider: "one", Paid: true, Promote: true, Tier: tc.tier, Priority: 1, Score: 10}, {Kind: "product", ID: "baseline", Provider: "two", Paid: true, Promote: true, Tier: "bronze", Priority: 1, Score: 10}}
			if rankDiscovery(items, "promoted", fmt.Sprint(i), 1)[0].ID == "candidate" {
				count++
			}
		}
		if count < tc.min || count > tc.max {
			t.Fatalf("%s relative exposure outside bounded expectation: %d", tc.tier, count)
		}
	}
}

func TestTierEntitlementsActualAdmissionAndSameAccountCustomer(t *testing.T) {
	db := tierEntitlementDB(t, nil)
	ctx := context.Background()
	actor, provider, _ := tierProvider(t, db, "provider-monthly")
	app := communityApp(db)
	// Providers retain the same authenticated identity for customer requests.
	own := communityCall(t, app, "POST", "/hall", actor, fmt.Sprintf(`{"client_request_id":%q,"message":"I need a mechanic near Abuja"}`, uuid.NewString()), 201)
	ownID := own["id"].(string)
	communityCall(t, app, "POST", "/hall/"+ownID+"/apply", actor, `{}`, 422)
	var owner string
	if err := db.QueryRow(ctx, `SELECT owner_user_id::text FROM chat_rooms WHERE id=$1`, ownID).Scan(&owner); err != nil || owner != actor {
		t.Fatal("customer/provider account identity split", owner, err)
	}
	for i := 0; i < 4; i++ {
		room, user := tierRoom(t, db)
		if _, err := db.Exec(ctx, `INSERT INTO town_hall_applications(room_id,provider_id,requester_user_id)VALUES($1,$2,$3)`, room, provider, actor); err != nil {
			t.Fatal(err)
		}
		status := 204
		if i == 3 {
			status = 409
		}
		communityCall(t, app, "POST", "/hall/"+room+"/providers/"+provider, user, `{"action":"approve"}`, status)
		if i == 0 {
			communityCall(t, app, "POST", "/hall/"+room+"/providers/"+provider, user, `{"action":"approve"}`, 204)
		}
	}
	m := &ProviderMarketplaceController{db: db}
	app.Get("/entitlements", m.ProviderEntitlements)
	usage := communityCall(t, app, "GET", "/entitlements", actor, "", 200)
	if usage["town_hall_rooms_used"] != float64(3) || usage["remaining_town_hall_rooms"] != float64(0) {
		t.Fatal("wrong structured room usage", usage)
	}
	communityCall(t, app, "GET", "/entitlements", uuid.NewString(), "", 403)
}
