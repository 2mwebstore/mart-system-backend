package tests

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"com-mart/backend/internal/models"
	"com-mart/backend/internal/utils"
)

func id(v uint64) string { return fmt.Sprint(v) }

func me(t *testing.T, token string) response {
	t.Helper()
	return call(t, http.MethodGet, "/auth/me", token, nil)
}

func TestLogoutEndsTheSession(t *testing.T) {
	e := setup(t)
	user := newUser(t, e.roleManager, e.branchTK)
	s := login(t, user)
	wantStatus(t, me(t, s.Access), http.StatusOK)

	wantStatus(t, call(t, http.MethodPost, "/auth/logout", "", gin.H{"refresh_token": s.Refresh}), http.StatusOK)

	wantStatus(t, me(t, s.Access), http.StatusUnauthorized)
	wantStatus(t, tryRefresh(t, s.Refresh), http.StatusUnauthorized)
}

func TestLogoutLeavesOtherDevicesSignedIn(t *testing.T) {
	e := setup(t)
	user := newUser(t, e.roleManager, e.branchTK)
	other := login(t, user)
	s := login(t, user)

	wantStatus(t, call(t, http.MethodPost, "/auth/logout", "", gin.H{"refresh_token": s.Refresh}), http.StatusOK)

	// Its access token went with the version bump, but it recovers by itself.
	wantStatus(t, me(t, other.Access), http.StatusUnauthorized)
	recovered := decodeSession(t, tryRefresh(t, other.Refresh))
	wantStatus(t, me(t, recovered.Access), http.StatusOK)
}

func TestDisablingAUserEndsEverythingAtOnce(t *testing.T) {
	e := setup(t)
	owner := login(t, e.owner).Access
	user := newUser(t, e.roleManager, e.branchTK)
	s := login(t, user)

	wantStatus(t, call(t, http.MethodPut, "/users/"+id(user.ID), owner, updateBody(user.Username, e.roleManager, false, []uint64{e.branchTK})), http.StatusOK)

	wantStatus(t, call(t, http.MethodGet, "/reports/dashboard?branch_id="+id(e.branchTK), s.Access, nil), http.StatusUnauthorized)
	wantStatus(t, call(t, http.MethodPost, "/expenses", s.Access, gin.H{"branch_id": e.branchTK, "category": "OTHER", "amount_cents": 1, "expense_date": today()}), http.StatusUnauthorized)
	wantStatus(t, tryRefresh(t, s.Refresh), http.StatusUnauthorized)
	wantDenied(t, tryLogin(t, user.Username, testPassword), http.StatusForbidden, "ACCOUNT_DISABLED")
}

// Going through the API also raises the token version, which would hide a missing
// check on the account itself. This covers a change made any other way.
func TestAnAccountChangedOutsideTheAPIIsStillRefused(t *testing.T) {
	e := setup(t)
	for name, change := range map[string]string{
		"disabled": "UPDATE users SET active = 0 WHERE id = ?",
		"deleted":  "UPDATE users SET deleted_at = NOW() WHERE id = ?",
	} {
		t.Run(name, func(t *testing.T) {
			user := newUser(t, e.roleManager, e.branchTK)
			s := login(t, user)
			wantStatus(t, me(t, s.Access), http.StatusOK)

			if err := env.db.Exec(change, user.ID).Error; err != nil {
				t.Fatal(err)
			}

			wantStatus(t, me(t, s.Access), http.StatusUnauthorized)
			wantStatus(t, tryRefresh(t, s.Refresh), http.StatusUnauthorized)
		})
	}
}

func TestReEnablingDoesNotReviveOldSessions(t *testing.T) {
	e := setup(t)
	owner := login(t, e.owner).Access
	user := newUser(t, e.roleManager, e.branchTK)
	s := login(t, user)
	path := "/users/" + id(user.ID)

	wantStatus(t, call(t, http.MethodPut, path, owner, updateBody(user.Username, e.roleManager, false, []uint64{e.branchTK})), http.StatusOK)
	wantStatus(t, call(t, http.MethodPut, path, owner, updateBody(user.Username, e.roleManager, true, []uint64{e.branchTK})), http.StatusOK)

	wantStatus(t, me(t, s.Access), http.StatusUnauthorized)
	wantStatus(t, tryRefresh(t, s.Refresh), http.StatusUnauthorized)
	login(t, user)
}

func TestRoleChangeAppliesToTheNextRequest(t *testing.T) {
	e := setup(t)
	owner := login(t, e.owner).Access
	user := newUser(t, e.roleManager, e.branchTK)
	s := login(t, user)
	dashboard := "/reports/dashboard?branch_id=" + id(e.branchTK)
	wantStatus(t, call(t, http.MethodGet, dashboard, s.Access, nil), http.StatusOK)

	wantStatus(t, call(t, http.MethodPut, "/users/"+id(user.ID), owner, updateBody(user.Username, e.roleCashier, true, []uint64{e.branchTK})), http.StatusOK)

	wantStatus(t, call(t, http.MethodGet, dashboard, s.Access, nil), http.StatusForbidden)
	wantStatus(t, me(t, s.Access), http.StatusOK) // still signed in, just with less
}

func TestPermissionGrantAndRevokeApplyToTheNextRequest(t *testing.T) {
	e := setup(t)
	owner := login(t, e.owner).Access
	role := newRole(t, "pos.sell")
	s := login(t, newUser(t, role, e.branchTK))
	var name string
	e.db.Raw("SELECT name FROM roles WHERE id = ?", role).Scan(&name)
	dashboard := "/reports/dashboard?branch_id=" + id(e.branchTK)
	setPermissions := func(perms ...string) {
		t.Helper()
		wantStatus(t, call(t, http.MethodPut, "/roles/"+id(role), owner, gin.H{"name": name, "permissions": perms}), http.StatusOK)
	}

	wantStatus(t, call(t, http.MethodGet, dashboard, s.Access, nil), http.StatusForbidden)
	setPermissions("pos.sell", "report.sales")
	wantStatus(t, call(t, http.MethodGet, dashboard, s.Access, nil), http.StatusOK)
	setPermissions("pos.sell")
	wantStatus(t, call(t, http.MethodGet, dashboard, s.Access, nil), http.StatusForbidden)
}

func TestBranchRemovalAppliesToTheNextRequest(t *testing.T) {
	e := setup(t)
	owner := login(t, e.owner).Access
	user := newUser(t, e.roleManager, e.branchTK, e.branchBKK)
	s := login(t, user)
	bkkSales := "/sales?branch_id=" + id(e.branchBKK)
	wantStatus(t, call(t, http.MethodGet, bkkSales, s.Access, nil), http.StatusOK)

	wantStatus(t, call(t, http.MethodPut, "/users/"+id(user.ID), owner, updateBody(user.Username, e.roleManager, true, []uint64{e.branchTK})), http.StatusOK)

	wantStatus(t, call(t, http.MethodGet, bkkSales, s.Access, nil), http.StatusForbidden)
}

func TestNewCredentialsEndExistingSessions(t *testing.T) {
	e := setup(t)
	owner := login(t, e.owner).Access

	cases := map[string][]string{
		"password": {"password", "a-brand-new-password"},
		"PIN":      {"pin", "9876"},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			user := newUser(t, e.roleManager, e.branchTK)
			s := login(t, user)
			r := call(t, http.MethodPut, "/users/"+id(user.ID), owner, updateBody(user.Username, e.roleManager, true, []uint64{e.branchTK}, extra...))
			wantStatus(t, r, http.StatusOK)

			wantStatus(t, me(t, s.Access), http.StatusUnauthorized)
			wantStatus(t, tryRefresh(t, s.Refresh), http.StatusUnauthorized)
		})
	}
	t.Run("the new password works and the old one doesn't", func(t *testing.T) {
		user := newUser(t, e.roleManager, e.branchTK)
		r := call(t, http.MethodPut, "/users/"+id(user.ID), owner, updateBody(user.Username, e.roleManager, true, []uint64{e.branchTK}, "password", "a-brand-new-password"))
		wantStatus(t, r, http.StatusOK)
		wantStatus(t, tryLogin(t, user.Username, testPassword), http.StatusUnauthorized)
		wantStatus(t, tryLogin(t, user.Username, "a-brand-new-password"), http.StatusOK)
	})
	t.Run("too long a password is refused rather than stored broken", func(t *testing.T) {
		user := newUser(t, e.roleManager, e.branchTK)
		long := string(make([]byte, 73))
		r := call(t, http.MethodPut, "/users/"+id(user.ID), owner, updateBody(user.Username, e.roleManager, true, []uint64{e.branchTK}, "password", long))
		wantStatus(t, r, http.StatusUnprocessableEntity)
		wantStatus(t, tryLogin(t, user.Username, testPassword), http.StatusOK)
	})
}

func TestResetPINEndsSessionsAndClearsLockout(t *testing.T) {
	e := setup(t)
	owner := login(t, e.owner).Access
	user := newUser(t, e.roleCashier, e.branchTK)
	s := decodeSession(t, tryPIN(t, e.tillTK, user.Username, testPIN))
	for i := 0; i < 5; i++ {
		tryPIN(t, e.tillTK, user.Username, "0000")
	}
	wantDenied(t, tryPIN(t, e.tillTK, user.Username, testPIN), http.StatusTooManyRequests, "ACCOUNT_LOCKED")

	reset := call(t, http.MethodPost, "/users/"+id(user.ID)+"/reset-pin", owner, gin.H{})
	wantStatus(t, reset, http.StatusOK)
	pin := reset.field(t, "pin").(string)

	wantStatus(t, me(t, s.Access), http.StatusUnauthorized)
	wantStatus(t, tryRefresh(t, s.Refresh), http.StatusUnauthorized)
	wantStatus(t, tryPIN(t, e.tillTK, user.Username, pin), http.StatusOK)
}

func TestDeletingAUserEndsTheirSessions(t *testing.T) {
	e := setup(t)
	owner := login(t, e.owner).Access
	user := newUser(t, e.roleManager, e.branchTK)
	s := login(t, user)

	wantStatus(t, call(t, http.MethodDelete, "/users/"+id(user.ID), owner, nil), http.StatusOK)

	wantStatus(t, me(t, s.Access), http.StatusUnauthorized)
	wantStatus(t, tryRefresh(t, s.Refresh), http.StatusUnauthorized)
}

// ageRevocation makes a rotated token look as if it was rotated long enough ago
// that replaying it can't be two tabs racing.
func ageRevocation(t *testing.T, refreshToken string) {
	t.Helper()
	err := env.db.Model(&models.RefreshToken{}).
		Where("token_hash = ?", utils.HashToken(refreshToken)).
		Update("revoked_at", time.Now().Add(-time.Hour)).Error
	if err != nil {
		t.Fatal(err)
	}
}

func tokenVersion(t *testing.T, userID uint64) int64 {
	t.Helper()
	return dbInt(t, "SELECT token_version FROM users WHERE id = ?", userID)
}

func TestReplayingARotatedRefreshTokenEndsThatSession(t *testing.T) {
	e := setup(t)
	user := newUser(t, e.roleManager, e.branchTK)
	otherDevice := login(t, user)
	first := login(t, user)
	second := decodeSession(t, tryRefresh(t, first.Refresh))
	ageRevocation(t, first.Refresh)

	wantStatus(t, tryRefresh(t, first.Refresh), http.StatusUnauthorized)

	// The thief and the victim share this login, and both lose it.
	wantStatus(t, tryRefresh(t, second.Refresh), http.StatusUnauthorized)
	wantStatus(t, me(t, second.Access), http.StatusUnauthorized)
	// Nothing ties another login to the stolen token.
	wantStatus(t, tryRefresh(t, otherDevice.Refresh), http.StatusOK)
}

func TestReplayJustAfterRotationIsOnlyRefused(t *testing.T) {
	e := setup(t)
	user := newUser(t, e.roleManager, e.branchTK)
	first := login(t, user)
	second := decodeSession(t, tryRefresh(t, first.Refresh))

	// Two tabs refreshing at once look like this: don't sign the user out.
	wantStatus(t, tryRefresh(t, first.Refresh), http.StatusUnauthorized)

	wantStatus(t, me(t, second.Access), http.StatusOK)
	wantStatus(t, tryRefresh(t, second.Refresh), http.StatusOK)
}

func TestReplayingADeadTokenDoesNothingMore(t *testing.T) {
	e := setup(t)
	user := newUser(t, e.roleManager, e.branchTK)
	first := login(t, user)
	decodeSession(t, tryRefresh(t, first.Refresh))
	ageRevocation(t, first.Refresh)
	wantStatus(t, tryRefresh(t, first.Refresh), http.StatusUnauthorized)
	version := tokenVersion(t, user.ID)
	fresh := login(t, user)

	// An attacker holding the old token can't use it to keep signing them out.
	for i := 0; i < 3; i++ {
		wantStatus(t, tryRefresh(t, first.Refresh), http.StatusUnauthorized)
	}

	if got := tokenVersion(t, user.ID); got != version {
		t.Fatalf("token version moved from %d to %d", version, got)
	}
	wantStatus(t, me(t, fresh.Access), http.StatusOK)
}

func TestSimultaneousRefreshesRotateOnce(t *testing.T) {
	e := setup(t)
	s := login(t, newUser(t, e.roleManager, e.branchTK))

	var ok atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tryRefresh(t, s.Refresh).Status == http.StatusOK {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := ok.Load(); got != 1 {
		t.Fatalf("want exactly one winner, got %d", got)
	}
}

func TestBadRefreshTokensAreRefused(t *testing.T) {
	e := setup(t)
	wantStatus(t, tryRefresh(t, "not-a-real-token"), http.StatusUnauthorized)

	s := login(t, newUser(t, e.roleManager, e.branchTK))
	err := e.db.Model(&models.RefreshToken{}).
		Where("token_hash = ?", utils.HashToken(s.Refresh)).
		Update("expires_at", time.Now().Add(-time.Minute)).Error
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, tryRefresh(t, s.Refresh), http.StatusUnauthorized)
}

func TestForgedAccessTokensAreRefused(t *testing.T) {
	e := setup(t)
	secret := []byte(e.cfg.JWTAccessSecret)
	now := time.Now().Unix()
	sign := func(claims jwt.MapClaims) string {
		t.Helper()
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	valid := func(userID uint64, version int64) jwt.MapClaims {
		return jwt.MapClaims{"user_id": userID, "token_version": version, "iss": utils.TokenIssuer, "aud": utils.TokenAudience, "iat": now, "exp": now + 60}
	}
	cashier := login(t, e.cashierTK).Access
	version := tokenVersion(t, e.cashierTK.ID)

	t.Run("the old token format, even correctly signed", func(t *testing.T) {
		old := sign(jwt.MapClaims{"user_id": e.owner.ID, "role_id": e.roleOwner, "role_name": "Owner", "permissions": allPermissions, "branch_ids": []uint64{e.branchTK}, "iat": now, "exp": now + 60})
		wantStatus(t, call(t, http.MethodGet, "/users", old, nil), http.StatusUnauthorized)
	})
	t.Run("a user that doesn't exist, correctly signed, claiming Owner", func(t *testing.T) {
		c := valid(99999999, 0)
		c["role_name"], c["permissions"] = "Owner", allPermissions
		wantStatus(t, call(t, http.MethodGet, "/users", sign(c), nil), http.StatusUnauthorized)
	})
	t.Run("a real cashier, correctly signed, claiming Owner powers", func(t *testing.T) {
		c := valid(e.cashierTK.ID, version)
		c["role_id"], c["role_name"], c["permissions"] = e.roleOwner, "Owner", allPermissions
		wantStatus(t, call(t, http.MethodGet, "/users", sign(c), nil), http.StatusForbidden)
	})
	t.Run("a stale token version", func(t *testing.T) {
		wantStatus(t, me(t, sign(valid(e.cashierTK.ID, version+1))), http.StatusUnauthorized)
	})
	t.Run("the real token still works", func(t *testing.T) {
		wantStatus(t, me(t, cashier), http.StatusOK)
	})
}
