package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequirePermissionFallsBackToLivePermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	calls := 0
	LivePermissions = func(userID uint64) []string {
		calls++
		if userID == 7 {
			return []string{"invoice.manage", "pos.sell"}
		}
		return nil
	}
	defer func() { LivePermissions = nil; livePermissionCache.Clear() }()

	run := func(userID uint64, tokenPerms []string, need string) (int, []string) {
		w := httptest.NewRecorder()
		c, r := gin.CreateTestContext(w)
		var seen []string
		r.GET("/x", func(c *gin.Context) {
			c.Set(CtxUserID, userID)
			c.Set(CtxPermissions, tokenPerms)
			c.Next()
		}, RequirePermission(need), func(c *gin.Context) {
			seen = PermissionsFrom(c)
			c.Status(http.StatusOK)
		})
		c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
		r.HandleContext(c)
		return w.Code, seen
	}

	if code, _ := run(7, []string{"pos.sell"}, "pos.sell"); code != http.StatusOK || calls != 0 {
		t.Fatalf("granted by the token: want 200 with no lookup, got %d (%d lookups)", code, calls)
	}
	code, seen := run(7, []string{"pos.sell"}, "invoice.manage")
	if code != http.StatusOK {
		t.Fatalf("granted after sign-in: want 200, got %d", code)
	}
	if len(seen) != 2 {
		t.Fatalf("handlers should see the merged permissions, got %v", seen)
	}
	if code, _ := run(8, []string{"pos.sell"}, "invoice.manage"); code != http.StatusForbidden {
		t.Fatalf("not granted anywhere: want 403, got %d", code)
	}
	before := calls
	run(8, []string{"pos.sell"}, "invoice.manage")
	if calls != before {
		t.Fatalf("a repeat miss within the cache window shouldn't query again")
	}
}
