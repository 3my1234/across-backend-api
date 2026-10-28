package controllers

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Evaluate only the static SQL string and developer-owned grouping helper from
// the actual handler AST. EXPLAIN never substitutes a hand-written approximation.
func inboxSQLFromHandler(t *testing.T, file, name string) string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var expression ast.Expr
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != name {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == "Query" && expression == nil {
				expression = call.Args[1]
			}
			return true
		})
	}
	var eval func(ast.Expr) string
	eval = func(e ast.Expr) string {
		switch n := e.(type) {
		case *ast.BasicLit:
			v, err := strconv.Unquote(n.Value)
			if err != nil {
				t.Fatal(err)
			}
			return v
		case *ast.BinaryExpr:
			if n.Op != token.ADD {
				t.Fatal("unexpected SQL expression")
			}
			return eval(n.X) + eval(n.Y)
		case *ast.CallExpr:
			id, ok := n.Fun.(*ast.Ident)
			if !ok || id.Name != "businessThreadMembers" || len(n.Args) != 1 {
				t.Fatal("unexpected SQL helper")
			}
			return businessThreadMembers(eval(n.Args[0]))
		default:
			t.Fatalf("unsupported static SQL %T", e)
			return ""
		}
	}
	if expression == nil {
		t.Fatal("actual handler SQL missing")
	}
	return eval(expression)
}

func TestInboxBoundedPageMatchesLegacyAndPlans(t *testing.T) {
	db := communityTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `ALTER TABLE provider_conversation_messages ADD COLUMN body text DEFAULT '';
	ALTER TABLE provider_conversations ADD COLUMN thread_id uuid;
	CREATE INDEX test_business_history ON provider_conversation_messages(conversation_id,created_at,id);
	CREATE INDEX test_business_unread ON provider_conversation_messages(conversation_id,sender_type,created_at);`); err != nil {
		t.Fatal(err)
	}
	buyer, owner, outsider, peer, provider, listing, root, alias, other := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err := db.Exec(ctx, `INSERT INTO users(id,full_name)VALUES($1,'Synthetic buyer'),($2,'Synthetic owner'),($3,'Synthetic outsider'),($4,'Synthetic peer');
	INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status)VALUES($5,$2,'Synthetic business','approved');
	INSERT INTO provider_members(provider_id,user_id,role)VALUES($5,$2,'owner');
	INSERT INTO provider_listings(id,provider_id,title,status)VALUES($6,$5,'Synthetic service','approved');
	INSERT INTO provider_conversations(id,provider_id,user_id,listing_id,status,last_message_at,buyer_last_read_at,provider_last_read_at,thread_id)
	VALUES($7,$5,$1,$6,'open','2026-01-31','2026-01-05','2026-01-05',$7),($8,$5,$1,$6,'closed','2026-01-30','2026-01-01','2026-01-01',$7),($9,$5,$3,$6,'open','2026-01-01',NULL,NULL,$9);
	INSERT INTO provider_conversation_messages(id,conversation_id,sender_type,created_at,body) SELECT gen_random_uuid(),$7,'provider','2026-01-01'::timestamptz+i*interval '1 second','Synthetic older body' FROM generate_series(1,1000)i;
	INSERT INTO provider_conversation_messages(id,conversation_id,sender_type,created_at,body) VALUES(gen_random_uuid(),$7,'provider','2026-01-10','Synthetic root unread'),(gen_random_uuid(),$8,'provider','2026-01-02','Synthetic alias unread'),
	('00000000-0000-4000-8000-000000000001',$8,'provider','2026-01-30','Synthetic tied predecessor'),
	('ffffffff-ffff-4fff-8fff-ffffffffffff',$8,'buyer','2026-01-30','Synthetic latest alias');
	INSERT INTO chat_rooms(id,kind,owner_user_id,title,last_message_at)SELECT gen_random_uuid(),'town_hall',$1,'Synthetic room','2026-01-29'::timestamptz-i*interval '1 second' FROM generate_series(1,60)i;
	INSERT INTO town_hall_requests(room_id,description,location,unit,message_only,first_provider_engaged_at)SELECT id,'Synthetic request','','job',false,now() FROM chat_rooms;
	INSERT INTO chat_participants(room_id,user_id,role,last_read_at)SELECT id,$1,'buyer','2026-01-01' FROM chat_rooms;
	INSERT INTO chat_participants(room_id,user_id,role,state)SELECT id,$4,'member','removed' FROM chat_rooms;
	INSERT INTO chat_messages(room_id,sender_user_id,body,client_message_id,created_at)SELECT id,$4,'Synthetic shared reply',gen_random_uuid(),'2026-01-02' FROM chat_rooms;
	ANALYZE;`, pgx.QueryExecModeSimpleProtocol, buyer, owner, outsider, peer, provider, listing, root, alias, other)
	if err != nil {
		t.Fatal(err)
	}
	app := communityApp(db)
	c := &CommunityController{db: db, market: &ProviderMarketplaceController{db: db}}
	app.Get("/legacy-inbox", c.legacyInbox)
	compare := func(actor, query string) map[string]any {
		t.Helper()
		before := communityCall(t, app, "GET", "/legacy-inbox"+query, actor, "", 200)
		after := communityCall(t, app, "GET", "/inbox"+query, actor, "", 200)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("inbox contract differs for actor/filter: before=%v after=%v", before, after)
		}
		return after
	}
	for _, actor := range []string{buyer, owner, outsider, peer} {
		compare(actor, "")
		for _, id := range []string{root, alias, other, uuid.NewString()} {
			compare(actor, "?conversation_id="+id)
		}
		cursor := ""
		pages := 0
		for {
			query := "?limit=7"
			if cursor != "" {
				query += "&cursor=" + url.QueryEscape(cursor)
			}
			page := compare(actor, query)
			pages++
			if page["has_more"] != true {
				break
			}
			cursor, _ = page["next_cursor"].(string)
			if cursor == "" || pages > 20 {
				t.Fatal("pagination stalled")
			}
		}
	}
	item := compare(buyer, "?conversation_id="+alias)["items"].([]any)[0].(map[string]any)
	if item["id"] != root || item["last_message"] != "Synthetic latest alias" || item["last_message_is_mine"] != true || item["unread_count"] != float64(3) {
		t.Fatal("grouped unread/latest contract", item)
	}
	ownerItem := compare(owner, "?conversation_id="+root)["items"].([]any)[0].(map[string]any)
	if ownerItem["last_message_is_mine"] != false || ownerItem["unread_count"] != float64(1) {
		t.Fatal("provider unread/latest contract", ownerItem)
	}
	if len(compare(outsider, "?conversation_id="+alias)["items"].([]any)) != 0 {
		t.Fatal("unauthorized alias visible")
	}
	beforeSQL := inboxSQLFromHandler(t, "inbox_baseline_test.go", "legacyInbox")
	afterSQL := inboxSQLFromHandler(t, "community_controller.go", "Inbox")
	// Alternating actual SQL plans: same rows, connection limits, bindings and
	// planner mode. Do not force an index or count API warmup as an improvement.
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		for arm, candidate := range []bool{false, true, true, false} {
			sql := beforeSQL
			if candidate {
				sql = afterSQL
			}
			tx, err := db.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err = tx.Exec(ctx, "SET LOCAL plan_cache_mode="+mode); err != nil {
				t.Fatal(err)
			}
			var raw []byte
			if err = tx.QueryRow(ctx, "EXPLAIN(ANALYZE,BUFFERS,FORMAT JSON) "+sql, buyer, "", nil, nil, 26, "").Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			t.Logf("INBOX_PLAN mode=%s arm=%d candidate=%v json=%s", mode, arm+1, candidate, string(raw))
		}
	}
	for arm, candidate := range []bool{false, true, true, false} {
		sql := beforeSQL
		if candidate {
			sql = afterSQL
		}
		samples := make([]float64, 0, 40)
		start := time.Now()
		for i := 0; i < 40; i++ {
			began := time.Now()
			rows, err := db.Query(ctx, sql, buyer, "", nil, nil, 26, "")
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				if _, err = rows.Values(); err != nil {
					t.Fatal(err)
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
			samples = append(samples, float64(time.Since(began))/float64(time.Millisecond))
		}
		sort.Float64s(samples)
		t.Logf("INBOX_SQL_RESULT arm=%d candidate=%v requests=40 seconds=%.6f p95_ms=%.3f p99_ms=%.3f", arm+1, candidate, time.Since(start).Seconds(), samples[37], samples[39])
	}
	// Old schema fallback remains intentional: JSON-based thread lookup must work
	// when the optional migration column is absent, without merging unrelated rows.
	if _, err = db.Exec(ctx, "ALTER TABLE provider_conversations DROP COLUMN thread_id"); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []string{buyer, owner, outsider, peer} {
		compare(actor, "")
		compare(actor, "?conversation_id="+alias)
	}
}
