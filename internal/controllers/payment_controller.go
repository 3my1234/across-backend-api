package controllers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"across/backend/internal/config"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PaymentController struct {
	db         *pgxpool.Pool
	cfg        config.Config
	httpClient *http.Client
}

func NewPaymentController(db *pgxpool.Pool, cfg config.Config) *PaymentController {
	return &PaymentController{
		db:  db,
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 12 * time.Second,
		},
	}
}

type tokenizedChargeRequest struct {
	OrderID  string `json:"order_id"`
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type flutterwaveCheckoutRequest struct {
	OrderID     string `json:"order_id"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	RedirectURL string `json:"redirect_url"`
}

func (p *PaymentController) TokenizedCharge(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	var req tokenizedChargeRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}

	var email, token string
	err := p.db.QueryRow(c.Context(), `
		SELECT email, flutterwave_token
		FROM users
		WHERE id = $1 AND is_active = true
	`, userID).Scan(&email, &token)
	if err != nil || token == "" {
		return fiber.NewError(fiber.StatusBadRequest, "saved payment token unavailable")
	}

	var orderAmount float64
	var orderCurrency, orderStatus string
	err = p.db.QueryRow(c.Context(), `SELECT total_amount, currency_code, order_status::text FROM orders WHERE id = $1 AND user_id = $2`, req.OrderID, userID).Scan(&orderAmount, &orderCurrency, &orderStatus)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "payable order not found")
	}
	if orderStatus != "Pending" {
		return fiber.NewError(fiber.StatusConflict, "order is not payable")
	}

	txRef := "ACROSS-" + req.OrderID + "-" + time.Now().UTC().Format("20060102150405")
	if p.mockPaymentsEnabled() {
		if err := p.settleOrderPayment(c.Context(), req.OrderID, txRef, "local-dev", orderAmount, orderCurrency); err != nil {
			return fiber.NewError(fiber.StatusConflict, err.Error())
		}
		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
			"tx_ref":  txRef,
			"gateway": "flutterwave",
			"mocked":  true,
		})
	}
	payload := map[string]any{
		"token":    token,
		"currency": orderCurrency,
		"amount":   orderAmount,
		"email":    email,
		"tx_ref":   txRef,
	}
	body, _ := json.Marshal(payload)
	httpReq, err := http.NewRequestWithContext(c.Context(), http.MethodPost, "https://api.flutterwave.com/v3/tokenized-charges", bytes.NewReader(body))
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "charge request failed")
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.FlutterwaveSecretKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "payment gateway unavailable")
	}
	defer resp.Body.Close()

	var gatewayResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&gatewayResp); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "invalid payment gateway response")
	}
	if resp.StatusCode >= 300 {
		return c.Status(fiber.StatusBadGateway).JSON(gatewayResp)
	}

	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"tx_ref":   txRef,
		"gateway":  "flutterwave",
		"response": gatewayResp,
	})
}

func (p *PaymentController) FlutterwaveCheckout(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	var req flutterwaveCheckoutRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	if strings.TrimSpace(req.OrderID) == "" || strings.TrimSpace(req.Amount) == "" || strings.TrimSpace(req.Currency) == "" {
		return fiber.NewError(fiber.StatusBadRequest, "order_id, amount, and currency are required")
	}

	var email, fullName string
	err := p.db.QueryRow(c.Context(), `
		SELECT email, full_name
		FROM users
		WHERE id = $1 AND is_active = true
	`, userID).Scan(&email, &fullName)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "user not found")
	}

	var orderAmount float64
	var orderCurrency, orderStatus string
	err = p.db.QueryRow(c.Context(), `SELECT total_amount, currency_code, order_status::text FROM orders WHERE id = $1 AND user_id = $2`, req.OrderID, userID).Scan(&orderAmount, &orderCurrency, &orderStatus)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "payable order not found")
	}
	if orderStatus != "Pending" {
		return fiber.NewError(fiber.StatusConflict, "order is not payable")
	}

	txRef := "ACROSS-" + req.OrderID + "-" + time.Now().UTC().Format("20060102150405")
	redirectURL := strings.TrimSpace(req.RedirectURL)
	if redirectURL == "" {
		redirectURL = "across://payments/flutterwave"
	}
	if p.mockPaymentsEnabled() {
		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
			"tx_ref":        txRef,
			"gateway":       "flutterwave",
			"mocked":        true,
			"redirect_url":  redirectURL,
			"checkout_link": "https://www.flutterwave.com",
		})
	}

	payload := map[string]any{
		"tx_ref":          txRef,
		"amount":          orderAmount,
		"currency":        strings.ToUpper(orderCurrency),
		"redirect_url":    redirectURL,
		"payment_options": "card,banktransfer,ussd",
		"customer": map[string]any{
			"email": email,
			"name":  strings.TrimSpace(fullName),
		},
		"customizations": map[string]any{
			"title":       "Atlantic Express Checkout",
			"description": "Pay securely with Flutterwave",
		},
	}
	body, _ := json.Marshal(payload)
	httpReq, err := http.NewRequestWithContext(c.Context(), http.MethodPost, "https://api.flutterwave.com/v3/payments", bytes.NewReader(body))
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "checkout request failed")
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.FlutterwaveSecretKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "payment gateway unavailable")
	}
	defer resp.Body.Close()

	var gatewayResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&gatewayResp); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "invalid payment gateway response")
	}
	if resp.StatusCode >= 300 {
		// Extract actual Flutterwave error message
		errMsg := "payment declined by gateway"
		if msg, ok := gatewayResp["message"].(string); ok && msg != "" {
			errMsg = msg
		} else if data, ok := gatewayResp["data"].(map[string]any); ok {
			if msg, ok := data["message"].(string); ok && msg != "" {
				errMsg = msg
			}
		}
		return fiber.NewError(fiber.StatusBadGateway, errMsg)
	}

	link := ""
	if data, ok := gatewayResp["data"].(map[string]any); ok {
		if rawLink, ok := data["link"].(string); ok {
			link = rawLink
		}
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"tx_ref":        txRef,
		"gateway":       "flutterwave",
		"checkout_link": link,
		"redirect_url":  redirectURL,
		"response":      gatewayResp,
	})
}

func (p *PaymentController) mockPaymentsEnabled() bool {
	// Mock payments only when we have NO live key configured
	// Live keys start with "FLWSECK-" followed by the actual hash, not "FLWSECK_TEST_xxx"
	hasLiveKey := p.cfg.FlutterwaveSecretKey != "" &&
		!strings.HasPrefix(p.cfg.FlutterwaveSecretKey, "FLWSECK_TEST") &&
		p.cfg.FlutterwaveSecretKey != "your-key"
	return !hasLiveKey
}

type flutterwaveWebhook struct {
	Event string `json:"event"`
	Data  struct {
		ID       any    `json:"id"`
		TxRef    string `json:"tx_ref"`
		Status   string `json:"status"`
		Amount   any    `json:"amount"`
		Currency string `json:"currency"`
	} `json:"data"`
}

func (p *PaymentController) FlutterwaveWebhook(c *fiber.Ctx) error {
	raw := c.BodyRaw()
	if !p.validWebhook(raw, c.Get("verif-hash")) {
		return fiber.NewError(fiber.StatusUnauthorized, "invalid webhook signature")
	}

	var event flutterwaveWebhook
	if err := json.Unmarshal(raw, &event); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid webhook")
	}
	if event.Event != "charge.completed" || event.Data.Status != "successful" {
		return c.SendStatus(fiber.StatusAccepted)
	}

	orderID, err := parseOrderID(event.Data.TxRef)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid tx_ref")
	}

	paidAmount, err := amountValue(event.Data.Amount)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payment amount")
	}
	if err := p.settleOrderPayment(c.Context(), orderID, event.Data.TxRef, stringify(event.Data.ID), paidAmount, event.Data.Currency); err != nil {
		return fiber.NewError(fiber.StatusConflict, err.Error())
	}
	var userID, amount string
	_ = p.db.QueryRow(c.Context(), `SELECT user_id, total_amount::text FROM orders WHERE id = $1`, orderID).Scan(&userID, &amount)
	if userID != "" {
		_ = CreateNotification(c.Context(), p.db, userID, orderID, nil, "payment_received",
			"Payment Confirmed", "Your payment of NGN "+amount+" has been received. Your order is being processed.", nil)
		tag, awardErr := p.db.Exec(c.Context(), `
			INSERT INTO xp_transactions(user_id, amount, reason, reference_id)
			SELECT $1, 50, 'purchase', 'purchase-' || $2
			WHERE NOT EXISTS (
				SELECT 1 FROM xp_transactions WHERE user_id = $1 AND reference_id = 'purchase-' || $2
			)
		`, userID, orderID)
		if awardErr == nil && tag.RowsAffected() > 0 {
			_ = CreateNotification(c.Context(), p.db, userID, orderID, nil, "xp_earned",
				"Purchase XP earned", "You earned 50 XP for your purchase!", map[string]any{"xp": 50})
		}
	}
	return c.SendStatus(fiber.StatusOK)
}

func (p *PaymentController) validWebhook(raw []byte, signature string) bool {
	if p.cfg.FlutterwaveWebhookSecret == "" {
		return false
	}
	if signature == p.cfg.FlutterwaveWebhookSecret {
		return true
	}
	mac := hmac.New(sha256.New, []byte(p.cfg.FlutterwaveWebhookSecret))
	_, _ = mac.Write(raw)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

func (p *PaymentController) settleOrderPayment(ctx context.Context, orderID, txRef, gatewayID string, paidAmount float64, paidCurrency string) error {
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var countryID, currencyCode, orderStatus, existingTxRef string
	var orderAmount float64
	var promisedAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT country_id, currency_code, total_amount,
			COALESCE(delivery_promised_at, now() + interval '14 days'),
			order_status::text, COALESCE(flutterwave_tx_ref, '')
		FROM orders
		WHERE id = $1
		FOR UPDATE
	`, orderID).Scan(&countryID, &currencyCode, &orderAmount, &promisedAt, &orderStatus, &existingTxRef); err != nil {
		return err
	}
	if !strings.EqualFold(currencyCode, strings.TrimSpace(paidCurrency)) || paidAmount+0.001 < orderAmount {
		return errors.New("payment amount or currency does not match order")
	}
	if orderStatus != "Pending" {
		if existingTxRef == txRef && (orderStatus == "Paid" || orderStatus == "Shipped" || orderStatus == "Delivered" || orderStatus == "Completed") {
			return tx.Commit(ctx)
		}
		return errors.New("order is not payable")
	}

	batchID, batchCode, err := p.ensureDailyBatch(ctx, tx, countryID, promisedAt, orderAmount)
	if err != nil {
		return err
	}
	packageLabel := fmt.Sprintf("%s-%s", batchCode, shortOrderLabel(orderID))

	tag, err := tx.Exec(ctx, `
		UPDATE orders
		SET order_status = 'Paid',
			batch_id = $2,
			package_label = COALESCE(NULLIF(package_label, ''), $3),
			flutterwave_tx_ref = $4,
			flutterwave_transaction_id = $5,
			paid_at = COALESCE(paid_at, now()),
			updated_at = now()
		WHERE id = $1 AND order_status = 'Pending'
	`, orderID, batchID, packageLabel, txRef, gatewayID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("order is not payable")
	}

	return tx.Commit(ctx)
}
func (p *PaymentController) ensureDailyBatch(ctx context.Context, tx pgx.Tx, countryID string, promisedAt time.Time, orderAmount float64) (string, string, error) {
	batchDate := time.Now().UTC().Format("2006-01-02")
	batchCode := fmt.Sprintf("BATCH-%s", strings.ReplaceAll(batchDate, "-", ""))

	var batchID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_batches(batch_code, country_id, batch_date, status, total_ngn_collected, current_location, notes)
		VALUES ($1, $2, $3::date, 'collecting_funds', $4, 'Payment settlement', '')
		ON CONFLICT (country_id, batch_date)
		DO UPDATE SET total_ngn_collected = order_batches.total_ngn_collected + EXCLUDED.total_ngn_collected,
			updated_at = now()
		RETURNING id
	`, batchCode, countryID, batchDate, orderAmount).Scan(&batchID); err != nil {
		return "", "", err
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO batch_events(batch_id, event_type, status, location, notes)
		VALUES ($1, 'payment_confirmed', 'collecting_funds', 'Payment settlement', $2)
	`, batchID, promisedAt.Format(time.RFC3339))
	if err != nil {
		return "", "", err
	}

	return batchID, batchCode, nil
}

func shortOrderLabel(orderID string) string {
	clean := strings.ReplaceAll(strings.ToUpper(orderID), "-", "")
	if len(clean) > 8 {
		return clean[:8]
	}
	return clean
}

func parseOrderID(txRef string) (string, error) {
	const prefix = "ACROSS-"
	if !strings.HasPrefix(txRef, prefix) {
		return "", errors.New("missing prefix")
	}
	rest := strings.TrimPrefix(txRef, prefix)
	if len(rest) < 37 || rest[36] != '-' {
		return "", errors.New("invalid order reference")
	}
	orderID := rest[:36]
	if _, err := uuid.Parse(orderID); err != nil {
		return "", errors.New("invalid order id")
	}
	return orderID, nil
}
func stringify(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func parseAmount(amount string) (float64, error) {
	return strconv.ParseFloat(amount, 64)
}

func amountValue(amount any) (float64, error) {
	switch value := amount.(type) {
	case float64:
		return value, nil
	case string:
		return parseAmount(value)
	default:
		return parseAmount(fmt.Sprint(value))
	}
}
