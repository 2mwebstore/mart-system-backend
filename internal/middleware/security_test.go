package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSecurityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	get := func(production bool) http.Header {
		r := gin.New()
		r.Use(SecurityHeaders(production))
		r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
		return w.Header()
	}
	h := get(false)
	want := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
	}
	for name, value := range want {
		if h.Get(name) != value {
			t.Errorf("%s = %q, want %q", name, h.Get(name), value)
		}
	}
	if h.Get("Permissions-Policy") == "" {
		t.Error("Permissions-Policy missing")
	}
	if h.Get("Strict-Transport-Security") != "" {
		t.Error("HSTS must only be sent in production")
	}
	if !strings.Contains(get(true).Get("Strict-Transport-Security"), "max-age=") {
		t.Error("HSTS missing in production")
	}
}

func TestMaxBodySize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reached := false
	r := gin.New()
	r.POST("/x", MaxBodySize(10), func(c *gin.Context) {
		reached = true
		if _, err := io.ReadAll(c.Request.Body); err != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.Status(http.StatusOK)
	})
	send := func(body string, knownLength bool) int {
		reached = false
		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
		if !knownLength {
			req.ContentLength = -1 // what a chunked upload looks like
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	if code := send("0123456789", true); code != http.StatusOK {
		t.Fatalf("at the limit: want 200, got %d", code)
	}
	if code := send("0123456789x", true); code != http.StatusRequestEntityTooLarge || reached {
		t.Fatalf("declared too large: want 413 before the handler, got %d reached=%v", code, reached)
	}
	if code := send("0123456789x", false); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked and too large: want 413, got %d", code)
	}
}

func TestRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limited := func(perMinute int) *gin.Engine {
		r := gin.New()
		r.POST("/x", RateLimit(perMinute), func(c *gin.Context) { c.Status(http.StatusOK) })
		return r
	}
	send := func(r *gin.Engine, addr string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/x", nil)
		req.RemoteAddr = addr
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	r := limited(2)
	for i := 0; i < 2; i++ {
		if code := send(r, "192.0.2.1:1000").Code; code != http.StatusOK {
			t.Fatalf("within the limit: want 200, got %d", code)
		}
	}
	w := send(r, "192.0.2.1:1001")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("over the limit: want 429, got %d", w.Code)
	}
	if retry, err := strconv.Atoi(w.Header().Get("Retry-After")); err != nil || retry <= 0 {
		t.Fatalf("Retry-After = %q", w.Header().Get("Retry-After"))
	}
	if code := send(r, "192.0.2.2:1000").Code; code != http.StatusOK {
		t.Fatalf("another client has its own allowance, got %d", code)
	}

	off := limited(0)
	for i := 0; i < 50; i++ {
		if code := send(off, "192.0.2.1:1000").Code; code != http.StatusOK {
			t.Fatalf("0 turns it off, got %d", code)
		}
	}
}
