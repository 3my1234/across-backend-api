package controllers

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"testing"
	"time"

	"across/backend/internal/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestAsyncMessageFanoutMatchedCriticalSection(t *testing.T) {
	if os.Getenv("FANOUT_PROFILE_LOCAL") != "true" {
		t.Skip("explicit disposable transaction profile only")
	}
	db, sender := asyncFanoutDB(t)
	ctx := context.Background()
	room := uuid.NewString()
	_, err := db.Exec(ctx, `INSERT INTO chat_rooms(id,kind,owner_user_id,title) VALUES($1,'town_hall',$2,'Profile');
INSERT INTO users(id,full_name) SELECT md5('profile-peer:'||i)::uuid,'Synthetic' FROM generate_series(1,10)i;
INSERT INTO chat_participants(room_id,user_id,role,state) SELECT $1,md5('profile-peer:'||i)::uuid,'member','active' FROM generate_series(1,10)i;
INSERT INTO user_push_tokens(id,user_id) SELECT gen_random_uuid(),md5('profile-peer:'||i)::uuid FROM generate_series(1,10)i;
INSERT INTO web_push_subscriptions(user_id,endpoint,p256dh,auth,vapid_public_key)
SELECT md5('profile-peer:'||i)::uuid,'https://example.invalid/push/'||i,'key','auth','vapid' FROM generate_series(1,10)i;`, pgx.QueryExecModeSimpleProtocol, room, sender)
	if err != nil {
		t.Fatal(err)
	}
	results := map[bool][]float64{false: {}, true: {}}
	for _, async := range []bool{false, true, true, false} {
		c := &CommunityController{db: db, market: &ProviderMarketplaceController{db: db, cfg: config.Config{AsyncMessageNotifications: async}}}
		for i := 0; i < 15; i++ {
			tx, err := db.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			var one int
			if err = tx.QueryRow(ctx, `SELECT 1 FROM chat_rooms WHERE id=$1 FOR UPDATE`, room).Scan(&one); err != nil {
				tx.Rollback(ctx)
				t.Fatal(err)
			}
			if err = c.notifyRoom(ctx, tx, room, sender, uuid.NewString(), "New chat message", "SECRET"); err != nil {
				tx.Rollback(ctx)
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			ms := float64(time.Since(started)) / 1e6
			if i >= 3 {
				results[async] = append(results[async], ms)
			}
			// Equal notification/delivery work in both arms; the async worker's
			// work stays OUTSIDE the measured room-lock critical section.
			if async {
				drainFanout(t, db)
			}
		}
	}
	median := func(xs []float64) float64 { sort.Float64s(xs); return (xs[len(xs)/2-1] + xs[len(xs)/2]) / 2 }
	raw, _ := json.Marshal(map[string]any{"recipients": 10, "samples_per_mode": 24, "inline_critical_median_ms": median(results[false]), "async_critical_median_ms": median(results[true]), "scope": "disposable uncontended room lock through notification/job commit; worker drained outside timing; not HTTP/VPS capacity"})
	t.Log("MESSAGE_FANOUT_PROFILE " + string(raw))
	var complete bool
	if err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM notifications)=600 AND (SELECT count(*) FROM notification_push_deliveries)=600 AND (SELECT count(*) FROM web_push_deliveries)=600 AND (SELECT count(*) FROM message_notification_jobs WHERE completed_at IS NULL)=0`).Scan(&complete); err != nil || !complete {
		t.Fatal("profile lost notification/delivery work", err)
	}
}
