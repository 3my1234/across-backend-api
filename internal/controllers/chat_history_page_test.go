package controllers

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestBusinessHistoryBoundedMergeMatchesOriginal(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE provider_conversations(id uuid PRIMARY KEY,provider_id uuid,user_id uuid,thread_id uuid);
 CREATE TABLE provider_conversation_messages(id uuid PRIMARY KEY,conversation_id uuid,sender_type text,body text,created_at timestamptz,media_keys text[] DEFAULT '{}',location jsonb,context jsonb DEFAULT '{}');
 CREATE INDEX history_page ON provider_conversation_messages(conversation_id,created_at,id);
 INSERT INTO provider_conversations SELECT md5('c'||i)::uuid,md5('p1')::uuid,md5('u1')::uuid,md5('c1')::uuid FROM generate_series(1,4)i;
 INSERT INTO provider_conversations VALUES(md5('c5')::uuid,md5('p1')::uuid,md5('outsider')::uuid,md5('c1')::uuid),(md5('c6')::uuid,md5('p1')::uuid,md5('u1')::uuid,md5('c6')::uuid);
 INSERT INTO provider_conversation_messages SELECT md5('m'||i||':'||j)::uuid,md5('c'||i)::uuid,'buyer','synthetic '||i||':'||j,'2026-01-01'::timestamptz+(j/3)*interval '1 second','{}',CASE WHEN j%7=0 THEN '{"latitude":9,"longitude":7}'::jsonb END,jsonb_build_object('title','product '||i) FROM generate_series(1,6)i CROSS JOIN generate_series(1,40)j;`)
	if err != nil {
		t.Fatal(err)
	}
	read := func(sql, thread string, stamp, id any, limit int) [][]any {
		t.Helper()
		rows, e := db.Query(ctx, sql, thread, stamp, id, limit)
		if e != nil {
			t.Fatal(e)
		}
		defer rows.Close()
		result := [][]any{}
		for rows.Next() {
			v, e := rows.Values()
			if e != nil {
				t.Fatal(e)
			}
			result = append(result, v)
		}
		if rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		return result
	}
	for _, thread := range []string{historyFixtureUUID("c1"), historyFixtureUUID("c3"), historyFixtureUUID("missing")} {
		for _, limit := range []int{1, 2, 17, 51} {
			var stamp, id any
			seen := map[string]bool{}
			for page := 0; page < 200; page++ {
				original := read(originalHistoryQuery(), thread, stamp, id, limit)
				optimized := read(businessHistoryPageSQL(), thread, stamp, id, limit)
				if !reflect.DeepEqual(original, optimized) {
					a, _ := json.Marshal(original)
					b, _ := json.Marshal(optimized)
					t.Fatalf("page mismatch thread=%s limit=%d page=%d: %s vs %s", thread, limit, page, a, b)
				}
				for _, v := range optimized {
					key := v[0].(string)
					if seen[key] {
						t.Fatal("duplicate across cursor pages")
					}
					seen[key] = true
					if v[2].(string) == "synthetic 5:1" {
						t.Fatal("cross-user message leak")
					}
				}
				if len(original) == 0 {
					break
				}
				last := original[len(original)-1]
				stamp = last[3].(time.Time)
				id = last[0]
				if page == 199 {
					t.Fatal("pagination did not terminate")
				}
			}
			expected := 160
			if thread == historyFixtureUUID("missing") {
				expected = 0
			}
			if len(seen) != expected {
				t.Fatalf("missing/leaked messages: got %d want %d", len(seen), expected)
			}
		}
	}
	// Before the optional thread/location/context columns exist, retain the old
	// physical-conversation fallback rather than requiring a schema migration.
	if _, err = db.Exec(ctx, `ALTER TABLE provider_conversations DROP COLUMN thread_id; ALTER TABLE provider_conversation_messages DROP COLUMN location; ALTER TABLE provider_conversation_messages DROP COLUMN context;`); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []pgx.QueryExecMode{pgx.QueryExecModeExec, pgx.QueryExecModeCacheStatement} {
		conn, e := db.Acquire(ctx)
		if e != nil {
			t.Fatal(e)
		}
		for _, q := range []string{originalHistoryQuery(), businessHistoryPageSQL()} {
			rows, e := conn.Query(ctx, q, mode, historyFixtureUUID("c1"), nil, nil, 51)
			if e != nil {
				conn.Release()
				t.Fatal(e)
			}
			count := 0
			for rows.Next() {
				count++
			}
			e = rows.Err()
			rows.Close()
			if e != nil || count != 40 {
				conn.Release()
				t.Fatal("old schema fallback", count, e)
			}
		}
		conn.Release()
	}
}
