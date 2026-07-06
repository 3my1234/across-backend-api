package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AdminController struct {
	db *pgxpool.Pool
}

func NewAdminController(db *pgxpool.Pool) *AdminController {
	return &AdminController{db: db}
}

func (a *AdminController) PendingManifest(c *fiber.Ctx) error {
	rows, err := a.db.Query(c.Context(), `
		SELECT o.id, o.order_status, o.current_tracking_stage, oi.sku, oi.title, oi.quantity, lh.code
		FROM orders o
		JOIN order_items oi ON oi.order_id = o.id
		LEFT JOIN logistics_hubs lh ON lh.id = oi.origin_hub_id
		WHERE o.order_status IN ('Paid', 'Shipped')
		ORDER BY o.created_at ASC
		LIMIT 1000
	`)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "manifest unavailable")
	}
	defer rows.Close()

	items := make([]fiber.Map, 0)
	for rows.Next() {
		var orderID, status, stage, sku, title string
		var hubCode *string
		var qty int
		if err := rows.Scan(&orderID, &status, &stage, &sku, &title, &qty, &hubCode); err != nil {
			return err
		}
		items = append(items, fiber.Map{"order_id": orderID, "status": status, "stage": stage, "sku": sku, "title": title, "quantity": qty, "hub_code": hubCode})
	}
	return c.JSON(fiber.Map{"items": items})
}

func (a *AdminController) BatchScanTracking(c *fiber.Ctx) error {
	var req struct {
		OrderIDs []string `json:"order_ids"`
		Stage    string   `json:"stage"`
		HubID    *string  `json:"hub_id"`
		Barcode  *string  `json:"barcode"`
	}
	if err := c.BodyParser(&req); err != nil || len(req.OrderIDs) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	tx, err := a.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())
	for _, orderID := range req.OrderIDs {
		_, err := tx.Exec(c.Context(), `
			UPDATE orders
			SET current_tracking_stage = $2::tracking_stage,
				order_status = CASE WHEN $2 = 'Delivered' THEN 'Delivered' ELSE order_status END,
				updated_at = now()
			WHERE id = $1
		`, orderID, req.Stage)
		if err != nil {
			return err
		}
		_, err = tx.Exec(c.Context(), `
			INSERT INTO tracking_events(order_id, hub_id, stage, barcode)
			VALUES ($1, $2, $3::tracking_stage, $4)
		`, orderID, req.HubID, req.Stage, req.Barcode)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(c.Context()); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (a *AdminController) SettleEscrow(c *fiber.Ctx) error {
	var req struct {
		OrderIDs []string `json:"order_ids"`
	}
	if err := c.BodyParser(&req); err != nil || len(req.OrderIDs) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	tx, err := a.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())
	for _, orderID := range req.OrderIDs {
		tag, err := tx.Exec(c.Context(), `
		UPDATE escrow_ledger
		SET escrow_status = 'released', released_at = now(), updated_at = now()
		WHERE order_id = $1 AND dispute_status <> 'active'
	`, orderID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		_, err = tx.Exec(c.Context(), `
		UPDATE orders
		SET order_status = 'Completed', ready_for_manual_settlement = false, updated_at = now()
		WHERE id = $1
	`, orderID)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(c.Context()); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (a *AdminController) FreezeDispute(c *fiber.Ctx) error {
	orderID := c.Params("order_id")
	tx, err := a.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())
	_, err = tx.Exec(c.Context(), `
		UPDATE escrow_ledger
		SET escrow_status = 'frozen', dispute_status = 'active', updated_at = now()
		WHERE order_id = $1
	`, orderID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(c.Context(), `
		UPDATE orders
		SET order_status = 'Disputed', updated_at = now()
		WHERE id = $1
	`, orderID)
	if err != nil {
		return err
	}
	if err := tx.Commit(c.Context()); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
