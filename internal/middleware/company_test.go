package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"com-mart/backend/internal/models"
	"com-mart/backend/internal/utils"
)

// throughGuard sends one signed-in request through RequireAuth and the
// guard, and reports the status and whether the handler behind was reached.
func throughGuard(t *testing.T, guard gin.HandlerFunc, scope string, p *Principal) (int, string, bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	reached := false
	r := gin.New()
	r.GET("/x", RequireAuth(testSecret, func(_, _ uint64) (*Principal, error) { return p, nil }), guard, func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+issue(t, utils.AccessClaims{UserID: p.UserID, TokenVersion: p.TokenVersion, Scope: scope, BranchID: p.BranchID}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code, w.Body.String(), reached
}

func superAdmin() *Principal {
	return &Principal{UserID: 1, RoleID: 1, RoleName: models.RoleSuperAdmin, Permissions: []string{}}
}

func companyOwner() *Principal {
	return &Principal{UserID: 2, CompanyID: 4, RoleID: 2, RoleName: models.RoleOwner, Permissions: []string{"system.manage", "system.reset"}, BranchIDs: []uint64{1}}
}

func TestRequireCompany(t *testing.T) {
	t.Run("lets a company's user through, with the company in the context", func(t *testing.T) {
		var seen uint64
		guard := func(c *gin.Context) {
			RequireCompany()(c)
			seen = CompanyIDFrom(c)
		}
		code, _, reached := throughGuard(t, guard, "", companyOwner())
		if code != http.StatusOK || !reached || seen != 4 {
			t.Fatalf("got %d, reached %v, company %d", code, reached, seen)
		}
	})
	t.Run("refuses the super admin", func(t *testing.T) {
		code, body, reached := throughGuard(t, RequireCompany(), "", superAdmin())
		if code != http.StatusForbidden || reached || !strings.Contains(body, "NO_COMPANY") {
			t.Fatalf("got %d %s, reached %v", code, body, reached)
		}
	})
}

func TestRequireSuperAdmin(t *testing.T) {
	t.Run("lets the super admin through", func(t *testing.T) {
		code, _, reached := throughGuard(t, RequireSuperAdmin(), "", superAdmin())
		if code != http.StatusOK || !reached {
			t.Fatalf("got %d, reached %v", code, reached)
		}
	})
	refused := map[string]struct {
		scope string
		p     *Principal
	}{
		"a company's Owner": {"", companyOwner()},
		// Each condition alone: a company, the role, a till session.
		"the Super admin role in a company": {"", &Principal{UserID: 3, CompanyID: 4, RoleName: models.RoleSuperAdmin}},
		"another role with no company":      {"", &Principal{UserID: 3, RoleName: models.RoleOwner}},
		// (RequireAuth already ends a till session with no branch; this one has one.)
		"a till session with no company": {utils.ScopePOS, &Principal{UserID: 1, RoleName: models.RoleSuperAdmin, BranchID: 1, BranchIDs: []uint64{1}}},
	}
	for name, c := range refused {
		t.Run("refuses "+name, func(t *testing.T) {
			code, body, reached := throughGuard(t, RequireSuperAdmin(), c.scope, c.p)
			if code != http.StatusForbidden || reached || !strings.Contains(body, "NOT_SUPER_ADMIN") {
				t.Fatalf("got %d %s, reached %v", code, body, reached)
			}
		})
	}
}
