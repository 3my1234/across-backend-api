package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"os"
	"testing"
)

func TestProviderPostRulesCombinedCapacityAndMedia(t *testing.T) {
	db := communityTestDB(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`ALTER TABLE provider_subscription_plans ADD COLUMN description text,ADD COLUMN updated_at timestamptz DEFAULT now();ALTER TABLE products ADD COLUMN factory_details jsonb DEFAULT '{}';ALTER TABLE provider_listings ADD COLUMN attributes jsonb DEFAULT '{}';`)
	for _, code := range []string{"provider-monthly", "provider-growth", "provider-business", "provider-diamond"} {
		exec(`INSERT INTO provider_subscription_plans(id,code,listing_limit) VALUES($1,$2,99)`, uuid.NewString(), code)
	}
	raw, e := os.ReadFile("../../migrations/095_provider_post_rules.sql")
	if e != nil {
		t.Fatal(e)
	}
	exec(string(raw))
	for _, tier := range []string{"", "bronze", "silver", "gold", "diamond"} {
		t.Run("tier_"+tier, func(t *testing.T) {
			policy := postPolicy(tier)
			user, provider := uuid.NewString(), uuid.NewString()
			exec(`INSERT INTO users(id,full_name) VALUES($1,'Test owner')`, user)
			exec(`INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status) VALUES($1,$2,'Rules test','approved')`, provider, user)
			if tier != "" {
				plan, sub := uuid.NewString(), uuid.NewString()
				exec(`INSERT INTO provider_subscription_plans(id,code,listing_limit,badge_tier) VALUES($1,$2,$3,$4)`, plan, "test-"+tier, policy.Posts, tier)
				exec(`INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,starts_at,current_period_end) VALUES($1,$2,$3,'active',now(),now()+interval '1 month')`, sub, provider, plan)
				exec(`INSERT INTO provider_subscription_payments VALUES($1,500,'NGN')`, sub)
			}
			images := []string{}
			for i := 0; i < policy.Images; i++ {
				images = append(images, fmt.Sprintf("https://example.test/%d.jpg", i))
			}
			product := uuid.NewString()
			exec(`INSERT INTO products(id,provider_id,moderation_status,image_urls,factory_details) VALUES($1,$2,'draft',$3,'{}')`, product, provider, images)
			if _, e = db.Exec(ctx, `UPDATE products SET image_urls=$2 WHERE id=$1`, product, append(images, "https://example.test/extra.jpg")); e == nil {
				t.Fatal("excess images accepted")
			}
			for i := 1; i < policy.Posts; i++ {
				exec(`INSERT INTO provider_listings(id,provider_id,status,media_urls) VALUES($1,$2,'draft',ARRAY['https://example.test/a.jpg'])`, uuid.NewString(), provider)
			}
			if _, e = db.Exec(ctx, `INSERT INTO products(id,provider_id,moderation_status,image_urls) VALUES($1,$2,'draft',ARRAY['https://example.test/a.jpg'])`, uuid.NewString(), provider); e == nil {
				t.Fatal("combined product/service post cap bypassed")
			}
			urls := []string{}
			for i := 0; i < policy.Videos; i++ {
				url := fmt.Sprintf("https://example.test/%s-%d.mp4", provider, i)
				urls = append(urls, url)
				exec(`INSERT INTO provider_post_video_assets VALUES($1,$2,60,100,now())`, url, provider)
			}
			payload, _ := json.Marshal(map[string]any{"video_urls": urls})
			exec(`UPDATE products SET factory_details=$2::jsonb WHERE id=$1`, product, string(payload))
			urls = append(urls, "https://example.test/unverified.mp4")
			payload, _ = json.Marshal(map[string]any{"video_urls": urls})
			if _, e = db.Exec(ctx, `UPDATE products SET factory_details=$2::jsonb WHERE id=$1`, product, string(payload)); e == nil {
				t.Fatal("excess/unverified video accepted")
			}
		})
	}
}
func TestPostVideoInspectionRejectsInvalidAndLongMedia(t *testing.T) {
	for _, raw := range []string{`{}`, `{"format":{"duration":"61"},"streams":[{"codec_type":"video","codec_name":"h264"}]}`, `{"format":{"duration":"20"},"streams":[{"codec_type":"audio","codec_name":"aac"}]}`, `{"format":{"duration":"20"},"streams":[{"codec_type":"video","codec_name":"hevc"}]}`, `{"format":{"duration":"NaN"}}`} {
		if _, e := verifiedVideoDuration([]byte(raw)); e == nil {
			t.Fatal("invalid video accepted")
		}
	}
	if d, e := verifiedVideoDuration([]byte(`{"format":{"duration":"60"},"streams":[{"codec_type":"video","codec_name":"h264","duration":"60"}]}`)); e != nil || d != 60 {
		t.Fatal("valid video rejected", e)
	}
}
