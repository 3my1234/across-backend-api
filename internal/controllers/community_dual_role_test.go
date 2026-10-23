package controllers

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestCommunityDualRoleBuyerAndOwnBusinessGuards(t *testing.T) {
	db := communityTestDB(t)
	ctx := context.Background()
	app := communityApp(db)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	owner, staff, buyer := uuid.NewString(), uuid.NewString(), uuid.NewString()
	provider, plan, subscription := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, user := range []string{owner, staff, buyer} {
		exec(`INSERT INTO users(id,full_name) VALUES($1,'Personal name')`, user)
	}
	exec(`INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status) VALUES($1,$2,'Business name','approved')`, provider, owner)
	for _, user := range []string{owner, staff} {
		exec(`INSERT INTO provider_members(provider_id,user_id,role) VALUES($1,$2,'owner')`, provider, user)
	}
	exec(`INSERT INTO provider_subscription_plans(id,code,listing_limit,badge_tier) VALUES($1,'provider-monthly',5,'bronze')`, plan)
	exec(`INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,current_period_end) VALUES($1,$2,$3,'active',now()+interval '1 month')`, subscription, provider, plan)
	exec(`INSERT INTO provider_subscription_payments VALUES($1,500,'NGN')`, subscription)
	create := func(user string) string {
		t.Helper()
		body := `{"message":"I need an independent mechanic","client_request_id":"` + uuid.NewString() + `"}`
		room := communityCall(t, app, "POST", "/hall", user, body, 201)["id"].(string)
		if retry := communityCall(t, app, "POST", "/hall", user, body, 201); retry["id"] != room {
			t.Fatal("request retry duplicated identity or room")
		}
		var savedOwner, role string
		if err := db.QueryRow(ctx, `SELECT r.owner_user_id::text,p.role FROM chat_rooms r JOIN chat_participants p ON p.room_id=r.id AND p.user_id=r.owner_user_id WHERE r.id=$1`, room).Scan(&savedOwner, &role); err != nil || savedOwner != user || role != "buyer" {
			t.Fatalf("buyer identity changed: %s %s %v", savedOwner, role, err)
		}
		if feed := communityCall(t, app, "GET", "/hall?mine=true", user, "", 200); feed["can_create"] != true {
			t.Fatal(feed)
		}
		return room
	}
	room := create(owner)
	communityCall(t, app, "POST", "/hall/"+room+"/apply", owner, "", 422)
	communityCall(t, app, "POST", "/hall/"+room+"/apply", staff, "", 403)
	staffRoom := create(staff)
	communityCall(t, app, "POST", "/hall/"+staffRoom+"/apply", owner, "", 403)
	// Legacy/stale applications, admission and offers must not bypass the guard.
	exec(`INSERT INTO town_hall_applications(room_id,provider_id,requester_user_id) VALUES($1,$2,$3)`, room, provider, staff)
	communityCall(t, app, "POST", "/hall/"+room+"/providers/"+provider, owner, `{"action":"approve"}`, 403)
	exec(`INSERT INTO chat_participants(room_id,user_id,provider_id,role) VALUES($1,$2,$3,'provider')`, room, staff, provider)
	communityCall(t, app, "POST", "/hall/"+room+"/offers", staff, `{"unit_price":500,"client_message_id":"`+uuid.NewString()+`"}`, 403)
	communityCall(t, app, "POST", "/rooms/"+room+"/messages", staff, `{"message":"Self deal","client_message_id":"`+uuid.NewString()+`"}`, 403)
	offer := uuid.NewString()
	exec(`INSERT INTO town_hall_offers(id,room_id,provider_id,sender_user_id,unit_price,terms) VALUES($1,$2,$3,$4,500,'Stale offer')`, offer, room, provider, staff)
	communityCall(t, app, "POST", "/hall/"+room+"/choose", owner, `{"offer_id":"`+offer+`","close_room":true}`, 403)
	// Free, expired and restricted businesses retain their customer capabilities.
	for _, condition := range []string{"free", "expired", "restricted"} {
		t.Run(condition, func(t *testing.T) {
			switch condition {
			case "free":
				exec(`UPDATE provider_subscriptions SET status='cancelled' WHERE id=$1`, subscription)
			case "expired":
				exec(`UPDATE provider_subscriptions SET status='active',current_period_end=now()-interval '1 second' WHERE id=$1`, subscription)
			case "restricted":
				exec(`UPDATE provider_organizations SET is_active=false,verification_status='rejected' WHERE id=$1`, provider)
			}
			request := create(owner)
			communityCall(t, app, "GET", "/hall/"+request, owner, "", 200)
			communityCall(t, app, "POST", "/rooms/"+request+"/messages", owner, `{"message":"My customer message","client_message_id":"`+uuid.NewString()+`"}`, 201)
			communityCall(t, app, "POST", "/hall/"+request+"/close", staff, "", 403)
			communityCall(t, app, "POST", "/hall/"+request+"/close", owner, "", 204)
			if condition != "restricted" {
				other := create(buyer)
				communityCall(t, app, "POST", "/hall/"+other+"/apply", owner, "", 403)
			}
		})
	}
	var people, businesses int
	if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM users),(SELECT count(*) FROM provider_organizations)`).Scan(&people, &businesses); err != nil || people != 3 || businesses != 1 {
		t.Fatalf("account continuity: %d %d %v", people, businesses, err)
	}
}
