package controllers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"across/backend/internal/config"
	"across/backend/internal/storage"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestProductChatMigrationPermissionsAttachmentsAndRetries(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY,full_name text,email text);
 CREATE TABLE provider_organizations(id uuid PRIMARY KEY,business_name text,verification_status text DEFAULT 'approved',is_active boolean DEFAULT true);
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,is_active boolean DEFAULT true,role text,created_at timestamptz DEFAULT now());
 CREATE TABLE provider_listings(id uuid PRIMARY KEY,provider_id uuid,title text,status text DEFAULT 'approved');
 CREATE TABLE products(id uuid PRIMARY KEY,provider_id uuid,title text,is_active boolean DEFAULT true,moderation_status text DEFAULT 'approved');
 CREATE TABLE provider_subscriptions(provider_id uuid,status text,current_period_end timestamptz);`)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"041_provider_conversations.sql", "064_product_payment_options_and_chat_images.sql"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", file))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, string(raw)); err != nil {
			t.Fatal(file, err)
		}
	}
	buyer, owner, other, seller := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	product, cardOnly, service := uuid.NewString(), uuid.NewString(), uuid.NewString()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users VALUES($1,'Buyer','buyer@example.test'),($2,'Seller','seller@example.test'),($3,'Other','other@example.test')`, []any{buyer, owner, other}},
		{`INSERT INTO provider_organizations(id,business_name) VALUES($1,'Seller')`, []any{seller}},
		{`INSERT INTO provider_members(provider_id,user_id,role) VALUES($1,$2,'owner')`, []any{seller, owner}},
		{`INSERT INTO products(id,provider_id,title,payment_mode) VALUES($1,$3,'Watch','contact'),($2,$3,'Card only','flutterwave')`, []any{product, cardOnly, seller}},
		{`INSERT INTO provider_listings(id,provider_id,title) VALUES($1,$2,'Repair')`, []any{service, seller}},
	}
	for _, statement := range statements {
		if _, err = db.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	controller := &ProviderMarketplaceController{db: db, s3: storage.NewS3(config.Config{AWSRegion: "eu-north-1", AWSAccessKeyID: "test", AWSSecretAccessKey: "test", S3BucketName: "test"})}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("X-User")); return c.Next() })
	app.Post("/products/:product_id/chat", controller.StartProductConversation)
	app.Post("/services/:listing_id/chat", controller.StartProviderConversation)
	app.Get("/chats", controller.ListBuyerConversations)
	app.Get("/seller/chats", controller.ListProviderConversations)
	app.Get("/chats/:conversation_id/messages", controller.BuyerConversationMessages)
	app.Post("/chats/:conversation_id/messages", controller.SendBuyerConversationMessage)
	app.Post("/seller/chats/:conversation_id/messages", controller.SendProviderConversationMessage)
	app.Post("/images", controller.PresignChatImage)
	call := func(method, url, user, body string) (int, map[string]any) {
		t.Helper()
		request := httptest.NewRequest(method, url, strings.NewReader(body))
		request.Header.Set("X-User", user)
		request.Header.Set("Content-Type", "application/json")
		response, err := app.Test(request, 10000)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result map[string]any
		_ = json.NewDecoder(response.Body).Decode(&result)
		return response.StatusCode, result
	}
	clientID := uuid.NewString()
	payload := `{"message":"Is this available?","client_message_id":"` + clientID + `"}`
	status, started := call("POST", "/products/"+product+"/chat", buyer, payload)
	if status != 201 {
		t.Fatal(status, started)
	}
	thread := started["id"].(string)
	status, retried := call("POST", "/products/"+product+"/chat", buyer, payload)
	if status != 201 || retried["message_id"] != started["message_id"] {
		t.Fatal("duplicate retry", status, retried)
	}
	status, _ = call("POST", "/products/"+cardOnly+"/chat", buyer, payload)
	if status != 402 {
		t.Fatal("card-only product opened chat", status)
	}
	status, _ = call("POST", "/services/"+service+"/chat", buyer, `{"message":"Repair please"}`)
	if status != 201 {
		t.Fatal("service compatibility", status)
	}
	status, list := call("GET", "/chats?product_id="+product, buyer, "")
	if status != 200 || len(list["items"].([]any)) != 1 {
		t.Fatal(status, list)
	}
	status, list = call("GET", "/seller/chats", owner, "")
	if status != 200 || len(list["items"].([]any)) != 2 {
		t.Fatal("seller must see service and product contacts", status, list)
	}
	status, _ = call("GET", "/chats/"+thread+"/messages", other, "")
	if status != 404 {
		t.Fatal("private history exposed", status)
	}
	status, _ = call("POST", "/seller/chats/"+thread+"/messages", other, `{"message":"Unauthorized"}`)
	if status != 403 {
		t.Fatal("reply authorization", status)
	}
	status, _ = call("POST", "/chats/"+thread+"/messages", buyer, `{"message":"Different","client_message_id":"`+clientID+`"}`)
	if status != 409 {
		t.Fatal("reference reused with a different body", status)
	}
	status, signed := call("POST", "/images", buyer, `{"filename":"screen.png","mime_type":"image/png","size":128}`)
	if status != 200 || !strings.HasPrefix(signed["key"].(string), "user-uploads/private-chat/"+buyer+"/") {
		t.Fatal(status, signed)
	}
	key := signed["key"].(string)
	if _, err := normalizeS3ViewKey(key); err == nil {
		t.Fatal("chat image accessible through public image proxy")
	}
	status, _ = call("POST", "/images", buyer, `{"mime_type":"image/png","size":6000000}`)
	if status != 422 {
		t.Fatal("oversized upload accepted", status)
	}
	oldClient := http.DefaultClient
	defer func() { http.DefaultClient = oldClient }()
	http.DefaultClient = &http.Client{Transport: paymentRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != "HEAD" {
			t.Fatal(request.Method)
		}
		return &http.Response{StatusCode: 200, ContentLength: 128, Header: http.Header{"Content-Type": []string{"image/png"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	encoded, _ := json.Marshal(map[string]any{"message": "", "media_keys": []string{key}, "client_message_id": uuid.NewString()})
	status, photo := call("POST", "/chats/"+thread+"/messages", buyer, string(encoded))
	if status != 201 || len(photo["media_urls"].([]any)) != 1 {
		t.Fatal("photo-only message", status, photo)
	}
	status, _ = call("POST", "/seller/chats/"+thread+"/messages", owner, string(encoded))
	if status != 422 {
		t.Fatal("another sender reused private media", status)
	}
	status, _ = call("POST", "/seller/chats/"+thread+"/messages", owner, `{"message":"Yes, available"}`)
	if status != 201 {
		t.Fatal("seller reply", status)
	}
	var count int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM provider_conversation_messages WHERE conversation_id=$1`, thread).Scan(&count)
	if count != 3 {
		t.Fatal("messages duplicated or missing", count)
	}
	if _, err = db.Exec(ctx, `UPDATE provider_conversations SET status='blocked' WHERE id=$1`, thread); err != nil {
		t.Fatal(err)
	}
	status, _ = call("POST", "/products/"+product+"/chat", buyer, `{"message":"Reopen blocked"}`)
	if status == 201 {
		t.Fatal("blocked chat was reopened")
	}
}
