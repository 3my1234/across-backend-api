package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const flutterwaveProviderName = "flutterwave"

type paymentCheckoutInput struct {
	Reference      string
	Amount         float64
	Currency       string
	RedirectURL    string
	PaymentMethods []string
	PaymentPlanID  *int64
	Customer       map[string]any
	Title          string
	Description    string
	Metadata       map[string]any
}

type paymentCheckoutResult struct {
	CheckoutURL string
	Raw         map[string]any
}

type verifiedProviderPayment struct {
	TransactionID string
	Reference     string
	Status        string
	Amount        any
	Currency      string
}

type paymentProvider interface {
	Name() string
	InitializeCheckout(context.Context, paymentCheckoutInput) (paymentCheckoutResult, error)
	VerifyPayment(context.Context, string, string) (verifiedProviderPayment, error)
}

type paymentProviderError struct {
	StatusCode int
	Message    string
	Response   map[string]any
}

func (e *paymentProviderError) Error() string { return e.Message }

type flutterwaveProvider struct {
	secretKey string
	client    *http.Client
}

func newFlutterwaveProvider(secretKey string, client *http.Client) paymentProvider {
	return &flutterwaveProvider{secretKey: secretKey, client: client}
}

func (p *flutterwaveProvider) Name() string { return flutterwaveProviderName }

func (p *flutterwaveProvider) InitializeCheckout(ctx context.Context, input paymentCheckoutInput) (paymentCheckoutResult, error) {
	payload := map[string]any{
		"tx_ref":          input.Reference,
		"amount":          input.Amount,
		"currency":        strings.ToUpper(strings.TrimSpace(input.Currency)),
		"redirect_url":    input.RedirectURL,
		"payment_options": strings.Join(input.PaymentMethods, ","),
		"customer":        input.Customer,
		"customizations": map[string]any{
			"title":       input.Title,
			"description": input.Description,
		},
		"meta": input.Metadata,
	}
	if input.PaymentPlanID != nil {
		payload["payment_plan"] = *input.PaymentPlanID
	}
	if input.PaymentPlanID == nil {
		payload["configurations"] = map[string]any{
			"session_duration":  30,
			"max_retry_attempt": 5,
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return paymentCheckoutResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.flutterwave.com/v3/payments", bytes.NewReader(body))
	if err != nil {
		return paymentCheckoutResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.secretKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return paymentCheckoutResult{}, &paymentProviderError{StatusCode: http.StatusBadGateway, Message: "payment gateway unavailable"}
	}
	defer resp.Body.Close()

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return paymentCheckoutResult{}, &paymentProviderError{StatusCode: http.StatusBadGateway, Message: "invalid payment gateway response"}
	}
	if resp.StatusCode >= http.StatusMultipleChoices {
		message := "payment declined by gateway"
		if value, ok := raw["message"].(string); ok && strings.TrimSpace(value) != "" {
			message = value
		}
		return paymentCheckoutResult{}, &paymentProviderError{StatusCode: resp.StatusCode, Message: message, Response: raw}
	}
	link := ""
	if data, ok := raw["data"].(map[string]any); ok {
		link, _ = data["link"].(string)
	}
	if strings.TrimSpace(link) == "" {
		return paymentCheckoutResult{}, &paymentProviderError{StatusCode: http.StatusBadGateway, Message: "payment gateway did not return a checkout link", Response: raw}
	}
	return paymentCheckoutResult{CheckoutURL: link, Raw: raw}, nil
}

func (p *flutterwaveProvider) VerifyPayment(ctx context.Context, transactionID, reference string) (verifiedProviderPayment, error) {
	endpoint := ""
	if transactionID != "" {
		if _, err := strconv.ParseInt(transactionID, 10, 64); err == nil {
			endpoint = "https://api.flutterwave.com/v3/transactions/" + transactionID + "/verify"
		}
	}
	if endpoint == "" && reference != "" {
		endpoint = "https://api.flutterwave.com/v3/transactions/verify_by_reference?tx_ref=" + url.QueryEscape(reference)
	}
	if endpoint == "" {
		return verifiedProviderPayment{}, fmt.Errorf("transaction id or reference is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return verifiedProviderPayment{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.secretKey)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return verifiedProviderPayment{}, err
	}
	defer resp.Body.Close()
	var result flutterwaveVerifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return verifiedProviderPayment{}, err
	}
	if resp.StatusCode >= http.StatusMultipleChoices {
		return verifiedProviderPayment{}, fmt.Errorf("flutterwave verification returned %d", resp.StatusCode)
	}
	return verifiedProviderPayment{
		TransactionID: gatewayID(result.Data.ID),
		Reference:     firstNonEmpty(result.Data.TxRef, result.Data.Reference),
		Status:        result.Data.Status,
		Amount:        result.Data.Amount,
		Currency:      result.Data.Currency,
	}, nil
}
