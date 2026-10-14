package controllers

import (
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"strings"
	"time"
)

type promotionCandidate struct {
	Provider string
	Payload  json.RawMessage
}

func (s *CommunityController) promotionCandidates(ctx context.Context, actor, country, city string, paths [][]string, categories ...string) ([]promotionCandidate, error) {
	category := ""
	if len(categories) > 0 {
		category = categories[0]
	}
	clean := [][]string{}
	for _, path := range paths {
		if len(path) < 2 || len(path) > 4 || path[0] != actor {
			continue
		}
		valid := true
		for _, id := range path {
			if _, err := uuid.Parse(id); err != nil {
				valid = false
				break
			}
		}
		if valid {
			clean = append(clean, path)
		}
		if len(clean) >= 200 {
			break
		}
	}
	raw, _ := json.Marshal(clean)
	// Validate all cached paths and fetch public content in one bounded query.
	// Cache entries cannot override current consent, friendship, blocks or payment.
	rows, err := s.db.Query(ctx, `WITH paths AS (
 SELECT ARRAY(SELECT jsonb_array_elements_text(path))::uuid[] AS ids FROM jsonb_array_elements($1::jsonb) path), eligible AS (
 SELECT DISTINCT o.id FROM paths w JOIN provider_organizations o ON o.owner_user_id=w.ids[array_length(w.ids,1)] JOIN provider_public_identity i ON i.provider_id=o.id
 WHERE i.verified AND i.paid_active AND NOT EXISTS(SELECT 1 FROM unnest(w.ids) AS n(user_id) WHERE NOT EXISTS(SELECT 1 FROM social_profiles p JOIN users u ON u.id=p.user_id WHERE p.user_id=n.user_id AND p.connection_promotions AND u.is_active))
 AND NOT EXISTS(SELECT 1 FROM generate_subscripts(w.ids,1) k WHERE k>1 AND NOT EXISTS(SELECT 1 FROM social_connections f WHERE f.user_low=LEAST(w.ids[k-1],w.ids[k]) AND f.user_high=GREATEST(w.ids[k-1],w.ids[k]) AND f.state='accepted'))
 AND NOT EXISTS(SELECT 1 FROM social_blocks b WHERE b.blocker_id=ANY(w.ids) AND b.blocked_id=ANY(w.ids)) ORDER BY o.id LIMIT 30)
 SELECT e.id::text,jsonb_build_object('id',content.id,'kind',content.kind,'title',content.title,'provider_id',e.id,'label','Sponsored · paid provider','image_url',content.image,
 'provider_identity',jsonb_build_object('provider_id',i.provider_id,'name',i.name,'avatar_url',i.avatar_url,'verified',i.verified,'paid_active',i.paid_active,'badge_tier',i.badge_tier))
 FROM eligible e JOIN provider_public_identity i ON i.provider_id=e.id JOIN LATERAL (
 SELECT id,kind,title,image FROM (
 SELECT p.id,'product' AS kind,p.title,COALESCE(p.image_urls[1],'') AS image FROM products p WHERE p.provider_id=e.id AND p.is_active AND p.moderation_status='approved' AND p.payment_mode='contact' AND ($4='' OR EXISTS(SELECT 1 FROM unnest(p.category_path) cat WHERE lower(cat)=lower($4))) AND EXISTS(SELECT 1 FROM product_delivery_areas a WHERE a.product_id=p.id AND a.country_code=$2 AND (a.city_key='' OR $3='' OR lower(a.city_key)=lower($3)))
 UNION ALL SELECT l.id,'service',l.title,COALESCE(l.media_urls[1],'') FROM provider_listings l WHERE l.provider_id=e.id AND l.status='approved' AND l.country_code=$2 AND ($3='' OR lower(l.city)=lower($3)) AND ($4='' OR lower(l.category)=lower($4))) listing ORDER BY id LIMIT 1) content ON true`, raw, country, city, category)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []promotionCandidate{}
	for rows.Next() {
		var item promotionCandidate
		if err = rows.Scan(&item.Provider, &item.Payload); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *CommunityController) Promotions(c *fiber.Ctx) error {
	actor := communityActor(c)
	c.Set("Cache-Control", "private, no-store")
	var opted bool
	err := s.db.QueryRow(c.Context(), `SELECT EXISTS(SELECT 1 FROM social_profiles WHERE user_id=$1::uuid AND connection_promotions)`, actor).Scan(&opted)
	if err != nil {
		return communityError(err)
	}
	empty := fiber.Map{"items": []fiber.Map{}, "pending": false}
	if !opted || s.cache == nil {
		return c.JSON(empty)
	}
	raw, err := s.cache.Get(c.Context(), "atl:social:promotions:"+actor).Bytes()
	if err == redis.Nil {
		_, err = s.db.Exec(c.Context(), `INSERT INTO social_promotion_jobs(user_id) VALUES($1::uuid) ON CONFLICT DO NOTHING`, actor)
		if err != nil {
			return communityError(err)
		}
		empty["pending"] = true
		return c.JSON(empty)
	}
	if err != nil {
		return c.JSON(empty)
	}
	var paths [][]string
	if json.Unmarshal(raw, &paths) != nil {
		return c.JSON(empty)
	}
	country := strings.ToUpper(c.Query("country", "NG"))
	if len(country) != 2 {
		return fiber.ErrBadRequest
	}
	city := strings.TrimSpace(c.Query("city"))
	if len(city) > 100 {
		return fiber.ErrBadRequest
	}
	category := strings.TrimSpace(c.Query("category"))
	if len(category) > 100 {
		return fiber.ErrBadRequest
	}
	candidates, err := s.promotionCandidates(c.Context(), actor, country, city, paths, category)
	if err != nil {
		return communityError(err)
	}
	items := []json.RawMessage{}
	for _, candidate := range candidates {
		key := "atl:social:cap:" + actor + ":" + candidate.Provider + ":" + time.Now().UTC().Format("20060102")
		allowed, err := s.cache.Eval(c.Context(), `local n=tonumber(redis.call('GET',KEYS[1]) or '0');if n>=3 then return 0 end;redis.call('INCR',KEYS[1]);redis.call('EXPIRE',KEYS[1],172800);return 1`, []string{key}).Int()
		if err != nil || allowed != 1 {
			continue
		}
		items = append(items, candidate.Payload)
		if len(items) >= 3 {
			break
		}
	}
	return c.JSON(fiber.Map{"items": items, "pending": false})
}
