package handlers

import (
	"testing"

	"com-mart/backend/internal/models"
)

func TestConvertMinor(t *testing.T) {
	usd, khr := models.CurrencyUSD, models.CurrencyKHR
	for _, tc := range []struct {
		amount   int64
		from, to models.Currency
		want     int64
	}{
		{2000, usd, khr, 82000}, // $20 at ៛4,100
		{500, usd, khr, 20500},  // $5
		{315, usd, khr, 12900},  // ៛12,915 rounded to ៛100
		{82000, khr, usd, 2000}, // back again
		{20500, khr, usd, 500},  //
		{1000, khr, usd, 24},    // ៛1,000 is 24.39¢
		{1100, khr, usd, 27},    // 26.83¢
		{unlimitedRefund, usd, khr, unlimitedRefund},
		{777, usd, usd, 777},
	} {
		if got := convertMinor(tc.amount, tc.from, tc.to, 4100); got != tc.want {
			t.Errorf("convertMinor(%d, %s→%s) = %d, want %d", tc.amount, tc.from, tc.to, got, tc.want)
		}
	}
}

func TestParseImportMoney(t *testing.T) {
	for _, tc := range []struct {
		in       string
		currency models.Currency
		want     int64
		bad      bool
	}{
		{"1.50", models.CurrencyUSD, 150, false},
		{"$1.50", models.CurrencyUSD, 150, false},
		{"", models.CurrencyUSD, 0, false},
		{"1500", models.CurrencyKHR, 1500, false},
		{"1,500", models.CurrencyKHR, 1500, false},
		{"៛15,600", models.CurrencyKHR, 15600, false},
		{"", models.CurrencyKHR, 0, false},
		{"1.50", models.CurrencyKHR, 0, true},
		{"-100", models.CurrencyKHR, 0, true},
		{"abc", models.CurrencyKHR, 0, true},
	} {
		got, err := parseImportMoney(tc.in, tc.currency)
		if (err != nil) != tc.bad || got != tc.want {
			t.Errorf("parseImportMoney(%q, %s) = %d, %v; want %d (bad %v)", tc.in, tc.currency, got, err, tc.want, tc.bad)
		}
	}
}
