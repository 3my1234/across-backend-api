package controllers

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
)

type productDeliveryArea struct {
	CountryCode      string  `json:"country_code"`
	State            string  `json:"state"`
	City             string  `json:"city"`
	DeliveredPrice   float64 `json:"delivered_price"`
	ItemPrice        float64 `json:"item_price,omitempty"`
	DeliveryFee      float64 `json:"delivery_fee"`
	CurrencyCode     string  `json:"currency_code"`
	UsesPrimaryPrice *bool   `json:"uses_primary_price,omitempty"`
}

var deliveryCountryCode = regexp.MustCompile(`^[A-Z]{2}$`)
var deliveryCurrencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

// Older portals submit the old numeric offer when only the primary price is
// edited. Keep an existing link unless they actually changed the offer price.
func preserveLegacyPrimaryPriceLinks(incoming, previous []productDeliveryArea) {
	for i := range incoming {
		if incoming[i].UsesPrimaryPrice != nil {
			continue
		}
		for _, old := range previous {
			if old.UsesPrimaryPrice == nil || !*old.UsesPrimaryPrice ||
				!strings.EqualFold(incoming[i].CountryCode, old.CountryCode) ||
				deliveryLocationKey(incoming[i].State) != old.State || deliveryLocationKey(incoming[i].City) != old.City ||
				!strings.EqualFold(incoming[i].CurrencyCode, old.CurrencyCode) {
				continue
			}
			itemPrice := incoming[i].ItemPrice
			if itemPrice == 0 {
				itemPrice = incoming[i].DeliveredPrice - incoming[i].DeliveryFee
			}
			if roundMoney(itemPrice) == old.ItemPrice {
				linked := true
				incoming[i].UsesPrimaryPrice = &linked
			}
		}
	}
}

func deliveryLocationKey(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func normalizeProductDeliveryAreas(areas []productDeliveryArea, stockCountry, mode, baseCurrency string, basePrice float64) ([]productDeliveryArea, error) {
	if areas == nil {
		// Older clients can only create the existing Nigerian-market offer.
		if baseCurrency != "NGN" || (mode == "merchant_local" && stockCountry != "NG") {
			return nil, fmt.Errorf("choose delivery areas and delivered prices for this market")
		}
		areas = []productDeliveryArea{{CountryCode: "NG", DeliveredPrice: basePrice, CurrencyCode: "NGN"}}
	}
	if len(areas) == 0 || len(areas) > 20 {
		return nil, fmt.Errorf("choose between 1 and 20 delivery areas")
	}
	seen := make(map[string]bool, len(areas))
	for i := range areas {
		areas[i].CountryCode = strings.ToUpper(strings.TrimSpace(areas[i].CountryCode))
		areas[i].State = deliveryLocationKey(areas[i].State)
		areas[i].City = deliveryLocationKey(areas[i].City)
		areas[i].CurrencyCode = strings.ToUpper(strings.TrimSpace(areas[i].CurrencyCode))
		usesPrimary := areas[i].UsesPrimaryPrice != nil && *areas[i].UsesPrimaryPrice
		if usesPrimary {
			if areas[i].CurrencyCode == "" {
				areas[i].CurrencyCode = baseCurrency
			}
			if areas[i].CurrencyCode != baseCurrency {
				return nil, fmt.Errorf("primary-price delivery areas must use %s", baseCurrency)
			}
			areas[i].ItemPrice = basePrice
			areas[i].DeliveredPrice = roundMoney(basePrice + areas[i].DeliveryFee)
		}
		if areas[i].ItemPrice > 0 {
			areas[i].DeliveredPrice = roundMoney(areas[i].ItemPrice + areas[i].DeliveryFee)
		}
		if areas[i].DeliveredPrice == 0 && areas[i].CurrencyCode == "" && areas[i].CountryCode == "NG" && baseCurrency == "NGN" {
			areas[i].DeliveredPrice, areas[i].CurrencyCode = basePrice, baseCurrency
		}
		if !deliveryCountryCode.MatchString(areas[i].CountryCode) || (areas[i].City != "" && areas[i].State == "") {
			return nil, fmt.Errorf("each delivery area needs a valid country; city restrictions also need a state")
		}
		if len([]rune(areas[i].State)) > 100 || len([]rune(areas[i].City)) > 100 {
			return nil, fmt.Errorf("delivery state and city must be 100 characters or fewer")
		}
		if !deliveryCurrencyCode.MatchString(areas[i].CurrencyCode) || areas[i].DeliveredPrice <= 0 || math.IsNaN(areas[i].DeliveredPrice) || math.IsInf(areas[i].DeliveredPrice, 0) || areas[i].DeliveryFee < 0 || math.IsNaN(areas[i].DeliveryFee) || math.IsInf(areas[i].DeliveryFee, 0) || areas[i].DeliveryFee >= areas[i].DeliveredPrice {
			return nil, fmt.Errorf("each delivery area needs a positive delivered price and three-letter currency")
		}
		areas[i].ItemPrice = roundMoney(areas[i].DeliveredPrice - areas[i].DeliveryFee)
		if areas[i].UsesPrimaryPrice == nil {
			linked := areas[i].CurrencyCode == baseCurrency && areas[i].ItemPrice == roundMoney(basePrice)
			areas[i].UsesPrimaryPrice = &linked
		}
		key := areas[i].CountryCode + "|" + areas[i].State + "|" + areas[i].City
		if seen[key] {
			return nil, fmt.Errorf("remove duplicate delivery areas")
		}
		seen[key] = true
	}
	return areas, nil
}

// A single stocked listing can be local for one buyer and cross-border for another.
func productRouteForDestination(stockCountry, stockState, buyerCountry string) string {
	if strings.EqualFold(stockCountry, buyerCountry) && !strings.EqualFold(stockState, "import_on_demand") {
		return "merchant_local"
	}
	return "merchant_cross_border"
}

func catalogProductRoute(factory map[string]any, buyerCountry string) string {
	stockCountry, _ := factory["inventory_country_code"].(string)
	stockState, _ := factory["stock_state"].(string)
	return productRouteForDestination(stockCountry, stockState, buyerCountry)
}

func catalogDeliveryFee(factory map[string]any, country, state, city string) float64 {
	areas, _ := factory["delivery_areas"].([]any)
	bestSpecificity, fee := -1, 0.0
	for _, raw := range areas {
		area, _ := raw.(map[string]any)
		areaCountry, _ := area["country_code"].(string)
		areaState, _ := area["state"].(string)
		areaCity, _ := area["city"].(string)
		if areaCountry != country || (areaState != "" && areaState != state) || (areaCity != "" && areaCity != city) {
			continue
		}
		specificity := 0
		if areaState != "" {
			specificity++
		}
		if areaCity != "" {
			specificity++
		}
		if specificity > bestSpecificity {
			bestSpecificity = specificity
			fee, _ = area["delivery_fee"].(float64)
		}
	}
	return fee
}

func replaceProductDeliveryAreas(ctx context.Context, tx pgx.Tx, productID string, areas []productDeliveryArea) error {
	// Keep the API usable during a rolling deployment before migration 056 is
	// applied. Normalized totals remain correct; durable links start afterward.
	var supportsLinks bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='product_delivery_areas' AND column_name='uses_primary_price')`).Scan(&supportsLinks); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM product_delivery_areas WHERE product_id=$1::uuid`, productID); err != nil {
		return err
	}
	for _, area := range areas {
		query := `INSERT INTO product_delivery_areas(product_id,country_code,state_key,city_key,delivered_price,delivery_fee,currency_code) VALUES($1::uuid,$2,$3,$4,$5,$6,$7)`
		args := []any{productID, area.CountryCode, area.State, area.City, area.DeliveredPrice, area.DeliveryFee, area.CurrencyCode}
		if supportsLinks {
			query = `INSERT INTO product_delivery_areas(product_id,country_code,state_key,city_key,delivered_price,delivery_fee,currency_code,uses_primary_price) VALUES($1::uuid,$2,$3,$4,$5,$6,$7,$8)`
			args = append(args, area.UsesPrimaryPrice != nil && *area.UsesPrimaryPrice)
		}
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}

type deliveryMarketQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func validateEnabledDeliveryMarkets(ctx context.Context, db deliveryMarketQueryer, areas []productDeliveryArea) error {
	codes := make([]string, 0, len(areas))
	for _, area := range areas {
		codes = append(codes, area.CountryCode)
	}
	rows, err := db.Query(ctx, `SELECT country_code,currency_code FROM countries_config WHERE country_code=ANY($1) AND is_active=true AND 'flutterwave'=ANY(active_payment_gateways)`, codes)
	if err != nil {
		return err
	}
	defer rows.Close()
	marketCurrency := make(map[string]string, len(areas))
	for rows.Next() {
		var country, currency string
		if err := rows.Scan(&country, &currency); err != nil {
			return err
		}
		marketCurrency[country] = currency
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, area := range areas {
		currency, ok := marketCurrency[area.CountryCode]
		if !ok {
			return fmt.Errorf("%s is not enabled as a buyer checkout market", area.CountryCode)
		}
		if currency != area.CurrencyCode {
			return fmt.Errorf("%s prices must be in %s", area.CountryCode, currency)
		}
	}
	return nil
}

func requestedDeliveryDestination(c *fiber.Ctx) (country, state, city string, err error) {
	country = strings.ToUpper(strings.TrimSpace(c.Query("country_code", "NG")))
	if !deliveryCountryCode.MatchString(country) {
		return "", "", "", fiber.NewError(fiber.StatusBadRequest, "valid destination country_code is required")
	}
	return country, deliveryLocationKey(c.Query("state")), deliveryLocationKey(c.Query("city")), nil
}
