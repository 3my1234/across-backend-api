package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Only flight services keep routes; every other service stays a plain chat.
const flightListingSQL = "(l.category||' '||l.title) ~* '(^|[^a-z])(flights?|airlines?|airways|air tickets?|travel agenc(y|ies))([^a-z]|$)'"
const flightRouteLimit = 60
const flightPendingLimit = 10

type flightRoute struct {
	ID       string   `json:"id"`
	Type     string   `json:"flight_type"`
	From     string   `json:"from_city"`
	To       string   `json:"to_city"`
	Price    float64  `json:"price"`
	Discount *float64 `json:"discount_price"`
	Active   bool     `json:"is_active"`
	Currency string   `json:"currency_code"`
}

// The snapshot saved with a request: later price changes never rewrite it.
type flightRequest struct {
	RouteID    string   `json:"route_id"`
	Type       string   `json:"flight_type"`
	From       string   `json:"from_city"`
	To         string   `json:"to_city"`
	Price      float64  `json:"price"`
	Discount   *float64 `json:"discount_price"`
	Currency   string   `json:"currency_code"`
	TravelDate string   `json:"travel_date"`
	Passengers int      `json:"passengers"`
}

func flightPlace(raw string) (string, bool) {
	value := strings.Join(strings.Fields(raw), " ")
	if n := len([]rune(value)); n < 2 || n > 60 {
		return "", false
	}
	letters := 0
	for _, r := range value {
		if unicode.IsLetter(r) {
			letters++
		} else if !unicode.IsMark(r) && !strings.ContainsRune(" -'.,()", r) {
			return "", false
		}
	}
	return value, letters >= 2
}

func validFlightRoute(in *flightRoute) error {
	in.Type = strings.ToLower(strings.TrimSpace(in.Type))
	if in.Type != "local" && in.Type != "international" {
		return fiber.NewError(422, "Choose local or international")
	}
	var ok bool
	if in.From, ok = flightPlace(in.From); !ok {
		return fiber.NewError(422, "Write the place the flight leaves from")
	}
	if in.To, ok = flightPlace(in.To); !ok {
		return fiber.NewError(422, "Write the place the flight goes to")
	}
	if strings.EqualFold(in.From, in.To) {
		return fiber.NewError(422, "The two places must be different")
	}
	valid := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 1 && v <= 100000000 }
	if !valid(in.Price) {
		return fiber.NewError(422, "Write the price of this flight")
	}
	in.Price = math.Round(in.Price*100) / 100
	if in.Discount != nil {
		if *in.Discount == 0 {
			in.Discount = nil
			return nil
		}
		rounded := math.Round(*in.Discount*100) / 100
		if !valid(rounded) || rounded >= in.Price {
			return fiber.NewError(422, "The discount price must be lower than the normal price")
		}
		in.Discount = &rounded
	}
	return nil
}

func (m *ProviderMarketplaceController) flightRoutes(ctx context.Context, listing string, all bool) ([]flightRoute, error) {
	rows, err := m.db.Query(ctx, `SELECT r.id::text,r.flight_type,r.from_city,r.to_city,r.price::float8,r.discount_price::float8,r.is_active,l.currency_code
 FROM provider_flight_routes r JOIN provider_listings l ON l.id=r.listing_id
 WHERE r.listing_id=$1::uuid AND ($2::boolean OR r.is_active) ORDER BY r.flight_type DESC,lower(r.from_city),lower(r.to_city),r.id LIMIT $3`, listing, all, flightRouteLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []flightRoute{}
	for rows.Next() {
		var r flightRoute
		if err = rows.Scan(&r.ID, &r.Type, &r.From, &r.To, &r.Price, &r.Discount, &r.Active, &r.Currency); err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return items, rows.Err()
}

// ListFlights is public: the routes customers may ask for.
func (m *ProviderMarketplaceController) ListFlights(c *fiber.Ctx) error {
	if uuid.Validate(c.Params("listing_id")) != nil {
		return fiber.ErrBadRequest
	}
	ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
	defer cancel()
	var eligible bool
	err := m.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_listings l JOIN provider_organizations p ON p.id=l.provider_id
 WHERE l.id=$1::uuid AND l.status='approved' AND p.verification_status='approved' AND p.is_active AND `+flightListingSQL+`
 AND ($2::boolean OR EXISTS(SELECT 1 FROM provider_subscriptions s WHERE s.provider_id=p.id AND s.status='active' AND s.current_period_end>now())))`, c.Params("listing_id"), !m.subscriptionsRequired()).Scan(&eligible)
	if err != nil {
		return fiber.NewError(503, "Flights are temporarily unavailable")
	}
	items := []flightRoute{}
	if eligible {
		if items, err = m.flightRoutes(ctx, c.Params("listing_id"), false); err != nil {
			return fiber.NewError(503, "Flights are temporarily unavailable")
		}
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{"items": items})
}

// ownFlightListing confirms an owner or manager is editing their own flight service.
func (m *ProviderMarketplaceController) ownFlightListing(ctx context.Context, c *fiber.Ctx) error {
	if uuid.Validate(c.Params("listing_id")) != nil || (c.Params("route_id") != "" && uuid.Validate(c.Params("route_id")) != nil) {
		return fiber.ErrBadRequest
	}
	actor, _ := c.Locals("user_id").(string)
	provider, role, err := m.providerForUser(ctx, actor)
	if err != nil || (role != "owner" && role != "manager") {
		return fiber.ErrForbidden
	}
	var flight bool
	err = m.db.QueryRow(ctx, `SELECT `+flightListingSQL+` FROM provider_listings l WHERE l.id=$1::uuid AND l.provider_id=$2::uuid AND l.status<>'archived'`, c.Params("listing_id"), provider).Scan(&flight)
	if err == pgx.ErrNoRows {
		return fiber.ErrNotFound
	}
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if !flight {
		return fiber.NewError(422, "Flight prices are only for flight services")
	}
	c.Set("Cache-Control", "private, no-store")
	return nil
}

func (m *ProviderMarketplaceController) MyFlights(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	if err := m.ownFlightListing(ctx, c); err != nil {
		return err
	}
	items, err := m.flightRoutes(ctx, c.Params("listing_id"), true)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	return c.JSON(fiber.Map{"items": items, "limit": flightRouteLimit})
}

func (m *ProviderMarketplaceController) SaveFlight(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	if err := m.ownFlightListing(ctx, c); err != nil {
		return err
	}
	in := flightRoute{Active: true}
	if len(c.Body()) > 4096 || c.BodyParser(&in) != nil {
		return fiber.ErrBadRequest
	}
	if err := validFlightRoute(&in); err != nil {
		return err
	}
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer tx.Rollback(ctx)
	if c.Method() == "POST" {
		// Serialises additions so that the limit holds.
		var count int
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('flight-routes:'||$1::text,0))`, c.Params("listing_id")); err == nil {
			err = tx.QueryRow(ctx, `SELECT count(*) FROM provider_flight_routes WHERE listing_id=$1::uuid`, c.Params("listing_id")).Scan(&count)
		}
		if err != nil {
			return fiber.ErrInternalServerError
		}
		if count >= flightRouteLimit {
			return fiber.NewError(422, fmt.Sprintf("You can add up to %d flights", flightRouteLimit))
		}
		err = tx.QueryRow(ctx, `INSERT INTO provider_flight_routes(listing_id,flight_type,from_city,to_city,price,discount_price,is_active) VALUES($1::uuid,$2,$3,$4,$5,$6,$7) RETURNING id::text`, c.Params("listing_id"), in.Type, in.From, in.To, in.Price, in.Discount, in.Active).Scan(&in.ID)
	} else {
		err = tx.QueryRow(ctx, `UPDATE provider_flight_routes SET flight_type=$3,from_city=$4,to_city=$5,price=$6,discount_price=$7,is_active=$8,updated_at=now() WHERE id=$2::uuid AND listing_id=$1::uuid RETURNING id::text`, c.Params("listing_id"), c.Params("route_id"), in.Type, in.From, in.To, in.Price, in.Discount, in.Active).Scan(&in.ID)
	}
	if err == pgx.ErrNoRows {
		return fiber.ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return fiber.NewError(409, "You already added this flight. Edit that one instead.")
	}
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if err = tx.Commit(ctx); err != nil {
		return fiber.ErrInternalServerError
	}
	return c.JSON(fiber.Map{"id": in.ID})
}

func (m *ProviderMarketplaceController) DeleteFlight(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	if err := m.ownFlightListing(ctx, c); err != nil {
		return err
	}
	if _, err := m.db.Exec(ctx, `DELETE FROM provider_flight_routes WHERE id=$1::uuid AND listing_id=$2::uuid`, c.Params("route_id"), c.Params("listing_id")); err != nil {
		return fiber.ErrInternalServerError
	}
	return c.SendStatus(204)
}

// buildFlightRequest reads the route from the database: the customer never sets a price.
func buildFlightRequest(ctx context.Context, tx pgx.Tx, listing, user, route, date string, passengers int, now time.Time) (flightRequest, error) {
	out := flightRequest{RouteID: route, TravelDate: date, Passengers: passengers}
	if uuid.Validate(route) != nil {
		return out, fiber.NewError(422, "Choose a flight")
	}
	if passengers < 1 || passengers > 9 {
		return out, fiber.NewError(422, "Choose 1 to 9 passengers")
	}
	travel, err := time.Parse("2006-01-02", date)
	today := now.UTC().Add(time.Hour).Truncate(24 * time.Hour) // Nigeria is UTC+1 all year
	if err != nil || travel.Before(today) || travel.After(today.AddDate(1, 0, 0)) {
		return out, fiber.NewError(422, "Choose a travel date from today up to one year ahead")
	}
	// One customer's requests for one listing are counted one at a time.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('flight-request:'||$1::text||$2::text,0))`, listing, user); err != nil {
		return out, fiber.ErrInternalServerError
	}
	err = tx.QueryRow(ctx, `SELECT r.flight_type,r.from_city,r.to_city,r.price::float8,r.discount_price::float8,l.currency_code FROM provider_flight_routes r JOIN provider_listings l ON l.id=r.listing_id
 WHERE r.id=$1::uuid AND r.listing_id=$2::uuid AND r.is_active AND `+flightListingSQL, route, listing).Scan(&out.Type, &out.From, &out.To, &out.Price, &out.Discount, &out.Currency)
	if err == pgx.ErrNoRows {
		return out, fiber.NewError(409, "This flight is no longer offered. Choose another one.")
	}
	if err != nil {
		return out, fiber.ErrInternalServerError
	}
	var waiting int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM provider_requests WHERE listing_id=$1::uuid AND user_id=$2::uuid AND status='pending' AND flight IS NOT NULL`, listing, user).Scan(&waiting); err != nil {
		return out, fiber.ErrInternalServerError
	}
	if waiting >= flightPendingLimit {
		return out, fiber.NewError(429, "You already have several requests waiting. Let the provider answer them first.")
	}
	return out, nil
}

func flightMoney(currency string, value float64) string {
	whole := fmt.Sprintf("%.0f", value)
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	return currency + " " + whole
}

// text keeps the request readable in older apps and in notifications.
func (f flightRequest) text(note string) string {
	price := f.Price
	if f.Discount != nil {
		price = *f.Discount
	}
	people := "passengers"
	if f.Passengers == 1 {
		people = "passenger"
	}
	day := f.TravelDate
	if parsed, err := time.Parse("2006-01-02", f.TravelDate); err == nil {
		day = parsed.Format("2 January 2006")
	}
	text := fmt.Sprintf("%s to %s (%s flight), %s, %d %s, %s each.", f.From, f.To, f.Type, day, f.Passengers, people, flightMoney(f.Currency, price))
	if note != "" {
		text += " " + note
	}
	return text
}

// addFlightCards attaches the saved request to chat messages that came from one.
// An older database (before migration 130) simply has none.
func (m *ProviderMarketplaceController) addFlightCards(ctx context.Context, items []fiber.Map) {
	if len(items) == 0 {
		return
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item["id"].(string))
	}
	rows, err := m.db.Query(ctx, `SELECT msg.id::text,r.id::text,r.status,r.message,to_jsonb(r)->'flight' FROM provider_conversation_messages msg JOIN provider_requests r ON r.id::text=to_jsonb(msg)->>'source_request_id'
 WHERE msg.id=ANY($1::uuid[]) AND jsonb_typeof(to_jsonb(r)->'flight')='object'`, ids)
	if err != nil {
		return
	}
	defer rows.Close()
	cards := map[string]fiber.Map{}
	for rows.Next() {
		var message, request, status, text string
		var flight json.RawMessage
		if rows.Scan(&message, &request, &status, &text, &flight) != nil {
			return
		}
		var saved flightRequest
		_ = json.Unmarshal(flight, &saved)
		note := strings.TrimSpace(strings.TrimPrefix(text, saved.text("")))
		cards[message] = fiber.Map{"request_id": request, "status": status, "note": note, "flight": flight}
	}
	if rows.Err() != nil {
		return
	}
	for _, item := range items {
		if card := cards[item["id"].(string)]; card != nil {
			item["flight_request"] = card
		}
	}
}

// answerFlight posts the provider's answer into the same chat, so that both sides see it
// at once and the card refreshes.
func answerFlight(ctx context.Context, tx pgx.Tx, request, actor, status string) (string, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT flight FROM provider_requests WHERE id=$1::uuid`, request).Scan(&raw); err != nil {
		return "", err
	}
	var flight flightRequest
	if json.Unmarshal(raw, &flight) != nil || flight.From == "" {
		return "", nil
	}
	route := flight.From + " to " + flight.To
	answer := map[string]string{
		"accepted":  route + " is available. Confirm the final price here before you pay the provider.",
		"rejected":  route + " is not available. You can ask about another flight or date.",
		"completed": route + " is done. Thank you.",
	}[status]
	if answer == "" {
		return "", nil
	}
	_, err := tx.Exec(ctx, `WITH sent AS (
 INSERT INTO provider_conversation_messages(conversation_id,sender_type,sender_user_id,body)
 SELECT msg.conversation_id,'provider',$2::uuid,$3 FROM provider_conversation_messages msg WHERE msg.source_request_id=$1::uuid RETURNING conversation_id,created_at)
 UPDATE provider_conversations c SET last_message_at=GREATEST(c.last_message_at,sent.created_at),provider_last_read_at=now(),updated_at=now() FROM sent WHERE c.id=sent.conversation_id`, request, actor, answer)
	return answer, err
}
