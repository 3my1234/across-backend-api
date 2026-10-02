package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"across/backend/internal/config"

	"github.com/jackc/pgx/v5/pgxpool"
)

type flutterwaveSettlementList struct {
	Status string `json:"status"`
	Meta   struct {
		PageInfo struct {
			TotalPages int `json:"total_pages"`
		} `json:"page_info"`
	} `json:"meta"`
	Data []struct {
		ID     any    `json:"id"`
		Status string `json:"status"`
	} `json:"data"`
}

type flutterwaveSettlementDetail struct {
	Status string `json:"status"`
	Data   struct {
		ID                any    `json:"id"`
		Status            string `json:"status"`
		Reference         string `json:"reference"`
		Destination       string `json:"destination"`
		SettlementAccount string `json:"settlement_account"`
		ProcessedAt       string `json:"processed_date"`
		Transactions      []struct {
			TxRef             string `json:"tx_ref"`
			ChargedAmount     any    `json:"charged_amount"`
			AppFee            any    `json:"app_fee"`
			LegacyAppFee      any    `json:"appfee"`
			MerchantFee       any    `json:"merchant_fee"`
			LegacyMerchantFee any    `json:"merchantfee"`
			SettlementAmount  any    `json:"settlement_amount"`
		} `json:"transactions"`
	} `json:"data"`
}

// ReconcileFlutterwaveSettlements imports the gateway's actual payout result.
// A successful charge only allocates a split; only a successful settlement
// proves that Flutterwave released the seller's money.
func ReconcileFlutterwaveSettlements(ctx context.Context, db *pgxpool.Pool, cfg config.Config, client *http.Client) (int, error) {
	if strings.TrimSpace(cfg.FlutterwaveSecretKey) == "" {
		return 0, nil
	}
	rows, err := db.Query(ctx, `
		SELECT DISTINCT pa.flutterwave_subaccount_id
		FROM provider_payout_accounts pa
		JOIN merchant_ledger ml ON ml.provider_id=pa.provider_id
		WHERE pa.status='active'
		  AND ml.settlement_status IN ('pending','on_hold')
		  AND ml.created_at >= now() - interval '120 days'
		LIMIT 100
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var subaccounts []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return 0, err
		}
		subaccounts = append(subaccounts, value)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	updated := 0
	for _, subaccountID := range subaccounts {
		ids, err := listFlutterwaveSettlementIDs(ctx, cfg.FlutterwaveSecretKey, subaccountID, client)
		if err != nil {
			return updated, err
		}
		for _, id := range ids {
			detail, err := getFlutterwaveSettlement(ctx, cfg.FlutterwaveSecretKey, id, client)
			if err != nil {
				return updated, err
			}
			count, err := applyFlutterwaveSettlement(ctx, db, subaccountID, detail)
			if err != nil {
				return updated, err
			}
			updated += count
		}
	}
	return updated, nil
}

func listFlutterwaveSettlementIDs(ctx context.Context, secret, subaccountID string, client *http.Client) ([]string, error) {
	ids := make([]string, 0)
	for page := 1; page <= 20; page++ {
		query := url.Values{}
		query.Set("subaccount_id", subaccountID)
		query.Set("from", time.Now().UTC().AddDate(0, 0, -120).Format("2006-01-02"))
		query.Set("to", time.Now().UTC().Format("2006-01-02"))
		query.Set("page", strconv.Itoa(page))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.flutterwave.com/v3/settlements?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+secret)
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		var result flutterwaveSettlementList
		decodeErr := json.NewDecoder(resp.Body).Decode(&result)
		_ = resp.Body.Close()
		if resp.StatusCode >= http.StatusMultipleChoices {
			return nil, fmt.Errorf("flutterwave settlements returned %d", resp.StatusCode)
		}
		if decodeErr != nil {
			return nil, decodeErr
		}
		for _, item := range result.Data {
			if id := gatewayID(item.ID); id != "" {
				ids = append(ids, id)
			}
		}
		if len(result.Data) == 0 || result.Meta.PageInfo.TotalPages == 0 || page >= result.Meta.PageInfo.TotalPages {
			break
		}
	}
	return ids, nil
}

func getFlutterwaveSettlement(ctx context.Context, secret, id string, client *http.Client) (flutterwaveSettlementDetail, error) {
	var result flutterwaveSettlementDetail
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.flutterwave.com/v3/settlements/"+url.PathEscape(id), nil)
	if err != nil {
		return result, err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusMultipleChoices {
		return result, fmt.Errorf("flutterwave settlement %s returned %d", id, resp.StatusCode)
	}
	err = json.NewDecoder(resp.Body).Decode(&result)
	return result, err
}

func applyFlutterwaveSettlement(ctx context.Context, db *pgxpool.Pool, subaccountID string, detail flutterwaveSettlementDetail) (int, error) {
	settlementID := gatewayID(detail.Data.ID)
	state := normalizeSettlementStatus(detail.Data.Status)
	destination := strings.TrimSpace(detail.Data.Destination)
	if destination == "" && strings.TrimSpace(detail.Data.SettlementAccount) != "" {
		destination = "bank account"
	}
	settledAt := parseFlutterwaveTime(detail.Data.ProcessedAt)
	count := 0
	for _, item := range detail.Data.Transactions {
		if strings.TrimSpace(item.TxRef) == "" {
			continue
		}
		actual, _ := amountValue(item.SettlementAmount)
		charged, _ := amountValue(item.ChargedAmount)
		appFee := firstSettlementAmount(item.AppFee, item.LegacyAppFee)
		merchantFee := firstSettlementAmount(item.MerchantFee, item.LegacyMerchantFee)
		tx, err := db.Begin(ctx)
		if err != nil {
			return count, err
		}
		var paidAt any
		if state == "settled" {
			paidAt = settledAt
		}
		tag, err := tx.Exec(ctx, `
			UPDATE payments p
			SET charged_amount=CASE WHEN $3>0 THEN $3 ELSE charged_amount END,
				gateway_fee=CASE WHEN $4>0 THEN $4 ELSE gateway_fee END,
				merchant_fee=CASE WHEN $5>0 THEN $5 ELSE merchant_fee END,
				provider_settlement_amount=$6,
				provider_settlement_id=$7,
				settlement_reference=$8,
				settlement_destination=$9,
				settlement_status=$10,
				settled_at=CASE WHEN $10='settled' THEN COALESCE($11::timestamptz,now()) ELSE settled_at END,
				updated_at=now()
			FROM orders o
			JOIN provider_payout_accounts pa ON pa.provider_id=o.provider_id
			WHERE p.order_id=o.id AND p.provider='flutterwave' AND p.purpose='order'
			  AND p.provider_reference=$1 AND pa.flutterwave_subaccount_id=$2
		`, item.TxRef, subaccountID, charged, appFee, merchantFee, actual,
			settlementID, detail.Data.Reference, destination, state, paidAt)
		if err == nil && tag.RowsAffected() > 0 {
			_, err = tx.Exec(ctx, `
				UPDATE merchant_ledger ml
				SET gateway_deductions=GREATEST(expected_net_amount-$2,0),
					settlement_amount=$2,net_amount=$2,
					settlement_id=$3,settlement_reference=$4,
					settlement_destination=$5,settlement_status=$6,
					settlement_checked_at=now(),
					status=CASE WHEN $6='settled' THEN 'paid' ELSE status END,
					paid_at=CASE WHEN $6='settled' THEN COALESCE($7::timestamptz,now()) ELSE paid_at END
				FROM orders o
				WHERE ml.order_id=o.id AND o.flutterwave_tx_ref=$1
			`, item.TxRef, actual, settlementID, detail.Data.Reference, destination, state, paidAt)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return count, err
		}
		if err := tx.Commit(ctx); err != nil {
			return count, err
		}
		if tag.RowsAffected() > 0 {
			count++
		}
	}
	return count, nil
}

func firstSettlementAmount(values ...any) float64 {
	for _, value := range values {
		if amount, err := amountValue(value); err == nil && amount > 0 {
			return amount
		}
	}
	return 0
}

func normalizeSettlementStatus(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.Contains(value, "hold"), strings.Contains(value, "flag"):
		return "on_hold"
	case value == "successful", value == "success", value == "completed", value == "processed":
		return "settled"
	case value == "failed":
		return "failed"
	case value == "reversed":
		return "reversed"
	default:
		return "pending"
	}
}

func parseFlutterwaveTime(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05.000Z"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	if unix, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.Unix(unix, 0).UTC()
	}
	return nil
}
