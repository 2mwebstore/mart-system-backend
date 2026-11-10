package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"com-mart/backend/internal/utils"
)

// SecurityHeaders sets the response headers an API should always send. It only
// ever returns JSON (or an image), so its content policy can forbid everything.
// HSTS is production-only: on plain-HTTP local development it would do nothing
// useful, and a browser remembering it for localhost is a nuisance.
func SecurityHeaders(production bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		if production {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}

// MaxBodySize refuses a request body over limit bytes: straight away if its
// Content-Length says so, or when the handler's read reaches the limit if not
// (see bind, which turns that read error into the same 413).
func MaxBodySize(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > limit {
			utils.RespondError(c, utils.ErrPayloadTooLarge)
			c.Abort()
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}

// RateLimit allows perMinute requests per client address per minute, each
// middleware instance counting on its own. Zero or less turns it off.
func RateLimit(perMinute int) gin.HandlerFunc {
	if perMinute <= 0 {
		return func(c *gin.Context) { c.Next() }
	}
	limiter := utils.NewSlidingWindow(time.Minute, perMinute)
	retryAfter := strconv.Itoa(int(limiter.Window().Seconds()))
	return func(c *gin.Context) {
		if !limiter.Allow(c.ClientIP(), time.Now()) {
			c.Header("Retry-After", retryAfter)
			utils.RespondError(c, utils.ErrRateLimited)
			c.Abort()
			return
		}
		c.Next()
	}
}
