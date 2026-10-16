package middleware

import (
	"slices"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"com-mart/backend/internal/utils"
)

// LivePermissions, when set (routes wires it to the database), returns a
// user's current role permissions. A token carries the permissions its user
// had when they signed in; without this, a permission granted afterwards — a
// new feature's permission, or a role edited in Users & Roles — would keep
// answering 403 until they signed out and in again. It's only asked when the
// token doesn't already grant what a route needs.
var LivePermissions func(userID uint64) []string

const livePermissionsTTL = 30 * time.Second

type cachedPermissions struct {
	keys    []string
	expires time.Time
}

// Cached briefly per user, so a role that really lacks a permission doesn't
// cost a database query on every request.
var livePermissionCache sync.Map // userID -> cachedPermissions

func livePermissionsFor(userID uint64) []string {
	if LivePermissions == nil || userID == 0 {
		return nil
	}
	if v, ok := livePermissionCache.Load(userID); ok {
		if e := v.(cachedPermissions); time.Now().Before(e.expires) {
			return e.keys
		}
	}
	keys := LivePermissions(userID)
	livePermissionCache.Store(userID, cachedPermissions{keys: keys, expires: time.Now().Add(livePermissionsTTL)})
	return keys
}

// grants reports whether the caller holds any of keys, checking the token
// first and the user's current role permissions second. When the role grants
// more than the token, the context is updated so handlers that check a
// permission themselves (hasPerm) see the same answer.
func grants(c *gin.Context, keys ...string) bool {
	token := PermissionsFrom(c)
	for _, k := range keys {
		if slices.Contains(token, k) {
			return true
		}
	}
	live := livePermissionsFor(UserIDFrom(c))
	for _, k := range keys {
		if slices.Contains(live, k) {
			merged := slices.Clone(token)
			for _, p := range live {
				if !slices.Contains(merged, p) {
					merged = append(merged, p)
				}
			}
			c.Set(CtxPermissions, merged)
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
