package controllers

import (
	"context"
	"github.com/google/uuid"
	"testing"
)

func TestCommunityInboxPreviewIsPrivate(t *testing.T) {
	db := communityTestDB(t)
	defer db.Close()
	ctx := context.Background()
	buyer, peer, outsider, room := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, id := range []string{buyer, peer, outsider} {
		if _, err := db.Exec(ctx, `INSERT INTO users(id,full_name,avatar_url)VALUES($1,'Test member','https://example.test/avatar.jpg')`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `INSERT INTO chat_rooms(id,kind,owner_user_id,direct_low,direct_high)VALUES($1,'direct',$2,LEAST($2::uuid,$3::uuid),GREATEST($2::uuid,$3::uuid))`, room, buyer, peer); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO chat_participants(room_id,user_id,role)VALUES($1,$2,'member'),($1,$3,'member')`, room, buyer, peer); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO chat_messages(room_id,sender_user_id,body,client_message_id)VALUES($1,$2,'Private latest reply',$3)`, room, peer, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	app := communityApp(db)
	data := communityCall(t, app, "GET", "/inbox", buyer, "", 200)
	items := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items=%v", items)
	}
	item := items[0].(map[string]any)
	if item["last_message"] != "Private latest reply" || item["avatar_url"] != "https://example.test/avatar.jpg" {
		t.Fatalf("missing preview/avatar %v", item)
	}
	if item["last_message_id"] == "" || item["last_message_is_mine"] != false {
		t.Fatal("inbox preview cannot acknowledge delivery", item)
	}
	other := communityCall(t, app, "GET", "/inbox?conversation_id="+room, outsider, "", 200)
	if len(other["items"].([]any)) != 0 {
		t.Fatal("outsider received private preview")
	}
}
