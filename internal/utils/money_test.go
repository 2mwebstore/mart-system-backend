package utils

import "testing"

func TestRoundRielTo100(t *testing.T) {
	cases := []struct {
		in, want int64
	}{
		{0, 0},
		{49, 0},
		{50, 100},
		{149, 100},
		{150, 200},
		{9430, 9400},
		{1230, 1200},
	}
	for _, c := range cases {
		if got := RoundRielTo100(c.in); got != c.want {
			t.Errorf("RoundRielTo100(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestUSDCentsToRiel(t *testing.T) {
	// 30 cents at ៛4,100/$1 = 1,230 riel, rounded to ៛1,200.
	if got := USDCentsToRiel(30, 4100); got != 1200 {
		t.Errorf("USDCentsToRiel(30, 4100) = %d, want 1200", got)
	}
}

func TestCalculateChange_SpecExample(t *testing.T) {
	// Build spec §3: due $2.70, received $5 -> change $2 + ៛1,200 (or
	// ៛9,400 all in riel), at ៛4,100/$1.
	result, err := CalculateChange(270, 500, 0, 4100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ChangeUSDCents != 200 {
		t.Errorf("ChangeUSDCents = %d, want 200", result.ChangeUSDCents)
	}
	if result.ChangeKHRRiel != 1200 {
		t.Errorf("ChangeKHRRiel = %d, want 1200", result.ChangeKHRRiel)
	}
	if result.ChangeAllKHRRiel != 9400 {
		t.Errorf("ChangeAllKHRRiel = %d, want 9400", result.ChangeAllKHRRiel)
	}
	if result.ShortCents != 0 {
		t.Errorf("ShortCents = %d, want 0", result.ShortCents)
	}
}

func TestCalculateChange_MixedCurrencyReceived(t *testing.T) {
	// Due $3.00, tendered $2 + ៛4,100 (~$1) at rate 4100 -> total ~$3.00,
	// exact change.
	result, err := CalculateChange(300, 200, 4100, 4100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ShortCents != 0 {
		t.Errorf("ShortCents = %d, want 0", result.ShortCents)
	}
	if result.ChangeUSDCents != 0 || result.ChangeKHRRiel != 0 {
		t.Errorf("expected no change, got %+v", result)
	}
}

func TestCalculateChange_Insufficient(t *testing.T) {
	result, err := CalculateChange(1000, 500, 0, 4100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ShortCents != 500 {
		t.Errorf("ShortCents = %d, want 500", result.ShortCents)
	}
}

func TestCalculateChange_InvalidRate(t *testing.T) {
	if _, err := CalculateChange(100, 200, 0, 0); err == nil {
		t.Error("expected error for zero exchange rate")
	}
}
