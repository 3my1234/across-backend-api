package controllers

import (
	"strings"
	"testing"
)

func TestProductQueriesTypeFlashSaleParameters(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "create",
			query: createProductInsertSQL,
			want:  []string{"$14::boolean", "$15::numeric", "ELSE NULL::numeric"},
		},
		{
			name:  "update",
			query: updateProductSQL,
			want:  []string{"$9::boolean", "$10::numeric", "ELSE NULL::numeric"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, typedExpression := range test.want {
				if !strings.Contains(test.query, typedExpression) {
					t.Fatalf("product query must explicitly type %s", typedExpression)
				}
			}
		})
	}
}

func TestValidateProductPrices(t *testing.T) {
	tests := []struct {
		name       string
		regular    float64
		original   float64
		flash      bool
		flashPrice float64
		wantError  bool
	}{
		{name: "regular price only", regular: 100},
		{name: "ordinary promotion", regular: 100, original: 150},
		{name: "flash sale", regular: 100, original: 150, flash: true, flashPrice: 52},
		{name: "original below regular", regular: 100, original: 10, wantError: true},
		{name: "flash above regular", regular: 100, flash: true, flashPrice: 120, wantError: true},
		{name: "missing flash price", regular: 100, flash: true, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateProductPrices(test.regular, test.original, test.flash, test.flashPrice)
			if (err != nil) != test.wantError {
				t.Fatalf("validateProductPrices() error = %v, wantError %v", err, test.wantError)
			}
		})
	}
}
