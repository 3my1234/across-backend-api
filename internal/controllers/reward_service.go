package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const welcomeXP = 100

type RewardService struct {
	db *pgxpool.Pool
}

func NewRewardService(db *pgxpool.Pool) *RewardService {
	return &RewardService{db: db}
}

func purchaseXP(total float64) int {
	switch {
	case total < 1000:
		return 10
	case total < 10000:
		return 100
	case total < 100000:
		return 500
	case total < 500000:
		return 1000
	default:
		return 2500
	}
}

func (r *RewardService) AwardWelcome(ctx context.Context, userID string) (bool, error) {
	return r.award(ctx, userID, "", welcomeXP, "welcome", "account-welcome", "welcome-xp:"+userID,
		"Welcome to Atlantic Express - 100 XP earned",
		"You received 100 XP, worth N100 in discounts. Earn more XP through daily logins and completed purchases.")
}

func (r *RewardService) AwardDailyLogin(ctx context.Context, userID string, now time.Time) (bool, error) {
	claimDate := now.UTC().Format("2006-01-02")
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	var inserted bool
	err = tx.QueryRow(ctx, `
        WITH claim AS (
            INSERT INTO xp_daily_login(user_id, claim_date, xp_awarded)
            VALUES ($1, $2::date, 1)
            ON CONFLICT (user_id, claim_date) DO NOTHING
            RETURNING 1
        )
        SELECT EXISTS(SELECT 1 FROM claim)
    `, userID, claimDate).Scan(&inserted)
	if err != nil || !inserted {
		if err == nil {
			err = tx.Commit(ctx)
		}
		return false, err
	}

	if _, err = tx.Exec(ctx, `
        INSERT INTO xp_transactions(user_id, amount, reason, reference_id)
        VALUES ($1, 1, 'daily_login', $2)
        ON CONFLICT DO NOTHING
    `, userID, "daily-"+claimDate); err != nil {
		return false, err
	}
	if err = insertNotification(ctx, tx, userID, "", nil, "xp_earned", "Daily login reward", "You earned 1 XP for logging in today.", map[string]any{"xp": 1, "reason": "daily_login"}, "daily-login:"+userID+":"+claimDate); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (r *RewardService) AwardPurchase(ctx context.Context, userID, orderID string, total float64) (bool, int, error) {
	amount := purchaseXP(total)
	awarded, err := r.award(ctx, userID, orderID, amount, "purchase", "purchase-"+orderID, "purchase-xp:"+orderID,
		"Purchase reward earned", fmt.Sprintf("You earned %d XP from your completed purchase.", amount))
	return awarded, amount, err
}

func (r *RewardService) award(ctx context.Context, userID, orderID string, amount int, reason, referenceID, eventKey, title, body string) (bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
        INSERT INTO xp_transactions(user_id, amount, reason, reference_id)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT DO NOTHING
    `, userID, amount, reason, referenceID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, tx.Commit(ctx)
	}
	if err := insertNotification(ctx, tx, userID, orderID, nil, "xp_earned", title, body,
		map[string]any{"xp": amount, "naira_value": amount, "reason": reason}, eventKey); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

const insertNotificationSQL = `
    INSERT INTO notifications(user_id, order_id, batch_id, type, title, body, data, event_key)
    VALUES (
        $1::uuid,
        NULLIF($2::text, '')::uuid,
        $3::uuid,
        $4::text,
        $5::text,
        $6::text,
        $7::jsonb,
        NULLIF($8::text, '')
    )
    ON CONFLICT DO NOTHING
`

func insertNotification(ctx context.Context, tx pgx.Tx, userID, orderID string, batchID *string, notificationType, title, body string, data map[string]any, eventKey string) error {
	dataJSON, err := marshalNotificationData(data)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, insertNotificationSQL, userID, orderID, batchID, notificationType, title, body, dataJSON, eventKey)
	return err
}
