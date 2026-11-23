package controllers

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

func TestDiscoverySponsoredVideoLinks(t *testing.T) {
	urls := publicDiscoveryVideos([]string{"file:///private/id.jpg", "http://media.test/v.mp4", "https://media.test/private/id.jpg", "https://media.test/%70rivate/id.jpg", "https://user:secret@media.test/v.mp4", "https://api.test/api/v1/public/videos/view/user-uploads/ad.mp4"})
	if len(urls) != 1 || urls[0] != "https://api.test/api/v1/public/videos/view/user-uploads/ad.mp4" {
		t.Fatal("video endpoint must survive; private/insecure links must not", urls)
	}
}

func TestDiscoverySponsoredViewerSeekAndEligibility(t *testing.T) {
	db, d, app := discoveryFixture(t)
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`ALTER TABLE products ADD COLUMN factory_details jsonb DEFAULT '{}'`)
	provider, owner, plan, sub := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO users(id,full_name) VALUES($1,'Test owner')`, owner)
	exec(`INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status) VALUES($1,$2,'Sponsored business','approved')`, provider, owner)
	exec(`INSERT INTO provider_subscription_plans(id,code,listing_limit,badge_tier) VALUES($1,'provider-diamond',60,'gold')`, plan)
	exec(`INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,current_period_end) VALUES($1,$2,$3,'active',now()+interval '1 month')`, sub, provider, plan)
	exec(`INSERT INTO provider_subscription_payments VALUES($1,5000,'NGN')`, sub)
	for i := 0; i < 28; i++ {
		id := uuid.NewString()
		exec(`INSERT INTO products(id,provider_id,title,is_active,moderation_status,published_at,factory_details) VALUES($1,$2,$3,true,'approved',now(),'{"video_urls":["https://api.test/api/v1/public/videos/view/user-uploads/ad.mp4"]}')`, id, provider, fmt.Sprint("Product ", i))
		exec(`INSERT INTO product_delivery_areas(product_id,country_code) VALUES($1,'NG')`, id)
	}
	exec(`INSERT INTO provider_listings(id,provider_id,title,status,published_at,attributes) VALUES(gen_random_uuid(),$1,'Video service','approved',now(),'{"video_urls":["https://media.test/service.mp4"]}')`, provider)
	prefix := "/discovery?country_code=NG&session=" + uuid.NewString() + "&kind=product&sponsored_viewer=1"
	first := communityCall(t, app, "GET", prefix, "", "", 200)
	if len(first["promoted"].([]any)) != 24 || first["sponsored_next"] == "" {
		t.Fatal("first bounded page", first)
	}
	seen := map[string]bool{}
	check := func(body map[string]any) {
		t.Helper()
		for _, raw := range body["promoted"].([]any) {
			card := raw.(map[string]any)
			id := card["id"].(string)
			if seen[id] || card["kind"] != "product" || card["placement"] != "promoted" {
				t.Fatal("duplicate/wrong scope", card)
			}
			seen[id] = true
			if len(card["video_urls"].([]any)) != 1 || card["video_urls"].([]any)[0] != "https://api.test/api/v1/public/videos/view/user-uploads/ad.mp4" {
				t.Fatal("video links were lost or rewritten to images", card)
			}
			receipt, err := d.readReceipt(card["receipt"].(string))
			if err != nil || receipt.ID != id || receipt.Placement != "promoted" {
				t.Fatal("wrong attribution", receipt, err)
			}
		}
	}
	check(first)
	second := communityCall(t, app, "GET", prefix+"&sponsored_after="+first["sponsored_next"].(string), "", "", 200)
	check(second)
	if len(seen) != 28 || second["sponsored_next"] != "" {
		t.Fatal("all ads must be reachable without changing rail diversity", len(seen), second)
	}
	compact := communityCall(t, app, "GET", "/discovery?country_code=NG&kind=product&promoted_limit=12&session="+uuid.NewString(), "", "", 200)
	if len(compact["promoted"].([]any)) != 1 {
		t.Fatal("compact rail business diversity changed", compact)
	}
	service := communityCall(t, app, "GET", "/discovery?country_code=NG&kind=service&sponsored_viewer=1&session="+uuid.NewString(), "", "", 200)
	if len(service["promoted"].([]any)) != 1 {
		t.Fatal("service ad lost", service)
	}
	exec(`INSERT INTO discovery_listing_settings(provider_id,kind,listing_id,enabled) SELECT provider_id,'product',id,false FROM products ORDER BY id LIMIT 26`)
	sparse := communityCall(t, app, "GET", prefix, "", "", 200)
	if len(sparse["promoted"].([]any)) != 2 {
		t.Fatal("disabled early candidates truncated later eligible ads", sparse)
	}
	exec(`DELETE FROM discovery_listing_settings`)
	exec(`INSERT INTO discovery_provider_settings(provider_id,enabled) VALUES($1,false)`, provider)
	disabled := communityCall(t, app, "GET", prefix, "", "", 200)
	if len(disabled["promoted"].([]any)) != 0 {
		t.Fatal("opt-out leaked", disabled)
	}
	exec(`UPDATE discovery_provider_settings SET enabled=true WHERE provider_id=$1`, provider)
	exec(`UPDATE provider_subscriptions SET current_period_end=now()-interval '1 minute' WHERE id=$1`, sub)
	expired := communityCall(t, app, "GET", prefix, "", "", 200)
	if len(expired["promoted"].([]any)) != 0 {
		t.Fatal("expired subscription promoted", expired)
	}
	communityCall(t, app, "GET", prefix+"&sponsored_after=invalid", "", "", 400)
	// The viewer may mix products and services; expired plans still stay out.
	mixed := communityCall(t, app, "GET", "/discovery?country_code=NG&sponsored_viewer=1&session="+uuid.NewString(), "", "", 200)
	if len(mixed["promoted"].([]any)) != 0 {
		t.Fatal("expired subscription promoted in mixed viewer", mixed)
	}
}
