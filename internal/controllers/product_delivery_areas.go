package controllers

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type productDeliveryArea struct {
	CountryCode    string  `json:"country_code"`
	State          string  `json:"state"`
	City           string  `json:"city"`
	DeliveredPrice float64 `json:"delivered_price"`
	CurrencyCode   string  `json:"currency_code"`
}

var deliveryCountryCode = regexp.MustCompile(`^[A-Z]{2}$`)
var deliveryCurrencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

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
		if areas[i].DeliveredPrice == 0 && areas[i].CurrencyCode == "" && areas[i].CountryCode == "NG" && baseCurrency == "NGN" {
			areas[i].DeliveredPrice, areas[i].CurrencyCode = basePrice, baseCurrency
		}
		if !deliveryCountryCode.MatchString(areas[i].CountryCode) || (areas[i].City != "" && areas[i].State == "") {
			return nil, fmt.Errorf("each delivery area needs a valid country; city restrictions also need a state")
		}
		if len([]rune(areas[i].State)) > 100 || len([]rune(areas[i].City)) > 100 {
			return nil, fmt.Errorf("delivery state and city must be 100 characters or fewer")
		}
		if !deliveryCurrencyCode.MatchString(areas[i].CurrencyCode) || areas[i].DeliveredPrice <= 0 || math.IsNaN(areas[i].DeliveredPrice) || math.IsInf(areas[i].DeliveredPrice, 0) {
			return nil, fmt.Errorf("each delivery area needs a positive delivered price and three-letter currency")
		}
		if mode == "merchant_local" && areas[i].CountryCode != stockCountry {
			return nil, fmt.Errorf("local stock can only be offered within its stock country; use international fulfilment for other countries")
		}
		key := areas[i].CountryCode + "|" + areas[i].State + "|" + areas[i].City
		if seen[key] {
			return nil, fmt.Errorf("remove duplicate delivery areas")
		}
		seen[key] = true
	}
	return areas, nil
}

func replaceProductDeliveryAreas(ctx context.Context, tx pgx.Tx, productID string, areas []productDeliveryArea) error {
	if _, err := tx.Exec(ctx, `DELETE FROM product_delivery_areas WHERE product_id=$1::uuid`, productID); err != nil {
		return err
	}
	for _, area := range areas {
		if _, err := tx.Exec(ctx, `INSERT INTO product_delivery_areas(product_id,country_code,state_key,city_key,delivered_price,currency_code) VALUES($1::uuid,$2,$3,$4,$5,$6)`, productID, area.CountryCode, area.State, area.City, area.DeliveredPrice, area.CurrencyCode); err != nil {
			return err
		}
	}
	return nil
}

func validateEnabledDeliveryMarkets(ctx context.Context, db *pgxpool.Pool, areas []productDeliveryArea) error {
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
