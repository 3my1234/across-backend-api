package main

import (
	"strings"
	"testing"
)

func TestDiagnosticUsesDatabaseQueryNotRequestParameter(t *testing.T) {
	nearby, err := diagnosticSQLFromSource("../../internal/controllers", "NearbyDrivers")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(nearby, "SELECT c.id") || !strings.Contains(nearby, "FROM provider_driver_presence d") || !strings.Contains(nearby, "driver_documents_approved(l.provider_id)") || !strings.Contains(nearby, "LIMIT 25") {
		t.Fatalf("diagnostic did not extract actual nearby SQL: %q", nearby)
	}
	history, err := diagnosticSQLFromSource("../../internal/controllers", "businessHistoryPageSQL")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(history, "provider_conversations requested") || !strings.Contains(history, "CROSS JOIN LATERAL") || !strings.Contains(history, "msg.created_at DESC,msg.id DESC LIMIT $4") {
		t.Fatal("actual history grouping/order missing")
	}
	if _, err = diagnosticSQLFromSource("../../internal/controllers", "DriverAvailability"); err == nil {
		t.Fatal("write entrypoint must not be selectable")
	}
}
