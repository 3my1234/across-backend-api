package controllers

import (
	"context"
	"strings"
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
		{name: "super admin approves procurement", role: "super_admin", status: "closed", action: "approve_procurement", wantTo: "funds_sent_to_procurement"},
		{name: "admin I approves procurement", role: "catalog_admin", status: "closed", action: "approve_procurement", wantTo: "funds_sent_to_procurement"},
		{name: "procurement starts directly after approval", role: "procurement_admin", status: "funds_sent_to_procurement", action: "start_procurement", wantTo: "purchasing"},
		{name: "procurement dispatches directly after checklist", role: "procurement_admin", status: "purchasing", action: "dispatch", wantTo: "enroute_nigeria"},
		{name: "courier confirms ready directly after receipt", role: "courier_admin", status: "enroute_nigeria", action: "confirm_ready_for_pickup", wantTo: "ready_for_pickup"},
		{name: "super admin cannot perform procurement action", role: "super_admin", status: "purchasing", action: "dispatch", wantErr: fiber.StatusForbidden},
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

func TestBatchTransitionNotificationSQLTypesEveryParameter(t *testing.T) {
	for _, typedParameter := range []string{
		"$1::uuid",
		"$2::text",
		"$3::text",
		"$4::text",
		"$5::jsonb",
		"$6::text",
	} {
		if !strings.Contains(insertBatchTransitionNotificationSQL, typedParameter) {
			t.Fatalf("notification SQL must explicitly type %s", typedParameter)
		}
	}
	if strings.Contains(insertBatchTransitionNotificationSQL, "jsonb_build_object") {
		t.Fatal("notification SQL must pass pre-encoded JSON instead of untyped parameters to jsonb_build_object")
	}
}

func TestConfirmReadyForPickupRequiresPickupDetailsAndChecklistConfirmation(t *testing.T) {
	tests := []struct {
		name    string
		request batchTransitionRequest
		wantErr bool
	}{
		{name: "missing pickup details", request: batchTransitionRequest{Action: "confirm_ready_for_pickup", ManifestChecked: true}, wantErr: true},
		{name: "missing checklist confirmation", request: batchTransitionRequest{Action: "confirm_ready_for_pickup", PickupLocation: "Lagos Hub", PickupPhone: "+2348000000000"}, wantErr: true},
		{name: "complete courier handoff", request: batchTransitionRequest{Action: "confirm_ready_for_pickup", PickupLocation: "Lagos Hub", PickupPhone: "+2348000000000", ManifestChecked: true}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateBatchActionPayload(context.Background(), nil, "", test.request)
			if test.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
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
