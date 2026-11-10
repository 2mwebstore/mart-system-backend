package handlers

import "testing"

// taxCents must match the admin app's sum (admin/src/utils/tax.ts) to the
// cent. Percents like 1.15 or 0.29 aren't exact in floating point
// (1.15 × 100 = 114.99999999999999), so they are rounded to hundredths, not cut.
func TestTaxCents(t *testing.T) {
	cases := []struct {
		base    int64
		percent float64
		want    int64
	}{
		{900, 10, 90},
		{505, 10, 51}, // 50.5 rounds half up
		{504, 10, 50},
		{1800, 10, 180},
		{1000, 7.5, 75},
		{333, 7.5, 25},     // 24.975
		{10000, 1.15, 115}, // not 114
		{10000, 0.29, 29},  // not 28
		{200, 0.25, 1},     // 0.5 rounds up
		{1, 0.25, 0},
		{0, 10, 0},
		{-100, 10, 0},
		{1000, 0, 0},
		{1000, -5, 0},
	}
	for _, c := range cases {
		if got := taxCents(c.base, c.percent); got != c.want {
			t.Errorf("taxCents(%d, %v) = %d, want %d", c.base, c.percent, got, c.want)
		}
	}
}
