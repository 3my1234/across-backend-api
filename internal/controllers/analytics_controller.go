package controllers

import (
	"context"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
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
	ctx, cancel := context.WithTimeout(c.UserContext(), 5*time.Second)
	defer cancel()
	role, _ := c.Locals("admin_role").(string)
	isSuperAdmin := role == "super_admin"

	rows, err := a.db.Query(ctx, `
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

	if err := rows.Err(); err != nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "analytics unavailable")
	}
	rows.Close() // Release before acquiring a connection for totals, including a pool of one.
	// Totals
	var totalOrders int
	var totalRevenue float64
	err = a.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(order_count), 0), COALESCE(SUM(total_revenue), 0) FROM daily_sales
	`).Scan(&totalOrders, &totalRevenue)
	if err != nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "analytics unavailable")
	}

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
		UserEmail    string  `json:"user_email"`
		Description  string  `json:"description"`
		RefundAmount float64 `json:"refund_amount"`
	}
	if err := c.BodyParser(&req); err != nil || strings.TrimSpace(req.Description) == "" {
		return fiber.NewError(fiber.StatusBadRequest, "description required")
	}
	for _, id := range []*string{&req.OrderID, &req.ProductID, &req.UserID} {
		*id = strings.TrimSpace(*id)
		if *id != "" {
			value, err := uuid.Parse(*id)
			if err != nil {
				return fiber.NewError(fiber.StatusBadRequest, "Order, product and customer IDs must be valid UUIDs.")
			}
			*id = value.String()
		}
	}
	if req.RefundAmount < 0 {
		return fiber.NewError(fiber.StatusBadRequest, "Refund amount cannot be negative.")
	}
	if email := strings.TrimSpace(req.UserEmail); email != "" {
		if len(email) > 254 {
			return fiber.NewError(fiber.StatusBadRequest, "Customer email is too long")
		}
		// Signup normalizes addresses. Match that form or the exact stored legacy
		// address using the existing email index; never choose between two users.
		var userID *string
		var matches int
		if err := a.db.QueryRow(c.Context(), `SELECT min(id::text),count(*) FROM users WHERE email=$1 OR email=$2`, email, strings.ToLower(email)).Scan(&userID, &matches); err != nil {
			return fiber.NewError(fiber.StatusServiceUnavailable, "Customer lookup unavailable. Please retry.")
		}
		if matches != 1 || userID == nil {
			return fiber.NewError(fiber.StatusBadRequest, "Use the exact email address of one existing customer account.")
		}
		if req.UserID != "" && req.UserID != *userID {
			return fiber.NewError(fiber.StatusBadRequest, "Customer ID and email do not match.")
		}
		req.UserID = *userID
	}

	var complaintID string
	err := a.db.QueryRow(c.Context(), `
		INSERT INTO complaints(order_id, product_id, user_id, admin_id, description, refund_amount)
		VALUES (NULLIF($1::text, '')::uuid, NULLIF($2::text, '')::uuid, NULLIF($3::text, '')::uuid, $4, $5, $6)
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
