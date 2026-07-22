package controllers

import "testing"

func TestPurchaseXP(t *testing.T) {
	cases := []struct {
		total float64
		want  int
	}{
		{0, 10}, {999.99, 10}, {1000, 100}, {9999.99, 100},
		{10000, 500}, {99999.99, 500}, {100000, 1000},
		{499999.99, 1000}, {500000, 2500},
	}
	for _, test := range cases {
		if got := purchaseXP(test.total); got != test.want {
			t.Fatalf("purchaseXP(%v) = %d, want %d", test.total, got, test.want)
		}
	}
}
