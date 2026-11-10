package tests

import (
	"net/http"
	"slices"
	"sort"
	"testing"
	"time"

	"com-mart/backend/internal/models"
)

func TestLoginGivesNothingAway(t *testing.T) {
	e := setup(t)
	unknown := tryLogin(t, unique("ghost"), "wrong-password")
	wrong := tryLogin(t, newUser(t, e.roleCashier).Username, "wrong-password")
	disabled := tryLogin(t, e.disabledCashier.Username, "wrong-password")

	if unknown.Status != http.StatusUnauthorized || unknown.Raw != wrong.Raw || unknown.Raw != disabled.Raw {
		t.Fatalf("responses differ:\n unknown:  %s\n wrong:    %s\n disabled: %s", unknown, wrong, disabled)
	}
}

func TestDisabledIsOnlyToldToWhoeverKnowsThePassword(t *testing.T) {
	e := setup(t)
	wantDenied(t, tryLogin(t, e.disabledCashier.Username, testPassword), http.StatusForbidden, "ACCOUNT_DISABLED")
	wantDenied(t, tryPIN(t, e.tillTK, e.disabledCashier.Username, testPIN), http.StatusForbidden, "ACCOUNT_DISABLED")
	wantDenied(t, tryPIN(t, e.tillTK, e.disabledCashier.Username, "0000"), http.StatusUnauthorized, "UNAUTHORIZED")
}

func TestWrongPasswordsLockTheAccount(t *testing.T) {
	e := setup(t)
	user := newUser(t, e.roleCashier, e.branchTK)
	for i := 0; i < 5; i++ {
		wantStatus(t, tryLogin(t, user.Username, "wrong-password"), http.StatusUnauthorized)
	}
	wantDenied(t, tryLogin(t, user.Username, "wrong-password"), http.StatusTooManyRequests, "ACCOUNT_LOCKED")
	wantDenied(t, tryLogin(t, user.Username, testPassword), http.StatusTooManyRequests, "ACCOUNT_LOCKED")
}

func TestAnUnknownUsernameLocksTheSameWay(t *testing.T) {
	setup(t)
	name := unique("ghost")
	for i := 0; i < 5; i++ {
		wantStatus(t, tryLogin(t, name, "wrong-password"), http.StatusUnauthorized)
	}
	wantDenied(t, tryLogin(t, name, "wrong-password"), http.StatusTooManyRequests, "ACCOUNT_LOCKED")
	// MySQL treats these as the same username, so the lockout has to as well.
	wantDenied(t, tryLogin(t, "GHOST"+name[5:], "wrong-password"), http.StatusTooManyRequests, "ACCOUNT_LOCKED")
	// PIN attempts on a username share the count, as they do on a real account.
	wantDenied(t, tryPIN(t, env.tillTK, name, "0000"), http.StatusTooManyRequests, "ACCOUNT_LOCKED")
}

func TestPINGuessesAreCappedAcrossTills(t *testing.T) {
	e := setup(t)
	user := newUser(t, e.roleCashier, e.branchTK)
	tills := []string{e.tillTK, e.tillTK2, e.tillBKK, e.tillTK, e.tillTK2}
	for _, till := range tills {
		wantStatus(t, tryPIN(t, till, user.Username, "0000"), http.StatusUnauthorized)
	}
	for _, till := range []string{e.tillTK, e.tillTK2, e.tillBKK} {
		wantDenied(t, tryPIN(t, till, user.Username, testPIN), http.StatusTooManyRequests, "ACCOUNT_LOCKED")
	}
	// They lock password sign-in too: it's one count per person.
	wantDenied(t, tryLogin(t, user.Username, testPassword), http.StatusTooManyRequests, "ACCOUNT_LOCKED")
}

func TestPINFailuresPileUpOverTheDay(t *testing.T) {
	e := setup(t)
	user := newUser(t, e.roleCashier, e.branchTK)
	var till models.Device
	if err := e.db.Where("device_key = ?", e.tillTK).First(&till).Error; err != nil {
		t.Fatal(err)
	}
	// Failures spread out so the 5-minute lockout never sees them.
	addFailures := func(n int, age time.Duration) {
		t.Helper()
		for i := 0; i < n; i++ {
			row := models.LoginAttempt{UserID: user.ID, DeviceID: &till.ID, Success: false, IP: "192.0.2.1", CreatedAt: time.Now().Add(-age)}
			if err := e.db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}

	addFailures(19, 2*time.Hour)
	wantStatus(t, tryPIN(t, e.tillTK, user.Username, testPIN), http.StatusOK)

	addFailures(1, 2*time.Hour)
	wantDenied(t, tryPIN(t, e.tillTK, user.Username, testPIN), http.StatusTooManyRequests, "ACCOUNT_LOCKED")

	// Password sign-in isn't what's being guessed, so it's left open.
	wantStatus(t, tryLogin(t, user.Username, testPassword), http.StatusOK)

	t.Run("failures older than a day don't count", func(t *testing.T) {
		setup(t)
		addFailures(30, 25*time.Hour)
		wantStatus(t, tryPIN(t, e.tillTK, user.Username, testPIN), http.StatusOK)
	})
}

func TestAUserWithNoPINLooksLikeOneWithAWrongPIN(t *testing.T) {
	e := setup(t)
	withPIN := newUser(t, e.roleCashier, e.branchTK)
	withoutPIN := newUser(t, e.roleCashier, e.branchTK)
	e.db.Exec("UPDATE users SET pin_hash = NULL WHERE id = ?", withoutPIN.ID)

	a := tryPIN(t, e.tillTK, withPIN.Username, "0000")
	b := tryPIN(t, e.tillTK, withoutPIN.Username, "0000")
	if a.Status != http.StatusUnauthorized || a.Raw != b.Raw {
		t.Fatalf("responses differ:\n with PIN:    %s\n without PIN: %s", a, b)
	}
	// Any PIN at all must fail, and they lock out like everyone else.
	for i := 0; i < 4; i++ {
		wantStatus(t, tryPIN(t, e.tillTK, withoutPIN.Username, testPIN), http.StatusUnauthorized)
	}
	wantDenied(t, tryPIN(t, e.tillTK, withoutPIN.Username, testPIN), http.StatusTooManyRequests, "ACCOUNT_LOCKED")
}

func TestLoginTimingDoesntRevealUsernames(t *testing.T) {
	e := setup(t)
	median := func(attempt func(i int)) time.Duration {
		samples := make([]time.Duration, 0, 8)
		for i := 0; i < 8; i++ {
			start := time.Now()
			attempt(i)
			samples = append(samples, time.Since(start))
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		return samples[len(samples)/2]
	}
	known := median(func(int) {
		tryLogin(t, newUser(t, e.roleCashier).Username, "wrong-password")
	})
	unknown := median(func(int) {
		tryLogin(t, unique("ghost"), "wrong-password")
	})
	if known > unknown*3 || unknown > known*3 {
		t.Fatalf("known %v vs unknown %v", known, unknown)
	}
}

func TestATillSessionCantReachTheBackOffice(t *testing.T) {
	e := setup(t)
	r := tryPIN(t, e.tillTK, e.owner.Username, testPIN)
	s := decodeSession(t, r)

	for _, p := range s.Permissions {
		if p == "system.reset" || p == "user.create" || p == "report.sales" {
			t.Errorf("till session was told it has %s", p)
		}
	}
	if !slices.Contains(s.Permissions, "pos.sell") {
		t.Error("till session should be able to sell")
	}
	for _, path := range []string{"/users", "/roles", "/platform/backups", "/reports/dashboard", "/reports/activity-log"} {
		wantStatus(t, call(t, http.MethodGet, path, s.Access, nil), http.StatusForbidden)
	}
	wantStatus(t, call(t, http.MethodPost, "/platform/reset-for-production", s.Access, map[string]string{"confirm": "RESET"}), http.StatusForbidden)

	t.Run("/auth/me tells the browser the same", func(t *testing.T) {
		got := me(t, s.Access)
		wantStatus(t, got, http.StatusOK)
		perms, _ := got.field(t, "permissions").([]interface{})
		for _, p := range perms {
			if p == "system.reset" {
				t.Fatal("/auth/me reported system.reset for a till session")
			}
		}
	})
	t.Run("refreshing keeps it a till session", func(t *testing.T) {
		refreshed := decodeSession(t, tryRefresh(t, s.Refresh))
		if slices.Contains(refreshed.Permissions, "system.reset") {
			t.Fatal("refresh widened the session")
		}
		wantStatus(t, call(t, http.MethodGet, "/users", refreshed.Access, nil), http.StatusForbidden)
	})
	t.Run("password sign-in isn't affected", func(t *testing.T) {
		wantStatus(t, call(t, http.MethodGet, "/users", login(t, e.owner).Access, nil), http.StatusOK)
	})
}

func TestStaffCanOnlySignInToTillsAtTheirBranch(t *testing.T) {
	e := setup(t)
	wantDenied(t, tryPIN(t, e.tillBKK, e.cashierTK.Username, testPIN), http.StatusForbidden, "TILL_NOT_ALLOWED")
	// A wrong PIN doesn't find out where someone works.
	wantDenied(t, tryPIN(t, e.tillBKK, e.cashierTK.Username, "0000"), http.StatusUnauthorized, "UNAUTHORIZED")
	wantStatus(t, tryPIN(t, e.tillTK, e.cashierTK.Username, testPIN), http.StatusOK)
	wantStatus(t, tryPIN(t, e.tillBKK, e.owner.Username, testPIN), http.StatusOK)
}

func TestForwardedHeadersCantForgeTheRecordedAddress(t *testing.T) {
	e := setup(t)
	user := newUser(t, e.roleCashier, e.branchTK)
	tryLogin(t, user.Username, "wrong-password", "X-Forwarded-For", "203.0.113.99", "X-Real-IP", "203.0.113.99")

	var ip string
	e.db.Raw("SELECT ip FROM login_attempts WHERE user_id = ? ORDER BY id DESC LIMIT 1", user.ID).Scan(&ip)
	if ip == "" || ip == "203.0.113.99" {
		t.Fatalf("recorded address is %q", ip)
	}
}
