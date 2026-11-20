package controllers

import (
	"context"
	"github.com/google/uuid"
	"testing"
)

func TestCommunityRoomPresentationAndTerminalReasons(t *testing.T) {
	db := communityTestDB(t)
	app := communityApp(db)
	ctx := context.Background()
	owner, member, outsider, provider := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	safetyExec(t, db, `INSERT INTO users(id,full_name,avatar_url) VALUES($1,'Host','https://media.example.test/host.jpg'),($2,'Provider',''),($3,'Outsider','')`, owner, member, outsider)
	safetyExec(t, db, `INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status)VALUES($1,$2,'Business','approved')`, provider, member)
	room := communityCall(t, app, "POST", "/hall", owner, `{"client_request_id":"`+uuid.NewString()+`","message":"I need a tailor"}`, 201)["id"].(string)
	safetyExec(t, db, `INSERT INTO chat_participants(room_id,user_id,provider_id,role)VALUES($1,$2,$3,'provider')`, room, member, provider)
	communityCall(t, app, "POST", "/rooms/"+room+"/messages", owner, `{"message":"Welcome","client_message_id":"`+uuid.NewString()+`"}`, 201)
	messages := communityCall(t, app, "GET", "/rooms/"+room+"/messages", owner, "", 200)
	first := messages["items"].([]any)[0].(map[string]any)
	if first["sender_is_host"] != true || first["sender_avatar_url"] != "https://media.example.test/host.jpg" {
		t.Fatal("host presentation missing", first)
	}
	detail := communityCall(t, app, "GET", "/hall/"+room, member, "", 200)
	if len(detail["participants"].([]any)) != 2 {
		t.Fatal("admitted participant list missing", detail)
	}
	public := communityCall(t, app, "GET", "/hall/"+room, outsider, "", 200)
	if public["participants"] != nil || public["applications"] != nil {
		t.Fatal("outsider received participant identities", public)
	}
	communityCall(t, app, "GET", "/rooms/"+room+"/messages", outsider, "", 403)
	safetyExec(t, db, `UPDATE chat_participants SET state='removed' WHERE room_id=$1 AND user_id=$2`, room, member)
	removed := communityCall(t, app, "GET", "/rooms/"+room+"/messages", member, "", 403)
	if removed["reason"] != "removed" || removed["items"] != nil {
		t.Fatal("removal reason/history", removed)
	}
	communityCall(t, app, "POST", "/hall/"+room+"/close", owner, "", 204)
	closed := communityCall(t, app, "GET", "/rooms/"+room+"/messages", member, "", 410)
	if closed["reason"] != "request_closed" || closed["items"] != nil {
		t.Fatal("closed reason/history", closed)
	}
	// A later expiry is distinct from a customer closing their request.
	expiredRoom := communityCall(t, app, "POST", "/hall", owner, `{"client_request_id":"`+uuid.NewString()+`","message":"Another request"}`, 201)["id"].(string)
	if _, err := db.Exec(ctx, `UPDATE town_hall_requests SET engagement_deadline=clock_timestamp()-interval '1 second' WHERE room_id=$1`, expiredRoom); err != nil {
		t.Fatal(err)
	}
	expired := communityCall(t, app, "GET", "/rooms/"+expiredRoom+"/messages", owner, "", 410)
	if expired["reason"] != "request_expired" {
		t.Fatal("expiry mislabelled", expired)
	}
}
