package middleware

import (
	"slices"

	"github.com/gin-gonic/gin"

	"com-mart/backend/internal/utils"
)

// grants reports whether the caller holds any of keys. RequireAuth loaded the
// permissions from the database for this request.
func grants(c *gin.Context, keys ...string) bool {
	held := PermissionsFrom(c)
	for _, k := range keys {
		if slices.Contains(held, k) {
			return true
		}
	}
	return false
}

// RequirePermission 403s unless the authenticated user's role grants the
// given permission key (see build spec §5 for the permission list). Must
// run after RequireAuth.
func RequirePermission(key string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !grants(c, key) {
			utils.RespondError(c, utils.ErrForbidden)
			c.Abort()
			return
		}
		c.Next()
	}
}

// RequireAnyPermission 403s unless the user holds at least one of the given
// permission keys.
func RequireAnyPermission(keys ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !grants(c, keys...) {
			utils.RespondError(c, utils.ErrForbidden)
			c.Abort()
			return
		}
		c.Next()
	}
}
