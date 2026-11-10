package handlers

// A company's limits: the most active branches and the most active user
// accounts it may have, set by the super admin (nil: no limit). Switched-off
// branches and accounts don't count, so a company at its limit can still add
// a switched-off one, or switch one off to make room for another. The limits
// hold for the super admin too, who raises one first. Lowering a limit below
// what a company has switches nothing off; it only stops more being switched
// on until the company is under it.

import (
	"fmt"
	"net/http"

	"gorm.io/gorm"

	"com-mart/backend/internal/utils"
)

type companyLimit int

const (
	branchLimit companyLimit = iota
	userLimit
)

// roomForActive refuses one more active branch or user account when the
// company already has as many as its limit. It locks the company's row until
// tx ends, so two requests at once can't both take the last place.
func roomForActive(tx *gorm.DB, companyID uint64, kind companyLimit) error {
	var limits struct {
		MaxBranches *int
		MaxUsers    *int
	}
	if err := tx.Raw("SELECT max_branches, max_users FROM companies WHERE id = ? FOR UPDATE", companyID).Scan(&limits).Error; err != nil {
		return err
	}
	max, table, code, what := limits.MaxBranches, "branches", "BRANCH_LIMIT_REACHED", "branches"
	if kind == userLimit {
		max, table, code, what = limits.MaxUsers, "users", "USER_LIMIT_REACHED", "user accounts"
	}
	if max == nil {
		return nil
	}
	var active int64
	if err := tx.Table(table).Where("company_id = ? AND active = 1 AND deleted_at IS NULL", companyID).Count(&active).Error; err != nil {
		return err
	}
	if active >= int64(*max) {
		return utils.NewAppError(http.StatusConflict, code, fmt.Sprintf("This company is at its limit of active %s (%d).", what, *max))
	}
	return nil
}
