package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
)

// Diagnostic only: compare query plans without weakening or changing the room lock.
func TestTownHallLockMatchedProfile(t *testing.T) {
	if os.Getenv("TOWN_HALL_LOCK_PROFILE") != "true" {
		t.Skip("explicit disposable profile only")
	}
	db := communityTestDB(t)
	ctx := context.Background()
	owner := uuid.NewString()
	_, err := db.Exec(ctx, `INSERT INTO users(id,full_name) VALUES($1,'Synthetic owner')`, owner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO chat_rooms(id,kind,owner_user_id,title) SELECT gen_random_uuid(),'town_hall',$1,'Synthetic room' FROM generate_series(1,100050)`, owner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO town_hall_requests(room_id,description,location,unit,message_only,engagement_deadline) SELECT id,'Synthetic request','','',true,clock_timestamp()+interval '24 hours' FROM chat_rooms; ANALYZE chat_rooms; ANALYZE town_hall_requests`)
	if err != nil {
		t.Fatal(err)
	}
	var room string
	if err = db.QueryRow(ctx, `SELECT id::text FROM chat_rooms LIMIT 1`).Scan(&room); err != nil {
		t.Fatal(err)
	}
	original := `SELECT kind,CASE WHEN EXISTS(SELECT 1 FROM town_hall_requests h WHERE h.room_id=chat_rooms.id AND h.engagement_deadline<=clock_timestamp()) THEN 'closed' ELSE state END FROM chat_rooms WHERE id=$1::uuid FOR UPDATE`
	candidate := `SELECT r.kind,CASE WHEN h.engagement_deadline<=clock_timestamp() THEN 'closed' ELSE r.state END FROM chat_rooms r LEFT JOIN town_hall_requests h ON h.room_id=r.id WHERE r.id=$1::uuid FOR UPDATE OF r`
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		for arm, query := range []string{original, candidate, candidate, original} {
			tx, e := db.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = tx.Exec(ctx, "SET LOCAL plan_cache_mode="+mode); e != nil {
				t.Fatal(e)
			}
			if _, e = tx.Exec(ctx, "PREPARE hall_lock_profile(uuid) AS "+query); e != nil {
				t.Fatal(e)
			}
			for n := 0; n < 3; n++ {
				var raw []byte
				if e = tx.QueryRow(ctx, fmt.Sprintf("EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) EXECUTE hall_lock_profile('%s')", room)).Scan(&raw); e != nil {
					t.Fatal(e)
				}
				var plan []map[string]any
				if e = json.Unmarshal(raw, &plan); e != nil {
					t.Fatal(e)
				}
				t.Logf("HALL_LOCK_PLAN mode=%s arm=%d sample=%d plan=%s", mode, arm, n, raw)
			}
			if _, e = tx.Exec(ctx, "DEALLOCATE hall_lock_profile"); e != nil {
				t.Fatal(e)
			}
			if e = tx.Rollback(ctx); e != nil {
				t.Fatal(e)
			}
		}
	}
}
