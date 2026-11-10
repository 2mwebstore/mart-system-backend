package middleware

import (
	"strconv"
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
	CtxBranchID    = "auth_branch_id"
	CtxScope       = "auth_scope"
	CtxCompanyID   = "auth_company_id"

	// BranchHeader names the branch the admin app is working at (its active
	// branch). The caller's role there decides what they may do.
	BranchHeader = "X-Branch-Id"
)

// Principal is what a user may do right now, as the database says.
type Principal struct {
	UserID uint64
	// CompanyID is the company they work for, 0 for a super admin.
	CompanyID uint64
	// The role they hold at the branch they're working at, and its permissions.
	RoleID      uint64
	RoleName    string
	Permissions []string
	// BranchIDs are their branches where they hold that same role.
	BranchIDs []uint64
	// BranchID is the branch whose role this is, 0 if the branch asked for
	// isn't one of theirs and they got their main role instead.
	BranchID     uint64
	TokenVersion uint64
}

// PrincipalLoader returns an active user's current access while working at
// branchID (0 for none), nil if the user is disabled or deleted, or an error if
// the lookup itself failed.
type PrincipalLoader func(userID, branchID uint64) (*Principal, error)

// workingBranch is the branch a request is working at. A till session is
// always at its till's branch. Otherwise it's the branch the request is about
// (?branch_id=), then the admin app's active branch (X-Branch-Id), then none.
// None of these grant anything: the role is the one the user holds at that
// branch, and BranchScope and the handlers still check every branch used.
func workingBranch(c *gin.Context, claims *utils.AccessClaims) uint64 {
	if claims.Scope == utils.ScopePOS {
		return claims.BranchID
	}
	for _, raw := range []string{c.Query("branch_id"), c.GetHeader(BranchHeader)} {
		if id, err := strconv.ParseUint(raw, 10, 64); err == nil && id != 0 {
			return id
		}
	}
	return 0
}

// RequireAuth validates the Bearer access token, looks the user up, and
// populates the request context from the database. The token only says who is
// calling, so disabling someone, changing their role or removing a branch
// applies to their very next request.
func RequireAuth(jwtAccessSecret string, load PrincipalLoader) gin.HandlerFunc {
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

		// A till session without its branch predates per-branch roles: refreshing
		// gives it one.
		if claims.Scope == utils.ScopePOS && claims.BranchID == 0 {
			utils.RespondError(c, utils.ErrUnauthorized)
			c.Abort()
			return
		}
		branchID := workingBranch(c, claims)
		principal, err := load(claims.UserID, branchID)
		if err != nil {
			utils.RespondError(c, err)
			c.Abort()
			return
		}
		if principal == nil || principal.TokenVersion != claims.TokenVersion {
			utils.RespondError(c, utils.ErrUnauthorized)
			c.Abort()
			return
		}
		// A till session whose user is no longer at the till's branch is over.
		if claims.Scope == utils.ScopePOS && principal.BranchID != claims.BranchID {
			utils.RespondError(c, utils.ErrUnauthorized)
			c.Abort()
			return
		}

		permissions := principal.Permissions
		switch claims.Scope {
		case "":
		case utils.ScopePOS:
			permissions = utils.POSScopePermissions(permissions)
		default:
			utils.RespondError(c, utils.ErrUnauthorized)
			c.Abort()
			return
		}

		c.Set(CtxUserID, principal.UserID)
		c.Set(CtxRoleID, principal.RoleID)
		c.Set(CtxRoleName, principal.RoleName)
		c.Set(CtxPermissions, permissions)
		c.Set(CtxBranchIDs, principal.BranchIDs)
		c.Set(CtxBranchID, principal.BranchID)
		c.Set(CtxScope, claims.Scope)
		c.Set(CtxCompanyID, principal.CompanyID)

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

// BranchIDFrom is the branch whose role the caller is acting with, 0 when it's
// their main role.
func BranchIDFrom(c *gin.Context) uint64 {
	v, _ := c.Get(CtxBranchID)
	id, _ := v.(uint64)
	return id
}

// CompanyIDFrom is the caller's company, 0 for a super admin. Every query on
// a company's table filters by it.
func CompanyIDFrom(c *gin.Context) uint64 {
	v, _ := c.Get(CtxCompanyID)
	id, _ := v.(uint64)
	return id
}

// ScopeFrom is the session's scope: utils.ScopePOS at a till, "" otherwise.
func ScopeFrom(c *gin.Context) string {
	v, _ := c.Get(CtxScope)
	scope, _ := v.(string)
	return scope
}
