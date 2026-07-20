package controllers

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"time"

	"across/backend/internal/auth"
	"across/backend/internal/config"
	"across/backend/internal/services"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type AuthController struct {
	db    *pgxpool.Pool
	cfg   config.Config
	email *services.EmailService
}

func NewAuthController(db *pgxpool.Pool, cfg config.Config) *AuthController {
	return &AuthController{db: db, cfg: cfg, email: services.NewEmailService(cfg)}
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
	verificationToken, err := newVerificationToken()
	if err != nil {
		return err
	}
	expiresAt := time.Now().Add(24 * time.Hour)

	var userID string
	err = a.db.QueryRow(c.Context(), `
		INSERT INTO users(country_id, email, phone, password_hash, full_name, is_active, email_verified, verification_token, verification_token_expires_at, verification_sent_at, verification_resend_count)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, false, false, $6, $7, now(), 0)
		RETURNING id
	`, countryID, req.Email, strings.TrimSpace(req.Phone), string(hash), req.FullName, verificationToken, expiresAt).Scan(&userID)
	if err != nil {
		return fiber.NewError(fiber.StatusConflict, "account already exists")
	}

	if err := a.sendVerificationEmail(c, userID, req.Email, req.FullName, verificationToken); err != nil {
		return err
	}
	_ = CreateNotification(c.Context(), a.db, userID, "", nil, "order_confirmed", "Verify your email",
		"Please verify your email address to activate your account.", map[string]any{"verification_required": true})

	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"message":                     "signup created. verify your email to activate your account",
		"requires_email_verification": true,
		"user_id":                     userID,
	})
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
	var emailVerified bool
	err := a.db.QueryRow(c.Context(), `
		SELECT id, password_hash, email_verified
		FROM users
		WHERE email = $1 AND is_active = true
	`, email).Scan(&userID, &hash, &emailVerified)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "invalid email or password")
	}
	if !emailVerified {
		return fiber.NewError(fiber.StatusForbidden, "please verify your email before signing in")
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
		INSERT INTO users(country_id, email, password_hash, full_name, is_active, email_verified)
		VALUES ($1, $2, 'gmail-oauth-placeholder', $3, true, true)
		ON CONFLICT (email) DO UPDATE SET full_name = EXCLUDED.full_name, is_active = true, email_verified = true, updated_at = now()
		RETURNING id
	`, countryID, req.Email, req.FullName).Scan(&userID)
	if err != nil {
		return err
	}
	_ = a.email.SendWelcomeEmail(req.Email, req.FullName)
	_ = CreateNotification(c.Context(), a.db, userID, "", nil, "order_confirmed", "Welcome to Atlantic Express!",
		"Thank you for joining ATLANTIC SHANSU LOGISTICS LIMITED. Start shopping for quality products from China!", nil)
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
		INSERT INTO users(country_id, email, password_hash, full_name, is_active, email_verified)
		VALUES ($1, $2, 'privy-google-oauth', $3, true, true)
		ON CONFLICT (email) DO UPDATE SET full_name = EXCLUDED.full_name, is_active = true, email_verified = true, updated_at = now()
		RETURNING id
	`, countryID, email, name).Scan(&userID)
	if err != nil {
		return err
	}
	_ = a.email.SendWelcomeEmail(email, name)
	_ = CreateNotification(c.Context(), a.db, userID, "", nil, "order_confirmed", "Welcome to Atlantic Express!",
		"Thank you for joining ATLANTIC SHANSU LOGISTICS LIMITED. Start shopping for quality products from China!", nil)
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

func (a *AuthController) VerifyEmail(c *fiber.Ctx) error {
	token := strings.TrimSpace(c.Query("token"))
	if token == "" {
		return fiber.NewError(fiber.StatusBadRequest, "verification token required")
	}

	var userID, email, fullName string
	err := a.db.QueryRow(c.Context(), `
		UPDATE users
		SET email_verified = true,
			is_active = true,
			verification_token = NULL,
			verification_token_expires_at = NULL,
			verification_sent_at = NULL,
			verification_resend_count = 0,
			updated_at = now()
		WHERE verification_token = $1
		  AND verification_token_expires_at >= now()
		RETURNING id, email, full_name
	`, token).Scan(&userID, &email, &fullName)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid or expired verification token")
	}

	_ = a.email.SendWelcomeEmail(email, fullName)
	_ = CreateNotification(c.Context(), a.db, userID, "", nil, "order_confirmed", "Welcome to Atlantic Express!",
		"Your email has been verified. Your account is now active.", nil)

	return c.JSON(fiber.Map{
		"message": "email verified successfully",
		"user_id": userID,
	})
}

func (a *AuthController) ResendVerification(c *fiber.Ctx) error {
	var req struct {
		Email string `json:"email"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" {
		return fiber.NewError(fiber.StatusBadRequest, "email required")
	}

	var userID, fullName string
	var emailVerified bool
	var sentAt *time.Time
	var resendCount int
	err := a.db.QueryRow(c.Context(), `
		SELECT id, full_name, email_verified, verification_sent_at, verification_resend_count
		FROM users
		WHERE email = $1
	`, email).Scan(&userID, &fullName, &emailVerified, &sentAt, &resendCount)
	if err != nil {
		return c.JSON(fiber.Map{"message": "if the email exists, a verification message will be sent"})
	}
	if emailVerified {
		return c.JSON(fiber.Map{"message": "account is already verified"})
	}
	if sentAt != nil && time.Since(*sentAt) < 5*time.Minute {
		return fiber.NewError(fiber.StatusTooManyRequests, "please wait before requesting another verification email")
	}

	verificationToken, err := newVerificationToken()
	if err != nil {
		return err
	}
	expiresAt := time.Now().Add(24 * time.Hour)
	_, err = a.db.Exec(c.Context(), `
		UPDATE users
		SET verification_token = $2,
			verification_token_expires_at = $3,
			verification_sent_at = now(),
			verification_resend_count = $4,
			updated_at = now()
		WHERE id = $1
	`, userID, verificationToken, expiresAt, resendCount+1)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "could not resend verification")
	}
	if err := a.sendVerificationEmail(c, userID, email, fullName, verificationToken); err != nil {
		return err
	}
	_ = CreateNotification(c.Context(), a.db, userID, "", nil, "order_confirmed", "Verification email resent",
		"Please check your inbox to activate your account.", map[string]any{"verification_required": true})
	return c.JSON(fiber.Map{"message": "verification email sent"})
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

func (a *AuthController) sendVerificationEmail(c *fiber.Ctx, userID, toEmail, toName, token string) error {
	baseURL := strings.TrimRight(strings.TrimSpace(a.cfg.PublicBaseURL), "/")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	link := baseURL + "/api/v1/auth/verify-email?token=" + token
	subject := "Verify your Atlantic Express email"
	body := "<p>Hello " + toName + ",</p><p>Click the link below to verify your email and activate your account:</p><p><a href=\"" + link + "\">Verify email</a></p><p>If you did not create this account, ignore this email.</p>"
	if err := a.email.SendHTML(toEmail, subject, body); err != nil {
		return err
	}
	_ = CreateNotification(c.Context(), a.db, userID, "", nil, "order_confirmed", "Verify your email", "Check your inbox to verify your account.", map[string]any{"verification_link": link})
	return nil
}

func newVerificationToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
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
