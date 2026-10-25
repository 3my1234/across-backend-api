package controllers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DiscoveryController struct {
	db      *pgxpool.Pool
	market  *ProviderMarketplaceController
	catalog *CatalogController
	key     []byte
}

func NewDiscoveryController(db *pgxpool.Pool, cfg config.Config) *DiscoveryController {
	return &DiscoveryController{db: db, market: NewProviderMarketplaceController(db, cfg), catalog: NewCatalogController(db, cfg), key: []byte(cfg.JWTSecret)}
}

type discoveryItem struct {
	CompareAtPrice float64   `json:"compare_at_price"`
	PriceMode      string    `json:"price_mode"`
	Kind           string    `json:"kind"`
	ID             string    `json:"id"`
	Provider       string    `json:"provider_id"`
	Title          string    `json:"title"`
	Category       []string  `json:"categories"`
	City           string    `json:"city"`
	Images         []string  `json:"image_urls"`
	Price          *float64  `json:"price"`
	Currency       string    `json:"currency"`
	Paid           bool      `json:"-"`
	Tier           string    `json:"-"`
	Priority       int       `json:"-"`
	Promote        bool      `json:"-"`
	Created        time.Time `json:"-"`
	Placement      string    `json:"placement"`
	Label          string    `json:"label,omitempty"`
	Receipt        string    `json:"receipt"`
	Score          int       `json:"-"`
}
type discoveryReceipt struct {
	Kind      string `json:"k"`
	ID        string `json:"i"`
	Provider  string `json:"p"`
	Session   string `json:"s"`
	Placement string `json:"v"`
	Issued    int64  `json:"t"`
}

func discoveryWords(values ...string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		for _, word := range strings.FieldsFunc(strings.ToLower(v), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
			if strings.Contains(" the and for with service professional product offer need from this your other new job ", " "+word+" ") {
				continue
			}
			if len(word) < 3 {
				continue
			}
			word = strings.TrimSuffix(word, "s")
			if len(word) > 40 || seen[word] {
				continue
			}
			seen[word] = true
			out = append(out, word)
			if len(out) == 16 {
				return out
			}
		}
	}
	return out
}
func discoveryRelevance(item discoveryItem, categories, words []string, city string) int {
	score := 0
	for _, a := range item.Category {
		for _, b := range categories {
			if strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) {
				score += 40
				break
			}
		}
	}
	text := " " + strings.Join(discoveryWords(item.Title, strings.Join(item.Category, " ")), " ") + " "
	for _, word := range words {
		if strings.Contains(text, " "+word+" ") {
			score += 8
		}
	}
	if city != "" && strings.EqualFold(strings.TrimSpace(city), strings.TrimSpace(item.City)) {
		score += 5
	}
	return score
}

// Relevance always precedes tier; deterministic weighted rotation only breaks
// ties. One card per business per rail prevents inventory size buying the rail.
func rankDiscovery(items []discoveryItem, placement, session string, limit int) []discoveryItem {
	picked := append([]discoveryItem{}, items...)
	rotate := func(item discoveryItem) float64 {
		sum := sha256.Sum256([]byte(session + placement + item.Provider + item.ID))
		n := float64(uint64(sum[0])<<24 | uint64(sum[1])<<16 | uint64(sum[2])<<8 | uint64(sum[3]))
		weight := 1.0
		if placement == "promoted" {
			switch item.Tier {
			case "silver":
				weight = 2
			case "gold":
				weight = 5
			case "diamond":
				weight = 3
			}
			weight *= float64(item.Priority)
		}
		return -math.Log((n+1)/4294967297) / weight
	}
	sort.SliceStable(picked, func(i, j int) bool {
		if picked[i].Score != picked[j].Score {
			return picked[i].Score > picked[j].Score
		}
		return rotate(picked[i]) < rotate(picked[j])
	})
	result := []discoveryItem{}
	providers := map[string]bool{}
	ids := map[string]bool{}
	for _, item := range picked {
		if placement == "promoted" && (!item.Paid || !item.Promote) {
			continue
		}
		key := item.Kind + item.ID
		if providers[item.Provider] || ids[key] {
			continue
		}
		providers[item.Provider] = true
		ids[key] = true
		item.Placement = placement
		if placement == "promoted" {
			item.Label = "Promoted"
		}
		result = append(result, item)
		if len(result) == limit {
			break
		}
	}
	return result
}
func (d *DiscoveryController) signReceipt(r discoveryReceipt) string {
	raw, _ := json.Marshal(r)
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, d.key)
	mac.Write([]byte("discovery-v1:" + payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (d *DiscoveryController) readReceipt(token string) (discoveryReceipt, error) {
	var r discoveryReceipt
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(token) > 1200 || len(d.key) < 16 {
		return r, fiber.ErrUnauthorized
	}
	mac := hmac.New(sha256.New, d.key)
	mac.Write([]byte("discovery-v1:" + parts[0]))
	signature, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil || !hmac.Equal(mac.Sum(nil), signature) {
		return r, fiber.ErrUnauthorized
	}
	raw, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil || json.Unmarshal(raw, &r) != nil || uuid.Validate(r.ID) != nil || uuid.Validate(r.Provider) != nil || (r.Kind != "product" && r.Kind != "service") || (r.Placement != "similar" && r.Placement != "recommended" && r.Placement != "promoted") || len(r.Session) != 64 || r.Issued > time.Now().Unix()+30 || r.Issued < time.Now().Add(-2*time.Hour).Unix() {
		return r, fiber.ErrUnauthorized
	}
	return r, nil
}

func (d *DiscoveryController) Feed(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 2*time.Second)
	defer cancel()
	country, state, city, err := requestedDeliveryDestination(c)
	if err != nil {
		return err
	}
	session := c.Query("session")
	if uuid.Validate(session) != nil {
		return fiber.NewError(422, "A discovery session reference is required")
	}
	sourceKind, sourceID := c.Query("source_kind"), c.Query("source_id")
	listingKind := c.Query("kind")
	if listingKind != "" && listingKind != "product" && listingKind != "service" {
		return fiber.NewError(400, "kind must be product or service")
	}
	// Detail feeds stay within the source type, including requests from older apps.
	if sourceID != "" {
		if listingKind != "" && listingKind != sourceKind {
			return fiber.NewError(400, "kind must match the source listing")
		}
		listingKind = sourceKind
	}
	// Dedicated carousel clients may request a bounded pool; old feeds keep
	// their original small promoted share. Existing eligibility/diversity applies.
	carouselLimit := 0
	if raw := c.Query("promoted_limit"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > 24 || sourceID != "" || listingKind == "" {
			return fiber.NewError(400, "promoted_limit requires a product/service catalogue and must be 1 to 24")
		}
		carouselLimit = n
	}
	categories := []string{}
	sourceTitle := ""
	if sourceID != "" {
		if uuid.Validate(sourceID) != nil || (sourceKind != "product" && sourceKind != "service") {
			return fiber.ErrBadRequest
		}
		err = d.db.QueryRow(ctx, `SELECT categories,title FROM (
 SELECT p.id,'product' kind,p.category_path categories,p.title,p.provider_id FROM products p WHERE p.is_active AND p.published_at IS NOT NULL AND p.moderation_status='approved' AND p.inventory_count>0
 UNION ALL SELECT l.id,'service',ARRAY[l.category],l.title,l.provider_id FROM provider_listings l WHERE l.status='approved' AND l.is_available_now) s JOIN provider_public_identity i ON i.provider_id=s.provider_id WHERE s.id=$1::uuid AND s.kind=$2 AND i.verified`, sourceID, sourceKind).Scan(&categories, &sourceTitle)
		if err != nil {
			return fiber.ErrNotFound
		}
	}
	if len(c.Query("interests")) > 240 || len(c.Query("search")) > 100 {
		return fiber.ErrBadRequest
	}
	words := discoveryWords(sourceTitle, strings.Join(categories, " "), c.Query("interests"), c.Query("search"))
	recent := map[string]bool{}
	if len(c.Query("recent")) > 1300 {
		return fiber.ErrBadRequest
	}
	if c.Query("recent") != "" {
		parts := strings.Split(c.Query("recent"), ",")
		if len(parts) > 24 {
			return fiber.ErrBadRequest
		}
		for _, key := range parts {
			pair := strings.Split(key, ":")
			if len(pair) != 2 || (pair[0] != "product" && pair[0] != "service") || uuid.Validate(pair[1]) != nil {
				return fiber.ErrBadRequest
			}
			recent[key] = true
		}
	}

	// A UUID seek pivot rotates the bounded candidate window; no OFFSET or
	// random sort of the entire catalogue. Related/search predicates stay in SQL.
	seed := sha256.Sum256([]byte(session + time.Now().UTC().Format("2006-01-02T15:04")))
	pivot := uuid.UUID(seed[:16]).String()
	query := discoveryCandidateSQL
	rows, err := d.db.Query(ctx, query, country, state, city, sourceID, words, categories, pivot, sourceID != "" || c.Query("search") != "", listingKind)
	if err != nil {
		return fiber.NewError(503, "Discovery is temporarily unavailable")
	}
	defer rows.Close()
	candidates := []discoveryItem{}
	for rows.Next() {
		var item discoveryItem
		if err = rows.Scan(&item.Kind, &item.ID, &item.Provider, &item.Title, &item.Category, &item.City, &item.Images, &item.Price, &item.Currency, &item.Paid, &item.Tier, &item.Priority, &item.Promote, &item.Created, &item.PriceMode, &item.CompareAtPrice); err != nil {
			return fiber.ErrInternalServerError
		}
		item.Score = discoveryRelevance(item, categories, words, city)
		if recent[item.Kind+":"+item.ID] {
			item.Score--
		}
		item.Images = d.catalog.normalizeImageURLs(item.Images)
		candidates = append(candidates, item)
	}
	if rows.Err() != nil {
		return fiber.NewError(503, "Discovery is temporarily unavailable")
	}
	rows.Close()
	digest := sha256.Sum256([]byte(session))
	sessionHash := hex.EncodeToString(digest[:])
	rails := fiber.Map{}
	used := map[string]bool{}
	placements := []string{"promoted", "recommended"}
	if sourceID != "" {
		placements = []string{"promoted", "similar"}
	}
	for _, placement := range placements {
		pool := []discoveryItem{}
		for _, item := range candidates {
			if !used[item.Kind+item.ID] {
				pool = append(pool, item)
			}
		}
		limit := 6
		if placement == "promoted" {
			limit = len(candidates) / 4
			if limit < 1 {
				limit = 1
			}
			if limit > 3 {
				limit = 3
			}
			if carouselLimit > 0 {
				limit = carouselLimit
			}
		}
		ranked := rankDiscovery(pool, placement, session+time.Now().UTC().Format("2006-01-02T15"), limit)
		cards := []fiber.Map{}
		for _, item := range ranked {
			used[item.Kind+item.ID] = true
			item.Receipt = d.signReceipt(discoveryReceipt{item.Kind, item.ID, item.Provider, sessionHash, placement, time.Now().Unix()})
			raw, _ := json.Marshal(item)
			card := fiber.Map{}
			_ = json.Unmarshal(raw, &card)
			cards = append(cards, card)
		}
		rails[placement] = cards
	}
	c.Set("Cache-Control", "private, no-store")
	c.Locals("public_provider_media", true)
	return providerIdentityJSON(c, d.db, rails, d.catalog.s3)
}

// Each half of each kind is capped before enrichment, using the UUID index.
// Availability and paid eligibility are always live; no cached grant survives
// expiry, suspension, moderation or an opt-out.
const discoveryCandidateSQL = `WITH product_candidates AS NOT MATERIALIZED (SELECT 'product' kind,p.id,p.provider_id,p.title,p.category_path categories,p.inventory_city city,p.image_urls images,
 CASE WHEN p.is_flash_sale AND a.currency_code=p.local_currency_code AND a.delivered_price-a.delivery_fee=p.local_selling_price AND p.flash_sale_price>0 AND p.flash_sale_price<p.local_selling_price THEN p.flash_sale_price+a.delivery_fee ELSE a.delivered_price END price,a.currency_code currency,p.plan_slot,p.created_at,'fixed'::text price_mode,CASE WHEN a.currency_code=p.local_currency_code AND a.delivered_price-a.delivery_fee=p.local_selling_price THEN CASE WHEN p.is_flash_sale AND p.flash_sale_price>0 AND p.flash_sale_price<p.local_selling_price THEN GREATEST(p.local_selling_price,COALESCE(p.compare_at_price,0))+a.delivery_fee WHEN p.compare_at_price>p.local_selling_price THEN p.compare_at_price+a.delivery_fee ELSE 0 END ELSE 0 END compare_at_price
 FROM products p JOIN provider_public_identity v ON v.provider_id=p.provider_id AND v.verified AND p.plan_slot<=v.listing_limit JOIN LATERAL (SELECT a.delivered_price,a.delivery_fee,a.currency_code FROM product_delivery_areas a JOIN countries_config market ON market.country_code=a.country_code AND market.currency_code=a.currency_code AND market.is_active AND 'flutterwave'=ANY(market.active_payment_gateways) WHERE a.product_id=p.id AND a.country_code=$1 AND (a.state_key='' OR a.state_key=$2) AND (a.city_key='' OR a.city_key=$3) ORDER BY (a.city_key<>'') DESC,(a.state_key<>'') DESC LIMIT 1) a ON true
 WHERE ($9::text='' OR $9='product') AND p.is_active AND p.published_at IS NOT NULL AND p.moderation_status='approved' AND p.inventory_count>0 AND p.payment_mode='contact' AND p.fulfillment_mode IN('merchant_local','merchant_cross_border') AND ($4='' OR p.id<>NULLIF($4,'')::uuid)
 AND (NOT $8 OR p.category_path && $6::text[] OR to_tsvector('simple',p.title||' '||p.description) @@ websearch_to_tsquery('simple',array_to_string($5::text[],' OR ')))), service_candidates AS NOT MATERIALIZED (SELECT 'service',l.id,l.provider_id,l.title,ARRAY[l.category,l.listing_type],l.city,l.media_urls,l.price,l.currency_code,l.plan_slot,l.created_at,COALESCE(l.attributes->>'price_mode','fixed'),0::numeric
 FROM provider_listings l JOIN provider_public_identity v ON v.provider_id=l.provider_id AND v.verified AND l.plan_slot<=v.listing_limit JOIN countries_config market ON market.country_code=l.country_code AND market.currency_code=l.currency_code AND market.is_active AND 'flutterwave'=ANY(market.active_payment_gateways)
 WHERE ($9::text='' OR $9='service') AND l.country_code=$1 AND l.published_at IS NOT NULL AND l.status='approved' AND l.is_available_now AND ($4='' OR l.id<>NULLIF($4,'')::uuid)
 AND (NOT $8 OR l.category=ANY($6::text[]) OR to_tsvector('simple',l.title||' '||l.description||' '||l.category) @@ websearch_to_tsquery('simple',array_to_string($5::text[],' OR ')))), content AS MATERIALIZED (
 (SELECT * FROM product_candidates WHERE id>=$7::uuid ORDER BY id LIMIT 128)
 UNION ALL (SELECT * FROM product_candidates WHERE id<$7::uuid ORDER BY id LIMIT 128)
 UNION ALL (SELECT * FROM service_candidates WHERE id>=$7::uuid ORDER BY id LIMIT 128)
 UNION ALL (SELECT * FROM service_candidates WHERE id<$7::uuid ORDER BY id LIMIT 128)
 )
 SELECT c.kind,c.id::text,c.provider_id::text,c.title,c.categories,c.city,c.images,c.price::float8,c.currency,
 i.paid_active,i.badge_tier,COALESCE(ls.priority,1),COALESCE(ls.enabled,true) AND COALESCE(ps.enabled,true),c.created_at,c.price_mode,c.compare_at_price::float8
 FROM content c JOIN provider_public_identity i ON i.provider_id=c.provider_id
 LEFT JOIN discovery_listing_settings ls ON ls.kind=c.kind AND ls.listing_id=c.id AND ls.provider_id=c.provider_id
 LEFT JOIN discovery_provider_settings ps ON ps.provider_id=c.provider_id
 WHERE i.verified AND c.plan_slot<=i.listing_limit`

func (d *DiscoveryController) Events(c *fiber.Ctx) error {
	var body struct {
		Events []struct {
			Receipt   string `json:"receipt"`
			Type      string `json:"type"`
			VisibleMS int    `json:"visible_ms"`
		} `json:"events"`
	}
	if len(c.Body()) > 40000 || c.BodyParser(&body) != nil || len(body.Events) == 0 || len(body.Events) > 25 {
		return fiber.ErrBadRequest
	}
	type event struct {
		r    discoveryReceipt
		kind string
	}
	events := []event{}
	for _, input := range body.Events {
		r, err := d.readReceipt(input.Receipt)
		if err != nil {
			return err
		}
		if input.Type != "impression" && input.Type != "click" && input.Type != "detail_open" && input.Type != "contact_tap" {
			return fiber.ErrBadRequest
		}
		if input.Type == "impression" && input.VisibleMS < 1000 {
			continue
		}
		events = append(events, event{r, input.Type})
	}
	ctx, cancel := context.WithTimeout(c.Context(), 2*time.Second)
	defer cancel()
	tx, err := d.db.Begin(ctx)
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	defer tx.Rollback(ctx)
	for _, e := range events {
		r := e.r
		key := sha256.Sum256([]byte(r.Session + r.Kind + r.ID + r.Placement + e.kind + time.Unix(r.Issued, 0).UTC().Format("2006-01-02")))
		_, err = tx.Exec(ctx, `WITH fresh AS (INSERT INTO discovery_event_dedup(event_key,expires_at) VALUES($1,now()+interval '26 hours') ON CONFLICT DO NOTHING RETURNING 1)
 INSERT INTO discovery_daily_metrics(provider_id,kind,listing_id,placement,day,impressions,clicks,detail_opens,contact_taps)
 SELECT $2::uuid,$3,$4::uuid,$5,$6::date,($7='impression')::int,($7='click')::int,($7='detail_open')::int,($7='contact_tap')::int FROM fresh
 ON CONFLICT(provider_id,kind,listing_id,placement,day) DO UPDATE SET impressions=discovery_daily_metrics.impressions+EXCLUDED.impressions,clicks=discovery_daily_metrics.clicks+EXCLUDED.clicks,detail_opens=discovery_daily_metrics.detail_opens+EXCLUDED.detail_opens,contact_taps=discovery_daily_metrics.contact_taps+EXCLUDED.contact_taps`, hex.EncodeToString(key[:]), r.Provider, r.Kind, r.ID, r.Placement, time.Unix(r.Issued, 0).UTC().Format("2006-01-02"), e.kind)
		if err != nil {
			return fiber.ErrServiceUnavailable
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return fiber.ErrServiceUnavailable
	}
	return c.SendStatus(204)
}

func (d *DiscoveryController) ProviderSummary(c *fiber.Ctx) error {
	provider, _, err := d.market.providerForUser(c.Context(), communityActor(c))
	if err != nil {
		return fiber.ErrForbidden
	}
	var enabled bool
	err = d.db.QueryRow(c.Context(), `SELECT COALESCE((SELECT enabled FROM discovery_provider_settings WHERE provider_id=$1::uuid),true)`, provider).Scan(&enabled)
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	var metrics []byte
	err = d.db.QueryRow(c.Context(), `SELECT jsonb_build_object('impressions',COALESCE(sum(impressions),0),'clicks',COALESCE(sum(clicks),0),'detail_opens',COALESCE(sum(detail_opens),0),'contact_taps',COALESCE(sum(contact_taps),0)) FROM discovery_daily_metrics WHERE provider_id=$1::uuid AND placement='promoted' AND day>=current_date-29`, provider).Scan(&metrics)
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	rows, err := d.db.Query(c.Context(), `SELECT x.kind,x.id::text,x.title,COALESCE(s.enabled,true),COALESCE(s.priority,1) FROM (
 SELECT 'product' kind,id,title,created_at FROM products WHERE provider_id=$1::uuid
 UNION ALL SELECT 'service',id,title,created_at FROM provider_listings WHERE provider_id=$1::uuid) x
 LEFT JOIN discovery_listing_settings s ON s.kind=x.kind AND s.listing_id=x.id AND s.provider_id=$1::uuid ORDER BY x.created_at DESC,x.id LIMIT 50`, provider)
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var kind, id, title string
		var on bool
		var priority int
		if err = rows.Scan(&kind, &id, &title, &on, &priority); err != nil {
			return fiber.ErrServiceUnavailable
		}
		items = append(items, fiber.Map{"kind": kind, "id": id, "title": title, "enabled": on, "priority": priority})
	}
	if rows.Err() != nil {
		return fiber.ErrServiceUnavailable
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"items": items, "enabled": enabled, "days": 30, "metrics": json.RawMessage(metrics), "note": "Viewable impressions and contact taps are not guaranteed customers, completed chats or sales."})
}
func (d *DiscoveryController) ProviderSettings(c *fiber.Ctx) error {
	provider, role, err := d.market.providerForUser(c.Context(), communityActor(c))
	if err != nil || (role != "owner" && role != "manager") {
		return fiber.ErrForbidden
	}
	var in struct {
		Enabled  *bool  `json:"enabled"`
		Kind     string `json:"kind"`
		ID       string `json:"listing_id"`
		Priority int    `json:"priority"`
	}
	if len(c.Body()) > 4096 || c.BodyParser(&in) != nil || in.Enabled == nil {
		return fiber.ErrBadRequest
	}
	if in.ID == "" {
		_, err = d.db.Exec(c.Context(), `INSERT INTO discovery_provider_settings(provider_id,enabled) VALUES($1::uuid,$2) ON CONFLICT(provider_id) DO UPDATE SET enabled=EXCLUDED.enabled,updated_at=now()`, provider, *in.Enabled)
	} else {
		if uuid.Validate(in.ID) != nil || (in.Kind != "product" && in.Kind != "service") || in.Priority < 1 || in.Priority > 3 {
			return fiber.ErrBadRequest
		}
		tag, e := d.db.Exec(c.Context(), `INSERT INTO discovery_listing_settings(provider_id,kind,listing_id,enabled,priority)
 SELECT $1::uuid,$2,$3::uuid,$4,$5 WHERE EXISTS(SELECT 1 FROM products WHERE id=$3::uuid AND provider_id=$1::uuid AND $2='product') OR EXISTS(SELECT 1 FROM provider_listings WHERE id=$3::uuid AND provider_id=$1::uuid AND $2='service')
 ON CONFLICT(kind,listing_id) DO UPDATE SET enabled=EXCLUDED.enabled,priority=EXCLUDED.priority,updated_at=now() WHERE discovery_listing_settings.provider_id=EXCLUDED.provider_id`, provider, in.Kind, in.ID, *in.Enabled, in.Priority)
		err = e
		if e == nil && tag.RowsAffected() != 1 {
			return fiber.ErrNotFound
		}
	}
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	return c.SendStatus(204)
}

// Keep raw dedup keys for only 26h and aggregates for 90d. Bounded chunks avoid
// a long retention transaction interfering with customer traffic.
func PurgeDiscoveryMetrics(ctx context.Context, db *pgxpool.Pool) error {
	_, err := db.Exec(ctx, `DELETE FROM discovery_event_dedup WHERE event_key IN(SELECT event_key FROM discovery_event_dedup WHERE expires_at<now() ORDER BY expires_at LIMIT 1000)`)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `DELETE FROM discovery_daily_metrics WHERE ctid IN(SELECT ctid FROM discovery_daily_metrics WHERE day<current_date-90 ORDER BY day LIMIT 1000)`)
	return err
}

func (d *DiscoveryController) AdminSummary(c *fiber.Ctx) error {
	rows, err := d.db.Query(c.Context(), `SELECT placement,COALESCE(sum(impressions),0)::bigint,COALESCE(sum(clicks),0)::bigint,COALESCE(sum(detail_opens),0)::bigint,COALESCE(sum(contact_taps),0)::bigint FROM discovery_daily_metrics WHERE day>=current_date-29 GROUP BY placement`)
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var placement string
		var impressions, clicks, opens, contacts int64
		if err = rows.Scan(&placement, &impressions, &clicks, &opens, &contacts); err != nil {
			return fiber.ErrServiceUnavailable
		}
		items = append(items, fiber.Map{"placement": placement, "impressions": impressions, "clicks": clicks, "detail_opens": opens, "contact_taps": contacts})
	}
	if rows.Err() != nil {
		return fiber.ErrServiceUnavailable
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"days": 30, "items": items})
}
