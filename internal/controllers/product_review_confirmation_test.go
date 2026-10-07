package controllers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestProductReviewDurableUpdateAndBoundedReward(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `ALTER TABLE orders ADD user_id uuid, ADD order_status text, ADD created_at timestamptz DEFAULT now();
 CREATE TABLE users(id uuid PRIMARY KEY,full_name text);
 CREATE TABLE products(id uuid PRIMARY KEY,is_active boolean DEFAULT true,review_count bigint DEFAULT 1,review_rating_sum bigint DEFAULT 5);
 CREATE TABLE order_items(order_id uuid,product_id uuid);
 CREATE TABLE reviews(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),product_id uuid,user_id uuid,order_id uuid,rating int,review_text text,media_urls text[],created_at timestamptz DEFAULT now());
 CREATE TABLE review_rewards(user_id uuid,order_id uuid,is_claimed boolean DEFAULT false,claimed_at timestamptz);
 CREATE TABLE xp_transactions(user_id uuid,amount int,reason text,reference_id text UNIQUE);
 CREATE TABLE notifications(user_id uuid,order_id uuid,batch_id uuid,type text,title text,body text,data jsonb,event_key text UNIQUE);`)
	if err != nil {
		t.Fatal(err)
	}
	buyer, product, oldOrder, newOrder, review := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO users VALUES($1,'Buyer')`, buyer); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO products(id) VALUES($1)`, product); err != nil {
		t.Fatal(err)
	}
	for i, order := range []string{oldOrder, newOrder} {
		if _, err = db.Exec(ctx, `INSERT INTO orders(id,user_id,order_status,created_at) VALUES($1,$2,'Delivered',now()+$3*interval '1 second')`, order, buyer, i); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, `INSERT INTO order_items VALUES($1,$2)`, order, product); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(ctx, `INSERT INTO reviews(id,product_id,user_id,order_id,rating,review_text,media_urls) VALUES($1,$2,$3,$4,5,'Old review','{}')`, review, product, buyer, oldOrder); err != nil {
		t.Fatal(err)
	}
	controller := NewReviewController(db)
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", buyer); return c.Next() })
	app.Put("/products/:product_id/reviews", controller.UpsertProductReview)
	app.Get("/products/:product_id/reviews/mine", controller.MyProductReview)
	save := func(text string) map[string]any {
		t.Helper()
		request := httptest.NewRequest("PUT", "/products/"+product+"/reviews", strings.NewReader(`{"rating":4,"review_text":"`+text+`","media_urls":[]}`))
		request.Header.Set("Content-Type", "application/json")
		response, e := app.Test(request, 6000)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("save status %d", response.StatusCode)
		}
		var body map[string]any
		if e = json.NewDecoder(response.Body).Decode(&body); e != nil {
			t.Fatal(e)
		}
		if body["review"].(map[string]any)["review_text"] != text {
			t.Fatal("wrong acknowledgement")
		}
		return body
	}
	save("Repeat purchase update")
	var count int
	var linkedOrder string
	if err = db.QueryRow(ctx, `SELECT count(*) FROM reviews WHERE product_id=$1 AND user_id=$2`, product, buyer).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate review: %d %v", count, err)
	}
	if err = db.QueryRow(ctx, `SELECT order_id::text FROM reviews WHERE id=$1`, review).Scan(&linkedOrder); err != nil || linkedOrder != oldOrder {
		t.Fatal("original reward order changed")
	}
	// Make the optional reward operation block; saving must still acknowledge
	// the durable review and roll back the timed-out reward transaction.
	if _, err = db.Exec(ctx, `UPDATE reviews SET order_id=$1 WHERE id=$2`, newOrder, review); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO review_rewards(user_id,order_id) VALUES($1,$2)`, buyer, newOrder); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `CREATE FUNCTION slow_reward() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(4);RETURN NEW;END;$$;
 CREATE TRIGGER slow_reward BEFORE UPDATE ON review_rewards FOR EACH ROW EXECUTE FUNCTION slow_reward();`); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	body := save("Saved despite reward delay")
	if time.Since(started) > 4*time.Second {
		t.Fatal("reward delayed acknowledgement")
	}
	if body["reward_pending"] != true || body["review_reward_claimed"] != false {
		t.Fatal("timed-out reward reported as credited")
	}
	if _, err = db.Exec(ctx, `DROP TRIGGER slow_reward ON review_rewards`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `DELETE FROM users WHERE id=$1`, buyer); err != nil {
		t.Fatal(err)
	}
	// Optional author/summary lookup has no row; the committed write still wins.
	save("Saved despite response enrichment failure")
	response, err := app.Test(httptest.NewRequest("GET", "/products/"+product+"/reviews/mine", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var mine map[string]any
	if err = json.NewDecoder(response.Body).Decode(&mine); err != nil {
		t.Fatal(err)
	}
	if mine["review"].(map[string]any)["review_text"] != "Saved despite response enrichment failure" {
		t.Fatal("read-back differs from acknowledged review")
	}
}
