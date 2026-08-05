package controllers

import (
	"strings"
	"testing"
)

func TestConfirmReceiptQueryTypesBothUUIDParameters(t *testing.T) {
	for _, typedParameter := range []string{"$1::uuid", "$2::uuid"} {
		if !strings.Contains(confirmReceiptUpdateSQL, typedParameter) {
			t.Fatalf("receipt update must explicitly type %s", typedParameter)
		}
	}
}
