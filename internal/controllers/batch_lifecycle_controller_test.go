package controllers

import (
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestValidateBatchActionEnforcesRoleAndOrder(t *testing.T) {
	tests := []struct {
		name    string
		role    string
		status  string
		action  string
		wantTo  string
		wantErr int
	}{
		{name: "admin I closes collection", role: "catalog_admin", status: "collecting_funds", action: "close_collection", wantTo: "closed"},
		{name: "procurement acknowledges funds", role: "procurement_admin", status: "funds_sent_to_procurement", action: "acknowledge_procurement_funds", wantTo: "procurement_acknowledged"},
		{name: "courier confirms arrival", role: "courier_admin", status: "enroute_nigeria", action: "confirm_arrival", wantTo: "arrived_local"},
		{name: "super admin may perform operational action", role: "super_admin", status: "procurement_complete", action: "dispatch", wantTo: "enroute_nigeria"},
		{name: "courier cannot procure", role: "courier_admin", status: "procurement_acknowledged", action: "start_procurement", wantErr: fiber.StatusForbidden},
		{name: "cannot skip funds handoff", role: "procurement_admin", status: "settled", action: "start_procurement", wantErr: fiber.StatusConflict},
		{name: "cannot move backward", role: "catalog_admin", status: "settled", action: "close_collection", wantErr: fiber.StatusConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec, err := validateBatchAction(test.role, test.status, test.action)
			if test.wantErr == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if spec.To != test.wantTo {
					t.Fatalf("target status = %q, want %q", spec.To, test.wantTo)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected Fiber error with status %d", test.wantErr)
			}
			fiberErr, ok := err.(*fiber.Error)
			if !ok || fiberErr.Code != test.wantErr {
				t.Fatalf("error = %#v, want Fiber status %d", err, test.wantErr)
			}
		})
	}
}

func TestISO4217CodeValidation(t *testing.T) {
	tests := []struct {
		value string
		valid bool
	}{
		{value: "NGN", valid: true},
		{value: "CNY", valid: true},
		{value: "ngn", valid: false},
		{value: "NG", valid: false},
		{value: "NGN1", valid: false},
	}
	for _, test := range tests {
		if got := isISO4217Code(test.value); got != test.valid {
			t.Fatalf("isISO4217Code(%q) = %t, want %t", test.value, got, test.valid)
		}
	}
}
