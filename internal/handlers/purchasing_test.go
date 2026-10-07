package handlers

import "testing"

func TestNetUnitCostCents(t *testing.T) {
	cases := []struct {
		name                string
		unit, discount, qty int64
		want                int64
	}{
		{"no discount", 510, 0, 10, 510},
		{"10% of the line", 510, 510, 10, 459},
		{"rounds to nearest cent", 100, 1, 3, 100}, // (300-1)/3 = 99.67 -> 100
		{"rounds down below half", 100, 2, 3, 99},  // (300-2)/3 = 99.33 -> 99
		{"whole line free", 250, 1000, 4, 0},
		{"zero qty keeps unit cost", 250, 0, 0, 250},
	}
	for _, c := range cases {
		if got := netUnitCostCents(c.unit, c.discount, c.qty); got != c.want {
			t.Errorf("%s: netUnitCostCents(%d, %d, %d) = %d, want %d", c.name, c.unit, c.discount, c.qty, got, c.want)
		}
	}
}
