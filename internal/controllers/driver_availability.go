package controllers

import (
	"context"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"math"
	"strconv"
	"strings"
	"time"
)

const driverListingSQL = "(lower(l.category) LIKE '%driver%' OR lower(l.category) LIKE '%chauffeur%' OR lower(l.category) LIKE '%taxi%')"

func (m *ProviderMarketplaceController) DriverAvailability(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	actor, _ := c.Locals("user_id").(string)
	provider, role, err := m.providerForUser(ctx, actor)
	if err != nil || (role != "owner" && role != "manager") {
		return fiber.ErrForbidden
	}
	c.Set("Cache-Control", "private, no-store")
	if c.Method() == "DELETE" {
		_, err = m.db.Exec(ctx, `DELETE FROM provider_driver_presence WHERE provider_id=$1::uuid`, provider)
		if err != nil {
			return fiber.ErrInternalServerError
		}
		return c.SendStatus(204)
	}
	if c.Method() == "GET" {
		var listing *string
		var expires *time.Time
		err = m.db.QueryRow(ctx, `SELECT (SELECT listing_id::text FROM provider_driver_presence WHERE provider_id=$1::uuid AND expires_at>now()),(SELECT expires_at FROM provider_driver_presence WHERE provider_id=$1::uuid AND expires_at>now())`, provider).Scan(&listing, &expires)
		if err != nil {
			return fiber.ErrInternalServerError
		}
		return c.JSON(fiber.Map{"online": listing != nil, "listing_id": listing, "expires_at": expires})
	}
	var req struct {
		ListingID  string    `json:"listing_id"`
		Latitude   *float64  `json:"latitude"`
		Longitude  *float64  `json:"longitude"`
		Accuracy   *float64  `json:"accuracy_m"`
		CapturedAt time.Time `json:"captured_at"`
	}
	if c.BodyParser(&req) != nil {
		return fiber.ErrBadRequest
	}
	if _, err = uuid.Parse(req.ListingID); err != nil {
		return fiber.NewError(422, "Choose an approved driver listing")
	}
	if req.Latitude == nil || req.Longitude == nil || req.Accuracy == nil || !validCoordinates(*req.Latitude, *req.Longitude) || math.IsNaN(*req.Accuracy) || math.IsInf(*req.Accuracy, 0) || *req.Accuracy < 0 || *req.Accuracy > 1000 {
		return fiber.NewError(422, "Wait for a location accurate to within 1 km")
	}
	age := time.Since(req.CapturedAt)
	if req.CapturedAt.IsZero() || age > time.Minute || age < -15*time.Second {
		return fiber.NewError(422, "Refresh your current location before going online")
	}
	// Upsert is a single statement; ownership, verification and listing approval
	// are checked on every heartbeat, including revocations while already online.
	tag, err := m.db.Exec(ctx, `INSERT INTO provider_driver_presence(provider_id,listing_id,latitude,longitude,accuracy_m,updated_at,expires_at)
 SELECT l.provider_id,l.id,$3,$4,$5,clock_timestamp(),clock_timestamp()+interval '120 seconds'
 FROM provider_listings l JOIN provider_public_identity i ON i.provider_id=l.provider_id
 WHERE l.id=$2::uuid AND l.provider_id=$1::uuid AND l.status='approved' AND i.verified AND driver_documents_approved(l.provider_id) AND `+driverListingSQL+`
 ON CONFLICT(provider_id) DO UPDATE SET listing_id=EXCLUDED.listing_id,latitude=EXCLUDED.latitude,longitude=EXCLUDED.longitude,accuracy_m=EXCLUDED.accuracy_m,updated_at=EXCLUDED.updated_at,expires_at=EXCLUDED.expires_at`, provider, req.ListingID, *req.Latitude, *req.Longitude, *req.Accuracy)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if tag.RowsAffected() == 0 {
		return fiber.NewError(403, "An approved driver listing and approved provider documents are required")
	}
	return c.JSON(fiber.Map{"online": true, "expires_in": 120})
}

func (m *ProviderMarketplaceController) NearbyDrivers(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	c.Set("Cache-Control", "private, no-store")
	lat, e := strconv.ParseFloat(c.Query("latitude"), 64)
	if e != nil {
		return fiber.NewError(422, "Choose your current location")
	}
	lon, e := strconv.ParseFloat(c.Query("longitude"), 64)
	if e != nil || !validCoordinates(lat, lon) {
		return fiber.NewError(422, "Choose your current location")
	}
	radius := 10.0
	if raw := strings.TrimSpace(c.Query("radius_km")); raw != "" {
		radius, e = strconv.ParseFloat(raw, 64)
		if e != nil || math.IsNaN(radius) || radius <= 0 || radius > 50 {
			return fiber.NewError(422, "Radius must be between 0 and 50 km")
		}
	}
	deltaLat := radius / 111.32
	scale := math.Max(.01, math.Abs(math.Cos(lat*math.Pi/180)))
	deltaLon := radius / (111.32 * scale)
	if math.Abs(lat)+deltaLat >= 90 {
		deltaLon = 180
	}
	rows, err := m.db.Query(ctx, `WITH candidates AS (
 SELECT l.id::text,l.provider_id::text,l.title,l.description,l.category,l.media_urls,l.city,l.country_code,l.price,l.currency_code,l.pricing_unit,i.paid_active,
 6371*2*asin(sqrt(LEAST(1.0,power(sin(radians(d.latitude-$1)/2),2)+cos(radians($1))*cos(radians(d.latitude))*power(sin(radians(d.longitude-$2)/2),2)))) AS distance
 FROM provider_driver_presence d JOIN provider_listings l ON l.id=d.listing_id AND l.provider_id=d.provider_id
 JOIN provider_public_identity i ON i.provider_id=d.provider_id
 WHERE d.expires_at>clock_timestamp() AND i.verified AND driver_documents_approved(l.provider_id) AND l.status='approved' AND `+driverListingSQL+`
 AND (point(d.longitude,d.latitude)<@box(point($2-$4,$1-$3),point($2+$4,$1+$3))
 OR point(d.longitude,d.latitude)<@box(point($2-$4-360,$1-$3),point($2+$4-360,$1+$3))
 OR point(d.longitude,d.latitude)<@box(point($2-$4+360,$1-$3),point($2+$4+360,$1+$3))))
 SELECT * FROM candidates WHERE distance<=$5 ORDER BY paid_active DESC,distance,id LIMIT 25`, lat, lon, deltaLat, deltaLon, radius)
	if err != nil {
		return fiber.NewError(503, "Nearby drivers are temporarily unavailable")
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var id, provider, title, description, category, city, country, currency, unit string
		var media []string
		var price *float64
		var distance float64
		var promoted bool
		if err = rows.Scan(&id, &provider, &title, &description, &category, &media, &city, &country, &price, &currency, &unit, &promoted, &distance); err != nil {
			return fiber.ErrInternalServerError
		}
		media = (&CatalogController{s3: m.s3}).normalizeImageURLs(media)
		items = append(items, fiber.Map{"id": id, "provider_id": provider, "title": title, "description": description, "category": category, "media_urls": media, "city": city, "country_code": country, "price": price, "currency_code": currency, "pricing_unit": unit, "listing_type": "artisan", "distance_km": math.Round(distance*2) / 2, "nearby_driver": true, "contact_available": true})
	}
	if rows.Err() != nil {
		return fiber.ErrInternalServerError
	}
	rows.Close()
	// Retain only provider identity and approximate distance. Never return coordinates.
	return providerIdentityJSON(c, m.db, fiber.Map{"items": items, "approximate_distances": true, "expires_in": 30}, m.s3)
}
