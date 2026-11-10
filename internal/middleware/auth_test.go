package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"com-mart/backend/internal/utils"
)

const testSecret = "unit-test-secret-unit-test-secret"

func testPrincipal() *Principal {
	return &Principal{
		UserID: 7, RoleID: 3, RoleName: "Cashier", TokenVersion: 2,
		Permissions: []string{"pos.sell", "report.sales"}, BranchIDs: []uint64{1},
	}
}

func issue(t *testing.T, claims utils.AccessClaims) string {
	t.Helper()
	token, err := utils.GenerateAccessToken(testSecret, time.Minute, claims)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

type seenContext struct {
	reached bool
	userID  uint64
	role    string
	perms   []string
	branch  []uint64
}

// runAuth sends one request through RequireAuth and reports what a handler
// behind it would have seen.
func runAuth(t *testing.T, header string, load PrincipalLoader) (*httptest.ResponseRecorder, seenContext) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	return serveAuth(t, req, load)
}

func serveAuth(t *testing.T, req *http.Request, load PrincipalLoader) (*httptest.ResponseRecorder, seenContext) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var seen seenContext
	r := gin.New()
	r.GET("/x", RequireAuth(testSecret, load), func(c *gin.Context) {
		seen = seenContext{true, UserIDFrom(c), RoleNameFrom(c), PermissionsFrom(c), BranchIDsFrom(c)}
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w, seen
}

func TestRequireAuthUsesTheDatabaseNotTheToken(t *testing.T) {
	token := issue(t, utils.AccessClaims{UserID: 7, TokenVersion: 2})
	w, seen := runAuth(t, "Bearer "+token, func(id, _ uint64) (*Principal, error) {
		if id != 7 {
			t.Fatalf("looked up user %d", id)
		}
		return testPrincipal(), nil
	})
	if w.Code != http.StatusOK || !seen.reached {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if seen.userID != 7 || seen.role != "Cashier" || !slices.Equal(seen.branch, []uint64{1}) || !slices.Equal(seen.perms, testPrincipal().Permissions) {
		t.Fatalf("context not filled from the loader: %+v", seen)
	}
}

// A token signed with the real secret but claiming Owner powers grants nothing:
// this is what stops a leaked secret or an old-format token from being trusted.
func TestRequireAuthIgnoresPowersClaimedInTheToken(t *testing.T) {
	now := time.Now().Unix()
	forged, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": 7, "token_version": 2, "iss": utils.TokenIssuer, "aud": utils.TokenAudience, "iat": now, "exp": now + 60,
		"role_id": 1, "role_name": "Owner", "permissions": []string{"system.reset", "user.view", "user.create", "user.update", "user.delete"}, "branch_ids": []int{1, 2, 3},
	}).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatal(err)
	}
	w, seen := runAuth(t, "Bearer "+forged, func(_, _ uint64) (*Principal, error) { return testPrincipal(), nil })
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	if seen.role == "Owner" || slices.Contains(seen.perms, "system.reset") || slices.Contains(seen.branch, 2) {
		t.Fatalf("claims from the token were believed: %+v", seen)
	}
}

func TestRequireAuthRefuses(t *testing.T) {
	valid := issue(t, utils.AccessClaims{UserID: 7, TokenVersion: 2})
	active := func(_, _ uint64) (*Principal, error) { return testPrincipal(), nil }
	cases := []struct {
		name   string
		header string
		load   PrincipalLoader
	}{
		{"no header", "", active},
		{"empty bearer", "Bearer ", active},
		{"wrong scheme", "Basic " + valid, active},
		{"garbage", "Bearer not.a.jwt", active},
		{"disabled or deleted user", "Bearer " + valid, func(_, _ uint64) (*Principal, error) { return nil, nil }},
		{"older token version", "Bearer " + issue(t, utils.AccessClaims{UserID: 7, TokenVersion: 1}), active},
		{"newer token version", "Bearer " + issue(t, utils.AccessClaims{UserID: 7, TokenVersion: 3}), active},
		{"unknown scope", "Bearer " + issue(t, utils.AccessClaims{UserID: 7, TokenVersion: 2, Scope: "admin"}), active},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, seen := runAuth(t, tc.header, tc.load)
			if w.Code != http.StatusUnauthorized || seen.reached {
				t.Fatalf("want 401 and no handler, got %d reached=%v", w.Code, seen.reached)
			}
		})
	}
}

func TestRequireAuthFailsClosedWhenTheLookupFails(t *testing.T) {
	token := issue(t, utils.AccessClaims{UserID: 7, TokenVersion: 2})
	w, seen := runAuth(t, "Bearer "+token, func(_, _ uint64) (*Principal, error) {
		return nil, errors.New("dial tcp 10.0.0.5:3306: connection refused")
	})
	if w.Code != http.StatusInternalServerError || seen.reached {
		t.Fatalf("want 500 and no handler, got %d reached=%v", w.Code, seen.reached)
	}
	if strings.Contains(w.Body.String(), "10.0.0.5") {
		t.Fatalf("internal detail in the response: %s", w.Body.String())
	}
}

func TestRequireAuthCutsATillSessionDownToTillWork(t *testing.T) {
	token := issue(t, utils.AccessClaims{UserID: 7, TokenVersion: 2, Scope: utils.ScopePOS, BranchID: 1})
	_, seen := runAuth(t, "Bearer "+token, func(_, _ uint64) (*Principal, error) {
		p := testPrincipal()
		p.BranchID = 1
		p.RoleName = "Owner"
		p.Permissions = []string{"pos.sell", "system.reset", "user.view", "user.create", "user.update", "user.delete"}
		return p, nil
	})
	if !slices.Equal(seen.perms, []string{"pos.sell"}) {
		t.Fatalf("till session should only keep till permissions, got %v", seen.perms)
	}
}

// whichBranch sends a request and reports the branch RequireAuth asked the
// loader for.
func whichBranch(t *testing.T, claims utils.AccessClaims, target string, header string) (int, uint64) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Authorization", "Bearer "+issue(t, claims))
	if header != "" {
		req.Header.Set(BranchHeader, header)
	}
	var asked uint64
	w, _ := serveAuth(t, req, func(_, branchID uint64) (*Principal, error) {
		asked = branchID
		p := testPrincipal()
		p.BranchID = branchID
		return p, nil
	})
	return w.Code, asked
}

func TestRequireAuthWorksAtTheBranchTheRequestIsAbout(t *testing.T) {
	web := utils.AccessClaims{UserID: 7, TokenVersion: 2}
	cases := []struct {
		name, target, header string
		want                 uint64
	}{
		{"nothing named", "/x", "", 0},
		{"the active branch", "/x", "3", 3},
		{"the branch asked about", "/x?branch_id=4", "", 4},
		{"the branch asked about beats the active one", "/x?branch_id=4", "3", 4},
		{"all branches falls back to the active one", "/x?branch_id=all", "3", 3},
		{"branch 0 is no branch", "/x?branch_id=0", "3", 3},
		{"junk is ignored", "/x?branch_id=x", "y", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, asked := whichBranch(t, web, tc.target, tc.header)
			if code != http.StatusOK || asked != tc.want {
				t.Fatalf("got %d asking for branch %d, want 200 and branch %d", code, asked, tc.want)
			}
		})
	}
}

func TestRequireAuthKeepsATillAtItsBranch(t *testing.T) {
	till := utils.AccessClaims{UserID: 7, TokenVersion: 2, Scope: utils.ScopePOS, BranchID: 5}
	code, asked := whichBranch(t, till, "/x?branch_id=4", "3")
	if code != http.StatusOK || asked != 5 {
		t.Fatalf("got %d asking for branch %d, want the till's branch 5", code, asked)
	}

	t.Run("a till token with no branch is refused", func(t *testing.T) {
		code, _ := whichBranch(t, utils.AccessClaims{UserID: 7, TokenVersion: 2, Scope: utils.ScopePOS}, "/x", "3")
		if code != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401", code)
		}
	})

	t.Run("a till whose user left its branch is refused", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer "+issue(t, till))
		w, seen := serveAuth(t, req, func(_, _ uint64) (*Principal, error) {
			return testPrincipal(), nil // their main role: branch 5 isn't theirs
		})
		if w.Code != http.StatusUnauthorized || seen.reached {
			t.Fatalf("got %d (handler reached: %v), want 401", w.Code, seen.reached)
		}
	})
}
