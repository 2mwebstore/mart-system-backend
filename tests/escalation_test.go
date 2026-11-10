package tests

import (
	"net/http"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

// A user.manage holder at one branch, with enough of their own permissions and
// limits to hand out the Cashier role.
func userAdmin(t *testing.T) (namedUser, uint64, string) {
	t.Helper()
	role := newRoleWithDiscount(t, 5, append([]string{"user.view", "user.create", "user.update", "user.delete"}, cashierPermissions...)...)
	user := newUser(t, role, env.branchTK)
	return user, role, login(t, user).Access
}

func userRole(t *testing.T, id uint64) uint64 {
	t.Helper()
	return uint64(dbInt(t, "SELECT role_id FROM users WHERE id = ?", id))
}

func TestUserAdminCannotMakeAnOwner(t *testing.T) {
	e := setup(t)
	_, _, token := userAdmin(t)
	name := unique("evil")

	body := updateBody(name, e.roleOwner, true, []uint64{e.branchTK}, "password", testPassword)
	wantDenied(t, call(t, http.MethodPost, "/users", token, body), http.StatusForbidden, "OWNER_ONLY")
	if n := dbInt(t, "SELECT COUNT(*) FROM users WHERE username = ?", name); n != 0 {
		t.Fatal("the account was created anyway")
	}
}

func TestUserAdminCannotPromoteThemself(t *testing.T) {
	e := setup(t)
	admin, role, token := userAdmin(t)

	for name, target := range map[string]uint64{"to Owner": e.roleOwner, "to Manager": e.roleManager} {
		t.Run(name, func(t *testing.T) {
			r := call(t, http.MethodPut, "/users/"+id(admin.ID), token, updateBody(admin.Username, target, true, []uint64{e.branchTK}))
			wantDenied(t, r, http.StatusForbidden, "CANNOT_CHANGE_OWN_ACCESS")
			if got := userRole(t, admin.ID); got != role {
				t.Fatalf("role changed to %d", got)
			}
		})
	}
}

func TestUserAdminCannotAddThemselfToABranch(t *testing.T) {
	e := setup(t)
	admin, role, token := userAdmin(t)

	r := call(t, http.MethodPut, "/users/"+id(admin.ID), token, updateBody(admin.Username, role, true, []uint64{e.branchTK, e.branchBKK}))
	wantDenied(t, r, http.StatusForbidden, "CANNOT_CHANGE_OWN_ACCESS")
	if n := dbInt(t, "SELECT COUNT(*) FROM user_branches WHERE user_id = ?", admin.ID); n != 1 {
		t.Fatalf("user now has %d branches", n)
	}
}

func TestUserAdminCanStillEditTheirOwnDetails(t *testing.T) {
	e := setup(t)
	admin, role, token := userAdmin(t)
	body := updateBody(admin.Username, role, true, []uint64{e.branchTK})
	body["phone"] = "012345678"
	wantStatus(t, call(t, http.MethodPut, "/users/"+id(admin.ID), token, body), http.StatusOK)
}

func TestUserAdminCannotTakeOverAnOwner(t *testing.T) {
	e := setup(t)
	_, _, token := userAdmin(t)
	target := newUser(t, e.roleOwner)
	takeover := "attacker-chosen-password"

	t.Run("password", func(t *testing.T) {
		r := call(t, http.MethodPut, "/users/"+id(target.ID), token, updateBody(target.Username, e.roleOwner, true, nil, "password", takeover))
		wantDenied(t, r, http.StatusForbidden, "OWNER_ONLY")
		wantStatus(t, tryLogin(t, target.Username, takeover), http.StatusUnauthorized)
	})
	t.Run("PIN", func(t *testing.T) {
		r := call(t, http.MethodPost, "/users/"+id(target.ID)+"/reset-pin", token, gin.H{})
		wantDenied(t, r, http.StatusForbidden, "OWNER_ONLY")
		if r.Data != nil && string(r.Data) != "null" {
			t.Fatalf("a PIN came back: %s", r)
		}
	})
	t.Run("delete", func(t *testing.T) {
		wantDenied(t, call(t, http.MethodDelete, "/users/"+id(target.ID), token, nil), http.StatusForbidden, "OWNER_ONLY")
		if n := dbInt(t, "SELECT COUNT(*) FROM users WHERE id = ? AND deleted_at IS NULL", target.ID); n != 1 {
			t.Fatal("the Owner was deleted")
		}
	})
	t.Run("disable", func(t *testing.T) {
		r := call(t, http.MethodPut, "/users/"+id(target.ID), token, updateBody(target.Username, e.roleOwner, false, nil))
		wantDenied(t, r, http.StatusForbidden, "OWNER_ONLY")
	})
}

func TestUserAdminCannotAssignARoleAboveTheirOwn(t *testing.T) {
	e := setup(t)
	_, _, token := userAdmin(t)
	stronger := newRole(t, "system.reset", "pos.sell")
	victim := newUser(t, e.roleCashier, e.branchTK)

	t.Run("on a new account", func(t *testing.T) {
		body := updateBody(unique("x"), stronger, true, []uint64{e.branchTK}, "password", testPassword)
		wantDenied(t, call(t, http.MethodPost, "/users", token, body), http.StatusForbidden, "ROLE_ABOVE_YOURS")
	})
	t.Run("on an existing account", func(t *testing.T) {
		r := call(t, http.MethodPut, "/users/"+id(victim.ID), token, updateBody(victim.Username, stronger, true, []uint64{e.branchTK}))
		wantDenied(t, r, http.StatusForbidden, "ROLE_ABOVE_YOURS")
		if got := userRole(t, victim.ID); got != e.roleCashier {
			t.Fatalf("role changed to %d", got)
		}
	})
	t.Run("nor change an account that already has one", func(t *testing.T) {
		strong := newUser(t, stronger, e.branchTK)
		r := call(t, http.MethodPut, "/users/"+id(strong.ID), token, updateBody(strong.Username, e.roleCashier, true, []uint64{e.branchTK}, "password", "attacker-password"))
		wantDenied(t, r, http.StatusForbidden, "ROLE_ABOVE_YOURS")
	})
}

func TestUserAdminStaysInsideTheirBranches(t *testing.T) {
	e := setup(t)
	_, _, token := userAdmin(t)

	t.Run("can't create someone at another branch", func(t *testing.T) {
		body := updateBody(unique("x"), e.roleCashier, true, []uint64{e.branchBKK}, "password", testPassword)
		wantDenied(t, call(t, http.MethodPost, "/users", token, body), http.StatusForbidden, "BRANCH_OUT_OF_SCOPE")
	})
	t.Run("can't move someone to another branch", func(t *testing.T) {
		u := newUser(t, e.roleCashier, e.branchTK)
		r := call(t, http.MethodPut, "/users/"+id(u.ID), token, updateBody(u.Username, e.roleCashier, true, []uint64{e.branchBKK}))
		wantDenied(t, r, http.StatusForbidden, "BRANCH_OUT_OF_SCOPE")
	})
	t.Run("can't touch someone at another branch", func(t *testing.T) {
		u := newUser(t, e.roleCashier, e.branchBKK)
		r := call(t, http.MethodPut, "/users/"+id(u.ID), token, updateBody(u.Username, e.roleCashier, true, []uint64{e.branchTK}))
		wantDenied(t, r, http.StatusForbidden, "USER_OUT_OF_SCOPE")
		wantDenied(t, call(t, http.MethodPost, "/users/"+id(u.ID)+"/reset-pin", token, gin.H{}), http.StatusForbidden, "USER_OUT_OF_SCOPE")
		wantDenied(t, call(t, http.MethodDelete, "/users/"+id(u.ID), token, nil), http.StatusForbidden, "USER_OUT_OF_SCOPE")
	})
	// Nobody but an Owner could see that account afterwards.
	t.Run("can't create someone with no branch", func(t *testing.T) {
		name := unique("x")
		body := updateBody(name, e.roleCashier, true, []uint64{}, "password", testPassword)
		wantDenied(t, call(t, http.MethodPost, "/users", token, body), http.StatusUnprocessableEntity, "BRANCH_REQUIRED")
		if n := dbInt(t, "SELECT COUNT(*) FROM users WHERE username = ?", name); n != 0 {
			t.Fatal("the account was created anyway")
		}
	})
	t.Run("can't take away someone's last branch", func(t *testing.T) {
		u := newUser(t, e.roleCashier, e.branchTK)
		r := call(t, http.MethodPut, "/users/"+id(u.ID), token, updateBody(u.Username, e.roleCashier, true, nil))
		wantDenied(t, r, http.StatusUnprocessableEntity, "BRANCH_REQUIRED")
		if n := dbInt(t, "SELECT COUNT(*) FROM user_branches WHERE user_id = ?", u.ID); n != 1 {
			t.Fatalf("user now has %d branches", n)
		}
	})
	// A new password on a shared account would be a way into the other branch.
	t.Run("can't set a password on someone who also works elsewhere", func(t *testing.T) {
		u := newUser(t, e.roleCashier, e.branchTK, e.branchBKK)
		r := call(t, http.MethodPut, "/users/"+id(u.ID), token, updateBody(u.Username, e.roleCashier, true, []uint64{e.branchTK, e.branchBKK}, "password", "attacker-password"))
		wantDenied(t, r, http.StatusForbidden, "USER_OUT_OF_SCOPE")
	})
}

func TestUserAdminCanDoTheirJob(t *testing.T) {
	e := setup(t)
	_, _, token := userAdmin(t)
	name := unique("hire")

	body := updateBody(name, e.roleCashier, true, []uint64{e.branchTK}, "password", testPassword, "pin", testPIN)
	created := call(t, http.MethodPost, "/users", token, body)
	wantStatus(t, created, http.StatusCreated)
	hire := created.id(t)

	edit := updateBody(name, e.roleCashier, true, []uint64{e.branchTK})
	edit["phone"] = "012999999"
	wantStatus(t, call(t, http.MethodPut, "/users/"+id(hire), token, edit), http.StatusOK)

	reset := call(t, http.MethodPost, "/users/"+id(hire)+"/reset-pin", token, gin.H{})
	wantStatus(t, reset, http.StatusOK)
	if pin, _ := reset.field(t, "pin").(string); len(pin) != 4 {
		t.Fatalf("want a 4-digit PIN, got %q", pin)
	}

	wantStatus(t, call(t, http.MethodDelete, "/users/"+id(hire), token, nil), http.StatusOK)
}

func TestManagerCannotManageUsersAtAll(t *testing.T) {
	e := setup(t)
	token := login(t, e.managerTK).Access
	forged := []string{"X-Role", "Owner", "X-User-Id", "1", "X-Is-Admin", "true", "X-Permissions", "user.create"}
	wantStatus(t, call(t, http.MethodGet, "/users", token, nil, forged...), http.StatusForbidden)
	body := updateBody(e.managerTK.Username, e.roleOwner, true, []uint64{e.branchTK})
	body["is_admin"] = true
	body["permissions"] = []string{"system.reset"}
	wantStatus(t, call(t, http.MethodPut, "/users/"+id(e.managerTK.ID), token, body), http.StatusForbidden)
}

// A role admin's work is their own branch's roles, so these are all TK's.
func TestRoleAdminCannotRaiseAnyone(t *testing.T) {
	e := setup(t)
	role := atBranch(t, newRole(t, "role.view", "role.create", "role.update", "role.delete", "pos.sell"), e.branchTK)
	admin := newUser(t, role, e.branchTK)
	token := login(t, admin).Access
	roleBody := func(perms ...string) gin.H {
		return gin.H{"name": unique("r"), "description": "x", "branch_id": e.branchTK, "permissions": perms, "max_discount_percent": 0, "refund_without_approval_cents": 0}
	}
	hasPermission := func(roleID uint64, key string) bool {
		return dbInt(t, "SELECT COUNT(*) FROM role_permissions rp JOIN permissions p ON p.id = rp.permission_id WHERE rp.role_id = ? AND p.`key` = ?", roleID, key) > 0
	}

	t.Run("can't grant their own role anything", func(t *testing.T) {
		r := call(t, http.MethodPut, "/roles/"+id(role), token, roleBody("role.view", "role.create", "role.update", "role.delete", "pos.sell", "system.reset", "system.manage"))
		wantDenied(t, r, http.StatusForbidden, "CANNOT_EDIT_OWN_ROLE")
		if hasPermission(role, "system.reset") {
			t.Fatal("the permission was granted anyway")
		}
	})
	t.Run("can't create a role with what they lack", func(t *testing.T) {
		wantDenied(t, call(t, http.MethodPost, "/roles", token, roleBody("system.reset")), http.StatusForbidden, "ROLE_ABOVE_YOURS")
	})
	t.Run("can't add what they lack to another role", func(t *testing.T) {
		other := atBranch(t, newRole(t, "pos.sell"), e.branchTK)
		r := call(t, http.MethodPut, "/roles/"+id(other), token, roleBody("pos.sell", "user.create"))
		wantDenied(t, r, http.StatusForbidden, "ROLE_ABOVE_YOURS")
		if hasPermission(other, "user.create") {
			t.Fatal("the permission was granted anyway")
		}
	})
	t.Run("can't change or delete a stronger role", func(t *testing.T) {
		stronger := atBranch(t, newRole(t, "system.reset"), e.branchTK)
		wantDenied(t, call(t, http.MethodPut, "/roles/"+id(stronger), token, roleBody("pos.sell")), http.StatusForbidden, "ROLE_ABOVE_YOURS")
		wantDenied(t, call(t, http.MethodDelete, "/roles/"+id(stronger), token, nil), http.StatusForbidden, "ROLE_ABOVE_YOURS")
	})
	t.Run("the Owner role is locked for them", func(t *testing.T) {
		wantDenied(t, call(t, http.MethodPut, "/roles/"+id(e.roleOwner), token, roleBody()), http.StatusConflict, "ROLE_LOCKED")
		wantDenied(t, call(t, http.MethodDelete, "/roles/"+id(e.roleOwner), token, nil), http.StatusConflict, "ROLE_LOCKED")
	})
	t.Run("the name Owner can't be taken", func(t *testing.T) {
		body := roleBody("pos.sell")
		body["name"] = "owner"
		wantStatus(t, call(t, http.MethodPost, "/roles", token, body), http.StatusUnprocessableEntity)
	})
	t.Run("can do their job with what they hold", func(t *testing.T) {
		created := call(t, http.MethodPost, "/roles", token, roleBody("pos.sell"))
		wantStatus(t, created, http.StatusCreated)
		newID := created.id(t)
		wantStatus(t, call(t, http.MethodPut, "/roles/"+id(newID), token, roleBody()), http.StatusOK)
		wantStatus(t, call(t, http.MethodDelete, "/roles/"+id(newID), token, nil), http.StatusOK)
	})
}

// atBranch makes a role belong to one branch.
func atBranch(t *testing.T, role, branch uint64) uint64 {
	t.Helper()
	if err := env.db.Exec("UPDATE roles SET branch_id = ? WHERE id = ?", branch, role).Error; err != nil {
		t.Fatal(err)
	}
	return role
}

func newRoleWithDiscount(t *testing.T, limit int, permissions ...string) uint64 {
	t.Helper()
	role, err := env.createRole(unique("role"), false, limit, permissions)
	if err != nil {
		t.Fatal(err)
	}
	return role
}

func discountLimit(t *testing.T, role uint64) int64 {
	t.Helper()
	return dbInt(t, "SELECT max_discount_percent FROM role_limits WHERE role_id = ?", role)
}

func refundLimit(t *testing.T, role uint64) int64 {
	t.Helper()
	return dbInt(t, "SELECT refund_without_approval_cents FROM role_limits WHERE role_id = ?", role)
}

func setRefundLimit(t *testing.T, role uint64, cents int64) {
	t.Helper()
	if err := env.db.Exec("UPDATE role_limits SET refund_without_approval_cents = ? WHERE role_id = ?", cents, role).Error; err != nil {
		t.Fatal(err)
	}
}

func roleWithRefund(cents int64) gin.H {
	body := roleWithDiscount(0)
	body["refund_without_approval_cents"] = cents
	return body
}

// roleWithDiscount is a TK role.
func roleWithDiscount(limit int) gin.H {
	return gin.H{"name": unique("r"), "description": "x", "branch_id": env.branchTK, "permissions": []string{"pos.sell"}, "max_discount_percent": limit, "refund_without_approval_cents": 0}
}

func TestRoleAdminCannotSetADiscountLimitAboveTheirOwn(t *testing.T) {
	e := setup(t)
	role := atBranch(t, newRoleWithDiscount(t, 20, "role.view", "role.create", "role.update", "role.delete", "pos.sell"), e.branchTK)
	token := login(t, newUser(t, role, e.branchTK)).Access

	t.Run("can't create a role above it", func(t *testing.T) {
		body := roleWithDiscount(21)
		wantDenied(t, call(t, http.MethodPost, "/roles", token, body), http.StatusForbidden, "DISCOUNT_ABOVE_YOURS")
		if n := dbInt(t, "SELECT COUNT(*) FROM roles WHERE name = ?", body["name"]); n != 0 {
			t.Fatal("the role was created anyway")
		}
	})
	t.Run("can't raise another role above it", func(t *testing.T) {
		other := atBranch(t, newRoleWithDiscount(t, 5, "pos.sell"), e.branchTK)
		wantDenied(t, call(t, http.MethodPut, "/roles/"+id(other), token, roleWithDiscount(21)), http.StatusForbidden, "DISCOUNT_ABOVE_YOURS")
		if got := discountLimit(t, other); got != 5 {
			t.Fatalf("limit changed to %d", got)
		}
	})
	t.Run("can't save a role that's already above it", func(t *testing.T) {
		other := atBranch(t, newRoleWithDiscount(t, 50, "pos.sell"), e.branchTK)
		wantDenied(t, call(t, http.MethodPut, "/roles/"+id(other), token, roleWithDiscount(50)), http.StatusForbidden, "DISCOUNT_ABOVE_YOURS")
	})
	t.Run("can't lift their own ceiling", func(t *testing.T) {
		wantDenied(t, call(t, http.MethodPut, "/roles/"+id(role), token, roleWithDiscount(100)), http.StatusForbidden, "CANNOT_EDIT_OWN_ROLE")
		if got := discountLimit(t, role); got != 20 {
			t.Fatalf("limit changed to %d", got)
		}
	})
	t.Run("can go as high as their own", func(t *testing.T) {
		created := call(t, http.MethodPost, "/roles", token, roleWithDiscount(20))
		wantStatus(t, created, http.StatusCreated)
		if got := discountLimit(t, created.id(t)); got != 20 {
			t.Fatalf("created with %d", got)
		}
		other := atBranch(t, newRoleWithDiscount(t, 5, "pos.sell"), e.branchTK)
		wantStatus(t, call(t, http.MethodPut, "/roles/"+id(other), token, roleWithDiscount(20)), http.StatusOK)
		if got := discountLimit(t, other); got != 20 {
			t.Fatalf("saved with %d", got)
		}
	})
	t.Run("can bring a role above it back down", func(t *testing.T) {
		other := atBranch(t, newRoleWithDiscount(t, 50, "pos.sell"), e.branchTK)
		wantStatus(t, call(t, http.MethodPut, "/roles/"+id(other), token, roleWithDiscount(10)), http.StatusOK)
		if got := discountLimit(t, other); got != 10 {
			t.Fatalf("saved with %d", got)
		}
	})
	t.Run("it's their role's limit now, not when they signed in", func(t *testing.T) {
		raise := roleWithDiscount(30)
		raise["permissions"] = []string{"role.view", "role.create", "role.update", "role.delete", "pos.sell"}
		wantStatus(t, call(t, http.MethodPut, "/roles/"+id(role), login(t, e.owner).Access, raise), http.StatusOK)
		wantStatus(t, call(t, http.MethodPost, "/roles", token, roleWithDiscount(30)), http.StatusCreated)
		wantDenied(t, call(t, http.MethodPost, "/roles", token, roleWithDiscount(31)), http.StatusForbidden, "DISCOUNT_ABOVE_YOURS")
	})
}

func TestRoleAdminCannotSetARefundLimitAboveTheirOwn(t *testing.T) {
	e := setup(t)
	role := atBranch(t, newRole(t, "role.view", "role.create", "role.update", "role.delete", "pos.sell"), e.branchTK)
	setRefundLimit(t, role, 2000)
	token := login(t, newUser(t, role, e.branchTK)).Access

	t.Run("can't create a role above it", func(t *testing.T) {
		body := roleWithRefund(2001)
		wantDenied(t, call(t, http.MethodPost, "/roles", token, body), http.StatusForbidden, "REFUND_ABOVE_YOURS")
		if n := dbInt(t, "SELECT COUNT(*) FROM roles WHERE name = ?", body["name"]); n != 0 {
			t.Fatal("the role was created anyway")
		}
	})
	t.Run("can't raise another role above it", func(t *testing.T) {
		other := atBranch(t, newRole(t, "pos.sell"), e.branchTK)
		wantDenied(t, call(t, http.MethodPut, "/roles/"+id(other), token, roleWithRefund(2001)), http.StatusForbidden, "REFUND_ABOVE_YOURS")
		if got := refundLimit(t, other); got != 0 {
			t.Fatalf("limit changed to %d", got)
		}
	})
	t.Run("can go as high as their own", func(t *testing.T) {
		created := call(t, http.MethodPost, "/roles", token, roleWithRefund(2000))
		wantStatus(t, created, http.StatusCreated)
		if got := refundLimit(t, created.id(t)); got != 2000 {
			t.Fatalf("created with %d", got)
		}
	})
}

func TestUserAdminCannotHandOutBiggerLimits(t *testing.T) {
	e := setup(t)
	_, _, token := userAdmin(t) // 5% and no refunds without approval, like a Cashier
	discounter := newRoleWithDiscount(t, 50, "pos.sell")
	refunder := newRole(t, "pos.sell")
	setRefundLimit(t, refunder, 5000)

	t.Run("on a new account", func(t *testing.T) {
		for role, code := range map[uint64]string{discounter: "DISCOUNT_ABOVE_YOURS", refunder: "REFUND_ABOVE_YOURS"} {
			name := unique("x")
			body := updateBody(name, role, true, []uint64{e.branchTK}, "password", testPassword)
			wantDenied(t, call(t, http.MethodPost, "/users", token, body), http.StatusForbidden, code)
			if n := dbInt(t, "SELECT COUNT(*) FROM users WHERE username = ?", name); n != 0 {
				t.Fatal("the account was created anyway")
			}
		}
	})
	t.Run("on an existing account", func(t *testing.T) {
		victim := newUser(t, e.roleCashier, e.branchTK)
		for role, code := range map[uint64]string{discounter: "DISCOUNT_ABOVE_YOURS", refunder: "REFUND_ABOVE_YOURS"} {
			r := call(t, http.MethodPut, "/users/"+id(victim.ID), token, updateBody(victim.Username, role, true, []uint64{e.branchTK}))
			wantDenied(t, r, http.StatusForbidden, code)
		}
		if got := userRole(t, victim.ID); got != e.roleCashier {
			t.Fatalf("role changed to %d", got)
		}
	})
	// A new password or PIN on such an account would be a way to use its limits.
	t.Run("nor take over an account that already has one", func(t *testing.T) {
		strong := newUser(t, discounter, e.branchTK)
		r := call(t, http.MethodPut, "/users/"+id(strong.ID), token, updateBody(strong.Username, discounter, true, []uint64{e.branchTK}, "password", "attacker-password"))
		wantDenied(t, r, http.StatusForbidden, "DISCOUNT_ABOVE_YOURS")
		wantStatus(t, tryLogin(t, strong.Username, "attacker-password"), http.StatusUnauthorized)
		wantDenied(t, call(t, http.MethodPost, "/users/"+id(strong.ID)+"/reset-pin", token, gin.H{}), http.StatusForbidden, "DISCOUNT_ABOVE_YOURS")
		wantDenied(t, call(t, http.MethodDelete, "/users/"+id(strong.ID), token, nil), http.StatusForbidden, "DISCOUNT_ABOVE_YOURS")
	})
	t.Run("a role within their limits is fine", func(t *testing.T) {
		same := newRoleWithDiscount(t, 5, "pos.sell")
		body := updateBody(unique("x"), same, true, []uint64{e.branchTK}, "password", testPassword)
		wantStatus(t, call(t, http.MethodPost, "/users", token, body), http.StatusCreated)
	})
}

func TestOwnerIsNotHeldBackByTheLimitCeilings(t *testing.T) {
	e := setup(t)
	token := login(t, e.owner).Access
	before := discountLimit(t, e.roleOwner)
	if err := e.db.Exec("UPDATE role_limits SET max_discount_percent = 10, refund_without_approval_cents = 0 WHERE role_id = ?", e.roleOwner).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		env.db.Exec("UPDATE role_limits SET max_discount_percent = ? WHERE role_id = ?", before, e.roleOwner)
	})

	created := call(t, http.MethodPost, "/roles", token, roleWithDiscount(50))
	wantStatus(t, created, http.StatusCreated)
	generous := created.id(t)
	wantStatus(t, call(t, http.MethodPut, "/roles/"+id(generous), token, roleWithDiscount(60)), http.StatusOK)
	wantStatus(t, call(t, http.MethodPost, "/roles", token, roleWithRefund(5000)), http.StatusCreated)

	hire := call(t, http.MethodPost, "/users", token, updateBody(unique("x"), generous, true, []uint64{e.branchTK}, "password", testPassword))
	wantStatus(t, hire, http.StatusCreated)
	hired := hire.id(t)
	t.Cleanup(func() { env.db.Exec("UPDATE users SET active = 0, deleted_at = NOW() WHERE id = ?", hired) })
	wantStatus(t, call(t, http.MethodPost, "/users/"+id(hired)+"/reset-pin", token, gin.H{}), http.StatusOK)
}

func TestOwnerIsNotHeldBackByTheseRules(t *testing.T) {
	e := setup(t)
	token := login(t, e.owner).Access
	stronger := newRole(t, "system.reset")

	name := unique("owner")
	created := call(t, http.MethodPost, "/users", token, updateBody(name, e.roleOwner, true, nil, "password", testPassword))
	wantStatus(t, created, http.StatusCreated)
	second := created.id(t)
	t.Cleanup(func() { env.db.Exec("DELETE FROM users WHERE id = ?", second) })

	wantStatus(t, call(t, http.MethodPut, "/users/"+id(second), token, updateBody(name, stronger, true, []uint64{e.branchBKK})), http.StatusOK)
	wantStatus(t, call(t, http.MethodPut, "/roles/"+id(stronger), token, gin.H{"name": unique("r"), "permissions": []string{"system.reset", "pos.sell"}}), http.StatusOK)
	wantStatus(t, call(t, http.MethodDelete, "/users/"+id(second), token, nil), http.StatusOK)

	t.Run("can save someone with no branch", func(t *testing.T) {
		hire := call(t, http.MethodPost, "/users", token, updateBody(unique("x"), e.roleCashier, true, nil, "password", testPassword))
		wantStatus(t, hire, http.StatusCreated)
		hired := hire.id(t)
		t.Cleanup(func() { env.db.Exec("UPDATE users SET active = 0, deleted_at = NOW() WHERE id = ?", hired) })
	})
}

// activeOwners tests need to be the only Owners around, so the fixture Owner
// steps aside and comes back afterwards.
func sideLineFixtureOwner(t *testing.T) {
	t.Helper()
	env.db.Exec("UPDATE users SET active = 0 WHERE id = ?", env.owner.ID)
	t.Cleanup(func() { env.db.Exec("UPDATE users SET active = 1 WHERE id = ?", env.owner.ID) })
}

func TestTheLastOwnerCantBeRemoved(t *testing.T) {
	e := setup(t)
	token := login(t, e.owner).Access
	me := id(e.owner.ID)

	t.Run("demoted by themself", func(t *testing.T) {
		r := call(t, http.MethodPut, "/users/"+me, token, updateBody(e.owner.Username, e.roleManager, true, []uint64{e.branchTK}))
		wantDenied(t, r, http.StatusConflict, "LAST_OWNER")
		if got := userRole(t, e.owner.ID); got != e.roleOwner {
			t.Fatalf("role changed to %d", got)
		}
	})
	t.Run("disabling themself is refused", func(t *testing.T) {
		r := call(t, http.MethodPut, "/users/"+me, token, updateBody(e.owner.Username, e.roleOwner, false, nil))
		wantDenied(t, r, http.StatusConflict, "CANNOT_DISABLE_SELF")
	})
	t.Run("deleting themself is refused", func(t *testing.T) {
		wantDenied(t, call(t, http.MethodDelete, "/users/"+me, token, nil), http.StatusConflict, "CANNOT_DELETE_SELF")
	})
	t.Run("with a second Owner, one can step down", func(t *testing.T) {
		second := newUser(t, e.roleOwner)
		r := call(t, http.MethodPut, "/users/"+id(second.ID), token, updateBody(second.Username, e.roleManager, true, []uint64{e.branchTK}))
		wantStatus(t, r, http.StatusOK)
	})
}

func TestTwoOwnersDisablingEachOtherLeaveOneStanding(t *testing.T) {
	e := setup(t)
	a, b := newUser(t, e.roleOwner), newUser(t, e.roleOwner)
	tokenA, tokenB := login(t, a).Access, login(t, b).Access
	sideLineFixtureOwner(t)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		call(t, http.MethodPut, "/users/"+id(b.ID), tokenA, updateBody(b.Username, e.roleOwner, false, nil))
	}()
	go func() {
		defer wg.Done()
		call(t, http.MethodPut, "/users/"+id(a.ID), tokenB, updateBody(a.Username, e.roleOwner, false, nil))
	}()
	wg.Wait()

	// Company B has its own Owner, who isn't part of this.
	active := dbInt(t, "SELECT COUNT(*) FROM users u JOIN roles r ON r.id = u.role_id WHERE r.name = 'Owner' AND u.company_id = ? AND u.active = 1 AND u.deleted_at IS NULL", e.companyA)
	if active != 1 {
		t.Fatalf("want exactly one active Owner left, got %d", active)
	}
}

// rolesListed is the roles GET /roles gives this token, by id.
func rolesListed(t *testing.T, token, query string, headers ...string) map[uint64]map[string]interface{} {
	t.Helper()
	r := call(t, http.MethodGet, "/roles"+query, token, nil, headers...)
	wantStatus(t, r, http.StatusOK)
	out := map[uint64]map[string]interface{}{}
	for _, row := range r.rows(t) {
		out[uint64(row["id"].(float64))] = row
	}
	return out
}

func TestRoleAdminStaysInsideTheirBranch(t *testing.T) {
	e := setup(t)
	role := atBranch(t, newRole(t, "role.view", "role.create", "role.update", "role.delete", "user.view", "user.create", "user.update", "user.delete", "pos.sell"), e.branchTK)
	token := login(t, newUser(t, role, e.branchTK)).Access
	body := func(branch interface{}) gin.H {
		return gin.H{"name": unique("r"), "description": "x", "branch_id": branch, "permissions": []string{"pos.sell"}, "max_discount_percent": 0, "refund_without_approval_cents": 0}
	}
	roleName := func(id uint64) string {
		var name string
		env.db.Raw("SELECT name FROM roles WHERE id = ?", id).Scan(&name)
		return name
	}

	t.Run("can't change or delete a shared role", func(t *testing.T) {
		shared := newRole(t, "pos.sell")
		before := roleName(shared)
		wantDenied(t, call(t, http.MethodPut, "/roles/"+id(shared), token, body(nil)), http.StatusForbidden, "ROLE_SHARED")
		wantDenied(t, call(t, http.MethodDelete, "/roles/"+id(shared), token, nil), http.StatusForbidden, "ROLE_SHARED")
		if roleName(shared) != before {
			t.Fatal("the shared role was changed anyway")
		}
	})
	t.Run("can't make a shared role", func(t *testing.T) {
		b := body(nil)
		wantDenied(t, call(t, http.MethodPost, "/roles", token, b), http.StatusUnprocessableEntity, "ROLE_BRANCH_REQUIRED")
		if n := dbInt(t, "SELECT COUNT(*) FROM roles WHERE name = ?", b["name"]); n != 0 {
			t.Fatal("the role was created anyway")
		}
	})
	t.Run("can't make one for another branch", func(t *testing.T) {
		wantDenied(t, call(t, http.MethodPost, "/roles", token, body(e.branchBKK)), http.StatusForbidden, "BRANCH_OUT_OF_SCOPE")
	})
	t.Run("can't see or touch another branch's role", func(t *testing.T) {
		theirs := atBranch(t, newRole(t, "pos.sell"), e.branchBKK)
		wantDenied(t, call(t, http.MethodPut, "/roles/"+id(theirs), token, body(e.branchTK)), http.StatusForbidden, "ROLE_OUT_OF_SCOPE")
		wantDenied(t, call(t, http.MethodDelete, "/roles/"+id(theirs), token, nil), http.StatusForbidden, "ROLE_OUT_OF_SCOPE")
		listed := rolesListed(t, token, "")
		if listed[theirs] != nil || listed[role] == nil || listed[e.roleCashier] == nil {
			t.Fatalf("want shared and TK roles only, got %v", listed)
		}
		wantStatus(t, call(t, http.MethodGet, "/roles?branch_id="+id(e.branchBKK), token, nil), http.StatusForbidden)
	})
	t.Run("can't hand out another branch's role", func(t *testing.T) {
		theirs := atBranch(t, newRole(t, "pos.sell"), e.branchBKK)
		r := call(t, http.MethodPost, "/users", token, updateBody(unique("x"), theirs, true, []uint64{e.branchTK}, "password", testPassword))
		wantDenied(t, r, http.StatusUnprocessableEntity, "ROLE_WRONG_BRANCH")
	})
	t.Run("a role stays at its branch", func(t *testing.T) {
		mine := atBranch(t, newRole(t, "pos.sell"), e.branchTK)
		wantStatus(t, call(t, http.MethodPut, "/roles/"+id(mine), token, body(e.branchBKK)), http.StatusOK)
		if got := dbInt(t, "SELECT branch_id FROM roles WHERE id = ?", mine); got != int64(e.branchTK) {
			t.Fatalf("moved to branch %d", got)
		}
	})
	t.Run("can't change a role they hold at another branch", func(t *testing.T) {
		admin := newUser(t, role, e.branchTK, e.branchBKK)
		bkkRole := atBranch(t, newRole(t, "pos.sell"), e.branchBKK)
		holdAt(t, admin, e.branchBKK, bkkRole)
		mine := login(t, admin).Access
		wantDenied(t, call(t, http.MethodPut, "/roles/"+id(bkkRole), mine, body(nil), atTK()...), http.StatusForbidden, "ROLE_OUT_OF_SCOPE")
	})
	t.Run("can do their job with their branch's roles", func(t *testing.T) {
		created := call(t, http.MethodPost, "/roles", token, body(e.branchTK))
		wantStatus(t, created, http.StatusCreated)
		if got, _ := created.field(t, "branch_id").(float64); uint64(got) != e.branchTK {
			t.Fatalf("created at branch %v", got)
		}
		newID := created.id(t)
		wantStatus(t, call(t, http.MethodPut, "/roles/"+id(newID), token, body(e.branchTK)), http.StatusOK)
		wantStatus(t, call(t, http.MethodDelete, "/roles/"+id(newID), token, nil), http.StatusOK)
	})
}

func TestARoleInUseAtOneBranchCantBeDeleted(t *testing.T) {
	e := setup(t)
	owner := login(t, e.owner).Access
	role := newRole(t, "pos.sell")
	holdAt(t, newUser(t, e.roleCashier, e.branchTK, e.branchBKK), e.branchBKK, role)
	wantDenied(t, call(t, http.MethodDelete, "/roles/"+id(role), owner, nil), http.StatusConflict, "ROLE_IN_USE")
}

func TestOwnerManagesSharedAndBranchRoles(t *testing.T) {
	e := setup(t)
	owner := login(t, e.owner).Access
	body := func(branch interface{}) gin.H {
		return gin.H{"name": unique("r"), "branch_id": branch, "permissions": []string{"pos.sell"}}
	}
	shared := call(t, http.MethodPost, "/roles", owner, body(nil))
	wantStatus(t, shared, http.StatusCreated)
	if shared.field(t, "branch_id") != nil {
		t.Fatalf("want a shared role, got %s", shared)
	}
	bkk := call(t, http.MethodPost, "/roles", owner, body(e.branchBKK))
	wantStatus(t, bkk, http.StatusCreated)
	unknown := call(t, http.MethodPost, "/roles", owner, body(99999999))
	wantDenied(t, unknown, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	if unknown.Error.Fields["branch_id"] == "" {
		t.Fatalf("an unknown branch should be blamed on branch_id: %s", unknown)
	}

	all := rolesListed(t, owner, "")
	atTK := rolesListed(t, owner, "?branch_id="+id(e.branchTK))
	if all[bkk.id(t)] == nil || all[shared.id(t)] == nil || atTK[bkk.id(t)] != nil || atTK[shared.id(t)] == nil {
		t.Fatalf("every role unfiltered, shared and TK's at TK: %v / %v", all, atTK)
	}
	wantStatus(t, call(t, http.MethodPut, "/roles/"+id(shared.id(t)), owner, body(nil)), http.StatusOK)
	wantStatus(t, call(t, http.MethodDelete, "/roles/"+id(bkk.id(t)), owner, nil), http.StatusOK)
}
