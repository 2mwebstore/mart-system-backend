package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"

	"com-mart/backend/internal/utils"
)

const (
	CtxUserID      = "auth_user_id"
	CtxRoleID      = "auth_role_id"
	CtxRoleName    = "auth_role_name"
	CtxPermissions = "auth_permissions"
	CtxBranchIDs   = "auth_branch_ids"
)

// RequireAuth validates the Bearer access token and populates the request
// context with the caller's identity/role/permissions/branch access, which
// RequirePermission and BranchScope read afterwards.
func RequireAuth(jwtAccessSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
			utils.RespondError(c, utils.ErrUnauthorized)
			c.Abort()
			return
		}

		claims, err := utils.ParseAccessToken(jwtAccessSecret, parts[1])
		if err != nil {
			utils.RespondError(c, utils.ErrUnauthorized)
			c.Abort()
			return
		}

		c.Set(CtxUserID, claims.UserID)
		c.Set(CtxRoleID, claims.RoleID)
		c.Set(CtxRoleName, claims.RoleName)
		c.Set(CtxPermissions, claims.Permissions)
		c.Set(CtxBranchIDs, claims.BranchIDs)

		c.Next()
	}
}

// UserIDFrom reads the authenticated user id set by RequireAuth.
func UserIDFrom(c *gin.Context) uint64 {
	v, _ := c.Get(CtxUserID)
	id, _ := v.(uint64)
	return id
}

func RoleNameFrom(c *gin.Context) string {
	v, _ := c.Get(CtxRoleName)
	name, _ := v.(string)
	return name
}

func PermissionsFrom(c *gin.Context) []string {
	v, _ := c.Get(CtxPermissions)
	perms, _ := v.([]string)
	return perms
}

func BranchIDsFrom(c *gin.Context) []uint64 {
	v, _ := c.Get(CtxBranchIDs)
	ids, _ := v.([]uint64)
	return ids
}
