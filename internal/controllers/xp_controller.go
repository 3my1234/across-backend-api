package controllers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

type XPController struct {
	db *pgxpool.Pool
}

func NewXPController(db *pgxpool.Pool) *XPController {
	return &XPController{db: db}
}

// ClaimDailyLogin awards 1 XP if user hasn't claimed today
func (x *XPController) ClaimDailyLogin(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)

	// Check if already claimed today
	var exists bool
	err := x.db.QueryRow(c.Context(), `
		SELECT EXISTS(SELECT 1 FROM xp_daily_login WHERE user_id = $1 AND claim_date = CURRENT_DATE)
	`, userID).Scan(&exists)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "check failed")
	}
	if exists {
		return c.JSON(fiber.Map{"claimed": false, "message": "Already claimed today", "xp": 0})
	}

	// Award 1 XP
	_, err = x.db.Exec(c.Context(), `
		INSERT INTO xp_daily_login(user_id, xp_awarded) VALUES ($1, 1)
	`, userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "award failed")
	}

	_, err = x.db.Exec(c.Context(), `
		INSERT INTO xp_transactions(user_id, amount, reason, reference_id)
		VALUES ($1, 1, 'daily_login', 'daily-' || $1)
	`, userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "transaction failed")
	}

	return c.JSON(fiber.Map{"claimed": true, "message": "1 XP earned for daily login!", "xp": 1})
}

// GetBalance returns user's total XP
func (x *XPController) GetBalance(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)

	var totalXP int
	err := x.db.QueryRow(c.Context(), `
		SELECT COALESCE(total_xp, 0) FROM user_xp_balance WHERE user_id = $1
	`, userID).Scan(&totalXP)
	if err != nil {
		totalXP = 0
	}

	return c.JSON(fiber.Map{"xp": totalXP, "naira_value": totalXP})
}

// GetHistory returns recent XP transactions
func (x *XPController) GetHistory(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)

	rows, err := x.db.Query(c.Context(), `
		SELECT amount, reason, created_at FROM xp_transactions
		WHERE user_id = $1
		ORDER BY created_at DESC LIMIT 50
	`, userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "query failed")
	}
	defer rows.Close()

	history := make([]fiber.Map, 0)
	for rows.Next() {
		var amount int
		var reason string
		var createdAt time.Time
		if err := rows.Scan(&amount, &reason, &createdAt); err != nil {
			continue
		}
		history = append(history, fiber.Map{
			"amount":     amount,
			"reason":     reason,
			"created_at": createdAt,
		})
	}
	return c.JSON(fiber.Map{"history": history})
}

// AwardPurchaseXP awards 50 XP after a successful purchase
func (x *XPController) AwardPurchaseXP(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	orderID := c.Params("order_id")

	// Check order belongs to user and is paid
	var orderStatus string
	err := x.db.QueryRow(c.Context(), `
		SELECT order_status FROM orders WHERE id = $1 AND user_id = $2
	`, orderID, userID).Scan(&orderStatus)
	if err != nil || orderStatus != "Paid" {
		return fiber.NewError(fiber.StatusBadRequest, "order not payable")
	}

	// Check if already awarded
	var exists bool
	x.db.QueryRow(c.Context(), `
		SELECT EXISTS(SELECT 1 FROM xp_transactions WHERE reference_id = 'purchase-' || $1)
	`, orderID).Scan(&exists)
	if exists {
		return c.JSON(fiber.Map{"awarded": false, "message": "Already awarded"})
	}

	// Award 50 XP
	_, err = x.db.Exec(c.Context(), `
		INSERT INTO xp_transactions(user_id, amount, reason, reference_id)
		VALUES ($1, 50, 'purchase', 'purchase-' || $2)
	`, userID, orderID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "award failed")
	}

	return c.JSON(fiber.Map{"awarded": true, "message": "50 XP earned for purchase!", "xp": 50})
}
