package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"time"
)

// BuyerPaymentHistory reads immutable charge identities and their current
// lifecycle. It never initializes a charge or infers bank settlement from payment.
func (p *PaymentController) BuyerPaymentHistory(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	if _, err := uuid.Parse(userID); err != nil {
		return fiber.ErrUnauthorized
	}
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	var cursorID any
	if page.CursorTime != nil {
		if _, err := uuid.Parse(page.CursorID); err != nil {
			return fiber.NewError(400, "invalid cursor")
		}
		cursorID = page.CursorID
	}
	c.Set("Cache-Control", "private, no-store")
	rows, err := p.db.Query(c.Context(), `SELECT p.id::text,p.order_id::text,p.provider,p.provider_reference,
 COALESCE(p.provider_transaction_id,''),p.amount,p.currency_code,p.charged_amount,
 p.payment_status,p.refund_status,p.chargeback_status,p.settlement_status,
 p.created_at,p.paid_at,o.order_status::text,o.platform_fee,o.shipping_fee,
 COALESCE((to_jsonb(o)->>'xp_discount')::int,0)
 FROM payments p JOIN orders o ON o.id=p.order_id
 WHERE p.user_id=$1::uuid AND o.user_id=$1::uuid AND p.purpose='order'
 AND ($2::timestamptz IS NULL OR (p.created_at,p.id)<($2,$3::uuid))
 ORDER BY p.created_at DESC,p.id DESC LIMIT $4`, userID, page.CursorTime, cursorID, page.Limit+1)
	if err != nil {
		return fiber.NewError(500, "Payment history is temporarily unavailable")
	}
	defer rows.Close()
	payments := make([]fiber.Map, 0, page.Limit+1)
	for rows.Next() {
		var id, orderID, provider, reference, transactionID, currency, status, refund, chargeback, settlement, orderStatus string
		var amount, fee, delivery float64
		var charged *float64
		var created time.Time
		var paid *time.Time
		var xp int
		if err = rows.Scan(&id, &orderID, &provider, &reference, &transactionID, &amount, &currency, &charged, &status, &refund, &chargeback, &settlement, &created, &paid, &orderStatus, &fee, &delivery, &xp); err != nil {
			return fiber.NewError(500, "Payment history is temporarily unavailable")
		}
		payments = append(payments, fiber.Map{"id": id, "order_id": orderID, "provider": provider, "reference": reference, "transaction_id": transactionID, "amount": amount, "currency": currency, "charged_amount": charged, "payment_status": status, "refund_status": refund, "chargeback_status": chargeback, "seller_settlement_status": settlement, "created_at": created, "paid_at": paid, "order_status": orderStatus, "service_fee": fee, "delivery_fee": delivery, "xp_discount": xp})
	}
	if rows.Err() != nil {
		return fiber.NewError(500, "Payment history is temporarily unavailable")
	}
	cursor := ""
	if len(payments) > page.Limit {
		payments = payments[:page.Limit]
		last := payments[len(payments)-1]
		cursor = encodeAdminCursor(last["created_at"].(time.Time), last["id"].(string))
	}
	return c.JSON(fiber.Map{"payments": payments, "next_cursor": cursor, "has_more": cursor != ""})
}
