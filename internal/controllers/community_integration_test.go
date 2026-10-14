package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func communityTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY,full_name text,avatar_url text DEFAULT '',is_active bool DEFAULT true);
 CREATE TABLE provider_organizations(id uuid PRIMARY KEY,owner_user_id uuid,business_name text,logo_url text DEFAULT '',verification_status text,is_active bool DEFAULT true);
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,role text,is_active bool DEFAULT true);
 CREATE TABLE provider_subscription_plans(id uuid PRIMARY KEY,code text,listing_limit int);
 CREATE TABLE provider_subscriptions(id uuid PRIMARY KEY,provider_id uuid,plan_id uuid,status text,starts_at timestamptz,current_period_end timestamptz,created_at timestamptz DEFAULT now());
 CREATE TABLE provider_subscription_payments(subscription_id uuid,amount numeric,currency_code text);
 CREATE TABLE products(id uuid PRIMARY KEY,provider_id uuid,title text,image_urls text[] DEFAULT '{}',category_path text[] DEFAULT '{}',payment_mode text DEFAULT 'contact',catalog_version int DEFAULT 0,updated_at timestamptz DEFAULT now(),moderation_status text,is_active bool,published_at timestamptz);
 CREATE TABLE product_delivery_areas(product_id uuid,country_code text,city_key text);
 CREATE TABLE provider_listings(id uuid PRIMARY KEY,provider_id uuid,title text,status text,published_at timestamptz,media_urls text[] DEFAULT '{}',country_code text DEFAULT 'NG',city text DEFAULT 'Abuja',category text DEFAULT 'food');
 CREATE TABLE provider_conversations(id uuid PRIMARY KEY,provider_id uuid,user_id uuid,listing_id uuid,product_id uuid,status text,last_message_at timestamptz,buyer_last_read_at timestamptz,provider_last_read_at timestamptz);
 CREATE TABLE provider_conversation_messages(id uuid PRIMARY KEY,conversation_id uuid,sender_type text,created_at timestamptz);
 CREATE TABLE notifications(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),user_id uuid,order_id uuid,batch_id uuid,type text,title text,body text,data jsonb,event_key text UNIQUE);`)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"077_provider_identity_contact_products.sql", "079_permanent_free_provider_tier.sql", "080_shared_chat_town_hall.sql", "081_social_statuses.sql", "084_town_hall_message_requests.sql"} {
		raw, err := os.ReadFile("../../migrations/" + file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, string(raw)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	return db
}
func communityApp(db *pgxpool.Pool) *fiber.App {
	s := &CommunityController{db: db, market: &ProviderMarketplaceController{db: db}}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("Actor")); return c.Next() })
	app.Get("/profile", s.Profile)
	app.Patch("/profile", s.UpdateProfile)
	app.Get("/users", s.FindUser)
	app.Get("/friends", s.Friends)
	app.Post("/friends", s.RequestFriend)
	app.Post("/friends/:connection_id/reply", s.ReplyFriend)
	app.Post("/blocks/:user_id", s.Block)
	app.Delete("/blocks/:user_id", s.Block)
	app.Get("/blocks", s.Blocked)
	app.Delete("/friends/:connection_id", s.RemoveFriend)
	app.Get("/statuses", s.Statuses)
	app.Post("/statuses", s.CreateStatus)
	app.Delete("/statuses/:status_id", s.DeleteStatus)
	app.Get("/hall", s.HallList)
	app.Post("/hall", s.HallCreate)
	app.Get("/hall/:room_id", s.HallDetail)
	app.Post("/hall/:room_id/apply", s.HallApply)
	app.Post("/hall/:room_id/providers/:provider_id", s.HallMembership)
	app.Post("/hall/:room_id/offers", s.HallOffer)
	app.Post("/hall/:room_id/choose", s.HallChoose)
	app.Post("/hall/:room_id/close", s.HallClose)
	app.Post("/direct", s.StartDirect)
	app.Get("/inbox", s.Inbox)
	app.Get("/rooms/:conversation_id/messages", s.Messages)
	app.Post("/rooms/:conversation_id/messages", s.Send)
	app.Post("/rooms/:conversation_id/read", s.MarkRead)
	app.Post("/rooms/:conversation_id/reports", s.Report)
	return app
}
func TestCommunityMessageOnlyHallRequest(t *testing.T) {
	db := communityTestDB(t)
	defer db.Close()
	actor := uuid.NewString()
	if _, err := db.Exec(context.Background(), `INSERT INTO users(id,full_name) VALUES($1,'Buyer')`, actor); err != nil {
		t.Fatal(err)
	}
	app := communityApp(db)
	body := fmt.Sprintf(`{"client_request_id":%q,"message":"I need 50 bags of rice at 48,500 naira in Abuja."}`, uuid.NewString())
	created := communityCall(t, app, "POST", "/hall", actor, body, 201)
	retry := communityCall(t, app, "POST", "/hall", actor, body, 201)
	if created["id"] != retry["id"] {
		t.Fatal("retry created another room")
	}
	detail := communityCall(t, app, "GET", "/hall/"+created["id"].(string), actor, "", 200)
	if detail["message_only"] != true || detail["quantity"] != nil || detail["target_unit_price"] != nil {
		t.Fatalf("invented structured values: %#v", detail)
	}
	if detail["description"] != "I need 50 bags of rice at 48,500 naira in Abuja." {
		t.Fatal("request text changed")
	}
	communityCall(t, app, "POST", "/hall", actor, fmt.Sprintf(`{"client_request_id":%q,"message":"  "}`, uuid.NewString()), 422)
}
func communityCall(t *testing.T, app *fiber.App, method, path, actor, body string, want int) map[string]any {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Actor", actor)
	req.Header.Set("Content-Type", "application/json")
	res, err := app.Test(req, 10000)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		t.Fatalf("%s %s got%d want%d: %s", method, path, res.StatusCode, want, raw)
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}
func TestCommunityUsernameFriendsAndPrivateChat(t *testing.T) {
	db := communityTestDB(t)
	ctx := context.Background()
	app := communityApp(db)
	buyer, friend, outsider := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, id := range []string{buyer, friend, outsider} {
		if _, err := db.Exec(ctx, `INSERT INTO users(id,full_name) VALUES($1,'Test friend')`, id); err != nil {
			t.Fatal(err)
		}
		communityCall(t, app, "GET", "/profile", id, "", 200)
	}
	communityCall(t, app, "PATCH", "/profile", friend, `{"handle":"emma_friend"}`, 200)
	communityCall(t, app, "GET", "/users?handle=emma_friend", buyer, "", 404)
	communityCall(t, app, "PATCH", "/profile", friend, `{"discoverable":true}`, 200)
	found := communityCall(t, app, "GET", "/users?handle=emma_friend", buyer, "", 200)
	if found["phone"] != nil || found["email"] != nil {
		t.Fatal("private identity leaked")
	}
	communityCall(t, app, "PATCH", "/profile", buyer, `{"handle":"emma_friend"}`, 409)
	communityCall(t, app, "POST", "/direct", buyer, `{"user_id":"`+friend+`"}`, 403)
	requested := communityCall(t, app, "POST", "/friends", buyer, `{"handle":"emma_friend"}`, 200)
	conn := requested["id"].(string)
	communityCall(t, app, "POST", "/friends/"+conn+"/reply", buyer, `{"accept":true}`, 409)
	communityCall(t, app, "POST", "/friends/"+conn+"/reply", outsider, `{"accept":true}`, 409)
	communityCall(t, app, "POST", "/friends/"+conn+"/reply", friend, `{"accept":true}`, 204)
	direct := communityCall(t, app, "POST", "/direct", buyer, `{"user_id":"`+friend+`"}`, 200)
	room := direct["id"].(string)
	repeat := communityCall(t, app, "POST", "/direct", friend, `{"user_id":"`+buyer+`"}`, 200)
	if repeat["id"] != room {
		t.Fatal("duplicate DM")
	}
	communityCall(t, app, "GET", "/rooms/"+room+"/messages", outsider, "", 403)
	client := uuid.NewString()
	sent := communityCall(t, app, "POST", "/rooms/"+room+"/messages", buyer, `{"message":"Hello friend","client_message_id":"`+client+`"}`, 201)
	retry := communityCall(t, app, "POST", "/rooms/"+room+"/messages", buyer, `{"message":"Hello friend","client_message_id":"`+client+`"}`, 201)
	if sent["id"] != retry["id"] {
		t.Fatal("retry duplicated message")
	}
	communityCall(t, app, "POST", "/rooms/"+room+"/messages", buyer, `{"message":"changed","client_message_id":"`+client+`"}`, 409)
	history := communityCall(t, app, "GET", "/rooms/"+room+"/messages", friend, "", 200)
	items := history["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["is_mine"] != false {
		t.Fatal(history)
	}
	inbox := communityCall(t, app, "GET", "/inbox", friend, "", 200)
	if inbox["items"].([]any)[0].(map[string]any)["unread_count"] != float64(1) {
		t.Fatal(inbox)
	}
	communityCall(t, app, "POST", "/rooms/"+room+"/read", friend, `{"message_id":"`+sent["id"].(string)+`"}`, 204)
	communityCall(t, app, "POST", "/rooms/"+room+"/reports", outsider, `{"reason":"spam"}`, 403)
	communityCall(t, app, "POST", "/rooms/"+room+"/reports", friend, `{"reason":"spam"}`, 201)
	communityCall(t, app, "POST", "/blocks/"+buyer, friend, "", 204)
	communityCall(t, app, "POST", "/rooms/"+room+"/messages", buyer, `{"message":"blocked","client_message_id":"`+uuid.NewString()+`"}`, 403)
	communityCall(t, app, "POST", "/direct", buyer, `{"user_id":"`+friend+`"}`, 403)
	var messages, notifications int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM chat_messages`).Scan(&messages)
	_ = db.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE type='social_message'`).Scan(&notifications)
	if messages != 1 || notifications != 1 {
		t.Fatal(messages, notifications)
	}
	blocked := communityCall(t, app, "GET", "/blocks", friend, "", 200)
	if len(blocked["items"].([]any)) != 1 {
		t.Fatal(blocked)
	}
	communityCall(t, app, "DELETE", "/blocks/"+buyer, friend, "", 204)
	// Unblocking does not silently restore friendship or its promotion audience.
	communityCall(t, app, "POST", "/direct", buyer, `{"user_id":"`+friend+`"}`, 403)
	communityCall(t, app, "POST", "/friends", buyer, `{"handle":"emma_friend"}`, 200)
	communityCall(t, app, "POST", "/friends/"+conn+"/reply", friend, `{"accept":true}`, 204)
	statusBody := `{"message":"My update","client_message_id":"` + uuid.NewString() + `","audience":"selected","recipients":["` + friend + `"]}`
	status := communityCall(t, app, "POST", "/statuses", buyer, statusBody, 201)
	retriedStatus := communityCall(t, app, "POST", "/statuses", buyer, statusBody, 201)
	if status["id"] != retriedStatus["id"] {
		t.Fatal("duplicate status")
	}
	feed := communityCall(t, app, "GET", "/statuses", friend, "", 200)
	if len(feed["items"].([]any)) != 1 {
		t.Fatal(feed)
	}
	feed = communityCall(t, app, "GET", "/statuses", outsider, "", 200)
	if len(feed["items"].([]any)) != 0 {
		t.Fatal("status privacy failure", feed)
	}
	communityCall(t, app, "DELETE", "/statuses/"+status["id"].(string), outsider, "", 404)
	communityCall(t, app, "DELETE", "/friends/"+conn, friend, "", 204)
	feed = communityCall(t, app, "GET", "/statuses", friend, "", 200)
	if len(feed["items"].([]any)) != 0 {
		t.Fatal("removed friend retains audience", feed)
	}
	communityCall(t, app, "DELETE", "/statuses/"+status["id"].(string), buyer, "", 204)
}
