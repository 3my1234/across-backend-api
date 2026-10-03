package controllers

import "testing"

func TestPurchaseXP(t *testing.T) {
	cases := []struct {
		total float64
		want  int
	}{
		{0, 1}, {999.99, 1}, {1000, 2}, {9999.99, 2},
		{10000, 5}, {99999.99, 5}, {100000, 10},
		{499999.99, 10}, {500000, 25},
	}
	for _, test := range cases {
		if got := purchaseXP(test.total); got != test.want {
			t.Fatalf("purchaseXP(%v) = %d, want %d", test.total, got, test.want)
		}
	}
}
