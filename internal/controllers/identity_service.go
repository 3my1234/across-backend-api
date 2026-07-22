package controllers

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type IdentityService struct {
	db *pgxpool.Pool
}

func NewIdentityService(db *pgxpool.Pool) *IdentityService {
	return &IdentityService{db: db}
}

func (s *IdentityService) ResolvePrivy(ctx context.Context, countryID, subject, email, name string) (string, bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "privy:"+subject); err != nil {
		return "", false, err
	}

	var userID string
	err = tx.QueryRow(ctx, `
        SELECT user_id FROM user_identities
        WHERE provider = 'privy' AND provider_subject = $1
        FOR UPDATE
    `, subject).Scan(&userID)
	if err == nil {
		_, err = tx.Exec(ctx, `
            UPDATE user_identities SET email = $2, updated_at = now()
            WHERE provider = 'privy' AND provider_subject = $1
        `, subject, email)
		if err == nil {
			_, err = tx.Exec(ctx, `
                UPDATE users
                SET email = $2,
                    full_name = CASE WHEN trim(full_name) = '' THEN $3 ELSE full_name END,
                    is_active = true, email_verified = true, privy_user_id = $4, updated_at = now()
                WHERE id = $1
            `, userID, email, name, subject)
		}
		if err != nil {
			return "", false, err
		}
		return userID, false, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}

	created := false
	err = tx.QueryRow(ctx, `SELECT id FROM users WHERE lower(email) = lower($1) FOR UPDATE`, email).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		created = true
		err = tx.QueryRow(ctx, `
            INSERT INTO users(country_id, email, password_hash, full_name, is_active, email_verified, privy_user_id)
            VALUES ($1, $2, 'privy-google-oauth', $3, true, true, $4)
            RETURNING id
        `, countryID, email, name, subject).Scan(&userID)
	} else if err == nil {
		_, err = tx.Exec(ctx, `
            UPDATE users
            SET full_name = CASE WHEN trim(full_name) = '' THEN $2 ELSE full_name END,
                is_active = true, email_verified = true, privy_user_id = $3, updated_at = now()
            WHERE id = $1
        `, userID, name, subject)
	}
	if err != nil {
		return "", false, err
	}

	_, err = tx.Exec(ctx, `
        INSERT INTO user_identities(user_id, provider, provider_subject, email)
        VALUES ($1, 'privy', $2, $3)
        ON CONFLICT (provider, provider_subject)
        DO UPDATE SET email = EXCLUDED.email, updated_at = now()
    `, userID, subject, strings.ToLower(email))
	if err != nil {
		return "", false, err
	}
	return userID, created, tx.Commit(ctx)
}
