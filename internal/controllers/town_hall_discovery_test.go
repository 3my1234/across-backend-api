package controllers

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestTownHallSearchTopicsAreasAndCursor(t *testing.T) {
	db := communityTestDB(t)
	ctx := context.Background()
	app := communityApp(db)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	raw, e := os.ReadFile("../../migrations/105_town_hall_search.sql")
	if e != nil {
		t.Fatal(e)
	}
	exec(string(raw))
	exec(`ALTER TABLE provider_organizations ADD COLUMN city text DEFAULT 'Abuja'`)
	buyer, other, providerUser, provider := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, id := range []string{buyer, other, providerUser} {
		exec(`INSERT INTO users(id,full_name)VALUES($1,'Test account')`, id)
	}
	exec(`INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status)VALUES($1,$2,'Grocer','approved')`, provider, providerUser)
	exec(`INSERT INTO provider_members(provider_id,user_id,role)VALUES($1,$2,'owner')`, provider, providerUser)
	exec(`INSERT INTO products(id,provider_id,title,category_path,moderation_status,is_active)VALUES($1,$2,'Beans supply',ARRAY['Groceries'],'approved',true)`, uuid.NewString(), provider)
	create := func(message, kind, category, pickup, destination string) (string, string) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"message": message, "client_request_id": uuid.NewString(), "request_kind": kind, "category": category, "pickup_area": pickup, "destination_area": destination})
		response := communityCall(t, app, "POST", "/hall", buyer, string(body), 201)
		return response["id"].(string), string(body)
	}
	beans, body := create("I need 100 bags of beans", "product", "Foodstuff", "Wuse 2", "Garki")
	if communityCall(t, app, "POST", "/hall", buyer, body, 201)["id"] != beans {
		t.Fatal("metadata changed idempotent identity")
	}
	altered := strings.Replace(body, "Garki", "Maitama", 1)
	communityCall(t, app, "POST", "/hall", buyer, altered, 409)
	legacy, _ := create("Looking for cowpeas for my shop", "", "", "", "")
	courier, _ := create("I need a courier to deliver a parcel", "transport", "Courier", "Wuse 2", "Garki")
	reverse, _ := create("Dispatch rider from Garki to Wuse 2", "transport", "Courier", "Garki", "Wuse 2")
	rice, _ := create("I need 50 bags of rice", "product", "Groceries", "Garki", "")
	_, _ = create("Need a plumber to repair a tap", "service", "Plumbing", "Garki", "")
	expired, _ := create("Need beans urgently", "product", "Foodstuff", "Wuse 2", "")
	exec(`UPDATE town_hall_requests SET engagement_deadline=now()-interval '1 second' WHERE room_id=$1::uuid`, expired)
	closed, _ := create("Supply beans please", "product", "Foodstuff", "Wuse 2", "")
	exec(`UPDATE chat_rooms SET state='closed' WHERE id=$1::uuid`, closed)
	ids := func(path, actor string) map[string]bool {
		t.Helper()
		out := map[string]bool{}
		for _, raw := range communityCall(t, app, "GET", path, actor, "", 200)["items"].([]any) {
			out[raw.(map[string]any)["id"].(string)] = true
		}
		return out
	}
	result := ids("/hall?q=beans", providerUser)
	if len(result) != 2 || !result[beans] || !result[legacy] {
		t.Fatal("bean synonyms/active visibility", result)
	}
	result = ids("/hall?category=foodstuff", providerUser)
	if !result[beans] || !result[rice] || !result[legacy] || result[courier] {
		t.Fatal("related categories and cautious unlabelled text", result)
	}
	result = ids("/hall?request_kind=transport&location=Wuse", providerUser)
	if len(result) != 1 || !result[courier] || result[reverse] {
		t.Fatal("pickup and destination confused", result)
	}
	result = ids("/hall?for_business=true", providerUser)
	if !result[beans] || !result[rice] || result[courier] {
		t.Fatal("actual business topics", result)
	}
	communityCall(t, app, "GET", "/hall?for_business=true", other, "", 403)
	if len(ids("/hall?request_kind=product", other)) != 2 {
		t.Fatal("creator supplied kinds or active state ignored")
	}
	first := communityCall(t, app, "GET", "/hall?q=beans&limit=1", providerUser, "", 200)
	cursor := first["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("missing relevance cursor")
	}
	second := communityCall(t, app, "GET", "/hall?q=beans&limit=1&cursor="+url.QueryEscape(cursor), providerUser, "", 200)
	if len(second["items"].([]any)) != 1 || first["items"].([]any)[0].(map[string]any)["id"] == second["items"].([]any)[0].(map[string]any)["id"] {
		t.Fatal("cursor repeated/skipped ranked request")
	}
	communityCall(t, app, "GET", "/hall?q=rice&cursor="+url.QueryEscape(cursor), providerUser, "", 400)
	communityCall(t, app, "GET", "/hall?q=beans&cursor="+url.QueryEscape(cursor), other, "", 400)
	communityCall(t, app, "GET", "/hall?since_hours=invalid", other, "", 422)
	detail := communityCall(t, app, "GET", "/hall/"+courier, buyer, "", 200)
	if detail["pickup_area"] != "Wuse 2" || detail["destination_area"] != "Garki" {
		t.Fatal("transport context lost", detail)
	}
	var participants, indexes int
	if e = db.QueryRow(ctx, `SELECT count(*) FROM chat_participants WHERE role='provider'`).Scan(&participants); e != nil || participants != 0 {
		t.Fatal("search auto-admitted providers", participants, e)
	}
	if e = db.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND indexname IN ('town_hall_request_search','town_hall_pickup_search','town_hall_category_search','town_hall_request_kind_feed')`).Scan(&indexes); e != nil || indexes != 4 {
		t.Fatal("search indexes missing", indexes, e)
	}
}
func TestTownHallSearchVocabularyBounded(t *testing.T) {
	if q := hallTextQuery("100 bags of beans", true); !strings.Contains(q, "cowpea") || strings.Contains(q, "100") {
		t.Fatal(q)
	}
	if len(hallTextQuery(strings.Repeat("beans ", 10000), true)) > 300 {
		t.Fatal("query expansion unbounded")
	}
	if strings.Contains(hallTextQuery("beans';DROP TABLE users;--", true), "'") {
		t.Fatal("raw query syntax forwarded")
	}
}
