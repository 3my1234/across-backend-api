package controllers

import (
	"context"
	"os"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// A customer may rate a service or product once the business has replied to them in chat.
func TestReviewsAfterProviderReply(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	must := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, query, args...); err != nil {
			t.Fatal(err, query[:min(len(query), 80)])
		}
	}
	must(`CREATE TABLE users(id uuid PRIMARY KEY,full_name text);
 CREATE TABLE provider_organizations(id uuid PRIMARY KEY);
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,role text,is_active boolean DEFAULT true);
 CREATE TABLE provider_listings(id uuid PRIMARY KEY,provider_id uuid,title text,status text DEFAULT 'approved',review_count int DEFAULT 0,review_rating_sum int DEFAULT 0);
 ALTER TABLE orders ADD COLUMN user_id uuid,ADD COLUMN order_status text,ADD COLUMN created_at timestamptz DEFAULT now();
 CREATE TABLE order_items(order_id uuid,product_id uuid);
 CREATE TABLE products(id uuid PRIMARY KEY,provider_id uuid,title text,is_active boolean DEFAULT true,review_count int DEFAULT 0,review_rating_sum int DEFAULT 0);
 CREATE TABLE reviews(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),product_id uuid,user_id uuid,order_id uuid,rating int,review_text text DEFAULT '',media_urls text[] DEFAULT '{}',created_at timestamptz DEFAULT now(),UNIQUE(product_id,user_id,order_id));
 CREATE TABLE review_rewards(user_id uuid,order_id uuid,is_claimed boolean,claimed_at timestamptz);
 CREATE TABLE provider_requests(id uuid PRIMARY KEY,listing_id uuid,provider_id uuid,user_id uuid,status text);
 CREATE TABLE provider_conversations(id uuid PRIMARY KEY,provider_id uuid,user_id uuid,listing_id uuid,product_id uuid);
 CREATE TABLE provider_conversation_messages(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),conversation_id uuid,sender_type text);
 CREATE TABLE xp_transactions(user_id uuid,amount int,reason text,reference_id text UNIQUE);
 CREATE TABLE notifications(user_id uuid,type text,title text,body text,data jsonb,event_key text UNIQUE,order_id uuid,batch_id uuid);
 CREATE TABLE provider_listing_reviews(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),request_id uuid NOT NULL UNIQUE,listing_id uuid,user_id uuid,rating smallint,review_text text DEFAULT '',created_at timestamptz DEFAULT now(),updated_at timestamptz DEFAULT now())`)
	raw, err := os.ReadFile("../../migrations/131_reviews_after_reply.sql")
	if err != nil {
		t.Fatal(err)
	}
	must(string(raw))

	buyer, quiet, staff, provider, other := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	service, otherService, product := uuid.NewString(), uuid.NewString(), uuid.NewString()
	must(`INSERT INTO users(id,full_name) VALUES($1,'Buyer'),($2,'Quiet'),($3,'Staff')`, buyer, quiet, staff)
	must(`INSERT INTO provider_members VALUES($1,$2,'staff',true)`, provider, staff)
	must(`INSERT INTO provider_listings(id,provider_id,title) VALUES($1,$3,'Tailor'),($2,$4,'Other')`, service, otherService, provider, other)
	must(`INSERT INTO products(id,provider_id,title) VALUES($1,$2,'Shoes')`, product, provider)
	// The buyer asked about the service; the business answered in a chat about the product.
	askService, askProduct, quietChat, staffChat := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	must(`INSERT INTO provider_conversations VALUES($1,$5,$6,$7,NULL),($2,$5,$6,NULL,$8),($3,$5,$9,$7,NULL),($4,$5,$10,$7,NULL)`, askService, askProduct, quietChat, staffChat, provider, buyer, service, product, quiet, staff)
	must(`INSERT INTO provider_conversation_messages(conversation_id,sender_type) VALUES($1,'buyer'),($2,'buyer'),($3,'buyer'),($4,'buyer')`, askService, askProduct, quietChat, staffChat)

	m := &ProviderMarketplaceController{db: db}
	rc := &ReviewController{db: db}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("Actor")); return c.Next() })
	app.Put("/l/:listing_id/review", m.UpsertListingReview)
	app.Get("/l/:listing_id/review/mine", m.MyListingReview)
	app.Put("/p/:product_id/reviews", rc.UpsertProductReview)
	app.Get("/p/:product_id/reviews/mine", rc.MyProductReview)

	// No reply yet: not allowed.
	if communityCall(t, app, "GET", "/l/"+service+"/review/mine", buyer, "", 200)["can_review"] != false {
		t.Fatal("a customer without a reply may not review")
	}
	communityCall(t, app, "PUT", "/l/"+service+"/review", buyer, `{"rating":5}`, 403)
	communityCall(t, app, "PUT", "/p/"+product+"/reviews", buyer, `{"rating":5}`, 403)

	// One reply, in any chat with this business, opens reviews for what they asked about.
	must(`INSERT INTO provider_conversation_messages(conversation_id,sender_type) VALUES($1,'provider')`, askProduct)
	if communityCall(t, app, "GET", "/l/"+service+"/review/mine", buyer, "", 200)["can_review"] != true {
		t.Fatal("answered customers may review")
	}
	first := communityCall(t, app, "PUT", "/l/"+service+"/review", buyer, `{"rating":4,"review_text":" Good work "}`, 200)
	if first["xp_awarded"].(float64) != 1 {
		t.Fatal("first review earns the reward", first)
	}
	again := communityCall(t, app, "PUT", "/l/"+service+"/review", buyer, `{"rating":2,"review_text":"Changed my mind"}`, 200)
	if again["xp_awarded"].(float64) != 0 {
		t.Fatal("changing a review earns nothing more")
	}
	var count, rating int
	if err = db.QueryRow(ctx, `SELECT count(*),max(rating) FROM provider_listing_reviews WHERE listing_id=$1`, service).Scan(&count, &rating); err != nil || count != 1 || rating != 2 {
		t.Fatal("one review per customer, updated in place", count, rating, err)
	}
	mine := communityCall(t, app, "GET", "/l/"+service+"/review/mine", buyer, "", 200)["review"].(map[string]any)
	if mine["review_text"] != "Changed my mind" {
		t.Fatal(mine)
	}
	// Only what they asked about, only with that business, never their own staff.
	communityCall(t, app, "PUT", "/l/"+otherService+"/review", buyer, `{"rating":1}`, 403)
	communityCall(t, app, "PUT", "/l/"+service+"/review", quiet, `{"rating":1}`, 403)
	must(`INSERT INTO provider_conversation_messages(conversation_id,sender_type) VALUES($1,'provider')`, staffChat)
	communityCall(t, app, "PUT", "/l/"+service+"/review", staff, `{"rating":5}`, 403)
	communityCall(t, app, "PUT", "/l/"+service+"/review", buyer, `{"rating":9}`, 422)
	communityCall(t, app, "PUT", "/l/not-a-listing/review", buyer, `{"rating":5}`, 400)

	// Products work the same way.
	if communityCall(t, app, "GET", "/p/"+product+"/reviews/mine", buyer, "", 200)["can_review"] != true {
		t.Fatal("product can_review")
	}
	communityCall(t, app, "PUT", "/p/"+product+"/reviews", buyer, `{"rating":5,"review_text":"Nice"}`, 201)
	communityCall(t, app, "PUT", "/p/"+product+"/reviews", buyer, `{"rating":3,"review_text":"Okay"}`, 200)
	if err = db.QueryRow(ctx, `SELECT count(*),max(rating) FROM reviews WHERE product_id=$1`, product).Scan(&count, &rating); err != nil || count != 1 || rating != 3 {
		t.Fatal("one product review", count, rating, err)
	}
	var rewards int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM xp_transactions WHERE user_id=$1`, buyer).Scan(&rewards); err != nil || rewards != 2 {
		t.Fatal("one reward for the service and one for the product", rewards, err)
	}

	// The old way still works: a completed booking.
	request := uuid.NewString()
	must(`INSERT INTO provider_requests VALUES($1,$2,$3,$4,'completed')`, request, otherService, other, quiet)
	communityCall(t, app, "PUT", "/l/"+otherService+"/review", quiet, `{"request_id":"`+request+`","rating":5}`, 200)
	communityCall(t, app, "PUT", "/l/"+otherService+"/review", quiet, `{"request_id":"`+uuid.NewString()+`","rating":5}`, 403)
}
