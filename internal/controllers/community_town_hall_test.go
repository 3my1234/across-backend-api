package controllers

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"sync"
	"testing"
)

func TestCommunityTownHallConcurrentSeatLimit(t *testing.T) {
	db := communityTestDB(t)
	ctx := context.Background()
	buyer, room := uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(ctx, `INSERT INTO users(id,full_name) VALUES($1,'Buyer')`, buyer); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO chat_rooms(id,kind,owner_user_id,title) VALUES($1,'town_hall',$2,'Concurrent admission')`, room, buyer); err != nil {
		t.Fatal(err)
	}
	users, providers := []string{}, []string{}
	for i := 0; i < 12; i++ {
		user, provider := uuid.NewString(), uuid.NewString()
		users = append(users, user)
		providers = append(providers, provider)
		if _, err := db.Exec(ctx, `INSERT INTO users(id,full_name) VALUES($1,'Provider')`, user); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status) VALUES($1,$2,'Provider','approved')`, provider, user); err != nil {
			t.Fatal(err)
		}
	}
	var group sync.WaitGroup
	results := make(chan error, 12)
	start := make(chan struct{})
	for i := range users {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			<-start
			_, err := db.Exec(ctx, `INSERT INTO chat_participants(room_id,user_id,provider_id,role) VALUES($1,$2,$3,'provider')`, room, users[i], providers[i])
			results <- err
		}(i)
	}
	close(start)
	group.Wait()
	close(results)
	success, full := 0, 0
	for err := range results {
		if err == nil {
			success++
			continue
		}
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23514" {
			full++
		} else {
			t.Fatal(err)
		}
	}
	if success != 10 || full != 2 {
		t.Fatalf("admitted%d full%d", success, full)
	}
}
func TestCommunityTownHallAdmissionExpiryAndHandoff(t *testing.T) {
	db := communityTestDB(t)
	ctx := context.Background()
	app := communityApp(db)
	buyer := uuid.NewString()
	_, err := db.Exec(ctx, `INSERT INTO users(id,full_name) VALUES($1,'Buyer')`, buyer)
	if err != nil {
		t.Fatal(err)
	}
	plan := uuid.NewString()
	_, err = db.Exec(ctx, `INSERT INTO provider_subscription_plans(id,code,listing_limit,badge_tier) VALUES($1,'provider-monthly',5,'bronze')`, plan)
	if err != nil {
		t.Fatal(err)
	}
	users, providers, subscriptions := []string{}, []string{}, []string{}
	for i := 0; i < 12; i++ {
		user, provider, sub := uuid.NewString(), uuid.NewString(), uuid.NewString()
		users = append(users, user)
		providers = append(providers, provider)
		subscriptions = append(subscriptions, sub)
		_, err = db.Exec(ctx, `INSERT INTO users(id,full_name) VALUES($1,'Provider')`, user)
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Exec(ctx, `INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status) VALUES($1,$2,'Rice supplier','approved')`, provider, user)
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Exec(ctx, `INSERT INTO provider_members(provider_id,user_id,role) VALUES($1,$2,'owner')`, provider, user)
		if err != nil {
			t.Fatal(err)
		}
		if i < 11 {
			_, err = db.Exec(ctx, `INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,current_period_end) VALUES($1,$2,$3,'active',now()+interval '1 month')`, sub, provider, plan)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec(ctx, `INSERT INTO provider_subscription_payments(subscription_id,amount,currency_code) VALUES($1,500,'NGN')`, sub)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	requestBody := `{"client_request_id":"` + uuid.NewString() + `","title":"50 bags of rice","description":"Rice for an occasion","location":"Abuja","quantity":50,"unit":"bag","target_unit_price":48500}`
	created := communityCall(t, app, "POST", "/hall", buyer, requestBody, 201)
	repeat := communityCall(t, app, "POST", "/hall", buyer, requestBody, 201)
	if repeat["id"] != created["id"] {
		t.Fatal("duplicate request on retry")
	}
	room := created["id"].(string)
	communityCall(t, app, "GET", "/hall/"+room, users[11], "", 200)
	communityCall(t, app, "POST", "/hall/"+room+"/apply", users[11], "", 403)
	communityCall(t, app, "GET", "/rooms/"+room+"/messages", users[0], "", 403)
	for i := 0; i < 11; i++ {
		communityCall(t, app, "POST", "/hall/"+room+"/apply", users[i], "", 204)
	}
	communityCall(t, app, "POST", "/hall/"+room+"/providers/"+providers[0], users[1], `{"action":"approve"}`, 403)
	for i := 0; i < 10; i++ {
		communityCall(t, app, "POST", "/hall/"+room+"/providers/"+providers[i], buyer, `{"action":"approve"}`, 204)
	}
	communityCall(t, app, "POST", "/hall/"+room+"/providers/"+providers[10], buyer, `{"action":"approve"}`, 409)
	offerBody := `{"unit_price":48800,"terms":"Pickup tomorrow","client_message_id":"` + uuid.NewString() + `"}`
	communityCall(t, app, "POST", "/hall/"+room+"/offers", users[0], offerBody, 204)
	communityCall(t, app, "POST", "/hall/"+room+"/offers", users[0], offerBody, 204)
	var offerMessages int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM chat_messages WHERE room_id=$1`, room).Scan(&offerMessages)
	if offerMessages != 1 {
		t.Fatal("offer retry duplicated the chat message")
	}
	communityCall(t, app, "POST", "/hall/"+room+"/offers", users[0], `{"unit_price":48900,"client_message_id":"`+uuid.NewString()+`"}`, 204)
	communityCall(t, app, "POST", "/hall/"+room+"/offers", users[0], offerBody, 204)
	var latestPrice float64
	_ = db.QueryRow(ctx, `SELECT unit_price::float8 FROM town_hall_offers WHERE room_id=$1 AND provider_id=$2`, room, providers[0]).Scan(&latestPrice)
	if latestPrice != 48900 {
		t.Fatal("late retry reverted a newer offer")
	}
	detail := communityCall(t, app, "GET", "/hall/"+room, users[11], "", 200)
	offer := detail["offers"].([]any)[0].(map[string]any)["id"].(string)
	if detail["applications"] != nil {
		t.Fatal("applicants exposed to outsider")
	}
	_, err = db.Exec(ctx, `UPDATE provider_subscriptions SET current_period_end=now()-interval '1 second' WHERE id=$1`, subscriptions[0])
	if err != nil {
		t.Fatal(err)
	}
	communityCall(t, app, "POST", "/hall/"+room+"/offers", users[0], `{"unit_price":48700,"client_message_id":"`+uuid.NewString()+`"}`, 402)
	communityCall(t, app, "POST", "/rooms/"+room+"/messages", users[0], `{"message":"Expired","client_message_id":"`+uuid.NewString()+`"}`, 402)
	communityCall(t, app, "POST", "/hall/"+room+"/providers/"+providers[1], buyer, `{"action":"remove"}`, 204)
	communityCall(t, app, "GET", "/rooms/"+room+"/messages", users[1], "", 403)
	communityCall(t, app, "POST", "/hall/"+room+"/providers/"+providers[10], buyer, `{"action":"approve"}`, 204)
	handed := communityCall(t, app, "POST", "/hall/"+room+"/choose", buyer, `{"offer_id":"`+offer+`","close_room":true}`, 200)
	retried := communityCall(t, app, "POST", "/hall/"+room+"/choose", buyer, `{"offer_id":"`+offer+`","close_room":true}`, 200)
	if retried["id"] != handed["id"] {
		t.Fatal("handoff retry changed the chat")
	}
	var receipts int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM chat_messages WHERE room_id=$1`, handed["id"]).Scan(&receipts)
	if receipts != 1 {
		t.Fatal("selected offer was not recorded exactly once")
	}
	communityCall(t, app, "GET", "/hall/"+room, users[11], "", 404)
	communityCall(t, app, "GET", "/hall/"+room, buyer, "", 404)
	communityCall(t, app, "GET", "/rooms/"+room+"/messages", buyer, "", 410)
	communityCall(t, app, "GET", "/rooms/"+room+"/messages", users[10], "", 403)
	communityCall(t, app, "POST", "/rooms/"+handed["id"].(string)+"/messages", users[0], `{"message":"Continue privately","client_message_id":"`+uuid.NewString()+`"}`, 201)
}
