package controllers

import (
	"context"
	"github.com/google/uuid"
	"net/http/httptest"
	"os"
	"testing"
)

func TestTownHallUnansweredExpiryAndRepost(t *testing.T) {
	db := communityTestDB(t)
	ctx := context.Background()
	app := communityApp(db)
	buyer, other := uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(ctx, `INSERT INTO users(id,full_name) VALUES($1,'Buyer'),($2,'Observer')`, buyer, other); err != nil {
		t.Fatal(err)
	}
	create := func() string {
		return communityCall(t, app, "POST", "/hall", buyer, `{"client_request_id":"`+uuid.NewString()+`","message":"I need a ride to Central Area"}`, 201)["id"].(string)
	}
	room := create()
	request := httptest.NewRequest("GET", "/hall", nil)
	request.Header.Set("Actor", other)
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	etag := response.Header.Get("ETag")
	response.Body.Close()
	if etag == "" {
		t.Fatal("missing conditional feed tag")
	}
	request = httptest.NewRequest("GET", "/hall", nil)
	request.Header.Set("Actor", other)
	request.Header.Set("If-None-Match", etag)
	response, err = app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 304 {
		t.Fatal("unchanged feed not conditional")
	}
	communityCall(t, app, "POST", "/rooms/"+room+"/messages", buyer, `{"message":"Still looking for a provider","client_message_id":"`+uuid.NewString()+`"}`, 201)
	if _, err := db.Exec(ctx, `UPDATE town_hall_requests SET engagement_deadline=clock_timestamp()-interval '1 second' WHERE room_id=$1`, room); err != nil {
		t.Fatal(err)
	}
	public := communityCall(t, app, "GET", "/hall", other, "", 200)
	request = httptest.NewRequest("GET", "/hall", nil)
	request.Header.Set("Actor", other)
	request.Header.Set("If-None-Match", etag)
	response, err = app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("expiry must invalidate conditional feed")
	}
	if len(public["items"].([]any)) != 0 {
		t.Fatal("expired request visible before sweeper")
	}
	own := communityCall(t, app, "GET", "/hall/"+room, buyer, "", 200)
	if own["state"] != "closed" || own["expired"] != true {
		t.Fatalf("owner expiry record: %v", own)
	}
	communityCall(t, app, "GET", "/hall/"+room, other, "", 404)
	communityCall(t, app, "POST", "/rooms/"+room+"/messages", buyer, `{"message":"A buyer bump must not revive this","client_message_id":"`+uuid.NewString()+`"}`, 409)
	var count int
	if err := db.QueryRow(ctx, `SELECT expire_unengaged_town_hall_requests(250)`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("sweep count %d err %v", count, err)
	}
	if err := db.QueryRow(ctx, `SELECT expire_unengaged_town_hall_requests(250)`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("idempotent sweep %d %v", count, err)
	}
	newer := create()
	if newer == room {
		t.Fatal("repost reused expired room")
	}
	communityCall(t, app, "GET", "/hall/"+room, buyer, "", 200)
	record := communityCall(t, app, "GET", "/rooms/"+room+"/messages", buyer, "", 200)
	if record["can_send"] != false || len(record["items"].([]any)) != 1 {
		t.Fatal("buyer history lost or expired room remains writable")
	}
	public = communityCall(t, app, "GET", "/hall", other, "", 200)
	if len(public["items"].([]any)) != 1 {
		t.Fatal("new repost not public")
	}
}

func TestTownHallExpiryMigrationPreservesLegacyEngagement(t *testing.T) {
	db := communityTestDB(t, true)
	ctx := context.Background()
	buyer, user, provider, engaged, idle := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err := db.Exec(ctx, `INSERT INTO users(id,full_name) VALUES($1,'Buyer'),($2,'Provider');`, buyer, user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status) VALUES($1,$2,'Legacy supplier','approved')`, provider, user); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO chat_rooms(id,kind,owner_user_id,title,created_at) VALUES($1,'town_hall',$3,'Engaged legacy request',now()-interval '10 minutes'),($2,'town_hall',$3,'Idle legacy request',now()-interval '10 minutes')`, engaged, idle, buyer); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO town_hall_requests(room_id,description,message_only,location,unit) VALUES($1,'Existing engaged request',true,'',''),($2,'Existing unanswered request',true,'','')`, engaged, idle); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO town_hall_applications(room_id,provider_id,requester_user_id,state) VALUES($1,$2,$3,'declined')`, engaged, provider, user); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/086_town_hall_idle_expiry.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT expire_unengaged_town_hall_requests(250)`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("legacy sweep %d %v", count, err)
	}
	var state string
	if err = db.QueryRow(ctx, `SELECT state FROM chat_rooms WHERE id=$1`, engaged).Scan(&state); err != nil || state != "open" {
		t.Fatalf("legacy engagement was expired: %s %v", state, err)
	}
}

func TestTownHallProviderEngagementPreventsExpiry(t *testing.T) {
	db := communityTestDB(t)
	ctx := context.Background()
	app := communityApp(db)
	buyer, user, provider, plan, sub := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users(id,full_name) VALUES($1,'Buyer'),($2,'Provider')`, []any{buyer, user}},
		{`INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status) VALUES($1,$2,'Supplier','approved')`, []any{provider, user}},
		{`INSERT INTO provider_members(provider_id,user_id,role) VALUES($1,$2,'owner')`, []any{provider, user}},
		{`INSERT INTO provider_subscription_plans(id,code,listing_limit,badge_tier) VALUES($1,'provider-monthly',5,'bronze')`, []any{plan}},
		{`INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,current_period_end) VALUES($1,$2,$3,'active',now()+interval '1 month')`, []any{sub, provider, plan}},
		{`INSERT INTO provider_subscription_payments(subscription_id,amount,currency_code) VALUES($1,500,'NGN')`, []any{sub}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	create := func() string {
		return communityCall(t, app, "POST", "/hall", buyer, `{"client_request_id":"`+uuid.NewString()+`","message":"I need rice for my event"}`, 201)["id"].(string)
	}
	engaged := create()
	communityCall(t, app, "POST", "/hall/"+engaged+"/apply", user, "", 204)
	if _, err := db.Exec(ctx, `UPDATE town_hall_requests SET engagement_deadline=clock_timestamp()-interval '1 second' WHERE room_id=$1`, engaged); err != nil {
		t.Fatal(err)
	}
	detail := communityCall(t, app, "GET", "/hall/"+engaged, user, "", 200)
	if detail["state"] != "open" || detail["provider_engaged"] != true {
		t.Fatal("application did not protect request")
	}
	late := create()
	if _, err := db.Exec(ctx, `UPDATE town_hall_requests SET engagement_deadline=clock_timestamp()-interval '1 second' WHERE room_id=$1`, late); err != nil {
		t.Fatal(err)
	}
	communityCall(t, app, "POST", "/hall/"+late+"/apply", user, "", 409)
	var count int
	if err := db.QueryRow(ctx, `SELECT expire_unengaged_town_hall_requests(250)`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("engaged expiry count %d %v", count, err)
	}
	// Even direct SQL cannot admit a late provider between an API check and insert.
	if _, err := db.Exec(ctx, `INSERT INTO town_hall_applications(room_id,provider_id,requester_user_id) VALUES($1,$2,$3)`, late, provider, user); err == nil {
		t.Fatal("late engagement bypassed database guard")
	}
	// An in-flight application owns the same room lock as expiry; a second
	// worker skips it and must not remove a request that is gaining engagement.
	pending := create()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO town_hall_applications(room_id,provider_id,requester_user_id) VALUES($1,$2,$3)`, pending, provider, user); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE town_hall_requests SET engagement_deadline=clock_timestamp()-interval '1 second' WHERE room_id=$1`, pending); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT expire_unengaged_town_hall_requests(250)`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("locked application expired: %d %v", count, err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT expire_unengaged_town_hall_requests(250)`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("committed engagement expired: %d %v", count, err)
	}
}
