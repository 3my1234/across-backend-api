package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EscrowController struct {
	db *pgxpool.Pool
}

func NewEscrowController(db *pgxpool.Pool) *EscrowController {
	return &EscrowController{db: db}
}

func (e *EscrowController) ConfirmReceipt(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	orderID := c.Params("order_id")

	tx, err := e.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())

	tag, err := tx.Exec(c.Context(), `
		UPDATE escrow_ledger el
		SET escrow_status = 'released', released_at = now(), updated_at = now()
		FROM orders o
		WHERE el.order_id = o.id
		  AND o.id = $1
		  AND o.user_id = $2
		  AND o.order_status = 'Delivered'
		  AND el.dispute_status = 'none'
	`, orderID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fiber.NewError(fiber.StatusConflict, "receipt cannot be confirmed")
	}

	_, err = tx.Exec(c.Context(), `
		UPDATE orders
		SET order_status = 'Completed', updated_at = now()
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

func (e *EscrowController) OpenDispute(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	orderID := c.Params("order_id")
	var req struct {
		Reason    string   `json:"reason"`
		MediaURLs []string `json:"media_urls"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}

	tx, err := e.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())

	tag, err := tx.Exec(c.Context(), `
		UPDATE escrow_ledger el
		SET escrow_status = 'frozen', dispute_status = 'active', updated_at = now()
		FROM orders o
		WHERE el.order_id = o.id
		  AND o.id = $1
		  AND o.user_id = $2
		  AND el.escrow_status = 'held_in_escrow'
	`, orderID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fiber.NewError(fiber.StatusConflict, "dispute cannot be opened")
	}

	_, err = tx.Exec(c.Context(), `
		UPDATE orders
		SET order_status = 'Disputed', updated_at = now()
		WHERE id = $1
	`, orderID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(c.Context(), `
		INSERT INTO admin_audit_logs(action, entity_type, entity_id, priority, metadata)
		VALUES ('buyer_opened_dispute', 'order', $1, 'high',
			jsonb_build_object('reason', $2, 'media_urls', $3::text[]))
	`, orderID, req.Reason, req.MediaURLs)
	if err != nil {
		return err
	}
	if err := tx.Commit(c.Context()); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
