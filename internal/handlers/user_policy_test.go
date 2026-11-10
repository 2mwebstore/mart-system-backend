package handlers

import (
	"errors"
	"testing"

	"com-mart/backend/internal/models"
)

var (
	ownerActor = userActor{ID: 1, RoleID: 1, IsOwner: true}
	// Holds user.manage and a little more, works at branches 1 and 2, and can
	// give 20% off and refund $20 without approval.
	adminActor = userActor{ID: 5, RoleID: 9, Permissions: []string{"user.view", "user.create", "user.update", "user.delete", "pos.sell"}, BranchIDs: []uint64{1, 2}, MaxDiscountPercent: 20, RefundWithoutApprovalCents: 2000}

	ownerRole  = roleDTO{ID: 1, Name: models.RoleOwner, Permissions: []string{"user.view", "user.create", "user.update", "user.delete", "pos.sell", "system.reset"}}
	smallRole  = roleDTO{ID: 3, Name: "Cashier", Permissions: []string{"pos.sell"}}
	biggerRole = roleDTO{ID: 4, Name: "Reset admin", Permissions: []string{"pos.sell", "system.reset"}}
	adminsRole = roleDTO{ID: 9, Name: "User admin", Permissions: []string{"user.view", "user.create", "user.update", "user.delete", "pos.sell"}}
	// Nothing the admin lacks but a limit.
	atAdminsLimits = roleDTO{ID: 5, Name: "Senior cashier", Permissions: []string{"pos.sell"}, MaxDiscountPercent: 20, RefundWithoutApprovalCents: 2000}
	biggerDiscount = roleDTO{ID: 6, Name: "Discounter", Permissions: []string{"pos.sell"}, MaxDiscountPercent: 21}
	biggerRefund   = roleDTO{ID: 7, Name: "Refunder", Permissions: []string{"pos.sell"}, RefundWithoutApprovalCents: 2001}

	branch1, branch3 = uint64(1), uint64(3)
	branch1Role      = roleDTO{ID: 10, Name: "Branch 1 helper", Permissions: []string{"pos.sell"}, BranchID: &branch1}
	branch3Role      = roleDTO{ID: 11, Name: "Branch 3 helper", Permissions: []string{"pos.sell"}, BranchID: &branch3}
)

func roles(r ...roleDTO) []roleDTO { return r }

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if want == nil && got != nil {
		t.Fatalf("want allowed, got %v", got)
	}
	if want != nil && !errors.Is(got, want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}

func TestCheckAssignRole(t *testing.T) {
	wantErr(t, adminActor.checkAssignRole(smallRole), nil)
	wantErr(t, adminActor.checkAssignRole(adminsRole), nil)
	wantErr(t, adminActor.checkAssignRole(ownerRole), errOwnerOnly)
	wantErr(t, adminActor.checkAssignRole(biggerRole), errRoleAboveYours)
	wantErr(t, adminActor.checkAssignRole(atAdminsLimits), nil)
	wantErr(t, adminActor.checkAssignRole(biggerDiscount), errDiscountAboveYours)
	wantErr(t, adminActor.checkAssignRole(biggerRefund), errRefundAboveYours)
	wantErr(t, ownerActor.checkAssignRole(ownerRole), nil)
	wantErr(t, ownerActor.checkAssignRole(biggerDiscount), nil)
	wantErr(t, ownerActor.checkAssignRole(biggerRole), nil)
}

func TestCheckAssignBranches(t *testing.T) {
	wantErr(t, adminActor.checkAssignBranches([]uint64{1}), nil)
	wantErr(t, adminActor.checkAssignBranches([]uint64{1, 2}), nil)
	wantErr(t, adminActor.checkAssignBranches(nil), errBranchRequired)
	wantErr(t, adminActor.checkAssignBranches([]uint64{}), errBranchRequired)
	wantErr(t, ownerActor.checkAssignBranches(nil), nil)
	wantErr(t, adminActor.checkAssignBranches([]uint64{1, 3}), errBranchOutOfScope)
	wantErr(t, ownerActor.checkAssignBranches([]uint64{1, 3, 99}), nil)
}

func TestCheckManageUser(t *testing.T) {
	inBranch1 := userDTO{ID: 20, RoleID: smallRole.ID, BranchIDs: []uint64{1}}
	alsoAtBranch3 := userDTO{ID: 21, RoleID: smallRole.ID, BranchIDs: []uint64{1, 3}}
	onlyAtBranch3 := userDTO{ID: 22, RoleID: smallRole.ID, BranchIDs: []uint64{3}}
	noBranches := userDTO{ID: 23, RoleID: smallRole.ID}

	wantErr(t, adminActor.checkManageUser(inBranch1, roles(smallRole)), nil)
	wantErr(t, adminActor.checkManageUser(noBranches, roles(smallRole)), nil)
	wantErr(t, adminActor.checkManageUser(inBranch1, roles(ownerRole)), errOwnerOnly)
	wantErr(t, adminActor.checkManageUser(inBranch1, roles(biggerRole)), errRoleAboveYours)
	// A new password on this account would be a way to give bigger discounts.
	wantErr(t, adminActor.checkManageUser(inBranch1, roles(biggerDiscount)), errDiscountAboveYours)
	wantErr(t, adminActor.checkManageUser(inBranch1, roles(biggerRefund)), errRefundAboveYours)
	// Every role they hold counts, not just the main one.
	wantErr(t, adminActor.checkManageUser(inBranch1, roles(smallRole, biggerRole)), errRoleAboveYours)
	wantErr(t, adminActor.checkManageUser(inBranch1, roles(smallRole, biggerDiscount)), errDiscountAboveYours)
	wantErr(t, adminActor.checkManageUser(onlyAtBranch3, roles(smallRole)), errUserOutOfScope)
	// A new password on this account would be a way into branch 3.
	wantErr(t, adminActor.checkManageUser(alsoAtBranch3, roles(smallRole)), errUserOutOfScope)
	wantErr(t, ownerActor.checkManageUser(onlyAtBranch3, roles(ownerRole)), nil)
}

func TestCheckSelfEdit(t *testing.T) {
	self := userDTO{ID: adminActor.ID, RoleID: adminsRole.ID, BranchIDs: []uint64{1, 2}}
	same := userReq{RoleID: adminsRole.ID, BranchIDs: []uint64{2, 1}}
	none := map[uint64]uint64{}
	wantErr(t, adminActor.checkSelfEdit(self, same, none), nil)
	wantErr(t, adminActor.checkSelfEdit(self, userReq{RoleID: smallRole.ID, BranchIDs: []uint64{1, 2}}, none), errCannotChangeOwnAccess)
	wantErr(t, adminActor.checkSelfEdit(self, userReq{RoleID: adminsRole.ID, BranchIDs: []uint64{1, 2, 3}}, none), errCannotChangeOwnAccess)
	wantErr(t, adminActor.checkSelfEdit(self, userReq{RoleID: adminsRole.ID, BranchIDs: []uint64{1}}, none), errCannotChangeOwnAccess)
	// Nor their role at one branch.
	wantErr(t, adminActor.checkSelfEdit(self, same, map[uint64]uint64{2: smallRole.ID}), errCannotChangeOwnAccess)

	withBranchRole := userDTO{ID: adminActor.ID, RoleID: adminsRole.ID, BranchIDs: []uint64{1, 2}, BranchRoles: []branchRoleDTO{{BranchID: 2, RoleID: smallRole.ID}}}
	wantErr(t, adminActor.checkSelfEdit(withBranchRole, same, map[uint64]uint64{2: smallRole.ID}), nil)
	wantErr(t, adminActor.checkSelfEdit(withBranchRole, same, none), errCannotChangeOwnAccess)
	wantErr(t, adminActor.checkSelfEdit(withBranchRole, same, map[uint64]uint64{2: adminsRole.ID + 100}), errCannotChangeOwnAccess)

	someoneElse := userDTO{ID: 20, RoleID: smallRole.ID, BranchIDs: []uint64{1}}
	wantErr(t, adminActor.checkSelfEdit(someoneElse, userReq{RoleID: adminsRole.ID}, map[uint64]uint64{1: smallRole.ID}), nil)

	ownerSelf := userDTO{ID: ownerActor.ID, RoleID: ownerRole.ID}
	wantErr(t, ownerActor.checkSelfEdit(ownerSelf, userReq{RoleID: smallRole.ID}, none), nil)
}

func TestCheckRoleChanges(t *testing.T) {
	wantErr(t, adminActor.checkRolePermissions([]string{"pos.sell"}), nil)
	wantErr(t, adminActor.checkRolePermissions(nil), nil)
	wantErr(t, adminActor.checkRolePermissions([]string{"pos.sell", "system.reset"}), errRoleAboveYours)
	wantErr(t, adminActor.checkRolePermissions([]string{"not.a.real.key"}), errRoleAboveYours)
	wantErr(t, ownerActor.checkRolePermissions([]string{"system.reset"}), nil)

	wantErr(t, adminActor.checkEditRole(smallRole), nil)
	wantErr(t, adminActor.checkEditRole(adminsRole), errCannotEditOwnRole)
	wantErr(t, adminActor.checkEditRole(biggerRole), errRoleAboveYours)
	wantErr(t, ownerActor.checkEditRole(biggerRole), nil)
}

func TestCheckRoleLimits(t *testing.T) {
	wantErr(t, adminActor.checkRoleLimits(0, 0), nil)
	wantErr(t, adminActor.checkRoleLimits(20, 2000), nil)
	wantErr(t, adminActor.checkRoleLimits(21, 0), errDiscountAboveYours)
	wantErr(t, adminActor.checkRoleLimits(0, 2001), errRefundAboveYours)
	wantErr(t, userActor{}.checkRoleLimits(1, 0), errDiscountAboveYours)
	wantErr(t, userActor{}.checkRoleLimits(0, 1), errRefundAboveYours)
	wantErr(t, ownerActor.checkRoleLimits(100, 1<<40), nil)
}

func TestCheckRoleScope(t *testing.T) {
	wantErr(t, adminActor.checkRoleScope(branch1Role), nil)
	wantErr(t, adminActor.checkRoleScope(smallRole), errRoleShared)
	wantErr(t, adminActor.checkRoleScope(branch3Role), errRoleOutOfScope)
	wantErr(t, ownerActor.checkRoleScope(smallRole), nil)
	wantErr(t, ownerActor.checkRoleScope(branch3Role), nil)

	wantErr(t, adminActor.checkNewRoleBranch(&branch1), nil)
	wantErr(t, adminActor.checkNewRoleBranch(nil), errRoleBranchRequired)
	wantErr(t, adminActor.checkNewRoleBranch(&branch3), errBranchOutOfScope)
	wantErr(t, ownerActor.checkNewRoleBranch(nil), nil)
	wantErr(t, ownerActor.checkNewRoleBranch(&branch3), nil)
}

func TestRoleBranches(t *testing.T) {
	cases := []struct {
		name       string
		actor      userActor
		at         uint64
		want       []uint64
		wantScoped bool
	}{
		{"admin, no branch named", adminActor, 0, []uint64{1, 2}, true},
		{"admin, one branch", adminActor, 2, []uint64{2}, true},
		{"owner, no branch named", ownerActor, 0, nil, false},
		{"owner, one branch", ownerActor, 3, []uint64{3}, true},
	}
	for _, tc := range cases {
		got, scoped := tc.actor.roleBranches(tc.at)
		if scoped != tc.wantScoped || len(got) != len(tc.want) {
			t.Fatalf("%s: got %v scoped=%v", tc.name, got, scoped)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("%s: got %v", tc.name, got)
			}
		}
	}
}

func TestCheckBranchRoles(t *testing.T) {
	none := map[uint64]roleDTO{}
	wantErr(t, checkBranchRoles(smallRole, []uint64{1, 3}, none), nil)
	wantErr(t, checkBranchRoles(smallRole, nil, none), nil)
	wantErr(t, checkBranchRoles(smallRole, []uint64{1, 3}, map[uint64]roleDTO{3: atAdminsLimits}), nil)

	// A branch's role at that branch only.
	wantErr(t, checkBranchRoles(branch1Role, []uint64{1}, none), nil)
	wantErr(t, checkBranchRoles(smallRole, []uint64{1, 3}, map[uint64]roleDTO{1: branch1Role}), nil)
	wantErr(t, checkBranchRoles(branch1Role, []uint64{1, 3}, none), errRoleWrongBranch)
	wantErr(t, checkBranchRoles(branch1Role, []uint64{1, 3}, map[uint64]roleDTO{3: branch3Role}), nil)
	wantErr(t, checkBranchRoles(smallRole, []uint64{1, 3}, map[uint64]roleDTO{3: branch1Role}), errRoleWrongBranch)
	wantErr(t, checkBranchRoles(branch1Role, nil, none), errRoleWrongBranch)

	// The main role has to apply somewhere.
	wantErr(t, checkBranchRoles(smallRole, []uint64{1}, map[uint64]roleDTO{1: atAdminsLimits}), errMainRoleUnused)
	wantErr(t, checkBranchRoles(smallRole, []uint64{1, 3}, map[uint64]roleDTO{1: atAdminsLimits, 3: biggerRole}), errMainRoleUnused)

	// Owner is never per branch.
	wantErr(t, checkBranchRoles(ownerRole, []uint64{1, 3}, map[uint64]roleDTO{3: smallRole}), errOwnerRoleGlobal)
	wantErr(t, checkBranchRoles(smallRole, []uint64{1, 3}, map[uint64]roleDTO{3: ownerRole}), errOwnerRoleGlobal)
	wantErr(t, checkBranchRoles(ownerRole, nil, none), nil)
}

func TestTrimUser(t *testing.T) {
	u := userDTO{ID: 30, RoleID: smallRole.ID, BranchIDs: []uint64{1, 3}, BranchRoles: []branchRoleDTO{{BranchID: 1, RoleID: 10}, {BranchID: 3, RoleID: 11}}}
	trimmed := u
	adminActor.trimUser(&trimmed)
	if len(trimmed.BranchIDs) != 1 || trimmed.BranchIDs[0] != 1 || len(trimmed.BranchRoles) != 1 || trimmed.BranchRoles[0].BranchID != 1 || !trimmed.HasOtherBranches {
		t.Fatalf("admin at 1 and 2 should see only branch 1 and be told there's more: %+v", trimmed)
	}

	inside := userDTO{ID: 31, RoleID: smallRole.ID, BranchIDs: []uint64{2, 1}, BranchRoles: []branchRoleDTO{}}
	adminActor.trimUser(&inside)
	if len(inside.BranchIDs) != 2 || inside.HasOtherBranches {
		t.Fatalf("nothing to trim: %+v", inside)
	}

	owners := u
	ownerActor.trimUser(&owners)
	if len(owners.BranchIDs) != 2 || len(owners.BranchRoles) != 2 || owners.HasOtherBranches {
		t.Fatalf("an Owner sees everything: %+v", owners)
	}
}
