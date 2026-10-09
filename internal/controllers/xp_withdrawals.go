package controllers

import (
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"regexp"
	"strings"
)

var withdrawalAccount = regexp.MustCompile(`^[0-9]{10}$`)

func (x *XPController) ListWithdrawals(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	return x.withdrawalList(c, userID)
}

func (x *XPController) AdminListWithdrawals(c *fiber.Ctx) error { return x.withdrawalList(c, "") }

func (x *XPController) withdrawalList(c *fiber.Ctx, userID string) error {
	rows, err := x.db.Query(c.Context(), `SELECT row_to_json(w) FROM
 (SELECT w.id,w.user_id,w.points,w.points AS amount_ngn,w.bank_name,w.account_number,w.account_name,
 w.status,w.payout_reference,w.admin_note,w.created_at,w.updated_at,u.email
 FROM xp_withdrawals w JOIN users u ON u.id=w.user_id
 WHERE ($1::text='' OR w.user_id::text=$1) ORDER BY w.created_at DESC LIMIT 100) w`, userID)
	if err != nil {
		return fiber.NewError(503, "Withdrawal history unavailable")
	}
	defer rows.Close()
	items := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		items = append(items, json.RawMessage(raw))
	}
	if err = rows.Err(); err != nil {
		return err
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"items": items, "minimum_xp": 1000, "ngn_per_xp": 1})
}

func (x *XPController) RequestWithdrawal(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	var req struct {
		RequestKey    string `json:"request_key"`
		Points        int    `json:"points"`
		BankName      string `json:"bank_name"`
		AccountNumber string `json:"account_number"`
		AccountName   string `json:"account_name"`
	}
	if c.BodyParser(&req) != nil {
		return fiber.NewError(400, "Invalid withdrawal request")
	}
	req.BankName = strings.TrimSpace(req.BankName)
	req.AccountName = strings.TrimSpace(req.AccountName)
	req.AccountNumber = strings.TrimSpace(req.AccountNumber)
	if _, err := uuid.Parse(req.RequestKey); err != nil {
		return fiber.NewError(400, "A valid request key is required")
	}
	if req.Points < 1000 || req.Points > 100000000 || len(req.BankName) < 2 || len(req.BankName) > 100 || len(req.AccountName) < 2 || len(req.AccountName) > 150 || !withdrawalAccount.MatchString(req.AccountNumber) {
		return fiber.NewError(400, "Enter at least 1,000 XP, your bank, account name and 10-digit Nigerian account number")
	}
	tx, err := x.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())
	if err = lockXPUser(c.Context(), tx, userID); err != nil {
		return err
	}
	// A retry must return the saved request, even after its balance was reserved.
	var existing string
	var same bool
	err = tx.QueryRow(c.Context(), `SELECT id::text,points=$3 AND bank_name=$4 AND account_number=$5 AND account_name=$6 FROM xp_withdrawals WHERE user_id=$1 AND request_key=$2`, userID, req.RequestKey, req.Points, req.BankName, req.AccountNumber, req.AccountName).Scan(&existing, &same)
	if err == nil {
		if !same {
			return fiber.NewError(409, "This request was already saved with different details. Refresh your withdrawal history.")
		}
		return c.JSON(fiber.Map{"id": existing, "already_requested": true})
	}
	if err != pgx.ErrNoRows {
		return err
	}
	var open bool
	if err = tx.QueryRow(c.Context(), `SELECT EXISTS(SELECT 1 FROM xp_withdrawals WHERE user_id=$1 AND status IN ('pending','processing'))`, userID).Scan(&open); err != nil {
		return err
	}
	if open {
		return fiber.NewError(409, "Your previous withdrawal is still being reviewed")
	}
	if err = releaseUnusedXP(c.Context(), tx, userID, false); err != nil {
		return err
	}
	available, _, err := availableXP(c.Context(), tx, userID)
	if err != nil {
		return err
	}
	if available < req.Points {
		return fiber.NewError(409, "You do not have enough available XP")
	}
	var id string
	err = tx.QueryRow(c.Context(), `INSERT INTO xp_withdrawals(user_id,request_key,points,bank_name,account_number,account_name) VALUES($1,$2,$3,$4,$5,$6) RETURNING id::text`, userID, req.RequestKey, req.Points, req.BankName, req.AccountNumber, req.AccountName).Scan(&id)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(c.Context(), `INSERT INTO xp_transactions(user_id,amount,reason,reference_id) VALUES($1,$2,'withdrawal_reserved',$3)`, userID, -req.Points, id); err != nil {
		return err
	}
	if err = tx.Commit(c.Context()); err != nil {
		return err
	}
	return c.Status(201).JSON(fiber.Map{"id": id, "status": "pending", "amount_ngn": req.Points, "message": "Request saved. Points are reserved while an admin reviews your bank details and arranges payment."})
}

func (x *XPController) AdminReviewWithdrawal(c *fiber.Ctx) error {
	id := c.Params("withdrawal_id")
	if _, err := uuid.Parse(id); err != nil {
		return fiber.ErrBadRequest
	}
	adminID, _ := c.Locals("admin_id").(string)
	var req struct {
		Status          string `json:"status"`
		PayoutReference string `json:"payout_reference"`
		Note            string `json:"note"`
	}
	if c.BodyParser(&req) != nil {
		return fiber.ErrBadRequest
	}
	req.PayoutReference = strings.TrimSpace(req.PayoutReference)
	req.Note = strings.TrimSpace(req.Note)
	if req.Status != "processing" && req.Status != "paid" && req.Status != "rejected" {
		return fiber.NewError(400, "Choose processing, paid or rejected")
	}
	if len(req.Note) > 1000 || len(req.PayoutReference) > 200 || (req.Status == "paid" && req.PayoutReference == "") || (req.Status == "rejected" && req.Note == "") {
		return fiber.NewError(400, "Paid requests need the bank transfer reference. Rejected requests need a reason.")
	}
	tx, err := x.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())
	var userID string
	if err = tx.QueryRow(c.Context(), `SELECT user_id::text FROM xp_withdrawals WHERE id=$1`, id).Scan(&userID); err != nil {
		return fiber.ErrNotFound
	}
	if err = lockXPUser(c.Context(), tx, userID); err != nil {
		return err
	}
	var status, reference string
	var points int
	if err = tx.QueryRow(c.Context(), `SELECT status,points,payout_reference FROM xp_withdrawals WHERE id=$1 FOR UPDATE`, id).Scan(&status, &points, &reference); err != nil {
		return err
	}
	if status == req.Status {
		if status == "paid" && reference != req.PayoutReference {
			return fiber.NewError(409, "This withdrawal was paid with a different reference")
		}
		return c.JSON(fiber.Map{"id": id, "status": status, "already_processed": true})
	}
	if status == "paid" || status == "rejected" {
		return fiber.NewError(409, "This withdrawal is already closed")
	}
	if req.Status == "paid" && status != "processing" {
		return fiber.NewError(409, "Mark the request processing before recording its completed bank transfer")
	}
	if req.Status == "rejected" {
		if _, err = tx.Exec(c.Context(), `INSERT INTO xp_transactions(user_id,amount,reason,reference_id) VALUES($1,$2,'withdrawal_released',$3) ON CONFLICT DO NOTHING`, userID, points, id); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(c.Context(), `UPDATE xp_withdrawals SET status=$2,payout_reference=$3,admin_note=$4,reviewed_by=$5::uuid,updated_at=now() WHERE id=$1`, id, req.Status, req.PayoutReference, req.Note, adminID); err != nil {
		return fiber.NewError(409, "Could not save this withdrawal. Check that its transfer reference has not already been used.")
	}
	if err = insertNotification(c.Context(), tx, userID, "", nil, "xp_withdrawal", "XP withdrawal update", "Your withdrawal is "+req.Status+". "+req.Note, map[string]any{"withdrawal_id": id, "status": req.Status, "amount_ngn": points}, "withdrawal:"+id+":"+req.Status); err != nil {
		return err
	}
	if err = tx.Commit(c.Context()); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"id": id, "status": req.Status})
}
