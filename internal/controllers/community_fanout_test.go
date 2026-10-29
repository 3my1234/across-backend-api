package controllers

import (
	"context"
	"github.com/google/uuid"
	"testing"
)

func TestCommunityFanoutAtomicPrivateAndDeduplicated(t *testing.T) {
	db := communityTestDB(t)
	ctx := context.Background()
	users := make([]string, 9)
	room, message := uuid.NewString(), uuid.NewString()
	for i := range users {
		users[i] = uuid.NewString()
		if _, err := db.Exec(ctx, `INSERT INTO users(id,full_name) VALUES($1,'Synthetic recipient')`, users[i]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `INSERT INTO chat_rooms(id,kind,owner_user_id,title) VALUES($1,'town_hall',$2,'Fanout')`, room, users[0]); err != nil {
		t.Fatal(err)
	}
	for i, user := range users {
		state := "active"
		if i == 3 {
			state = "removed"
		}
		if _, err := db.Exec(ctx, `INSERT INTO chat_participants(room_id,user_id,role,state) VALUES($1,$2,'member',$3)`, room, user, state); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `INSERT INTO social_blocks(blocker_id,blocked_id) VALUES($1,$2),($3,$1)`, users[0], users[1], users[2]); err != nil {
		t.Fatal(err)
	}
	controller := &CommunityController{db: db}
	call := func(commit bool) {
		t.Helper()
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if err = controller.notifyRoom(ctx, tx, room, users[0], message, "New chat message", "Private text must not enter notification preview"); err != nil {
			t.Fatal(err)
		}
		if commit {
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	call(false)
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM notifications`).Scan(&count); err != nil || count != 0 {
		t.Fatal("rolled-back message leaked notifications", count, err)
	}
	call(true)
	call(true)
	rows, err := db.Query(ctx, `SELECT user_id::text,body,data->>'view',data->>'sender_user_id',data->>'chat_room_id',event_key FROM notifications ORDER BY user_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	expected := map[string]bool{}
	for _, id := range users[4:] {
		expected[id] = true
	}
	for rows.Next() {
		var id, body, view, sender, gotRoom, key string
		if err = rows.Scan(&id, &body, &view, &sender, &gotRoom, &key); err != nil {
			t.Fatal(err)
		}
		if !expected[id] || body != "Open your chat to read the message." || view != "messages" || sender != users[0] || gotRoom != room || key != "shared-chat:"+message+":"+id {
			t.Fatal("recipient privacy, event key, or retry contract changed")
		}
		delete(expected, id)
	}
	if rows.Err() != nil || len(expected) != 0 {
		t.Fatal("eligible recipients missing", len(expected), rows.Err())
	}
}
