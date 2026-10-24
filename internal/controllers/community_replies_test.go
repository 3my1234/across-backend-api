package controllers

import (
	"context"
	"github.com/google/uuid"
	"testing"
)

func TestCommunityTownHallRepliesPermissionsAndOwnActivity(t *testing.T) {
	db := communityTestDB(t)
	ctx := context.Background()
	app := communityApp(db)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	buyer, other, outsider := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, user := range []string{buyer, other, outsider} {
		exec(`INSERT INTO users(id,full_name) VALUES($1,'Customer')`, user)
		communityCall(t, app, "GET", "/profile", user, "", 200)
	}
	plan := uuid.NewString()
	exec(`INSERT INTO provider_subscription_plans(id,code,listing_limit,badge_tier) VALUES($1,'provider-monthly',5,'bronze')`, plan)
	providers, users := []string{}, []string{}
	for _, name := range []string{"Provider A", "Provider B"} {
		user, provider, sub := uuid.NewString(), uuid.NewString(), uuid.NewString()
		providers = append(providers, provider)
		users = append(users, user)
		exec(`INSERT INTO users(id,full_name) VALUES($1,$2)`, user, name)
		exec(`INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status) VALUES($1,$2,$3,'approved')`, provider, user, name)
		exec(`INSERT INTO provider_members(provider_id,user_id,role) VALUES($1,$2,'owner')`, provider, user)
		exec(`INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,current_period_end) VALUES($1,$2,$3,'active',now()+interval '1 month')`, sub, provider, plan)
		exec(`INSERT INTO provider_subscription_payments VALUES($1,500,'NGN')`, sub)
		feed := communityCall(t, app, "GET", "/hall", user, "", 200)
		if feed["can_create"] != true {
			t.Fatal(feed)
		}
	}
	room := communityCall(t, app, "POST", "/hall", buyer, `{"message":"I need a driver","client_request_id":"`+uuid.NewString()+`"}`, 201)["id"].(string)
	if result := communityCall(t, app, "GET", "/hall/activity", buyer, "", 200); result["count"] != float64(0) {
		t.Fatal("own request counted", result)
	}
	if feed := communityCall(t, app, "GET", "/hall", buyer, "", 200); feed["can_create"] != true {
		t.Fatal(feed)
	}
	for i, user := range users {
		communityCall(t, app, "POST", "/hall/"+room+"/apply", user, "", 204)
		communityCall(t, app, "POST", "/hall/"+room+"/providers/"+providers[i], buyer, `{"action":"approve"}`, 204)
	}
	var before, after int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM notifications`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	communityCall(t, app, "POST", "/hall/"+room+"/providers/"+providers[0], buyer, `{"action":"approve"}`, 204)
	if err := db.QueryRow(ctx, `SELECT count(*) FROM notifications`).Scan(&after); err != nil || before != after {
		t.Fatal("approval retry duplicated notification")
	}
	exec(`DELETE FROM notifications`)
	parent := communityCall(t, app, "POST", "/rooms/"+room+"/messages", users[0], `{"message":"I can arrive at noon","client_message_id":"`+uuid.NewString()+`"}`, 201)["id"].(string)
	if result := communityCall(t, app, "GET", "/hall/activity", users[0], "", 200); result["count"] != float64(0) {
		t.Fatal("own message counted", result)
	}
	if result := communityCall(t, app, "GET", "/hall/activity", buyer, "", 200); result["count"] != float64(1) {
		t.Fatal("peer activity missing", result)
	}
	ref := uuid.NewString()
	body := `{"message":"I can arrive earlier","client_message_id":"` + ref + `","reply_to_id":"` + parent + `"}`
	reply := communityCall(t, app, "POST", "/rooms/"+room+"/messages", users[1], body, 201)
	quote := reply["reply_to"].(map[string]any)
	if quote["id"] != parent || quote["sender_name"] != "Provider A" || quote["body"] != "I can arrive at noon" {
		t.Fatal(quote)
	}
	repeated := communityCall(t, app, "POST", "/rooms/"+room+"/messages", users[1], body, 201)
	if repeated["id"] != reply["id"] {
		t.Fatal("duplicate reply")
	}
	communityCall(t, app, "POST", "/rooms/"+room+"/messages", users[1], `{"message":"I can arrive earlier","client_message_id":"`+ref+`"}`, 409)
	history := communityCall(t, app, "GET", "/rooms/"+room+"/messages", buyer, "", 200)
	items := history["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["reply_to"] == nil {
		t.Fatal(history)
	}
	communityCall(t, app, "POST", "/rooms/"+room+"/messages", outsider, body, 403)
	otherRoom := communityCall(t, app, "POST", "/hall", other, `{"message":"A separate request","client_request_id":"`+uuid.NewString()+`"}`, 201)["id"].(string)
	communityCall(t, app, "POST", "/rooms/"+otherRoom+"/messages", other, `{"message":"Cross room quote","client_message_id":"`+uuid.NewString()+`","reply_to_id":"`+parent+`"}`, 422)
	var wrong int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM notifications n JOIN chat_messages m ON n.event_key LIKE 'shared-chat:'||m.id::text||':%' WHERE n.user_id=m.sender_user_id`).Scan(&wrong); err != nil || wrong != 0 {
		t.Fatal("self notification", wrong, err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE type='social_message'`).Scan(&after); err != nil || after != 4 {
		t.Fatal("peer notifications or retry dedup", after, err)
	}
}
