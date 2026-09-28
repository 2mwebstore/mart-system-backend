package middleware

import (
	"slices"

	"github.com/gin-gonic/gin"

	"com-mart/backend/internal/utils"
)

// RequirePermission 403s unless the authenticated user's role grants the
// given permission key (see build spec §5 for the permission list). Must
// run after RequireAuth.
func RequirePermission(key string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !slices.Contains(PermissionsFrom(c), key) {
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
		granted := PermissionsFrom(c)
		for _, k := range keys {
			if slices.Contains(granted, k) {
				c.Next()
				return
			}
		}
		utils.RespondError(c, utils.ErrForbidden)
		c.Abort()
	}
}
