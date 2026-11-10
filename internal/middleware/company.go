package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"com-mart/backend/internal/models"
	"com-mart/backend/internal/utils"
)

var (
	// ErrNoCompany refuses a super admin on a company's routes: they manage
	// companies and see none of their data.
	ErrNoCompany = utils.NewAppError(http.StatusForbidden, "NO_COMPANY", "This page is for a company's staff. A super admin manages companies only.")
	// ErrNotSuperAdmin refuses a company's staff on the platform routes.
	ErrNotSuperAdmin = utils.NewAppError(http.StatusForbidden, "NOT_SUPER_ADMIN", "Only a super admin can do this.")
)

// RequireCompany 403s a caller who doesn't work for a company. Every route
// on a company's data sits behind it. Must run after RequireAuth.
func RequireCompany() gin.HandlerFunc {
	return func(c *gin.Context) {
		if CompanyIDFrom(c) == 0 {
			utils.RespondError(c, ErrNoCompany)
			c.Abort()
			return
		}
		c.Next()
	}
}

// RequireSuperAdmin 403s anyone but a super admin: a user with no company
// holding the Super admin role. Must run after RequireAuth.
func RequireSuperAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if CompanyIDFrom(c) != 0 || RoleNameFrom(c) != models.RoleSuperAdmin || ScopeFrom(c) != "" {
			utils.RespondError(c, ErrNotSuperAdmin)
			c.Abort()
			return
		}
		c.Next()
	}
}
