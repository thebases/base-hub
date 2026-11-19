package models

import "testing"

func TestRoundToFactorExactTies(t *testing.T) {
	cases := []struct{ amount, factor, want float64 }{
		{1.005, 0.01, 1.01},
		{2.675, 0.01, 2.68},
		{-1.005, 0.01, -1.01},
		{1.004, 0.01, 1.0},
		{10.5, 1, 11},
		{-10.5, 1, -11},
		{1234.5678, 0.05, 1234.55},
		{0.0001, 0.01, 0},
		{5, 0, 5},
	}
	for _, c := range cases {
		if got := roundToFactor(c.amount, c.factor); got != c.want {
			t.Errorf("roundToFactor(%v, %v) = %v, want %v", c.amount, c.factor, got, c.want)
		}
	}
}
