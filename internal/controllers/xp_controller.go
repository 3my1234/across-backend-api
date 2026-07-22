package controllers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

type XPController struct {
	db      *pgxpool.Pool
	rewards *RewardService
}

func NewXPController(db *pgxpool.Pool) *XPController {
	return &XPController{db: db, rewards: NewRewardService(db)}
}

func (x *XPController) ClaimDailyLogin(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	claimed, err := x.rewards.AwardDailyLogin(c.Context(), userID, time.Now())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "daily reward unavailable")
	}
	if !claimed {
		return c.JSON(fiber.Map{"claimed": false, "message": "Today's login reward was already claimed", "xp": 0})
	}
	return c.JSON(fiber.Map{"claimed": true, "message": "You earned 1 XP for today's login", "xp": 1})
}

func (x *XPController) GetBalance(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	var totalXP int
	if err := x.db.QueryRow(c.Context(), `SELECT COALESCE(SUM(amount), 0)::int FROM xp_transactions WHERE user_id = $1`, userID).Scan(&totalXP); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "XP balance unavailable")
	}
	return c.JSON(fiber.Map{"xp": totalXP, "naira_value": totalXP})
}

func (x *XPController) GetHistory(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	rows, err := x.db.Query(c.Context(), `
        SELECT amount, reason, reference_id, created_at
        FROM xp_transactions
        WHERE user_id = $1
        ORDER BY created_at DESC
        LIMIT 50
    `, userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "XP history unavailable")
	}
	defer rows.Close()

	history := make([]fiber.Map, 0)
	for rows.Next() {
		var amount int
		var reason, referenceID string
		var createdAt time.Time
		if err := rows.Scan(&amount, &reason, &referenceID, &createdAt); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "XP history unavailable")
		}
		history = append(history, fiber.Map{"amount": amount, "reason": reason, "reference_id": referenceID, "created_at": createdAt})
	}
	return c.JSON(fiber.Map{"history": history})
}

func (x *XPController) AwardPurchaseXP(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	orderID := c.Params("order_id")
	var orderStatus string
	var total float64
	if err := x.db.QueryRow(c.Context(), `SELECT order_status::text, total_amount FROM orders WHERE id = $1 AND user_id = $2`, orderID, userID).Scan(&orderStatus, &total); err != nil {
		return fiber.NewError(fiber.StatusNotFound, "order not found")
	}
	if orderStatus != "Paid" && orderStatus != "Shipped" && orderStatus != "Delivered" && orderStatus != "Completed" {
		return fiber.NewError(fiber.StatusConflict, "purchase reward is available after payment confirmation")
	}
	awarded, amount, err := x.rewards.AwardPurchase(c.Context(), userID, orderID, total)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "purchase reward unavailable")
	}
	return c.JSON(fiber.Map{"awarded": awarded, "xp": amount, "message": func() string {
		if awarded {
			return "Purchase XP awarded"
		}
		return "Purchase XP was already awarded"
	}()})
}
