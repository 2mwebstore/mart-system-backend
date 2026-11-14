package services

import (
	"testing"

	"com-mart/backend/internal/models"
)

func TestFormatMoney(t *testing.T) {
	for _, tc := range []struct {
		amount   int64
		currency models.Currency
		want     string
	}{
		{1234, models.CurrencyUSD, "$12.34"},
		{-5, models.CurrencyUSD, "-$0.05"},
		{0, models.CurrencyKHR, "៛0"},
		{900, models.CurrencyKHR, "៛900"},
		{15600, models.CurrencyKHR, "៛15,600"},
		{1234567, models.CurrencyKHR, "៛1,234,567"},
		{-4100, models.CurrencyKHR, "-៛4,100"},
	} {
		if got := FormatMoney(tc.amount, tc.currency); got != tc.want {
			t.Errorf("FormatMoney(%d, %s) = %q, want %q", tc.amount, tc.currency, got, tc.want)
		}
	}
}
