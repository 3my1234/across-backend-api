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
	"log"
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
	rewards    *RewardService
}

func NewPaymentController(db *pgxpool.Pool, cfg config.Config) *PaymentController {
	return &PaymentController{
		db:      db,
		cfg:     cfg,
		rewards: NewRewardService(db),
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
	var userID string
	var total float64
	if err := p.db.QueryRow(c.Context(), `SELECT user_id, total_amount FROM orders WHERE id = $1`, orderID).Scan(&userID, &total); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "payment settled but post-payment processing failed")
	}
	_ = CreateNotificationOnce(c.Context(), p.db, userID, orderID, nil, "payment_received",
		"Payment confirmed", fmt.Sprintf("Your payment of NGN %.2f has been received. Your order is being processed.", total),
		map[string]any{"amount": total, "currency": "NGN"}, "payment-received:"+orderID)
	if _, _, err := p.rewards.AwardPurchase(c.Context(), userID, orderID, total); err != nil {
		log.Printf("purchase reward failed order_id=%s: %v", orderID, err)
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
	var countryCode, operationalTimezone, batchDate string
	if err := tx.QueryRow(ctx, `
		SELECT c.country_code, tz.effective_timezone,
			(now() AT TIME ZONE tz.effective_timezone)::date::text
		FROM countries_config c
		CROSS JOIN LATERAL (
			SELECT CASE
				WHEN c.country_code = 'NG' AND c.operational_timezone = 'UTC'
					THEN 'Africa/Lagos'
				ELSE c.operational_timezone
			END AS effective_timezone
		) tz
		WHERE c.id = $1 AND c.is_active = true
	`, countryID).Scan(&countryCode, &operationalTimezone, &batchDate); err != nil {
		return "", "", err
	}

	routeKey := strings.ToUpper(strings.TrimSpace(countryCode))
	if routeKey == "NG" {
		routeKey = "LOS"
	}
	transportMode := "air"
	lockKey := fmt.Sprintf("daily-batch:%s:%s:%s:%s", countryID, batchDate, routeKey, transportMode)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return "", "", err
	}

	var batchID, batchCode string
	err := tx.QueryRow(ctx, `
		SELECT id, batch_code
		FROM order_batches
		WHERE country_id = $1
		  AND batch_date = $2::date
		  AND route_key = $3
		  AND transport_mode = $4
		  AND status = 'collecting_funds'::batch_status
		  AND membership_locked = false
		ORDER BY batch_sequence DESC
		LIMIT 1
		FOR UPDATE
	`, countryID, batchDate, routeKey, transportMode).Scan(&batchID, &batchCode)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", "", err
	}

	if errors.Is(err, pgx.ErrNoRows) {
		var sequence int
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(MAX(batch_sequence), 0) + 1
			FROM order_batches
			WHERE country_id = $1
			  AND batch_date = $2::date
			  AND route_key = $3
			  AND transport_mode = $4
		`, countryID, batchDate, routeKey, transportMode).Scan(&sequence); err != nil {
			return "", "", err
		}
		batchCode = fmt.Sprintf("%s-%s-%s-%s-%02d",
			strings.ToUpper(countryCode),
			routeKey,
			strings.ToUpper(transportMode),
			strings.ReplaceAll(batchDate, "-", ""),
			sequence,
		)
		if err := tx.QueryRow(ctx, `
			INSERT INTO order_batches(
				batch_code, country_id, batch_date, status, transport_mode, route_key,
				batch_sequence, total_ngn_collected, current_location, notes, opened_at,
				membership_locked
			)
			VALUES (
				$1, $2, $3::date, 'collecting_funds', $4, $5,
				$6, $7, 'Payment settlement', '', now(), false
			)
			RETURNING id
		`, batchCode, countryID, batchDate, transportMode, routeKey, sequence, orderAmount).Scan(&batchID); err != nil {
			return "", "", err
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE order_batches
			SET total_ngn_collected = total_ngn_collected + $2,
				updated_at = now(),
				version = version + 1
			WHERE id = $1
			  AND status = 'collecting_funds'::batch_status
			  AND membership_locked = false
		`, batchID, orderAmount); err != nil {
			return "", "", err
		}
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO batch_events(batch_id, event_type, status, location, notes, metadata)
		VALUES (
			$1, 'payment_confirmed', 'collecting_funds', 'Payment settlement', $2,
			jsonb_build_object(
				'operational_timezone', $3,
				'business_date', $4,
				'amount', $5
			)
		)
	`, batchID, promisedAt.Format(time.RFC3339), operationalTimezone, batchDate, orderAmount)
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
