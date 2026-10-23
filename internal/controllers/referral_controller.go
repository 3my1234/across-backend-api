package controllers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func normalizedReferralCode(code string) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return "", nil
	}
	if len(code) != 12 {
		return "", errors.New("Enter the 12-character referral code, or leave it blank")
	}
	for _, ch := range code {
		if !(ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
			return "", errors.New("Invalid referral code")
		}
	}
	return code, nil
}

// Called only inside new-account creation, before its transaction commits.
// Existing accounts cannot claim/reforge an attribution through a login call.
func attachReferralTx(ctx context.Context, tx pgx.Tx, userID, raw string) error {
	code, err := normalizedReferralCode(raw)
	if err != nil {
		return fiber.NewError(422, err.Error())
	}
	if code == "" {
		return nil
	}
	var referrer string
	err = tx.QueryRow(ctx, `SELECT codes.user_id::text FROM referral_codes codes JOIN users u ON u.id=codes.user_id WHERE codes.code=$1 AND u.is_active AND u.email_verified AND EXISTS(SELECT 1 FROM referral_program_settings WHERE id AND enabled)`, code).Scan(&referrer)
	if errors.Is(err, pgx.ErrNoRows) || referrer == userID {
		return fiber.NewError(422, "This referral code is unavailable. Check it or leave it blank")
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO user_referrals(referred_user_id,referrer_user_id) VALUES($1::uuid,$2::uuid)`, userID, referrer)
	return err
}

type ReferralController struct{ db *pgxpool.Pool }

func NewReferralController(db *pgxpool.Pool) *ReferralController { return &ReferralController{db: db} }
func (r *ReferralController) Validate(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	if err := r.available(c); err != nil {
		return err
	}
	code, err := normalizedReferralCode(c.Query("code"))
	if err != nil || code == "" {
		return c.Status(422).JSON(fiber.Map{"message": "Enter a valid 12-character referral code"})
	}
	var valid bool
	err = r.db.QueryRow(c.Context(), `SELECT EXISTS(SELECT 1 FROM referral_codes codes JOIN users u ON u.id=codes.user_id WHERE codes.code=$1 AND u.is_active AND u.email_verified)`, code).Scan(&valid)
	if err != nil {
		return fiber.NewError(503, "Referral validation is temporarily unavailable")
	}
	return c.JSON(fiber.Map{"valid": valid})
}
func (r *ReferralController) Mine(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	if err := r.available(c); err != nil {
		return err
	}
	userID, _ := c.Locals("user_id").(string)
	var code string
	// Lazy, idempotent issuance for existing accounts; random collision retries
	// never replace a code already shared by this account.
	for attempt := 0; attempt < 4; attempt++ {
		var bytes [6]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			return fiber.ErrInternalServerError
		}
		candidate := strings.ToUpper(hex.EncodeToString(bytes[:]))
		err := r.db.QueryRow(c.Context(), `INSERT INTO referral_codes(user_id,code) SELECT id,$2 FROM users WHERE id=$1::uuid AND is_active AND email_verified ON CONFLICT(user_id) DO UPDATE SET user_id=EXCLUDED.user_id RETURNING code`, userID, candidate).Scan(&code)
		if err == nil {
			break
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			continue
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.ErrForbidden
		}
		return fiber.NewError(503, "Referrals are temporarily unavailable")
	}
	if code == "" {
		return fiber.ErrInternalServerError
	}
	var pending, verified, paid int
	err := r.db.QueryRow(c.Context(), `SELECT count(*) FILTER(WHERE signup_awarded_at IS NULL),count(*) FILTER(WHERE signup_awarded_at IS NOT NULL),count(*) FILTER(WHERE provider_awarded_at IS NOT NULL) FROM user_referrals WHERE referrer_user_id=$1::uuid`, userID).Scan(&pending, &verified, &paid)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	return c.JSON(fiber.Map{"code": code, "pending_signups": pending, "verified_signups": verified, "paid_providers": paid, "earned_xp": verified*5 + paid*495, "signup_xp": 5, "paid_provider_total_xp": 500, "paid_provider_additional_xp": 495, "minimum_withdrawal_xp": 1000})
}

func (r *ReferralController) available(c *fiber.Ctx) error {
	var enabled bool
	if err := r.db.QueryRow(c.Context(), `SELECT enabled FROM referral_program_settings WHERE id=true`).Scan(&enabled); err != nil || !enabled {
		return fiber.NewError(503, "The referral program is temporarily unavailable")
	}
	return nil
}
