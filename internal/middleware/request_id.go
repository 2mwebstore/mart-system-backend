package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const RequestIDKey = "request_id"
const LoggerKey = "logger"

// RequestID assigns a UUID per request (reusing an inbound X-Request-Id if
// present), echoes it back in the response header, and stashes a
// request-scoped zerolog logger in the context for handlers/services to use.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		reqID := c.GetHeader("X-Request-Id")
		if reqID == "" {
			reqID = uuid.NewString()
		}
		c.Set(RequestIDKey, reqID)
		c.Header("X-Request-Id", reqID)

		reqLogger := log.With().Str("request_id", reqID).Str("path", c.Request.URL.Path).Logger()
		c.Set(LoggerKey, reqLogger)

		c.Next()
	}
}

// LoggerFrom returns the request-scoped logger, falling back to the global
// logger if the middleware wasn't hit (e.g. in a unit test).
func LoggerFrom(c *gin.Context) zerolog.Logger {
	if l, ok := c.Get(LoggerKey); ok {
		if logger, ok := l.(zerolog.Logger); ok {
			return logger
		}
	}
	return log.Logger
}
