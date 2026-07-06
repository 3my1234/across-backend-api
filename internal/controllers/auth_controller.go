package controllers

import (
	"strings"
	"time"

	"across/backend/internal/auth"
	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type AuthController struct {
	db  *pgxpool.Pool
	cfg config.Config
}

func NewAuthController(db *pgxpool.Pool, cfg config.Config) *AuthController {
	return &AuthController{db: db, cfg: cfg}
}

func (a *AuthController) Signup(c *fiber.Ctx) error {
	var req struct {
		FullName string `json:"full_name"`
		Email    string `json:"email"`
		Phone    string `json:"phone"`
		Password string `json:"password"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.FullName = strings.TrimSpace(req.FullName)
	if req.FullName == "" || req.Email == "" || len(req.Password) < 8 {
		return fiber.NewError(fiber.StatusBadRequest, "name, email, and 8+ character password are required")
	}

	countryID, err := ensureCountry(c, a.db)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	var userID string
	err = a.db.QueryRow(c.Context(), `
		INSERT INTO users(country_id, email, phone, password_hash, full_name)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5)
		RETURNING id
	`, countryID, req.Email, strings.TrimSpace(req.Phone), string(hash), req.FullName).Scan(&userID)
	if err != nil {
		return fiber.NewError(fiber.StatusConflict, "account already exists")
	}
	return a.respondSession(c, userID)
}

func (a *AuthController) Login(c *fiber.Ctx) error {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))

	var userID, hash string
	err := a.db.QueryRow(c.Context(), `
		SELECT id, password_hash
		FROM users
		WHERE email = $1 AND is_active = true
	`, email).Scan(&userID, &hash)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "invalid email or password")
	}
	return a.respondSession(c, userID)
}

func (a *AuthController) Gmail(c *fiber.Ctx) error {
	var req struct {
		Email    string `json:"email"`
		FullName string `json:"full_name"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.FullName = strings.TrimSpace(req.FullName)
	if req.Email == "" || !strings.Contains(req.Email, "@gmail.") && !strings.HasSuffix(req.Email, "@gmail.com") {
		return fiber.NewError(fiber.StatusBadRequest, "valid gmail address required")
	}
	if req.FullName == "" {
		req.FullName = "Across Buyer"
	}

	countryID, err := ensureCountry(c, a.db)
	if err != nil {
		return err
	}
	var userID string
	err = a.db.QueryRow(c.Context(), `
		INSERT INTO users(country_id, email, password_hash, full_name)
		VALUES ($1, $2, 'gmail-oauth-placeholder', $3)
		ON CONFLICT (email) DO UPDATE SET full_name = EXCLUDED.full_name, updated_at = now()
		RETURNING id
	`, countryID, req.Email, req.FullName).Scan(&userID)
	if err != nil {
		return err
	}
	return a.respondSession(c, userID)
}

func (a *AuthController) VerifyPrivy(c *fiber.Ctx) error {
	var req struct {
		PrivyToken    string `json:"privy_token"`
		PrivyTokenAlt string `json:"privyToken"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	token := strings.TrimSpace(req.PrivyToken)
	if token == "" {
		token = strings.TrimSpace(req.PrivyTokenAlt)
	}
	if token == "" {
		return fiber.NewError(fiber.StatusBadRequest, "privy token required")
	}

	email, name, err := a.verifyPrivyToken(c, token)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, err.Error())
	}

	countryID, err := ensureCountry(c, a.db)
	if err != nil {
		return err
	}
	var userID string
	err = a.db.QueryRow(c.Context(), `
		INSERT INTO users(country_id, email, password_hash, full_name)
		VALUES ($1, $2, 'privy-google-oauth', $3)
		ON CONFLICT (email) DO UPDATE SET full_name = EXCLUDED.full_name, updated_at = now()
		RETURNING id
	`, countryID, email, name).Scan(&userID)
	if err != nil {
		return err
	}
	return a.respondSession(c, userID)
}

func (a *AuthController) verifyPrivyToken(c *fiber.Ctx, token string) (string, string, error) {
	if a.cfg.PrivyVerificationMode == "local" {
		var req struct {
			Email    string `json:"email"`
			FullName string `json:"full_name"`
		}
		_ = c.BodyParser(&req)
		email := strings.ToLower(strings.TrimSpace(req.Email))
		if email == "" {
			email = "privy.local@across.dev"
		}
		name := strings.TrimSpace(req.FullName)
		if name == "" {
			name = "Across Google Buyer"
		}
		return email, name, nil
	}
	return "", "", fiber.NewError(fiber.StatusNotImplemented, "server-side Privy JWT verification is not configured")
}

func (a *AuthController) Session(c *fiber.Ctx) error {
	userID := c.Locals("user_id")
	return c.JSON(fiber.Map{"authenticated": true, "user_id": userID})
}

func (a *AuthController) respondSession(c *fiber.Ctx, userID string) error {
	token, expiresAt, err := auth.Sign(userID, a.cfg.JWTSecret, 24*30*time.Hour)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{
		"access_token": token,
		"expires_at":   expiresAt,
		"user_id":      userID,
	})
}

func ensureCountry(c *fiber.Ctx, db *pgxpool.Pool) (string, error) {
	var countryID string
	err := db.QueryRow(c.Context(), `
		INSERT INTO countries_config(country_code, currency_code, base_escrow_days, active_payment_gateways)
		VALUES ('NG', 'NGN', 14, ARRAY['flutterwave'])
		ON CONFLICT (country_code) DO UPDATE SET updated_at = now()
		RETURNING id
	`).Scan(&countryID)
	return countryID, err
}
