package config

import (
	"testing"
	"time"
)

func TestProviderSubscriptionPolicyUsesEnvironmentUntilAdminOverride(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	cfg := Config{ProviderSubscriptionsEnforced: true, ProviderSubscriptionsStartAt: &start}
	policy := NewProviderSubscriptionPolicy(nil, cfg)
	cfg.ProviderSubscriptionPolicy = policy
	if cfg.ProviderSubscriptionsRequired(start.Add(-time.Second)) || !cfg.ProviderSubscriptionsRequired(start) {
		t.Fatal("environment launch date was not respected")
	}
	if access := policy.Current(start); access.Source != "environment" || !access.Required {
		t.Fatalf("unexpected fallback access: %+v", access)
	}
	policy.current.Store(&providerSubscriptionSnapshot{enforced: false, source: "admin"})
	if cfg.ProviderSubscriptionsRequired(start) {
		t.Fatal("admin free access must override the environment")
	}
	policy.current.Store(&providerSubscriptionSnapshot{enforced: true, source: "admin"})
	if !cfg.ProviderSubscriptionsRequired(start.Add(-time.Second)) {
		t.Fatal("admin paid access should take effect immediately")
	}
}
