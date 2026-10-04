package config

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ProviderSubscriptionAccess struct {
	Enforced  bool       `json:"enforced"`
	StartAt   *time.Time `json:"start_at"`
	Required  bool       `json:"required"`
	Source    string     `json:"source"`
	UpdatedAt *time.Time `json:"updated_at"`
}

type providerSubscriptionSnapshot struct {
	enforced  bool
	startAt   *time.Time
	source    string
	updatedAt *time.Time
}

type ProviderSubscriptionPolicy struct {
	db       *pgxpool.Pool
	fallback providerSubscriptionSnapshot
	current  atomic.Pointer[providerSubscriptionSnapshot]
}

func NewProviderSubscriptionPolicy(db *pgxpool.Pool, cfg Config) *ProviderSubscriptionPolicy {
	fallback := providerSubscriptionSnapshot{enforced: cfg.ProviderSubscriptionsEnforced, startAt: cfg.ProviderSubscriptionsStartAt, source: "environment"}
	p := &ProviderSubscriptionPolicy{db: db, fallback: fallback}
	p.current.Store(&fallback)
	return p
}

func (p *ProviderSubscriptionPolicy) Required(at time.Time) bool {
	s := p.current.Load()
	return s.enforced && (s.startAt == nil || !at.Before(*s.startAt))
}

func (p *ProviderSubscriptionPolicy) Current(at time.Time) ProviderSubscriptionAccess {
	s := p.current.Load()
	return ProviderSubscriptionAccess{Enforced: s.enforced, StartAt: s.startAt, Required: s.enforced && (s.startAt == nil || !at.Before(*s.startAt)), Source: s.source, UpdatedAt: s.updatedAt}
}

func (p *ProviderSubscriptionPolicy) Refresh(ctx context.Context) error {
	var enforced *bool
	var startAt, updatedAt *time.Time
	if err := p.db.QueryRow(ctx, `SELECT enforced,start_at,updated_at FROM provider_subscription_access WHERE singleton=TRUE`).Scan(&enforced, &startAt, &updatedAt); err != nil {
		return err
	}
	if enforced == nil {
		p.current.Store(&p.fallback)
		return nil
	}
	p.current.Store(&providerSubscriptionSnapshot{enforced: *enforced, startAt: startAt, source: "admin", updatedAt: updatedAt})
	return nil
}

func (p *ProviderSubscriptionPolicy) Set(ctx context.Context, enforced bool, startAt *time.Time, adminID string) error {
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var oldEnforced *bool
	var oldStartAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT enforced,start_at FROM provider_subscription_access WHERE singleton=TRUE FOR UPDATE`).Scan(&oldEnforced, &oldStartAt); err != nil {
		return err
	}
	var updatedAt time.Time
	err = tx.QueryRow(ctx, `UPDATE provider_subscription_access
		SET enforced=$1,start_at=$2,updated_at=now(),updated_by=$3::uuid
		WHERE singleton=TRUE RETURNING updated_at`, enforced, startAt, adminID).Scan(&updatedAt)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO provider_subscription_access_events(admin_id,old_enforced,old_start_at,new_enforced,new_start_at)
		VALUES($1::uuid,$2,$3,$4,$5)`, adminID, oldEnforced, oldStartAt, enforced, startAt); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	p.current.Store(&providerSubscriptionSnapshot{enforced: enforced, startAt: startAt, source: "admin", updatedAt: &updatedAt})
	return nil
}

func (p *ProviderSubscriptionPolicy) Run(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.Refresh(ctx); err != nil {
				log.Printf("provider subscription access refresh failed: %v", err)
			}
		}
	}
}
