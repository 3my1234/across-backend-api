package controllers

import "testing"

func TestPurchaseXP(t *testing.T) {
	cases := []struct {
		total float64
		want  int
	}{
		{0, 0}, {999.99, 0}, {1000, 1}, {9999.99, 9},
		{10000, 10}, {25000, 25}, {100000, 25},
	}
	for _, test := range cases {
		if got := purchaseXP(test.total); got != test.want {
			t.Fatalf("purchaseXP(%v) = %d, want %d", test.total, got, test.want)
		}
	}
}
