package controllers

// Opt-in, loopback-only query investigation. Creates only a randomly named test
// schema through settlementTestDB; no production configuration is accepted.
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const originalBusinessHistorySQL = `SELECT msg.id::text,msg.sender_type,msg.body,msg.created_at,msg.media_keys,to_jsonb(msg)->'location',COALESCE(to_jsonb(msg)->'context','{}'::jsonb)
 FROM provider_conversation_messages msg WHERE msg.conversation_id IN (`

func originalHistoryQuery() string {
	return originalBusinessHistorySQL + businessThreadMembers("$1::uuid") + `) AND ($2::timestamptz IS NULL OR (msg.created_at,msg.id)<($2,$3::uuid)) ORDER BY msg.created_at DESC,msg.id DESC LIMIT $4`
}

func lateralHistoryQuery() string {
	return businessHistoryPageSQL()
}

func TestBusinessHistoryQueryInvestigation(t *testing.T) {
	if os.Getenv("CHAT_HISTORY_INVESTIGATE") != "true" {
		t.Skip("explicit isolated investigation only")
	}
	db := historyInvestigationDB(t)
	ctx := context.Background()
	dir := os.Getenv("CHAT_HISTORY_EVIDENCE_DIR")
	if dir == "" {
		t.Fatal("evidence directory required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		if _, err = conn.Exec(ctx, "SET plan_cache_mode="+mode); err != nil {
			t.Fatal(err)
		}
		for _, q := range []struct{ name, sql string }{{"original", originalHistoryQuery()}, {"lateral", lateralHistoryQuery()}} {
			name := q.name + "_" + mode
			if _, err = conn.Exec(ctx, "PREPARE "+name+"(uuid,timestamptz,uuid,int) AS "+q.sql); err != nil {
				t.Fatal(err)
			}
			for _, c := range []struct{ name, expr string }{{"small", "md5('c1')::uuid"}, {"large", "md5('c2')::uuid"}, {"hot", "md5('c3')::uuid"}, {"grouped", "md5('c5')::uuid"}} {
				for _, cursor := range []struct{ name, values string }{{"first", "NULL,NULL"}, {"cursor", "'2026-01-01 00:02:30+00'::timestamptz,'ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid"}} {
					sql := "EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) EXECUTE " + name + "(" + c.expr + "," + cursor.values + ",51)"
					// Warm both versions identically before recording a plan.
					for i := 0; i < 2; i++ {
						var discard json.RawMessage
						if err = conn.QueryRow(ctx, sql).Scan(&discard); err != nil {
							t.Fatal(err)
						}
					}
					var raw json.RawMessage
					start := time.Now()
					if err = conn.QueryRow(ctx, sql).Scan(&raw); err != nil {
						t.Fatal(err)
					}
					file := q.name + "-" + mode + "-" + c.name + "-" + cursor.name + ".json"
					if err = os.WriteFile(filepath.Join(dir, file), raw, 0600); err != nil {
						t.Fatal(err)
					}
					t.Logf("%s roundtrip=%s", file, time.Since(start))
				}
			}
		}
	}
	fmt.Fprintln(os.Stdout, "Synthetic fixture: 1000 conversations; small5/large1000/hot10000/grouped10000 histories; existing indexes retained")
}

func historyInvestigationDB(t *testing.T) *pgxpool.Pool {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE provider_conversations(id uuid PRIMARY KEY,provider_id uuid,user_id uuid,thread_id uuid,history_revision bigint DEFAULT 1,buyer_last_read_at timestamptz,provider_last_read_at timestamptz,status text DEFAULT 'open',created_at timestamptz DEFAULT now(),last_message_at timestamptz DEFAULT now());
 CREATE INDEX provider_conversations_pair_thread ON provider_conversations(provider_id,user_id,thread_id);
 CREATE TABLE provider_conversation_messages(id uuid PRIMARY KEY,conversation_id uuid,sender_type text,body text,created_at timestamptz,media_keys text[] DEFAULT '{}',location jsonb,context jsonb DEFAULT '{}');
 CREATE INDEX provider_conversation_messages_page ON provider_conversation_messages(conversation_id,created_at,id);
 CREATE INDEX provider_conversation_messages_unread ON provider_conversation_messages(conversation_id,sender_type,created_at);
 INSERT INTO provider_conversations(id,provider_id,user_id,thread_id) SELECT md5('c'||i)::uuid,md5('p'||i)::uuid,md5('u'||i)::uuid,md5('c'||i)::uuid FROM generate_series(1,1000) i;
 INSERT INTO provider_conversation_messages(id,conversation_id,sender_type,body,created_at)
 SELECT md5('m'||i||':'||j)::uuid,md5('c'||i)::uuid,'buyer',repeat('synthetic message ',8),'2026-01-01 00:00:00+00'::timestamptz+j*interval '1 second' FROM generate_series(1,1000) i CROSS JOIN generate_series(1,300) j;
 DELETE FROM provider_conversation_messages WHERE conversation_id=md5('c1')::uuid AND created_at>'2026-01-01 00:00:05+00';
 INSERT INTO provider_conversation_messages(id,conversation_id,sender_type,body,created_at)
 SELECT md5('extra'||i||':'||j)::uuid,md5('c'||i)::uuid,'provider',repeat('synthetic message ',8),'2026-01-01 00:00:00+00'::timestamptz+j*interval '1 second'
 FROM generate_series(2,13) i CROSS JOIN generate_series(301,10000) j WHERE (i=3 OR j<=1000);
 UPDATE provider_conversations SET provider_id=md5('p4')::uuid,user_id=md5('u4')::uuid,thread_id=md5('c4')::uuid WHERE id IN (SELECT md5('c'||i)::uuid FROM generate_series(4,13)i);
 ANALYZE provider_conversations; ANALYZE provider_conversation_messages;`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}
