package controllers

import (
	"context"
	"github.com/google/uuid"
	"reflect"
	"testing"
	"time"
)

func TestThreadWriteSelectionBoundedMatchesLegacy(t *testing.T) {
	db := communityTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `ALTER TABLE provider_conversations ADD COLUMN thread_id uuid;
 CREATE INDEX selection_pair ON provider_conversations(provider_id,user_id,thread_id);
 CREATE INDEX selection_history ON provider_conversation_messages(conversation_id,created_at,id);`)
	if err != nil {
		t.Fatal(err)
	}
	root, alias, empty, other, provider, buyer := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err = db.Exec(ctx, `INSERT INTO provider_conversations(id,provider_id,user_id,thread_id,status) VALUES($1,$5,$6,$1,'open'),($2,$5,$6,$1,'open'),($3,$5,$6,$3,'open'),($4,$5,gen_random_uuid(),$4,'blocked')`, root, alias, empty, other, provider, buyer)
	if err != nil {
		t.Fatal(err)
	}
	compare := func() {
		t.Helper()
		for _, id := range []string{root, alias, empty, other} {
			var before, after []any
			for i, q := range []string{baselineWriteSelectionSQL(), businessWriteSelectionSQL()} {
				rows, e := db.Query(ctx, q, id)
				if e != nil {
					t.Fatal(e)
				}
				if !rows.Next() {
					rows.Close()
					t.Fatal("selection missing")
				}
				values, e := rows.Values()
				rows.Close()
				if e != nil {
					t.Fatal(e)
				}
				if i == 0 {
					before = values
				} else {
					after = values
				}
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("selection differs: %v / %v", before, after)
			}
		}
	}
	compare() // Empty histories and unrelated blocked thread.
	_, err = db.Exec(ctx, `INSERT INTO provider_conversation_messages(id,conversation_id,sender_type,created_at) SELECT gen_random_uuid(),c.id,'buyer','2026-01-01'::timestamptz+i*interval '1 second' FROM provider_conversations c CROSS JOIN generate_series(1,3000)i WHERE c.id IN($1,$2);
 INSERT INTO provider_conversation_messages(id,conversation_id,sender_type,created_at) VALUES('00000000-0000-4000-8000-000000000001',$1,'buyer','2026-02-01'),('ffffffff-ffff-4fff-8fff-ffffffffffff',$2,'provider','2026-02-01'); ANALYZE;`, root, alias)
	if err != nil {
		t.Fatal(err)
	}
	compare() // Latest alias selection, including timestamp tie broken by UUID.
	if _, err = db.Exec(ctx, `UPDATE provider_conversations SET status='blocked' WHERE id=$1`, alias); err != nil {
		t.Fatal(err)
	}
	compare() // Any blocked member still blocks the grouped thread.
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		for arm, candidate := range []bool{false, true, true, false} {
			q := baselineWriteSelectionSQL()
			if candidate {
				q = businessWriteSelectionSQL()
			}
			tx, e := db.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback(ctx)
			if _, e = tx.Exec(ctx, "SET LOCAL plan_cache_mode="+mode); e != nil {
				t.Fatal(e)
			}
			var plan []byte
			if e = tx.QueryRow(ctx, "EXPLAIN(ANALYZE,BUFFERS,FORMAT JSON) "+q, root).Scan(&plan); e != nil {
				t.Fatal(e)
			}
			began := time.Now()
			for i := 0; i < 40; i++ {
				var thread, write string
				var blocked bool
				if e = tx.QueryRow(ctx, q, root).Scan(&thread, &write, &blocked); e != nil {
					t.Fatal(e)
				}
				if thread != root || write != alias || !blocked {
					t.Fatal("thread contract changed")
				}
			}
			t.Logf("THREAD_SELECTION_RESULT mode=%s arm=%d candidate=%v requests=40 seconds=%.6f plan=%s", mode, arm+1, candidate, time.Since(began).Seconds(), plan)
			if e = tx.Rollback(ctx); e != nil {
				t.Fatal(e)
			}
		}
	}
	if _, err = db.Exec(ctx, "ALTER TABLE provider_conversations DROP COLUMN thread_id"); err != nil {
		t.Fatal(err)
	}
	compare() // Pre-migration JSON fallback must retain physical ownership.
}
