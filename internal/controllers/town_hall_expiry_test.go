package controllers

import (
	"context"
	"github.com/google/uuid"
	"testing"
)

func TestTownHallHardLifetimeCleanupAndReportEvidence(t *testing.T) {
	db := communityTestDB(t)
	defer db.Close()
	ctx := context.Background()
	app := communityApp(db)
	buyer, peer := uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(ctx, `INSERT INTO users(id,full_name) VALUES($1,'Buyer'),($2,'Peer')`, buyer, peer); err != nil {
		t.Fatal(err)
	}
	create := func() string {
		return communityCall(t, app, "POST", "/hall", buyer, `{"client_request_id":"`+uuid.NewString()+`","message":"Need a supplier today"}`, 201)["id"].(string)
	}
	idle, engaged, reported, ended, live := create(), create(), create(), create(), create()
	var seconds float64
	if err := db.QueryRow(ctx, `SELECT extract(epoch from h.engagement_deadline-r.created_at) FROM town_hall_requests h JOIN chat_rooms r ON r.id=h.room_id WHERE r.id=$1`, live).Scan(&seconds); err != nil || seconds < 86399 || seconds > 86401 {
		t.Fatalf("lifetime=%v err=%v", seconds, err)
	}
	communityCall(t, app, "POST", "/rooms/"+reported+"/messages", buyer, `{"message":"Evidence for a report","client_message_id":"`+uuid.NewString()+`"}`, 201)
	communityCall(t, app, "POST", "/rooms/"+reported+"/reports", buyer, `{"reason":"Please review this request"}`, 201)
	if _, err := db.Exec(ctx, `UPDATE town_hall_requests SET first_provider_engaged_at=now() WHERE room_id=$1`, engaged); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE town_hall_requests SET engagement_deadline=clock_timestamp()-interval '1 second' WHERE room_id=ANY($1::uuid[])`, []string{idle, engaged, reported}); err != nil {
		t.Fatal(err)
	}
	for _, room := range []string{idle, engaged, reported} {
		communityCall(t, app, "GET", "/hall/"+room, buyer, "", 404)
		communityCall(t, app, "GET", "/rooms/"+room+"/messages", buyer, "", 410)
		communityCall(t, app, "POST", "/rooms/"+room+"/messages", buyer, `{"message":"Too late","client_message_id":"`+uuid.NewString()+`"}`, 409)
	}
	communityCall(t, app, "POST", "/hall/"+ended+"/close", buyer, "", 204)
	for _, path := range []string{"/hall", "/hall?mine=true", "/inbox"} {
		body := communityCall(t, app, "GET", path, buyer, "", 200)
		items := body["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("%s returned ended requests: %v", path, items)
		}
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := ensureDirectTx(ctx, tx, buyer, peer, map[string]any{"town_hall_request_id": engaged, "title": "Selected supplier"})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var removed int
	if err = db.QueryRow(ctx, `SELECT purge_ended_town_hall_requests(250)`).Scan(&removed); err != nil || removed != 3 {
		t.Fatalf("purge=%d err=%v", removed, err)
	}
	if err = db.QueryRow(ctx, `SELECT purge_ended_town_hall_requests(250)`).Scan(&removed); err != nil || removed != 0 {
		t.Fatalf("repeat=%d err=%v", removed, err)
	}
	var evidence, directs int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM chat_messages WHERE room_id=$1`, reported).Scan(&evidence); err != nil || evidence != 1 {
		t.Fatalf("report evidence=%d err=%v", evidence, err)
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM chat_rooms WHERE id=$1 AND kind='direct'`, direct).Scan(&directs); err != nil || directs != 1 {
		t.Fatalf("direct chat deleted: %v", err)
	}
	tx, err = db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE chat_rooms SET state='closed',closed_at=now() WHERE id=$1`, live); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT purge_ended_town_hall_requests(250)`).Scan(&removed); err != nil || removed != 0 {
		t.Fatalf("locked=%d err=%v", removed, err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT purge_ended_town_hall_requests(250)`).Scan(&removed); err != nil || removed != 1 {
		t.Fatalf("unlocked=%d err=%v", removed, err)
	}
}
