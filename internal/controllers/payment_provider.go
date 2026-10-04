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

const (
	flutterwaveProviderName   = "flutterwave"
	marketplaceServiceFeeRate = 0.01
)

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
	Subaccounts    []paymentSubaccount
}

type paymentSubaccount struct {
	ID                    string  `json:"id"`
	TransactionChargeType string  `json:"transaction_charge_type"`
	TransactionCharge     float64 `json:"transaction_charge"`
}

type collectionSubaccountInput struct {
	AccountBank    string
	AccountNumber  string
	BusinessName   string
	BusinessMobile string
	BusinessEmail  string
	Country        string
	SwiftCode      string
	RoutingNumber  string
	BankBranch     string
}

type collectionSubaccountResult struct {
	NumericID    int64
	SubaccountID string
	AccountName  string
	BankName     string
}

type flutterwaveBank struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type paymentCheckoutResult struct {
	CheckoutURL string
	Raw         map[string]any
}

type paymentPlanDetails struct {
	ID       int64   `json:"id"`
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
	Interval string  `json:"interval"`
	Status   string  `json:"status"`
}

type gatewaySubscription struct {
	ID       int64   `json:"id"`
	PlanID   int64   `json:"plan"`
	Amount   float64 `json:"amount"`
	Status   string  `json:"status"`
	Customer struct {
		Email string `json:"customer_email"`
	} `json:"customer"`
}

type verifiedProviderPayment struct {
	TransactionID string
	Reference     string
	Status        string
	Amount        any
	ChargedAmount any
	AppFee        any
	MerchantFee   any
	Currency      string
}

type paymentProvider interface {
	Name() string
	InitializeCheckout(context.Context, paymentCheckoutInput) (paymentCheckoutResult, error)
	GetPaymentPlan(context.Context, int64) (paymentPlanDetails, error)
	CreateMonthlyPaymentPlan(context.Context, string, int64) (paymentPlanDetails, error)
	ListActiveSubscriptions(context.Context, int64) ([]gatewaySubscription, error)
	CancelSubscription(context.Context, int64) error
	VerifyPayment(context.Context, string, string) (verifiedProviderPayment, error)
	ListBanks(context.Context, string) ([]flutterwaveBank, error)
	CreateCollectionSubaccount(context.Context, collectionSubaccountInput) (collectionSubaccountResult, error)
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

func (p *flutterwaveProvider) GetPaymentPlan(ctx context.Context, id int64) (paymentPlanDetails, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.flutterwave.com/v3/payment-plans/"+strconv.FormatInt(id, 10), nil)
	if err != nil {
		return paymentPlanDetails{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.secretKey)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return paymentPlanDetails{}, &paymentProviderError{StatusCode: http.StatusBadGateway, Message: "subscription plan could not be verified"}
	}
	defer resp.Body.Close()
	var result struct {
		Status string             `json:"status"`
		Data   paymentPlanDetails `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || resp.StatusCode != http.StatusOK || !strings.EqualFold(result.Status, "success") || result.Data.ID != id {
		return paymentPlanDetails{}, &paymentProviderError{StatusCode: http.StatusBadGateway, Message: "subscription plan could not be verified"}
	}
	return result.Data, nil
}

func (p *flutterwaveProvider) CreateMonthlyPaymentPlan(ctx context.Context, name string, amountNGN int64) (paymentPlanDetails, error) {
	body, err := json.Marshal(map[string]any{"name": name, "amount": amountNGN, "currency": "NGN", "interval": "monthly"})
	if err != nil {
		return paymentPlanDetails{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.flutterwave.com/v3/payment-plans", bytes.NewReader(body))
	if err != nil {
		return paymentPlanDetails{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.secretKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return paymentPlanDetails{}, &paymentProviderError{StatusCode: http.StatusBadGateway, Message: "Flutterwave payment plan could not be created"}
	}
	defer resp.Body.Close()
	var result struct {
		Status string             `json:"status"`
		Data   paymentPlanDetails `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices || !strings.EqualFold(result.Status, "success") || result.Data.ID <= 0 {
		return paymentPlanDetails{}, &paymentProviderError{StatusCode: http.StatusBadGateway, Message: "Flutterwave payment plan could not be created"}
	}
	return result.Data, nil
}

func (p *flutterwaveProvider) ListActiveSubscriptions(ctx context.Context, planID int64) ([]gatewaySubscription, error) {
	items := []gatewaySubscription{}
	for page := 1; page <= 20; page++ {
		endpoint := fmt.Sprintf("https://api.flutterwave.com/v3/subscriptions?plan=%d&status=active&page=%d", planID, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+p.secretKey)
		req.Header.Set("Accept", "application/json")
		resp, err := p.client.Do(req)
		if err != nil {
			return nil, &paymentProviderError{StatusCode: http.StatusBadGateway, Message: "Flutterwave subscriptions could not be checked"}
		}
		var result struct {
			Status string                `json:"status"`
			Data   []gatewaySubscription `json:"data"`
			Meta   struct {
				PageInfo struct {
					TotalPages int `json:"total_pages"`
				} `json:"page_info"`
			} `json:"meta"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if decodeErr != nil || resp.StatusCode != http.StatusOK || !strings.EqualFold(result.Status, "success") {
			return nil, &paymentProviderError{StatusCode: http.StatusBadGateway, Message: "Flutterwave subscriptions could not be checked"}
		}
		for _, item := range result.Data {
			if item.PlanID != planID || !strings.EqualFold(item.Status, "active") || item.ID <= 0 {
				return nil, &paymentProviderError{StatusCode: http.StatusBadGateway, Message: "Flutterwave returned inconsistent subscription data"}
			}
			items = append(items, item)
		}
		if result.Meta.PageInfo.TotalPages == 0 || page >= result.Meta.PageInfo.TotalPages {
			return items, nil
		}
	}
	return nil, &paymentProviderError{StatusCode: http.StatusBadGateway, Message: "Flutterwave subscription list is too large to inspect safely"}
}

func (p *flutterwaveProvider) CancelSubscription(ctx context.Context, id int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "https://api.flutterwave.com/v3/subscriptions/"+strconv.FormatInt(id, 10)+"/cancel", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.secretKey)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return &paymentProviderError{StatusCode: http.StatusBadGateway, Message: "Flutterwave subscription cancellation could not be confirmed"}
	}
	defer resp.Body.Close()
	var result struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices || !strings.EqualFold(result.Status, "success") {
		return &paymentProviderError{StatusCode: http.StatusBadGateway, Message: "Flutterwave subscription cancellation failed"}
	}
	return nil
}

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
	if len(input.Subaccounts) > 0 {
		payload["subaccounts"] = input.Subaccounts
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

func (p *flutterwaveProvider) ListBanks(ctx context.Context, country string) ([]flutterwaveBank, error) {
	country = strings.ToUpper(strings.TrimSpace(country))
	if len(country) != 2 {
		return nil, fmt.Errorf("a two-letter country code is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.flutterwave.com/v3/banks/"+url.PathEscape(country), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.secretKey)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, &paymentProviderError{StatusCode: http.StatusBadGateway, Message: "bank list is temporarily unavailable"}
	}
	defer resp.Body.Close()
	var result struct {
		Status  string            `json:"status"`
		Message string            `json:"message"`
		Data    []flutterwaveBank `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, &paymentProviderError{StatusCode: http.StatusBadGateway, Message: "invalid bank-list response"}
	}
	if resp.StatusCode >= http.StatusMultipleChoices || strings.ToLower(result.Status) != "success" {
		message := strings.TrimSpace(result.Message)
		if message == "" {
			message = "banks are not available for this country"
		}
		return nil, &paymentProviderError{StatusCode: resp.StatusCode, Message: message}
	}
	return result.Data, nil
}

func (p *flutterwaveProvider) CreateCollectionSubaccount(ctx context.Context, input collectionSubaccountInput) (collectionSubaccountResult, error) {
	payload := map[string]any{
		"account_bank":            strings.TrimSpace(input.AccountBank),
		"account_number":          strings.TrimSpace(input.AccountNumber),
		"business_name":           strings.TrimSpace(input.BusinessName),
		"business_mobile":         strings.TrimSpace(input.BusinessMobile),
		"business_contact_mobile": strings.TrimSpace(input.BusinessMobile),
		"business_email":          strings.TrimSpace(input.BusinessEmail),
		"business_contact":        strings.TrimSpace(input.BusinessName),
		"country":                 strings.ToUpper(strings.TrimSpace(input.Country)),
		"split_type":              "percentage",
		"split_value":             marketplaceServiceFeeRate,
	}
	meta := make([]map[string]string, 0, 3)
	if value := strings.TrimSpace(input.SwiftCode); value != "" {
		meta = append(meta, map[string]string{"swiftCode": value})
	}
	if value := strings.TrimSpace(input.RoutingNumber); value != "" {
		meta = append(meta, map[string]string{"routingNumber": value})
	}
	if value := strings.TrimSpace(input.BankBranch); value != "" {
		meta = append(meta, map[string]string{"bank_branch": value})
	}
	if len(meta) > 0 {
		payload["meta"] = meta
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return collectionSubaccountResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.flutterwave.com/v3/subaccounts", bytes.NewReader(body))
	if err != nil {
		return collectionSubaccountResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.secretKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return collectionSubaccountResult{}, &paymentProviderError{StatusCode: http.StatusBadGateway, Message: "Flutterwave could not create the seller settlement account"}
	}
	defer resp.Body.Close()
	var result struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Data    struct {
			ID           int64  `json:"id"`
			SubaccountID string `json:"subaccount_id"`
			FullName     string `json:"full_name"`
			BankName     string `json:"bank_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return collectionSubaccountResult{}, &paymentProviderError{StatusCode: http.StatusBadGateway, Message: "invalid Flutterwave subaccount response"}
	}
	if resp.StatusCode >= http.StatusMultipleChoices || strings.ToLower(result.Status) != "success" || strings.TrimSpace(result.Data.SubaccountID) == "" {
		message := strings.TrimSpace(result.Message)
		if message == "" {
			message = "Flutterwave rejected the seller settlement account"
		}
		return collectionSubaccountResult{}, &paymentProviderError{StatusCode: resp.StatusCode, Message: message}
	}
	return collectionSubaccountResult{
		NumericID: result.Data.ID, SubaccountID: result.Data.SubaccountID,
		AccountName: result.Data.FullName, BankName: result.Data.BankName,
	}, nil
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
		ChargedAmount: result.Data.ChargedAmount,
		AppFee:        result.Data.AppFee,
		MerchantFee:   result.Data.MerchantFee,
		Currency:      result.Data.Currency,
	}, nil
}
