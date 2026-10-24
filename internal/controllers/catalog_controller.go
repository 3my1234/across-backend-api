package controllers

import (
	"across/backend/internal/config"
	"across/backend/internal/storage"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CatalogController struct {
	db  *pgxpool.Pool
	s3  *storage.S3
	cfg config.Config
}

func NewCatalogController(db *pgxpool.Pool, cfg config.Config) *CatalogController {
	return &CatalogController{db: db, s3: storage.NewS3(cfg), cfg: cfg}
}

func (cc *CatalogController) ListProducts(c *fiber.Ctx) error {
	c.Locals("public_provider_media", true)
	destinationCountry, destinationState, destinationCity, destinationErr := requestedDeliveryDestination(c)
	if destinationErr != nil {
		return destinationErr
	}
	stockScope := strings.TrimSpace(strings.ToLower(c.Query("stock_scope")))
	if stockScope != "" && stockScope != "all" && stockScope != "local" && stockScope != "international" {
		return fiber.NewError(fiber.StatusBadRequest, "stock_scope must be all, local, or international")
	}
	latitudeRaw := strings.TrimSpace(c.Query("latitude"))
	longitudeRaw := strings.TrimSpace(c.Query("longitude"))
	hasLocation := latitudeRaw != "" || longitudeRaw != ""
	latitude, longitude := 0.0, 0.0
	var parseErr error
	if hasLocation {
		if latitudeRaw == "" || longitudeRaw == "" {
			return fiber.NewError(fiber.StatusBadRequest, "latitude and longitude must be supplied together")
		}
		latitude, parseErr = strconv.ParseFloat(latitudeRaw, 64)
		if parseErr != nil || latitude < -90 || latitude > 90 {
			return fiber.NewError(fiber.StatusBadRequest, "valid latitude is required")
		}
		longitude, parseErr = strconv.ParseFloat(longitudeRaw, 64)
		if parseErr != nil || longitude < -180 || longitude > 180 {
			return fiber.NewError(fiber.StatusBadRequest, "valid longitude is required")
		}
	}
	rows, err := cc.db.Query(c.Context(), `
		SELECT p.id, p.sku, p.title, p.description, p.category_path, p.image_urls,
			offer.currency_code,
			CASE WHEN p.is_flash_sale AND offer.currency_code=p.local_currency_code AND offer.delivered_price-offer.delivery_fee=p.local_selling_price AND p.flash_sale_price > 0 AND p.flash_sale_price < p.local_selling_price THEN p.flash_sale_price+offer.delivery_fee ELSE offer.delivered_price END,
			CASE WHEN offer.currency_code=p.local_currency_code AND offer.delivered_price-offer.delivery_fee=p.local_selling_price THEN CASE WHEN p.is_flash_sale AND p.flash_sale_price>0 AND p.flash_sale_price<p.local_selling_price THEN GREATEST(p.local_selling_price,COALESCE(p.compare_at_price,0))+offer.delivery_fee WHEN p.compare_at_price>p.local_selling_price THEN p.compare_at_price+offer.delivery_fee ELSE 0 END ELSE 0 END,
			p.inventory_count,
			p.factory_details || jsonb_build_object('payment_mode',p.payment_mode,'catalog_updated_at',p.updated_at,'catalog_version',p.catalog_version,'provider_name',COALESCE((SELECT seller.business_name FROM provider_organizations seller WHERE seller.id=p.provider_id),''),'inventory_country_code',p.inventory_country_code,'inventory_city',p.inventory_city,'inventory_location',p.inventory_location,'stock_state',p.stock_state,'handling_time_hours',p.handling_time_hours,'delivery_min_days',p.delivery_min_days,'delivery_max_days',p.delivery_max_days,'delivery_methods',p.delivery_methods,'delivery_areas',COALESCE((SELECT jsonb_agg(jsonb_build_object('country_code',a.country_code,'state',a.state_key,'city',a.city_key,'delivered_price',a.delivered_price,'delivery_fee',a.delivery_fee,'currency_code',a.currency_code)) FROM product_delivery_areas a WHERE a.product_id=p.id),'[]'::jsonb)),
			COALESCE(lh.id::text, ''), COALESCE(lh.name, ''), COALESCE(lh.city, ''),
			(p.is_flash_sale AND offer.currency_code=p.local_currency_code AND offer.delivered_price-offer.delivery_fee=p.local_selling_price), COALESCE(p.flash_sale_price+offer.delivery_fee, 0), p.review_count, p.sold_count,
			CASE WHEN p.review_count > 0 THEN p.review_rating_sum::float8 / p.review_count ELSE 0 END,
			p.provider_id::text,p.fulfillment_mode,
			CASE WHEN $1::boolean AND p.inventory_country_code=$5 AND p.stock_state<>'import_on_demand' AND p.inventory_latitude IS NOT NULL AND p.inventory_longitude IS NOT NULL THEN 6371 * 2 * asin(sqrt(power(sin(radians((p.inventory_latitude::float8-$2::float8)/2)),2)+cos(radians($2::float8))*cos(radians(p.inventory_latitude::float8))*power(sin(radians((p.inventory_longitude::float8-$3::float8)/2)),2))) END AS distance_km
		FROM products p
        LEFT JOIN provider_public_identity visibility ON visibility.provider_id=p.provider_id
		JOIN LATERAL (SELECT a.delivered_price,a.delivery_fee,a.currency_code FROM product_delivery_areas a JOIN countries_config market ON market.country_code=a.country_code AND market.currency_code=a.currency_code AND market.is_active=true AND 'flutterwave'=ANY(market.active_payment_gateways) WHERE a.product_id=p.id AND a.country_code=$5 AND (a.state_key='' OR a.state_key=$6) AND (a.city_key='' OR a.city_key=$7) ORDER BY (a.city_key<>'') DESC,(a.state_key<>'') DESC LIMIT 1) offer ON TRUE
		LEFT JOIN logistics_hubs lh ON lh.id = p.origin_hub_id
		WHERE p.is_active = true AND p.moderation_status='approved' AND (p.provider_id IS NULL OR p.plan_slot<=COALESCE(visibility.listing_limit,1))
		  AND p.provider_id IS NOT NULL AND p.fulfillment_mode IN ('merchant_local','merchant_cross_border')
		  AND ($8::text NOT IN ('local','international') OR ($8='local' AND p.inventory_country_code=$5) OR ($8='international' AND p.inventory_country_code<>$5))
		  AND EXISTS(SELECT 1 FROM provider_organizations po WHERE po.id=p.provider_id AND po.verification_status='approved' AND po.is_active=true AND ($4::boolean OR EXISTS(SELECT 1 FROM provider_subscriptions ps WHERE ps.provider_id=po.id AND ps.status='active' AND ps.current_period_end>now())))
		  AND (p.payment_mode='contact' OR EXISTS(SELECT 1 FROM provider_payout_accounts pa WHERE pa.provider_id=p.provider_id AND pa.payment_provider='flutterwave' AND pa.status='active'))
		ORDER BY CASE WHEN COALESCE(visibility.verified AND visibility.paid_active,false) THEN CASE visibility.badge_tier WHEN 'gold' THEN 3 WHEN 'diamond' THEN 3 WHEN 'silver' THEN 2 ELSE 1 END ELSE 0 END DESC, CASE WHEN p.inventory_country_code=$5 THEN 0 ELSE 1 END, distance_km ASC NULLS LAST, p.created_at DESC
		LIMIT 80
	`, hasLocation, latitude, longitude, !cc.cfg.ProviderSubscriptionsRequired(time.Now().UTC()), destinationCountry, destinationState, destinationCity, stockScope)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "catalog unavailable")
	}
	defer rows.Close()

	products := make([]fiber.Map, 0)
	for rows.Next() {
		var id, sku, title, description, currency, hubID, hubName, hubCity string
		var categories, images []string
		var price, compareAtPrice, flashSalePrice, averageRating float64
		var reviewCount, soldCount int64
		var inventory int
		var factoryRaw []byte
		var isFlashSale bool
		var providerID *string
		var distanceKM *float64
		var fulfillmentMode string
		if err := rows.Scan(&id, &sku, &title, &description, &categories, &images, &currency, &price, &compareAtPrice, &inventory, &factoryRaw, &hubID, &hubName, &hubCity, &isFlashSale, &flashSalePrice, &reviewCount, &soldCount, &averageRating, &providerID, &fulfillmentMode, &distanceKM); err != nil {
			return err
		}
		factory := map[string]any{}
		_ = json.Unmarshal(factoryRaw, &factory)
		productMap := fiber.Map{
			"id":               id,
			"sku":              sku,
			"title":            title,
			"description":      description,
			"category_path":    categories,
			"image_urls":       cc.normalizeImageURLs(images),
			"currency":         currency,
			"price":            price,
			"delivery_fee":     catalogDeliveryFee(factory, destinationCountry, destinationState, destinationCity),
			"compare_at_price": compareAtPrice,
			"inventory_count":  inventory,
			"review_count":     reviewCount,
			"sold_count":       soldCount,
			"average_rating":   averageRating,
			"provider_id":      stringValue(providerID),
			"fulfillment_mode": catalogProductRoute(factory, destinationCountry),
			"factory_details":  factory,
			"distance_km":      distanceKM,
			"origin_hub": fiber.Map{
				"id":   hubID,
				"name": hubName,
				"city": hubCity,
			},
		}
		if isFlashSale {
			productMap["is_flash_sale"] = true
			productMap["flash_sale_price"] = flashSalePrice
		}
		products = append(products, productMap)
	}
	if err := rows.Err(); err != nil {
		return fiber.ErrInternalServerError
	}
	rows.Close()
	return providerIdentityJSON(c, cc.db, fiber.Map{"products": products}, cc.s3)
}

// ListFlashSales is deliberately separate from the general catalogue. It keeps
// the query bounded and cursor-paginated even when the catalogue grows to
// millions of products.
func (cc *CatalogController) ListFlashSales(c *fiber.Ctx) error {
	c.Locals("public_provider_media", true)
	destinationCountry, destinationState, destinationCity, destinationErr := requestedDeliveryDestination(c)
	if destinationErr != nil {
		return destinationErr
	}
	// Deals change during the day. A short edge TTL keeps the Home rail fresh
	// without turning every buyer refresh into a database request.
	c.Set(fiber.HeaderCacheControl, "public, max-age=5, stale-while-revalidate=15")
	c.Vary(fiber.HeaderAcceptEncoding)
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	var cursorID any
	if page.CursorTime != nil {
		cursorID = page.CursorID
	}
	rows, err := cc.db.Query(c.Context(), `
		SELECT p.id, p.sku, p.title, p.description, p.category_path, p.image_urls,
			offer.currency_code, p.flash_sale_price+offer.delivery_fee,
			GREATEST(p.local_selling_price,COALESCE(p.compare_at_price,0))+offer.delivery_fee, p.inventory_count,
			p.factory_details || jsonb_build_object('payment_mode',p.payment_mode,'catalog_updated_at',p.updated_at,'catalog_version',p.catalog_version,'provider_name',COALESCE((SELECT seller.business_name FROM provider_organizations seller WHERE seller.id=p.provider_id),''),'inventory_country_code',p.inventory_country_code,'inventory_city',p.inventory_city,'inventory_location',p.inventory_location,'stock_state',p.stock_state,'handling_time_hours',p.handling_time_hours,'delivery_min_days',p.delivery_min_days,'delivery_max_days',p.delivery_max_days,'delivery_methods',p.delivery_methods,'delivery_areas',COALESCE((SELECT jsonb_agg(jsonb_build_object('country_code',a.country_code,'state',a.state_key,'city',a.city_key,'delivered_price',a.delivered_price,'delivery_fee',a.delivery_fee,'currency_code',a.currency_code)) FROM product_delivery_areas a WHERE a.product_id=p.id),'[]'::jsonb)), COALESCE(lh.id::text, ''), COALESCE(lh.name, ''), COALESCE(lh.city, ''),
			p.created_at, COUNT(*) OVER() AS total_count, p.review_count, p.sold_count,
			CASE WHEN p.review_count > 0 THEN p.review_rating_sum::float8 / p.review_count ELSE 0 END,
			p.provider_id::text,p.fulfillment_mode,CASE WHEN COALESCE(visibility.verified AND visibility.paid_active,false) THEN CASE visibility.badge_tier WHEN 'gold' THEN 3 WHEN 'diamond' THEN 3 WHEN 'silver' THEN 2 ELSE 1 END ELSE 0 END
		FROM products p
        LEFT JOIN provider_public_identity visibility ON visibility.provider_id=p.provider_id
		JOIN LATERAL (SELECT a.delivered_price,a.delivery_fee,a.currency_code FROM product_delivery_areas a JOIN countries_config market ON market.country_code=a.country_code AND market.currency_code=a.currency_code AND market.is_active=true AND 'flutterwave'=ANY(market.active_payment_gateways) WHERE a.product_id=p.id AND a.country_code=$6 AND (a.state_key='' OR a.state_key=$7) AND (a.city_key='' OR a.city_key=$8) ORDER BY (a.city_key<>'') DESC,(a.state_key<>'') DESC LIMIT 1) offer ON TRUE
		LEFT JOIN logistics_hubs lh ON lh.id = p.origin_hub_id
		WHERE p.is_active = true AND p.moderation_status='approved' AND (p.provider_id IS NULL OR p.plan_slot<=COALESCE(visibility.listing_limit,1)) AND p.is_flash_sale = true AND p.inventory_count > 0
		  AND p.provider_id IS NOT NULL AND p.fulfillment_mode IN ('merchant_local','merchant_cross_border')
		  AND EXISTS(SELECT 1 FROM provider_organizations po WHERE po.id=p.provider_id AND po.verification_status='approved' AND po.is_active=true AND ($5::boolean OR EXISTS(SELECT 1 FROM provider_subscriptions ps WHERE ps.provider_id=po.id AND ps.status='active' AND ps.current_period_end>now())))
		  AND (p.payment_mode='contact' OR EXISTS(SELECT 1 FROM provider_payout_accounts pa WHERE pa.provider_id=p.provider_id AND pa.payment_provider='flutterwave' AND pa.status='active'))
		  AND offer.currency_code=p.local_currency_code AND offer.delivered_price-offer.delivery_fee=p.local_selling_price
		  AND p.flash_sale_price > 0 AND p.flash_sale_price < p.local_selling_price
		  AND ($1 = '' OR p.sku ILIKE '%' || $1 || '%' OR p.title ILIKE '%' || $1 || '%'
			OR p.description ILIKE '%' || $1 || '%')
		  AND ($2::timestamptz IS NULL OR (CASE WHEN COALESCE(visibility.verified AND visibility.paid_active,false) THEN CASE visibility.badge_tier WHEN 'gold' THEN 3 WHEN 'diamond' THEN 3 WHEN 'silver' THEN 2 ELSE 1 END ELSE 0 END,p.created_at,p.id)<($9::int,$2,$3::uuid))
		ORDER BY CASE WHEN COALESCE(visibility.verified AND visibility.paid_active,false) THEN CASE visibility.badge_tier WHEN 'gold' THEN 3 WHEN 'diamond' THEN 3 WHEN 'silver' THEN 2 ELSE 1 END ELSE 0 END DESC, p.created_at DESC, p.id DESC LIMIT $4
	`, page.Search, page.CursorTime, cursorID, page.Limit+1, !cc.cfg.ProviderSubscriptionsRequired(time.Now().UTC()), destinationCountry, destinationState, destinationCity, promotedCursorRank(c.Query("cursor")))
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "flash sales unavailable")
	}
	defer rows.Close()
	products := make([]fiber.Map, 0, page.Limit+1)
	var total int64
	for rows.Next() {
		var id, sku, title, description, currency, hubID, hubName, hubCity string
		var categories, images []string
		var price, compareAt, averageRating float64
		var reviewCount, soldCount int64
		var inventory int
		var factoryRaw []byte
		var createdAt time.Time
		var providerID *string
		var fulfillmentMode string
		var paidPriority int
		if err := rows.Scan(&id, &sku, &title, &description, &categories, &images, &currency, &price, &compareAt, &inventory, &factoryRaw, &hubID, &hubName, &hubCity, &createdAt, &total, &reviewCount, &soldCount, &averageRating, &providerID, &fulfillmentMode, &paidPriority); err != nil {
			return err
		}
		factory := map[string]any{}
		_ = json.Unmarshal(factoryRaw, &factory)
		products = append(products, fiber.Map{
			"id": id, "sku": sku, "title": title, "description": description,
			"category_path": categories, "image_urls": cc.normalizeImageURLs(images), "currency": currency,
			"price": price, "delivery_fee": catalogDeliveryFee(factory, destinationCountry, destinationState, destinationCity), "compare_at_price": compareAt, "inventory_count": inventory,
			"review_count": reviewCount, "sold_count": soldCount, "average_rating": averageRating,
			"provider_id": stringValue(providerID), "fulfillment_mode": catalogProductRoute(factory, destinationCountry),
			"is_flash_sale": true, "flash_sale_price": price, "factory_details": factory, "created_at": createdAt, "visibility_rank": paidPriority, "promoted": paidPriority > 0,
			"origin_hub": fiber.Map{"id": hubID, "name": hubName, "city": hubCity},
		})
	}
	nextCursor := ""
	if len(products) > page.Limit {
		products = products[:page.Limit]
		last := products[len(products)-1]
		nextCursor = encodeRankedCursor(last["created_at"].(time.Time), last["id"].(string), last["visibility_rank"].(int))
	}
	for _, product := range products {
		delete(product, "created_at")
	}
	if err := rows.Err(); err != nil {
		return fiber.ErrInternalServerError
	}
	rows.Close()
	return providerIdentityJSON(c, cc.db, fiber.Map{"products": products, "page": adminPageMeta(page, total, len(products), nextCursor)}, cc.s3)
}

func (cc *CatalogController) GetProduct(c *fiber.Ctx) error {
	c.Locals("public_provider_media", true)
	destinationCountry, destinationState, destinationCity, destinationErr := requestedDeliveryDestination(c)
	if destinationErr != nil {
		return destinationErr
	}
	productID := c.Params("product_id")
	var id, sku, title, description, currency, hubID, hubName, hubCity string
	var categories, images []string
	var price, compareAtPrice, flashSalePrice, averageRating float64
	var reviewCount, soldCount int64
	var inventory int
	var factoryRaw []byte
	var isFlashSale bool
	var providerID *string
	var fulfillmentMode string
	err := cc.db.QueryRow(c.Context(), `
		SELECT p.id, p.sku, p.title, p.description, p.category_path, p.image_urls,
			offer.currency_code,
			CASE WHEN p.is_flash_sale AND offer.currency_code=p.local_currency_code AND offer.delivered_price-offer.delivery_fee=p.local_selling_price AND p.flash_sale_price > 0 AND p.flash_sale_price < p.local_selling_price THEN p.flash_sale_price+offer.delivery_fee ELSE offer.delivered_price END,
			CASE WHEN offer.currency_code=p.local_currency_code AND offer.delivered_price-offer.delivery_fee=p.local_selling_price THEN CASE WHEN p.is_flash_sale AND p.flash_sale_price>0 AND p.flash_sale_price<p.local_selling_price THEN GREATEST(p.local_selling_price,COALESCE(p.compare_at_price,0))+offer.delivery_fee WHEN p.compare_at_price>p.local_selling_price THEN p.compare_at_price+offer.delivery_fee ELSE 0 END ELSE 0 END,
			p.inventory_count,
			p.factory_details || jsonb_build_object('payment_mode',p.payment_mode,'catalog_updated_at',p.updated_at,'catalog_version',p.catalog_version,'provider_name',COALESCE((SELECT seller.business_name FROM provider_organizations seller WHERE seller.id=p.provider_id),''),'inventory_country_code',p.inventory_country_code,'inventory_city',p.inventory_city,'inventory_location',p.inventory_location,'stock_state',p.stock_state,'handling_time_hours',p.handling_time_hours,'delivery_min_days',p.delivery_min_days,'delivery_max_days',p.delivery_max_days,'delivery_methods',p.delivery_methods,'delivery_areas',COALESCE((SELECT jsonb_agg(jsonb_build_object('country_code',a.country_code,'state',a.state_key,'city',a.city_key,'delivered_price',a.delivered_price,'delivery_fee',a.delivery_fee,'currency_code',a.currency_code)) FROM product_delivery_areas a WHERE a.product_id=p.id),'[]'::jsonb)),
			COALESCE(lh.id::text, ''), COALESCE(lh.name, ''), COALESCE(lh.city, ''),
			(p.is_flash_sale AND offer.currency_code=p.local_currency_code AND offer.delivered_price-offer.delivery_fee=p.local_selling_price), COALESCE(p.flash_sale_price+offer.delivery_fee, 0), p.review_count, p.sold_count,
			CASE WHEN p.review_count > 0 THEN p.review_rating_sum::float8 / p.review_count ELSE 0 END,
			p.provider_id::text,p.fulfillment_mode
		FROM products p
        LEFT JOIN provider_public_identity visibility ON visibility.provider_id=p.provider_id
		JOIN LATERAL (SELECT a.delivered_price,a.delivery_fee,a.currency_code FROM product_delivery_areas a JOIN countries_config market ON market.country_code=a.country_code AND market.currency_code=a.currency_code AND market.is_active=true AND 'flutterwave'=ANY(market.active_payment_gateways) WHERE a.product_id=p.id AND a.country_code=$3 AND (a.state_key='' OR a.state_key=$4) AND (a.city_key='' OR a.city_key=$5) ORDER BY (a.city_key<>'') DESC,(a.state_key<>'') DESC LIMIT 1) offer ON TRUE
		LEFT JOIN logistics_hubs lh ON lh.id = p.origin_hub_id
		WHERE p.id = $1 AND p.is_active = true AND p.moderation_status='approved' AND p.plan_slot<=COALESCE(visibility.listing_limit,1)
		  AND p.provider_id IS NOT NULL AND p.fulfillment_mode IN ('merchant_local','merchant_cross_border')
		  AND EXISTS(SELECT 1 FROM provider_organizations po WHERE po.id=p.provider_id AND po.verification_status='approved' AND po.is_active=true AND ($2::boolean OR EXISTS(SELECT 1 FROM provider_subscriptions ps WHERE ps.provider_id=po.id AND ps.status='active' AND ps.current_period_end>now())))
		  AND (p.payment_mode='contact' OR EXISTS(SELECT 1 FROM provider_payout_accounts pa WHERE pa.provider_id=p.provider_id AND pa.payment_provider='flutterwave' AND pa.status='active'))
	`, productID, !cc.cfg.ProviderSubscriptionsRequired(time.Now().UTC()), destinationCountry, destinationState, destinationCity).Scan(&id, &sku, &title, &description, &categories, &images, &currency, &price, &compareAtPrice, &inventory, &factoryRaw, &hubID, &hubName, &hubCity, &isFlashSale, &flashSalePrice, &reviewCount, &soldCount, &averageRating, &providerID, &fulfillmentMode)
	if errors.Is(err, pgx.ErrNoRows) {
		return fiber.NewError(fiber.StatusNotFound, "product not found")
	}
	if err != nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "Product details are temporarily unavailable. Please retry.")
	}
	factory := map[string]any{}
	_ = json.Unmarshal(factoryRaw, &factory)
	return providerIdentityJSON(c, cc.db, fiber.Map{
		"product": fiber.Map{
			"id":               id,
			"sku":              sku,
			"title":            title,
			"description":      description,
			"category_path":    categories,
			"image_urls":       cc.normalizeImageURLs(images),
			"currency":         currency,
			"price":            price,
			"delivery_fee":     catalogDeliveryFee(factory, destinationCountry, destinationState, destinationCity),
			"compare_at_price": compareAtPrice,
			"inventory_count":  inventory,
			"review_count":     reviewCount,
			"sold_count":       soldCount,
			"average_rating":   averageRating,
			"provider_id":      stringValue(providerID),
			"fulfillment_mode": catalogProductRoute(factory, destinationCountry),
			"factory_details":  factory,
			"origin_hub": fiber.Map{
				"id":   hubID,
				"name": hubName,
				"city": hubCity,
			},
			"is_flash_sale": isFlashSale,
			"flash_sale_price": func() float64 {
				if isFlashSale {
					return flashSalePrice
				}
				return 0
			}(),
		},
	}, cc.s3)
}

// ListRecommendations returns a deliberately small, server-selected set. The
// query uses the existing category_path GIN index and never transfers the full
// catalogue to a device for client-side filtering.
func (cc *CatalogController) ListRecommendations(c *fiber.Ctx) error {
	c.Locals("public_provider_media", true)
	destinationCountry, destinationState, destinationCity, destinationErr := requestedDeliveryDestination(c)
	if destinationErr != nil {
		return destinationErr
	}
	productID := c.Params("product_id")
	limit := 10
	if requested, err := strconv.Atoi(c.Query("limit", "10")); err == nil {
		if requested < 1 {
			requested = 1
		}
		if requested > 20 {
			requested = 20
		}
		limit = requested
	}
	c.Set(fiber.HeaderCacheControl, "public, max-age=5, stale-while-revalidate=10")
	c.Vary(fiber.HeaderAcceptEncoding)

	var sourceCategories []string
	if err := cc.db.QueryRow(c.Context(), `
		SELECT category_path
		FROM products
		WHERE id = $1 AND is_active = true AND moderation_status='approved'
	`, productID).Scan(&sourceCategories); err != nil {
		return fiber.NewError(fiber.StatusNotFound, "product not found")
	}

	query := `
		WITH related AS MATERIALIZED (
			SELECT p.id
			FROM products p
        LEFT JOIN provider_public_identity visibility ON visibility.provider_id=p.provider_id
			WHERE p.id <> $1
				AND p.is_active = true
				AND p.moderation_status='approved' AND p.plan_slot<=COALESCE(visibility.listing_limit,1)
				AND p.provider_id IS NOT NULL AND p.fulfillment_mode IN ('merchant_local','merchant_cross_border')
				AND EXISTS(SELECT 1 FROM provider_organizations po WHERE po.id=p.provider_id AND po.verification_status='approved' AND po.is_active=true AND ($4::boolean OR EXISTS(SELECT 1 FROM provider_subscriptions ps WHERE ps.provider_id=po.id AND ps.status='active' AND ps.current_period_end>now())))
				AND (p.payment_mode='contact' OR EXISTS(SELECT 1 FROM provider_payout_accounts pa WHERE pa.provider_id=p.provider_id AND pa.payment_provider='flutterwave' AND pa.status='active'))
				AND EXISTS(SELECT 1 FROM product_delivery_areas area WHERE area.product_id=p.id AND area.country_code=$5 AND (area.state_key='' OR area.state_key=$6) AND (area.city_key='' OR area.city_key=$7))
				AND p.inventory_count > 0
				AND cardinality($2::text[]) > 0
				AND p.category_path && $2::text[]
			ORDER BY (p.category_path[array_length(p.category_path,1)] = ($2::text[])[array_length($2::text[],1)]) DESC, COALESCE(visibility.verified AND visibility.paid_active,false) DESC, p.created_at DESC, p.id DESC
			LIMIT $3
		)
		SELECT p.id, p.sku, p.title, p.description, p.category_path, p.image_urls,
			offer.currency_code,
			CASE WHEN p.is_flash_sale AND offer.currency_code=p.local_currency_code AND offer.delivered_price-offer.delivery_fee=p.local_selling_price AND p.flash_sale_price > 0 AND p.flash_sale_price < p.local_selling_price THEN p.flash_sale_price+offer.delivery_fee ELSE offer.delivered_price END,
			CASE WHEN offer.currency_code=p.local_currency_code AND offer.delivered_price-offer.delivery_fee=p.local_selling_price THEN CASE WHEN p.is_flash_sale AND p.flash_sale_price>0 AND p.flash_sale_price<p.local_selling_price THEN GREATEST(p.local_selling_price,COALESCE(p.compare_at_price,0))+offer.delivery_fee WHEN p.compare_at_price>p.local_selling_price THEN p.compare_at_price+offer.delivery_fee ELSE 0 END ELSE 0 END,
			p.inventory_count, p.factory_details || jsonb_build_object('payment_mode',p.payment_mode,'catalog_updated_at',p.updated_at,'catalog_version',p.catalog_version,'provider_name',COALESCE((SELECT seller.business_name FROM provider_organizations seller WHERE seller.id=p.provider_id),''),'inventory_country_code',p.inventory_country_code,'inventory_city',p.inventory_city,'inventory_location',p.inventory_location,'stock_state',p.stock_state,'handling_time_hours',p.handling_time_hours,'delivery_min_days',p.delivery_min_days,'delivery_max_days',p.delivery_max_days,'delivery_methods',p.delivery_methods,'delivery_areas',COALESCE((SELECT jsonb_agg(jsonb_build_object('country_code',a.country_code,'state',a.state_key,'city',a.city_key,'delivered_price',a.delivered_price,'delivery_fee',a.delivery_fee,'currency_code',a.currency_code)) FROM product_delivery_areas a WHERE a.product_id=p.id),'[]'::jsonb)),
			COALESCE(lh.id::text, ''), COALESCE(lh.name, ''), COALESCE(lh.city, ''),
			(p.is_flash_sale AND offer.currency_code=p.local_currency_code AND offer.delivered_price-offer.delivery_fee=p.local_selling_price), COALESCE(p.flash_sale_price+offer.delivery_fee, 0), p.review_count, p.sold_count,
			CASE WHEN p.review_count > 0 THEN p.review_rating_sum::float8 / p.review_count ELSE 0 END,
			p.provider_id::text,p.fulfillment_mode
		FROM related candidate
		JOIN products p ON p.id = candidate.id
        LEFT JOIN provider_public_identity visibility ON visibility.provider_id=p.provider_id
		JOIN LATERAL (SELECT a.delivered_price,a.delivery_fee,a.currency_code FROM product_delivery_areas a JOIN countries_config market ON market.country_code=a.country_code AND market.currency_code=a.currency_code AND market.is_active=true AND 'flutterwave'=ANY(market.active_payment_gateways) WHERE a.product_id=p.id AND a.country_code=$5 AND (a.state_key='' OR a.state_key=$6) AND (a.city_key='' OR a.city_key=$7) ORDER BY (a.city_key<>'') DESC,(a.state_key<>'') DESC LIMIT 1) offer ON TRUE
		LEFT JOIN logistics_hubs lh ON lh.id = p.origin_hub_id
		ORDER BY (p.category_path[array_length(p.category_path,1)] = ($2::text[])[array_length($2::text[],1)]) DESC, COALESCE(visibility.verified AND visibility.paid_active,false) DESC, p.created_at DESC, p.id DESC
		LIMIT $3
	`
	rows, err := cc.db.Query(c.Context(), query, productID, sourceCategories, limit, !cc.cfg.ProviderSubscriptionsRequired(time.Now().UTC()), destinationCountry, destinationState, destinationCity)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "recommendations unavailable")
	}
	defer rows.Close()

	products := make([]fiber.Map, 0, limit)
	for rows.Next() {
		var id, sku, title, description, currency, hubID, hubName, hubCity string
		var categories, images []string
		var price, compareAtPrice, flashSalePrice, averageRating float64
		var reviewCount, soldCount int64
		var inventory int
		var factoryRaw []byte
		var isFlashSale bool
		var providerID *string
		var fulfillmentMode string
		if err := rows.Scan(&id, &sku, &title, &description, &categories, &images, &currency, &price, &compareAtPrice, &inventory, &factoryRaw, &hubID, &hubName, &hubCity, &isFlashSale, &flashSalePrice, &reviewCount, &soldCount, &averageRating, &providerID, &fulfillmentMode); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "recommendations unavailable")
		}
		factory := map[string]any{}
		_ = json.Unmarshal(factoryRaw, &factory)
		products = append(products, fiber.Map{
			"id": id, "sku": sku, "title": title, "description": description,
			"category_path": categories, "image_urls": cc.normalizeImageURLs(images), "currency": currency,
			"price": price, "delivery_fee": catalogDeliveryFee(factory, destinationCountry, destinationState, destinationCity), "compare_at_price": compareAtPrice, "inventory_count": inventory,
			"review_count": reviewCount, "sold_count": soldCount, "average_rating": averageRating,
			"provider_id": stringValue(providerID), "fulfillment_mode": catalogProductRoute(factory, destinationCountry),
			"is_flash_sale": isFlashSale, "flash_sale_price": flashSalePrice, "factory_details": factory,
			"origin_hub": fiber.Map{"id": hubID, "name": hubName, "city": hubCity},
		})
	}
	if err := rows.Err(); err != nil {
		return fiber.ErrInternalServerError
	}
	rows.Close()
	return providerIdentityJSON(c, cc.db, fiber.Map{"products": products}, cc.s3)
}

func (cc *CatalogController) normalizeImageURLs(images []string) []string {
	normalized := make([]string, 0, len(images))
	for _, image := range images {
		item := strings.TrimSpace(image)
		if item == "" {
			continue
		}
		if strings.HasPrefix(item, "user-uploads/") {
			normalized = append(normalized, cc.s3.ObjectURL(item))
			continue
		}
		const marker = "/api/v1/public/images/view/"
		if markerIndex := strings.Index(item, marker); markerIndex >= 0 {
			key := strings.TrimLeft(item[markerIndex+len(marker):], "/")
			if decoded, err := url.PathUnescape(key); err == nil {
				key = decoded
			}
			normalized = append(normalized, cc.s3.ObjectURL(key))
			continue
		}
		if key := cc.s3.KeyFromURL(item); key != "" {
			normalized = append(normalized, cc.s3.ObjectURL(key))
			continue
		}
		normalized = append(normalized, item)
	}
	return normalized
}
