package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBranchScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name  string
		held  []uint64
		query string
		want  int
	}{
		{"own branch", []uint64{1, 2}, "?branch_id=2", http.StatusOK},
		{"other branch", []uint64{1}, "?branch_id=2", http.StatusForbidden},
		{"no branches at all", nil, "?branch_id=1", http.StatusForbidden},
		{"no branch asked for", []uint64{1}, "", http.StatusOK},
		{"all is left to the handler", []uint64{1}, "?branch_id=all", http.StatusOK},
		{"not a number", []uint64{1}, "?branch_id=1%20OR%201=1", http.StatusBadRequest},
		{"negative", []uint64{1}, "?branch_id=-2", http.StatusBadRequest},
		{"encoded digit", []uint64{1}, "?branch_id=%32", http.StatusForbidden},
		{"leading zero", []uint64{1}, "?branch_id=02", http.StatusForbidden},
		{"first of two values is what's checked and used", []uint64{1}, "?branch_id=2&branch_id=1", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/x", func(c *gin.Context) {
				c.Set(CtxBranchIDs, tc.held)
				c.Next()
			}, BranchScope(), func(c *gin.Context) { c.Status(http.StatusOK) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x"+tc.query, nil))
			if w.Code != tc.want {
				t.Fatalf("want %d, got %d", tc.want, w.Code)
			}
		})
	}
}
