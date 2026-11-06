package controllers

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSharedMessageMetadataBatchRollbackAndRetry(t *testing.T) {
	db := communityTestDB(t)
	ctx := context.Background()
	actor, room := uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(ctx, `INSERT INTO users(id,full_name) VALUES($1,'Sender');`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO chat_rooms(id,kind,owner_user_id) VALUES($1,'town_hall',$2)`, room, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO chat_participants(room_id,user_id,role) VALUES($1,$2,'member')`, room, actor); err != nil {
		t.Fatal(err)
	}
	var revision int
	if err := db.QueryRow(ctx, `SELECT revision FROM chat_rooms WHERE id=$1`, room).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	input := conversationMessageInput{Body: "Atomic message", MediaKeys: []string{}, ClientID: uuid.NewString()}
	// Fail the second metadata statement, after the room update has executed.
	if _, err := db.Exec(ctx, `CREATE FUNCTION reject_read_update() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'injected read update failure';END$$;
CREATE TRIGGER reject_read BEFORE UPDATE ON chat_participants FOR EACH ROW EXECUTE FUNCTION reject_read_update();`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = insertSharedMessage(ctx, tx, room, actor, input)
	if err == nil || !strings.Contains(err.Error(), "injected read update failure") {
		t.Fatalf("expected second statement failure, got %v", err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var count, after int
	if err = db.QueryRow(ctx, `SELECT revision,(SELECT count(*) FROM chat_messages WHERE room_id=$1) FROM chat_rooms WHERE id=$1`, room).Scan(&after, &count); err != nil {
		t.Fatal(err)
	}
	if after != revision || count != 0 {
		t.Fatalf("partial write survived: revision=%d messages=%d", after, count)
	}
	if _, err = db.Exec(ctx, `DROP TRIGGER reject_read ON chat_participants`); err != nil {
		t.Fatal(err)
	}
	var original string
	for attempt := 0; attempt < 2; attempt++ {
		tx, err = db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		id, _, fresh, e := insertSharedMessage(ctx, tx, room, actor, input)
		if e != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(e)
		}
		if fresh != (attempt == 0) {
			t.Fatalf("incorrect retry freshness: %v", fresh)
		}
		if attempt == 0 {
			original = id
		} else if id != original {
			t.Fatal("retry created another message")
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.QueryRow(ctx, `SELECT revision,(SELECT count(*) FROM chat_messages WHERE room_id=$1) FROM chat_rooms WHERE id=$1`, room).Scan(&after, &count); err != nil {
		t.Fatal(err)
	}
	if after != revision+1 || count != 1 {
		t.Fatalf("retry changed metadata twice: revision=%d messages=%d", after, count)
	}
}
