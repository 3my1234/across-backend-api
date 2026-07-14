package controllers

import (
	"github.com/gofiber/fiber/v2"
)

func PriceBreakdown(c *fiber.Ctx) error {
	var req struct {
		CostPriceRMB         float64 `json:"cost_price_rmb"`
		ExchangeRateSnapshot float64 `json:"exchange_rate_snapshot"`
		ShippingEstimateNGN  float64 `json:"shipping_estimate_ngn"`
		DesiredProfitPercent float64 `json:"desired_profit_percent"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	if req.CostPriceRMB <= 0 || req.ExchangeRateSnapshot <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "cost_price_rmb and exchange_rate are required")
	}
	if req.DesiredProfitPercent <= 0 {
		req.DesiredProfitPercent = 30
	}
	if req.ShippingEstimateNGN <= 0 {
		req.ShippingEstimateNGN = 2500
	}
	costInNGN := req.CostPriceRMB * req.ExchangeRateSnapshot
	duty := costInNGN * 0.20
	platformFee := costInNGN * 0.05
	subtotal := costInNGN + duty + req.ShippingEstimateNGN + platformFee
	profit := subtotal * (req.DesiredProfitPercent / 100)
	suggestedPrice := subtotal + profit
	return c.JSON(fiber.Map{
		"cost_rmb":                req.CostPriceRMB,
		"exchange_rate":           req.ExchangeRateSnapshot,
		"cost_in_ngn":             roundMoney(costInNGN),
		"duty_20_pct":             roundMoney(duty),
		"shipping_estimate":       roundMoney(req.ShippingEstimateNGN),
		"platform_fee_5_pct":      roundMoney(platformFee),
		"desired_profit_pct":      req.DesiredProfitPercent,
		"profit_amount":           roundMoney(profit),
		"suggested_selling_price": roundMoney(suggestedPrice),
		"vat":                     100.0,
		"stamp_duty":              170.0,
		"estimated_final_price":   roundMoney(suggestedPrice + 100 + 170),
	})
}
