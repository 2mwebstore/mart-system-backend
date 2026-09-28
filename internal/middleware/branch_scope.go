package middleware

import (
	"slices"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"com-mart/backend/internal/utils"
)

// BranchScope 403s if the request's `branch_id` query/param targets a
// branch the caller doesn't have access to. Owner tokens carry every
// branch ID (assigned at login, see services/auth_service.go), so this
// check is uniform across roles — no special-casing "is Owner" here.
// Must run after RequireAuth.
func BranchScope() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.Query("branch_id")
		if raw == "" {
			raw = c.Param("branch_id")
		}
		if raw == "" {
			c.Next()
			return
		}

		branchID, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			utils.RespondError(c, utils.NewAppError(400, "INVALID_BRANCH_ID", "branch_id must be a number."))
			c.Abort()
			return
		}

		if !slices.Contains(BranchIDsFrom(c), branchID) {
			utils.RespondError(c, utils.ErrForbidden)
			c.Abort()
			return
		}
		c.Next()
	}
}

// ScopeToBranches returns a GORM scope that restricts a query to the
// caller's allowed branch IDs. Every repository query on a branch-scoped
// table must apply this.
func ScopeToBranches(c *gin.Context) func(db *gorm.DB) *gorm.DB {
	ids := BranchIDsFrom(c)
	return func(db *gorm.DB) *gorm.DB {
		return db.Where("branch_id IN ?", ids)
	}
}
