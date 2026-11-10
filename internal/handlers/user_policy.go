package handlers

import (
	"net/http"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"com-mart/backend/internal/middleware"
	"com-mart/backend/internal/models"
	"com-mart/backend/internal/utils"
)

// The user.* and role.* permissions say someone may use the Users and Roles
// pages. These rules decide which people and roles they may touch, so
// that holding either permission can't be turned into more access than the
// holder already has. An Owner is exempt from all of them.
var (
	errOwnerOnly             = utils.NewAppError(http.StatusForbidden, "OWNER_ONLY", "Only an Owner can change an Owner account or give someone the Owner role.")
	errRoleAboveYours        = utils.NewAppError(http.StatusForbidden, "ROLE_ABOVE_YOURS", "You can't assign or change a role that has permissions you don't hold.")
	errDiscountAboveYours    = utils.NewAppError(http.StatusForbidden, "DISCOUNT_ABOVE_YOURS", "You can't set or assign a discount limit higher than your own role's.")
	errRefundAboveYours      = utils.NewAppError(http.StatusForbidden, "REFUND_ABOVE_YOURS", "You can't set or assign a refund limit higher than your own role's.")
	errCannotEditOwnRole     = utils.NewAppError(http.StatusForbidden, "CANNOT_EDIT_OWN_ROLE", "You can't change your own role's permissions.")
	errCannotChangeOwnAccess = utils.NewAppError(http.StatusForbidden, "CANNOT_CHANGE_OWN_ACCESS", "You can't change your own role or branches.")
	errBranchOutOfScope      = utils.NewAppError(http.StatusForbidden, "BRANCH_OUT_OF_SCOPE", "You can only assign branches you have access to.")
	errBranchRequired        = utils.NewAppError(http.StatusUnprocessableEntity, "BRANCH_REQUIRED", "Pick at least one branch for this user.")
	errUserOutOfScope        = utils.NewAppError(http.StatusForbidden, "USER_OUT_OF_SCOPE", "That user works at a branch you don't have access to.")
	errLastOwner             = utils.NewAppError(http.StatusConflict, "LAST_OWNER", "This is the only active Owner. Make someone else an Owner first.")
	errRoleShared            = utils.NewAppError(http.StatusForbidden, "ROLE_SHARED", "Only an Owner can change a shared role.")
	errRoleOutOfScope        = utils.NewAppError(http.StatusForbidden, "ROLE_OUT_OF_SCOPE", "That role belongs to a branch you don't have access to.")
	errRoleBranchRequired    = utils.NewAppError(http.StatusUnprocessableEntity, "ROLE_BRANCH_REQUIRED", "Pick the branch this role is for.")
	errRoleWrongBranch       = utils.NewAppError(http.StatusUnprocessableEntity, "ROLE_WRONG_BRANCH", "That role belongs to another branch and can only be held there.")
	errMainRoleUnused        = utils.NewAppError(http.StatusUnprocessableEntity, "MAIN_ROLE_UNUSED", "The main role has to apply at one of this user's branches at least.")
	errOwnerRoleGlobal       = utils.NewAppError(http.StatusUnprocessableEntity, "OWNER_ROLE_GLOBAL", "The Owner role covers every branch, so it can't be set per branch.")
)

// userActor is whoever is making a change to a user or role. Their role,
// permissions and branches are the ones they hold at the branch they're
// working at (see middleware.RequireAuth), so what they do here stays inside
// the branches where they hold that role.
type userActor struct {
	ID          uint64
	RoleID      uint64
	IsOwner     bool
	Permissions []string
	BranchIDs   []uint64
	// Their own role's limits: the most they can put on a role or hand out.
	MaxDiscountPercent         int
	RefundWithoutApprovalCents int64
}

// actorFrom reads the actor's role limits from the database on every request,
// so a limit an Owner has just lowered applies at once.
func (a *API) actorFrom(c *gin.Context) (userActor, error) {
	u := userActor{
		ID:          middleware.UserIDFrom(c),
		RoleID:      roleIDFrom(c),
		IsOwner:     middleware.RoleNameFrom(c) == models.RoleOwner,
		Permissions: middleware.PermissionsFrom(c),
		BranchIDs:   middleware.BranchIDsFrom(c),
	}
	if u.IsOwner {
		return u, nil
	}
	// A role with no limits row has limits of zero.
	var limit models.RoleLimit
	if err := a.DB.Where("role_id = ?", u.RoleID).Limit(1).Find(&limit).Error; err != nil {
		return u, err
	}
	u.MaxDiscountPercent, u.RefundWithoutApprovalCents = limit.MaxDiscountPercent, limit.RefundWithoutApprovalCents
	return u, nil
}

func holdsAll(held, wanted []string) bool {
	for _, w := range wanted {
		if !slices.Contains(held, w) {
			return false
		}
	}
	return true
}

func containsAll(ids, wanted []uint64) bool {
	for _, w := range wanted {
		if !slices.Contains(ids, w) {
			return false
		}
	}
	return true
}

// checkAssignRole: someone can only hand out a role whose permissions they
// hold themselves and whose limits are no higher than their own.
func (u userActor) checkAssignRole(role roleDTO) error {
	if u.IsOwner {
		return nil
	}
	if role.Name == models.RoleOwner {
		return errOwnerOnly
	}
	if !holdsAll(u.Permissions, role.Permissions) {
		return errRoleAboveYours
	}
	return u.checkRoleLimits(role.MaxDiscountPercent, role.RefundWithoutApprovalCents)
}

// checkAssignBranches: someone can only put a user at branches they work at
// themselves, and must give them at least one. A user with no branch is on
// nobody's list but an Owner's, so it would be lost to whoever saved it.
func (u userActor) checkAssignBranches(ids []uint64) error {
	if u.IsOwner {
		return nil
	}
	if len(ids) == 0 {
		return errBranchRequired
	}
	if !containsAll(u.BranchIDs, ids) {
		return errBranchOutOfScope
	}
	return nil
}

// checkManageUser: someone can only change an account that has no more access
// than they do. Setting a new password on a stronger account, or on one working
// at another branch too, would otherwise be a way to sign in as it. targetRoles
// is every role the account holds, its main role and any per-branch ones.
func (u userActor) checkManageUser(target userDTO, targetRoles []roleDTO) error {
	if u.IsOwner {
		return nil
	}
	for _, role := range targetRoles {
		if err := u.checkAssignRole(role); err != nil {
			return err
		}
	}
	if !containsAll(u.BranchIDs, target.BranchIDs) {
		return errUserOutOfScope
	}
	return nil
}

// checkBranchRoles holds for everyone, Owners included: which roles can be
// held where. main applies at every branch in branchIDs that has no entry in
// perBranch.
//   - The Owner role covers every branch, so it's never set per branch and an
//     Owner has no per-branch roles.
//   - A branch's own role can only be held at that branch.
//   - The main role has to apply somewhere. Otherwise a request that names no
//     branch would act with a role the user holds nowhere.
func checkBranchRoles(main roleDTO, branchIDs []uint64, perBranch map[uint64]roleDTO) error {
	if main.Name == models.RoleOwner && len(perBranch) > 0 {
		return errOwnerRoleGlobal
	}
	for _, role := range perBranch {
		if role.Name == models.RoleOwner {
			return errOwnerRoleGlobal
		}
	}
	if len(branchIDs) == 0 && main.BranchID != nil {
		return errRoleWrongBranch
	}
	mainUsed := false
	for _, b := range branchIDs {
		role, ok := perBranch[b]
		if !ok {
			role, mainUsed = main, true
		}
		if role.BranchID != nil && *role.BranchID != b {
			return errRoleWrongBranch
		}
	}
	if len(branchIDs) > 0 && !mainUsed {
		return errMainRoleUnused
	}
	return nil
}

// trimUser leaves out the branches the actor can't see. The account is marked
// so the page can say it works elsewhere too. checkManageUser already keeps
// the actor from changing it, so nothing is lost by not showing them.
func (u userActor) trimUser(target *userDTO) {
	if u.IsOwner {
		return
	}
	ids := []uint64{}
	for _, id := range target.BranchIDs {
		if slices.Contains(u.BranchIDs, id) {
			ids = append(ids, id)
		} else {
			target.HasOtherBranches = true
		}
	}
	roles := []branchRoleDTO{}
	for _, r := range target.BranchRoles {
		if slices.Contains(u.BranchIDs, r.BranchID) {
			roles = append(roles, r)
		}
	}
	target.BranchIDs, target.BranchRoles = ids, roles
}

// scopeUsers keeps a users query to the people the actor shares a branch with.
// An Owner is on every branch, so anyone with a branch shares one with them;
// someone with no branch shares nothing with anybody.
func (u userActor) scopeUsers(q *gorm.DB) *gorm.DB {
	if u.IsOwner {
		return q
	}
	if len(u.BranchIDs) == 0 {
		return q.Where("1 = 0")
	}
	return q.Where("(r.name = ? OR u.id IN (SELECT ub.user_id FROM user_branches ub WHERE ub.branch_id IN ?))", models.RoleOwner, u.BranchIDs)
}

// checkSelfEdit: nobody but an Owner changes their own role, branches or
// per-branch roles. perBranch is what the request asks for.
func (u userActor) checkSelfEdit(target userDTO, req userReq, perBranch map[uint64]uint64) error {
	if u.IsOwner || target.ID != u.ID {
		return nil
	}
	if req.RoleID != target.RoleID || !containsAll(req.BranchIDs, target.BranchIDs) || !containsAll(target.BranchIDs, req.BranchIDs) {
		return errCannotChangeOwnAccess
	}
	if len(perBranch) != len(target.BranchRoles) {
		return errCannotChangeOwnAccess
	}
	for _, r := range target.BranchRoles {
		if perBranch[r.BranchID] != r.RoleID {
			return errCannotChangeOwnAccess
		}
	}
	return nil
}

func (u userActor) checkRolePermissions(wanted []string) error {
	if u.IsOwner || holdsAll(u.Permissions, wanted) {
		return nil
	}
	return errRoleAboveYours
}

// checkRoleLimits: a role can't be given a bigger discount or refund limit
// than the actor's own role has.
func (u userActor) checkRoleLimits(discountPercent int, refundCents int64) error {
	if u.IsOwner {
		return nil
	}
	if discountPercent > u.MaxDiscountPercent {
		return errDiscountAboveYours
	}
	if refundCents > u.RefundWithoutApprovalCents {
		return errRefundAboveYours
	}
	return nil
}

// checkEditRole: nobody but an Owner changes their own role. A role they hold
// at another branch is shared or that branch's, so checkRoleScope already keeps
// them from it.
func (u userActor) checkEditRole(role roleDTO) error {
	if u.IsOwner {
		return nil
	}
	if role.ID == u.RoleID {
		return errCannotEditOwnRole
	}
	return u.checkRolePermissions(role.Permissions)
}

// checkRoleScope: a shared role is the Owner's to change, and a branch's role
// is for whoever runs that branch.
func (u userActor) checkRoleScope(role roleDTO) error {
	if u.IsOwner {
		return nil
	}
	if role.BranchID == nil {
		return errRoleShared
	}
	if !slices.Contains(u.BranchIDs, *role.BranchID) {
		return errRoleOutOfScope
	}
	return nil
}

// checkNewRoleBranch: anyone but an Owner makes roles for one of their own
// branches. An Owner may also make a shared one (nil).
func (u userActor) checkNewRoleBranch(branchID *uint64) error {
	if u.IsOwner {
		return nil
	}
	if branchID == nil {
		return errRoleBranchRequired
	}
	if !slices.Contains(u.BranchIDs, *branchID) {
		return errBranchOutOfScope
	}
	return nil
}

// roleBranches is which branches' roles the actor sees: shared roles plus
// those of the branches returned. at is the branch asked for, 0 for none.
// scoped is false for an Owner who asked for no branch: they see every role.
func (u userActor) roleBranches(at uint64) (branches []uint64, scoped bool) {
	if at != 0 {
		return []uint64{at}, true
	}
	if u.IsOwner {
		return nil, false
	}
	return u.BranchIDs, true
}

// guardLastOwner refuses to remove the company's only active Owner, which
// would leave nobody able to manage it. It locks the Owner rows until the
// transaction ends, so two Owners disabling each other at the same moment can't
// both get through.
func guardLastOwner(tx *gorm.DB, companyID, userID uint64) error {
	var owners []uint64
	err := tx.Table("users u").
		Joins("JOIN roles r ON r.id = u.role_id").
		Where("u.company_id = ? AND r.name = ? AND u.active = 1 AND u.deleted_at IS NULL", companyID, models.RoleOwner).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Pluck("u.id", &owners).Error
	if err != nil {
		return err
	}
	if len(owners) == 1 && owners[0] == userID {
		return errLastOwner
	}
	return nil
}

// endSessions signs a user out everywhere: nothing they hold can be refreshed,
// and their access tokens stop working on the next request.
func endSessions(tx *gorm.DB, userID uint64) error {
	if err := tx.Model(&models.RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", time.Now()).Error; err != nil {
		return err
	}
	return tx.Model(&models.User{}).Where("id = ?", userID).
		UpdateColumn("token_version", gorm.Expr("token_version + 1")).Error
}
