package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func runGate(t *testing.T, held []string, gate gin.HandlerFunc) int {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		c.Set(CtxPermissions, held)
		c.Next()
	}, gate, func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	return w.Code
}

func TestRequirePermission(t *testing.T) {
	if code := runGate(t, []string{"pos.sell"}, RequirePermission("pos.sell")); code != http.StatusOK {
		t.Fatalf("held: want 200, got %d", code)
	}
	if code := runGate(t, []string{"pos.sell"}, RequirePermission("user.create")); code != http.StatusForbidden {
		t.Fatalf("not held: want 403, got %d", code)
	}
	if code := runGate(t, nil, RequirePermission("pos.sell")); code != http.StatusForbidden {
		t.Fatalf("no permissions: want 403, got %d", code)
	}
}

func TestRequireAnyPermission(t *testing.T) {
	if code := runGate(t, []string{"b"}, RequireAnyPermission("a", "b")); code != http.StatusOK {
		t.Fatalf("one held: want 200, got %d", code)
	}
	if code := runGate(t, []string{"c"}, RequireAnyPermission("a", "b")); code != http.StatusForbidden {
		t.Fatalf("none held: want 403, got %d", code)
	}
}
