package repositories

import (
	"errors"
	"slices"

	"gorm.io/gorm"

	"com-mart/backend/internal/models"
)

// BranchRole is the role a user holds at one of their branches.
type BranchRole struct {
	BranchID   uint64
	BranchName string
	BranchCode string
	RoleID     uint64
	RoleName   string
}

// Access is what a user may do while working at one branch.
type Access struct {
	UserID uint64
	// CompanyID is the company the user works for, 0 for a super admin.
	CompanyID    uint64
	TokenVersion uint64
	MainRoleID   uint64
	// The acting role: the one they hold at the branch they're working at.
	RoleID      uint64
	RoleName    string
	Permissions []string
	// BranchID is the branch whose role is acting, 0 when it's the fallback.
	BranchID uint64
	// BranchIDs are their branches where they hold the acting role. Anything
	// done with its permissions stays inside them.
	BranchIDs []uint64
	// Branches is their role at each of their branches, in branch order.
	Branches []BranchRole
	// RolePermissions holds the permissions of every role in Branches.
	RolePermissions map[uint64][]string
}

// Access loads an active user's access while working at branchID. Their role
// at that branch decides. With no branch (0), or one that isn't theirs, it's
// their main role, or the role at their first branch if the main role applies
// at none of them. Returns nil for a user who is disabled or deleted, or whose
// company is.
//
// Everything stays inside the user's company. An Owner is on every active
// branch of it as Owner. Anyone else is on their assigned branches with the
// role set for that branch, or their main role. A super admin (no company) is
// on no branch.
func (r *UserRepository) Access(userID, branchID uint64) (*Access, error) {
	var user struct {
		CompanyID    *uint64
		RoleID       uint64
		RoleName     string
		TokenVersion uint64
	}
	// The main role must be the company's own; a super admin's is the one role
	// with no company.
	err := r.db.Table("users u").
		Select("u.company_id, u.role_id, ro.name AS role_name, u.token_version").
		Joins("JOIN roles ro ON ro.id = u.role_id AND ro.deleted_at IS NULL AND ro.company_id <=> u.company_id").
		Joins("LEFT JOIN companies co ON co.id = u.company_id").
		Where("u.id = ? AND u.active = 1 AND u.deleted_at IS NULL", userID).
		Where("u.company_id IS NULL OR (co.active = 1 AND co.deleted_at IS NULL)").
		Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if user.CompanyID == nil {
		if user.RoleName != models.RoleSuperAdmin {
			return nil, nil
		}
		return &Access{
			UserID: userID, TokenVersion: user.TokenVersion, MainRoleID: user.RoleID, RoleID: user.RoleID, RoleName: user.RoleName,
			Permissions: []string{}, BranchIDs: []uint64{}, Branches: []BranchRole{}, RolePermissions: map[uint64][]string{user.RoleID: {}},
		}, nil
	}
	companyID := *user.CompanyID

	branches := []BranchRole{}
	if user.RoleName == models.RoleOwner {
		err = r.db.Table("branches b").
			Select("b.id AS branch_id, b.name AS branch_name, b.code AS branch_code, ? AS role_id, ? AS role_name", user.RoleID, user.RoleName).
			Where("b.company_id = ? AND b.active = 1 AND b.deleted_at IS NULL", companyID).Order("b.id").Scan(&branches).Error
	} else {
		// A branch is left out if it or the role there is another company's,
		// or the role is a per-branch Owner or another branch's role. Saving a
		// user refuses all of these, so this only guards against rows written
		// some other way.
		err = r.db.Table("user_branches ub").
			Select("b.id AS branch_id, b.name AS branch_name, b.code AS branch_code, ro.id AS role_id, ro.name AS role_name").
			Joins("JOIN branches b ON b.id = ub.branch_id AND b.deleted_at IS NULL AND b.company_id = ?", companyID).
			Joins("JOIN users u ON u.id = ub.user_id").
			Joins("JOIN roles ro ON ro.id = COALESCE(ub.role_id, u.role_id) AND ro.deleted_at IS NULL AND ro.company_id = ?", companyID).
			Where("ub.user_id = ?", userID).
			Where("NOT (ub.role_id IS NOT NULL AND ro.name = ?)", models.RoleOwner).
			Where("ro.branch_id IS NULL OR ro.branch_id = ub.branch_id").
			Order("b.id").Scan(&branches).Error
	}
	if err != nil {
		return nil, err
	}

	a := &Access{UserID: userID, CompanyID: companyID, TokenVersion: user.TokenVersion, MainRoleID: user.RoleID, RoleID: user.RoleID, RoleName: user.RoleName}
	if i := slices.IndexFunc(branches, func(b BranchRole) bool { return b.BranchID == branchID }); branchID != 0 && i >= 0 {
		a.RoleID, a.RoleName, a.BranchID = branches[i].RoleID, branches[i].RoleName, branchID
	} else if len(branches) > 0 && !slices.ContainsFunc(branches, func(b BranchRole) bool { return b.RoleID == user.RoleID }) {
		// The main role applies nowhere, so falling back to it would hand out
		// a role they don't hold at any branch.
		a.RoleID, a.RoleName = branches[0].RoleID, branches[0].RoleName
	}
	a.Branches = branches
	a.BranchIDs = []uint64{}
	roleIDs := []uint64{a.RoleID}
	for _, b := range branches {
		if b.RoleID == a.RoleID {
			a.BranchIDs = append(a.BranchIDs, b.BranchID)
		}
		if !slices.Contains(roleIDs, b.RoleID) {
			roleIDs = append(roleIDs, b.RoleID)
		}
	}

	var links []struct {
		RoleID uint64
		Key    string
	}
	if err := r.db.Table("role_permissions rp").
		Select("rp.role_id, p.`key` AS `key`").
		Joins("JOIN permissions p ON p.id = rp.permission_id").
		Where("rp.role_id IN ?", roleIDs).Order("p.id").
		Scan(&links).Error; err != nil {
		return nil, err
	}
	a.RolePermissions = make(map[uint64][]string, len(roleIDs))
	for _, id := range roleIDs {
		a.RolePermissions[id] = []string{}
	}
	for _, l := range links {
		a.RolePermissions[l.RoleID] = append(a.RolePermissions[l.RoleID], l.Key)
	}
	a.Permissions = a.RolePermissions[a.RoleID]
	return a, nil
}
