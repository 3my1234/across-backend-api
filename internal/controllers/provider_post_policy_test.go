package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gofiber/fiber/v2"
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
	slots, e := os.ReadFile("../../migrations/097_provider_post_visibility_slots.sql")
	if e != nil {
		t.Fatal(e)
	}
	exec(string(slots))
	for _, tier := range []string{"", "bronze", "silver", "gold", "diamond"} {
		t.Run("tier_"+tier, func(t *testing.T) {
			policy := map[string]providerPostPolicy{"": {Posts: 1, Images: 1}, "bronze": {Posts: 5, Images: 2}, "silver": {Posts: 10, Images: 4}, "gold": {Posts: 20, Images: 8, Videos: 2}, "diamond": {Posts: 40, Images: 8, Videos: 5}}[tier]
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

			if tier == "gold" {
				// Expiry changes discovery immediately, but keeps every stored record.
				exec(`UPDATE provider_subscriptions SET current_period_end=now()-interval '1 second' WHERE provider_id=$1`, provider)
				countVisible := func() int {
					var count int
					err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM products p JOIN provider_public_identity i ON i.provider_id=p.provider_id WHERE p.provider_id=$1 AND p.moderation_status<>'archived' AND p.plan_slot<=i.listing_limit)+(SELECT count(*) FROM provider_listings l JOIN provider_public_identity i ON i.provider_id=l.provider_id WHERE l.provider_id=$1 AND l.status<>'archived' AND l.plan_slot<=i.listing_limit)`, provider).Scan(&count)
					if err != nil {
						t.Fatal(err)
					}
					return count
				}
				if got := countVisible(); got != 1 {
					t.Fatalf("expiry exposes %d posts, want 1", got)
				}
				exec(`UPDATE products SET moderation_status='archived' WHERE id=$1`, product)
				exec(`INSERT INTO products(id,provider_id,moderation_status,image_urls) VALUES($1,$2,'draft',ARRAY['https://example.test/new.jpg'])`, uuid.NewString(), provider)
				if got := countVisible(); got != 1 {
					t.Fatalf("reusing the free allowance exposes %d posts", got)
				}
				exec(`UPDATE provider_subscriptions SET current_period_end=now()+interval '1 month' WHERE provider_id=$1`, provider)
				if got := countVisible(); got != 20 {
					t.Fatalf("renewal exposes %d posts, want 20", got)
				}
				var retained int
				if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM products WHERE provider_id=$1)+(SELECT count(*) FROM provider_listings WHERE provider_id=$1)`, provider).Scan(&retained); err != nil || retained != 21 {
					t.Fatalf("records were lost: %d %v", retained, err)
				}
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

func TestPublicPostMediaCurrentTier(t *testing.T) {
	for _, tier := range []string{"", "bronze", "silver", "gold", "diamond"} {
		policy := postPolicy(tier)
		images := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}
		videos := []any{"1", "2", "3", "4", "5", "6"}
		item := fiber.Map{"image_urls": images, "factory_details": map[string]any{"video_urls": videos}}
		limitPublicPostMedia(item, tier)
		expectedImages, expectedVideos := policy.Images, policy.Videos
		if tier != "" {
			expectedImages = 9
			expectedVideos = 6
		}
		if len(item["image_urls"].([]string)) != expectedImages || len(item["factory_details"].(map[string]any)["video_urls"].([]any)) != expectedVideos {
			t.Fatalf("wrong media limits for %q: %#v", tier, item)
		}
		if len(images) != 9 || len(videos) != 6 {
			t.Fatal("stored media changed")
		}
	}
}
