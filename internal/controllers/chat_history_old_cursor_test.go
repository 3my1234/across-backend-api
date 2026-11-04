package controllers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Correctness guard for the known generic-plan limitation: an old cursor can
// scan newer rows, but must still return exactly the original page and order.
func TestBusinessHistoryOldCursorGenericPlanMatchesOriginal(t *testing.T) {
	if os.Getenv("CHAT_HISTORY_INVESTIGATE") != "true" {
		t.Skip("explicit isolated large-history investigation")
	}
	db := historyInvestigationDB(t)
	ctx := context.Background()
	conn, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err = conn.Exec(ctx, "SET plan_cache_mode=force_generic_plan"); err != nil {
		t.Fatal(err)
	}
	var baseline [][]any
	for _, v := range []struct{ name, query string }{{"old_cursor_original", originalHistoryQuery()}, {"old_cursor_optimized", businessHistoryPageSQL()}} {
		if _, err = conn.Exec(ctx, "PREPARE "+v.name+"(uuid,timestamptz,uuid,int) AS "+v.query); err != nil {
			t.Fatal(err)
		}
		execute := "EXECUTE " + v.name + "(md5('c3')::uuid,'2026-01-01 00:02:30+00'::timestamptz,'ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid,51)"
		rows, e := conn.Query(ctx, execute)
		if e != nil {
			t.Fatal(e)
		}
		items := [][]any{}
		for rows.Next() {
			values, e := rows.Values()
			if e != nil {
				t.Fatal(e)
			}
			items = append(items, values)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			t.Fatal(e)
		}
		if len(items) != 51 {
			t.Fatal("old generic page length", len(items))
		}
		if baseline == nil {
			baseline = items
		} else if !reflect.DeepEqual(baseline, items) {
			t.Fatal("generic old cursor changed page contents/order")
		}
		var plan json.RawMessage
		if err = conn.QueryRow(ctx, "EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) "+execute).Scan(&plan); err != nil {
			t.Fatal(err)
		}
		if dir := os.Getenv("CHAT_HISTORY_EVIDENCE_DIR"); dir != "" {
			if err = os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(dir, v.name+".json"), plan, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Log("Generic prepared old-page contents, membership and descending order match the original; this is not a claim of bounded generic cursor scanning")
}
