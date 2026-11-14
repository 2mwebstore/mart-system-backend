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

// The riel amount due is rounded to ៛100, so paying exactly that pays the
// sale, with no change, even when it comes to a cent or two under the
// dollars. Less riel than that, or dollars a cent short, is still short.
func TestCalculateChange_ExactRielTotal(t *testing.T) {
	cases := []struct {
		name                 string
		due, usd, khr        int64
		wantShort, wantKHR   int64
		wantUSD, wantAllKHRs int64
	}{
		{name: "the riel total shown, a cent under", due: 315, khr: 12900},
		{name: "two cents under (៛246 shows as ៛200)", due: 6, khr: 200},
		{name: "dollars and riel to the riel total", due: 315, usd: 300, khr: 600},
		{name: "a riel total rounded up", due: 317, khr: 13000},
		{name: "៛100 under the riel total", due: 315, khr: 12800, wantShort: 3},
		{name: "dollars a cent short, no riel", due: 315, usd: 314, wantShort: 1},
		// ៛1,845 shows as ៛1,800: $0.44 is worth more than that, but an
		// all-dollar payment is still checked to the cent.
		{name: "dollars only, worth the riel shown", due: 45, usd: 44, wantShort: 1},
		{name: "more riel than due still gets change", due: 315, khr: 20000, wantUSD: 100, wantKHR: 3000, wantAllKHRs: 7100},
	}
	for _, tc := range cases {
		got, err := CalculateChange(tc.due, tc.usd, tc.khr, 4100)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		want := ChangeResult{ShortCents: tc.wantShort, ChangeUSDCents: tc.wantUSD, ChangeKHRRiel: tc.wantKHR, ChangeAllKHRRiel: tc.wantAllKHRs}
		if got != want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, want)
		}
	}
}

func TestCoversRielDue(t *testing.T) {
	cases := []struct {
		due, usd, khr int64
		want          bool
	}{
		{315, 0, 12900, true},
		{315, 0, 12899, false},
		{315, 300, 600, true},
		{315, 300, 500, false},
		{6, 0, 200, true},
		{6, 0, 100, false},
		{0, 0, 0, true},
	}
	for _, tc := range cases {
		if got := CoversRielDue(tc.due, tc.usd, tc.khr, 4100); got != tc.want {
			t.Errorf("CoversRielDue(%d, $%d, ៛%d) = %v, want %v", tc.due, tc.usd, tc.khr, got, tc.want)
		}
	}
}

// A riel company: the cash due is rounded to ៛100, dollars count at the rate
// and all change is given in riel.
func TestCalculateChangeKHR(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		due, usd, khr        int64
		changeKHR, shortRiel int64
	}{
		{"riel", 15600, 0, 20000, 4400, 0},
		{"dollars", 15600, 500, 0, 4900, 0},
		{"both", 15600, 300, 4000, 700, 0},
		{"exact", 15600, 0, 15600, 0, 0},
		{"due rounded up to ៛100", 17050, 0, 17100, 0, 0},
		{"due rounded down to ៛100", 17040, 0, 17000, 0, 0},
		{"short of the rounded due", 17050, 0, 17000, 0, 100},
		{"short", 15600, 0, 15000, 0, 600},
		{"change rounded to ៛100", 15600, 1, 20000, 4400, 0}, // 1¢ is ៛41
		{"odd cents", 4000, 101, 0, 100, 0},                  // $1.01 is ៛4,141
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CalculateChangeKHR(tc.due, tc.usd, tc.khr, 4100)
			if err != nil {
				t.Fatal(err)
			}
			want := ChangeResult{ChangeKHRRiel: tc.changeKHR, ChangeAllKHRRiel: tc.changeKHR, ShortCents: tc.shortRiel}
			if got != want {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
	if _, err := CalculateChangeKHR(1000, 0, 1000, 0); err == nil {
		t.Fatal("a zero rate was accepted")
	}
}

func TestCalculateChangeIn(t *testing.T) {
	usd, _ := CalculateChangeIn("USD", 1000, 1250, 0, 4100)
	if usd.ChangeUSDCents != 200 || usd.ChangeKHRRiel != 2100 {
		t.Fatalf("dollars: %+v", usd)
	}
	khr, _ := CalculateChangeIn("KHR", 15600, 0, 20000, 4100)
	if khr.ChangeUSDCents != 0 || khr.ChangeKHRRiel != 4400 {
		t.Fatalf("riel: %+v", khr)
	}
}

func TestRielInAndLoyaltyUnit(t *testing.T) {
	if got := RielIn("USD", 315, 4100); got != 12900 {
		t.Fatalf("$3.15 is ៛%d, want ៛12,900", got)
	}
	if got := RielIn("KHR", 17050, 4100); got != 17050 {
		t.Fatalf("៛17,050 is ៛%d", got)
	}
	if LoyaltyUnit("USD") != 100 || LoyaltyUnit("KHR") != 1000 {
		t.Fatal("loyalty is per $1 or ៛1,000")
	}
}
