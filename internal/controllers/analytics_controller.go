package controllers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AnalyticsController struct {
	db *pgxpool.Pool
}

func NewAnalyticsController(db *pgxpool.Pool) *AnalyticsController {
	return &AnalyticsController{db: db}
}

// GetDailySales - Super admin sees count + revenue, Admin I sees count only
func (a *AnalyticsController) GetDailySales(c *fiber.Ctx) error {
	role, _ := c.Locals("admin_role").(string)
	isSuperAdmin := role == "super_admin"

	rows, err := a.db.Query(c.Context(), `
		SELECT sale_date, order_count, total_revenue FROM daily_sales
		ORDER BY sale_date DESC LIMIT 30
	`)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "analytics unavailable")
	}
	defer rows.Close()

	sales := make([]fiber.Map, 0)
	for rows.Next() {
		var date time.Time
		var count int
		var revenue float64
		if err := rows.Scan(&date, &count, &revenue); err != nil {
			continue
		}
		entry := fiber.Map{
			"date":        date,
			"order_count": count,
		}
		if isSuperAdmin {
			entry["total_revenue"] = revenue
		}
		sales = append(sales, entry)
	}

	// Totals
	var totalOrders int
	var totalRevenue float64
	a.db.QueryRow(c.Context(), `
		SELECT COALESCE(SUM(order_count), 0), COALESCE(SUM(total_revenue), 0) FROM daily_sales
	`).Scan(&totalOrders, &totalRevenue)

	result := fiber.Map{
		"daily":        sales,
		"total_orders": totalOrders,
	}
	if isSuperAdmin {
		result["total_revenue"] = totalRevenue
	}

	return c.JSON(result)
}

// GetProfitLoss - Super admin only
func (a *AnalyticsController) GetProfitLoss(c *fiber.Ctx) error {
	role, _ := c.Locals("admin_role").(string)
	if role != "super_admin" {
		return fiber.NewError(fiber.StatusForbidden, "only super admin")
	}

	rows, err := a.db.Query(c.Context(), `
		SELECT
			COALESCE(ds.sale_date, dl.loss_date) AS date,
			COALESCE(ds.order_count, 0) AS orders,
			COALESCE(ds.total_revenue, 0) AS revenue,
			COALESCE(dl.complaint_count, 0) AS complaints,
			COALESCE(dl.total_refunded, 0) AS refunds,
			COALESCE(ds.total_revenue, 0) - COALESCE(dl.total_refunded, 0) AS profit
		FROM daily_sales ds
		FULL OUTER JOIN daily_losses dl ON dl.loss_date = ds.sale_date
		ORDER BY date DESC LIMIT 30
	`)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "analytics unavailable")
	}
	defer rows.Close()

	entries := make([]fiber.Map, 0)
	for rows.Next() {
		var date time.Time
		var orders, complaints int
		var revenue, refunds, profit float64
		if err := rows.Scan(&date, &orders, &revenue, &complaints, &refunds, &profit); err != nil {
			continue
		}
		entries = append(entries, fiber.Map{
			"date":       date,
			"orders":     orders,
			"revenue":    revenue,
			"complaints": complaints,
			"refunds":    refunds,
			"profit":     profit,
		})
	}

	return c.JSON(fiber.Map{"profit_loss": entries})
}

// ListComplaints - Admin I and Super Admin
func (a *AnalyticsController) ListComplaints(c *fiber.Ctx) error {
	rows, err := a.db.Query(c.Context(), `
		SELECT c.id, c.description, c.refund_amount, c.status,
			COALESCE(p.title, 'Deleted Product') AS product_title,
			COALESCE(u.email, 'Deleted User') AS user_email,
			c.created_at, c.updated_at
		FROM complaints c
		LEFT JOIN products p ON p.id = c.product_id
		LEFT JOIN users u ON u.id = c.user_id
		ORDER BY c.created_at DESC LIMIT 50
	`)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "query failed")
	}
	defer rows.Close()

	complaints := make([]fiber.Map, 0)
	for rows.Next() {
		var id, description, status, productTitle, userEmail string
		var refundAmount float64
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &description, &refundAmount, &status, &productTitle, &userEmail, &createdAt, &updatedAt); err != nil {
			continue
		}
		complaints = append(complaints, fiber.Map{
			"id":            id,
			"description":   description,
			"refund_amount": refundAmount,
			"status":        status,
			"product_title": productTitle,
			"user_email":    userEmail,
			"created_at":    createdAt,
			"updated_at":    updatedAt,
		})
	}
	return c.JSON(fiber.Map{"complaints": complaints})
}

// CreateComplaint - Admin I or Super Admin logs a complaint/refund
func (a *AnalyticsController) CreateComplaint(c *fiber.Ctx) error {
	adminID, _ := c.Locals("admin_id").(string)

	var req struct {
		OrderID      string  `json:"order_id"`
		ProductID    string  `json:"product_id"`
		UserID       string  `json:"user_id"`
		Description  string  `json:"description"`
		RefundAmount float64 `json:"refund_amount"`
	}
	if err := c.BodyParser(&req); err != nil || req.Description == "" {
		return fiber.NewError(fiber.StatusBadRequest, "description required")
	}

	var complaintID string
	err := a.db.QueryRow(c.Context(), `
		INSERT INTO complaints(order_id, product_id, user_id, admin_id, description, refund_amount)
		VALUES (NULLIF($1, ''), NULLIF($2, ''), NULLIF($3, ''), $4, $5, $6)
		RETURNING id
	`, req.OrderID, req.ProductID, req.UserID, adminID, req.Description, req.RefundAmount).Scan(&complaintID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to create")
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"complaint_id": complaintID,
		"message":      "Complaint logged",
	})
}

// ResolveComplaint - Mark as resolved
func (a *AnalyticsController) ResolveComplaint(c *fiber.Ctx) error {
	complaintID := c.Params("complaint_id")

	tag, err := a.db.Exec(c.Context(), `
		UPDATE complaints SET status = 'resolved', updated_at = now()
		WHERE id = $1 AND status = 'unresolved'
	`, complaintID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "update failed")
	}
	if tag.RowsAffected() == 0 {
		return fiber.NewError(fiber.StatusNotFound, "complaint not found or already resolved")
	}
	return c.JSON(fiber.Map{"message": "Complaint resolved"})
}
