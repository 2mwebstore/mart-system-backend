package tests

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"
)

// holdAt gives a user a role at one of their branches other than their main one.
func holdAt(t *testing.T, user namedUser, branch, role uint64) {
	t.Helper()
	r := env.db.Exec("UPDATE user_branches SET role_id = ? WHERE user_id = ? AND branch_id = ?", role, user.ID, branch)
	if r.Error != nil || r.RowsAffected != 1 {
		t.Fatalf("user %d isn't at branch %d: %v", user.ID, branch, r.Error)
	}
}

func atTK() []string  { return []string{"X-Branch-Id", id(env.branchTK)} }
func atBKK() []string { return []string{"X-Branch-Id", id(env.branchBKK)} }

// managerHereCashierThere is a Manager at TK and a Cashier at BKK.
func managerHereCashierThere(t *testing.T) namedUser {
	t.Helper()
	u := newUser(t, env.roleManager, env.branchTK, env.branchBKK)
	holdAt(t, u, env.branchBKK, env.roleCashier)
	return u
}

type sessionBranch struct {
	ID          uint64   `json:"id"`
	RoleID      uint64   `json:"role_id"`
	RoleName    string   `json:"role_name"`
	Permissions []string `json:"permissions"`
}

func sessionBranches(t *testing.T, r response) map[uint64]sessionBranch {
	t.Helper()
	var body struct {
		Branches []sessionBranch `json:"branches"`
	}
	if err := json.Unmarshal(r.Data, &body); err != nil {
		t.Fatal(err)
	}
	out := map[uint64]sessionBranch{}
	for _, b := range body.Branches {
		out[b.ID] = b
	}
	return out
}

func TestTheSessionSaysTheRoleAtEachBranch(t *testing.T) {
	e := setup(t)
	u := managerHereCashierThere(t)
	r := tryLogin(t, u.Username, testPassword)
	s := decodeSession(t, r)

	branches := sessionBranches(t, r)
	if tk := branches[e.branchTK]; tk.RoleID != e.roleManager || tk.RoleName != "Manager" || !slices.Contains(tk.Permissions, "expense.create") {
		t.Errorf("at TK: %+v", tk)
	}
	if bkk := branches[e.branchBKK]; bkk.RoleID != e.roleCashier || bkk.RoleName != "Cashier" || slices.Contains(bkk.Permissions, "expense.create") {
		t.Errorf("at BKK: %+v", bkk)
	}
	// Naming no branch, they're their main role.
	if s.User.RoleName != "Manager" || !slices.Contains(s.Permissions, "expense.create") {
		t.Errorf("signed in as %s with %v", s.User.RoleName, s.Permissions)
	}

	t.Run("/auth/me answers for the branch they're at", func(t *testing.T) {
		me := call(t, http.MethodGet, "/auth/me", s.Access, nil, atBKK()...)
		wantStatus(t, me, http.StatusOK)
		var body struct {
			User struct {
				RoleName string `json:"role_name"`
			} `json:"user"`
			Permissions []string `json:"permissions"`
		}
		if err := json.Unmarshal(me.Data, &body); err != nil {
			t.Fatal(err)
		}
		if body.User.RoleName != "Cashier" || slices.Contains(body.Permissions, "expense.create") {
			t.Errorf("at BKK: %+v", body)
		}
		if len(sessionBranches(t, me)) != 2 {
			t.Errorf("both branches should be listed: %s", me)
		}
	})
}

func TestPermissionsFollowTheBranch(t *testing.T) {
	e := setup(t)
	token := login(t, managerHereCashierThere(t)).Access

	t.Run("the branch a request is about", func(t *testing.T) {
		wantStatus(t, call(t, http.MethodGet, "/expenses?branch_id="+id(e.branchTK), token, nil), http.StatusOK)
		wantStatus(t, call(t, http.MethodGet, "/expenses?branch_id="+id(e.branchBKK), token, nil), http.StatusForbidden)
		// Whatever the active branch says.
		wantStatus(t, call(t, http.MethodGet, "/expenses?branch_id="+id(e.branchBKK), token, nil, atTK()...), http.StatusForbidden)
	})

	t.Run("the active branch, for requests about no branch", func(t *testing.T) {
		wantStatus(t, call(t, http.MethodGet, "/reports/activity-log", token, nil, atTK()...), http.StatusOK)
		wantStatus(t, call(t, http.MethodGet, "/reports/activity-log", token, nil, atBKK()...), http.StatusForbidden)
		wantStatus(t, call(t, http.MethodGet, "/expenses", token, nil, atBKK()...), http.StatusForbidden)
		wantStatus(t, call(t, http.MethodGet, "/customers", token, nil, atBKK()...), http.StatusOK)
	})

	t.Run("a Manager's powers at TK don't reach BKK", func(t *testing.T) {
		body := gin.H{"branch_id": e.branchBKK, "category": "OTHER", "amount_cents": 100, "expense_date": today(), "note": unique("n")}
		wantStatus(t, call(t, http.MethodPost, "/expenses", token, body, atTK()...), http.StatusForbidden)
		if n := dbInt(t, "SELECT COUNT(*) FROM expenses WHERE note = ?", body["note"]); n != 0 {
			t.Fatal("the expense was saved anyway")
		}
		body["branch_id"] = e.branchTK
		wantStatus(t, call(t, http.MethodPost, "/expenses", token, body, atTK()...), http.StatusCreated)
	})

	t.Run("naming no branch, their main role at its branches", func(t *testing.T) {
		wantStatus(t, call(t, http.MethodGet, "/reports/activity-log", token, nil), http.StatusOK)
		wantStatus(t, call(t, http.MethodGet, "/expenses", token, nil), http.StatusOK)
	})

	t.Run("a branch that isn't theirs gets their main role, and no way in", func(t *testing.T) {
		elsewhere := []string{"X-Branch-Id", "99999999"}
		wantStatus(t, call(t, http.MethodGet, "/reports/activity-log", token, nil, elsewhere...), http.StatusOK)
		wantStatus(t, call(t, http.MethodGet, "/expenses?branch_id=99999999", token, nil), http.StatusForbidden)
		cashier := login(t, newUser(t, e.roleCashier, e.branchBKK)).Access
		wantStatus(t, call(t, http.MethodGet, "/expenses?branch_id="+id(e.branchTK), cashier, nil, atTK()...), http.StatusForbidden)
	})

	// Saving a user refuses this, but a row written some other way mustn't
	// hand out a role they hold nowhere.
	t.Run("a main role held nowhere isn't used", func(t *testing.T) {
		u := newUser(t, e.roleManager, e.branchTK, e.branchBKK)
		holdAt(t, u, e.branchTK, e.roleCashier)
		holdAt(t, u, e.branchBKK, e.roleCashier)
		wantStatus(t, call(t, http.MethodGet, "/reports/activity-log", login(t, u).Access, nil), http.StatusForbidden)
	})
}

func TestATillWorksWithTheRoleAtItsBranch(t *testing.T) {
	e := setup(t)
	u := managerHereCashierThere(t)
	discounted := func(till string) gin.H {
		body := saleBody(till, unique("key"), e.product)
		body["discount_cents"] = 150 // 15%: a Manager may, a Cashier may not
		return body
	}
	open := func(token, till string) {
		t.Helper()
		wantStatus(t, call(t, http.MethodPost, "/shifts/open", token, gin.H{"device_key": till, "opening_usd_cents": 0, "opening_khr_riel": 0}), http.StatusCreated)
	}

	tillTK, tillBKK := newTill(t, e.branchTK), newTill(t, e.branchBKK)
	atTill := decodeSession(t, tryPIN(t, tillBKK, u.Username, testPIN))
	if atTill.User.RoleName != "Cashier" || slices.Contains(atTill.Permissions, "pos.discount") {
		t.Fatalf("at the BKK till: %s with %v", atTill.User.RoleName, atTill.Permissions)
	}
	open(atTill.Access, tillBKK)

	t.Run("a Cashier there can't give the discount", func(t *testing.T) {
		wantStatus(t, call(t, http.MethodPost, "/sales", atTill.Access, discounted(tillBKK)), http.StatusForbidden)
	})
	t.Run("naming TK changes nothing", func(t *testing.T) {
		wantStatus(t, call(t, http.MethodPost, "/sales", atTill.Access, discounted(tillBKK), atTK()...), http.StatusForbidden)
		wantStatus(t, call(t, http.MethodGet, "/sales?branch_id="+id(e.branchTK), atTill.Access, nil, atTK()...), http.StatusForbidden)
		wantStatus(t, call(t, http.MethodPost, "/sales", atTill.Access, saleBody(tillTK, unique("key"), e.product), atTK()...), http.StatusForbidden)
	})
	t.Run("nor does a refresh", func(t *testing.T) {
		again := decodeSession(t, tryRefresh(t, atTill.Refresh))
		if again.User.RoleName != "Cashier" {
			t.Fatalf("refreshed as %s", again.User.RoleName)
		}
		wantStatus(t, call(t, http.MethodPost, "/sales", again.Access, discounted(tillBKK)), http.StatusForbidden)
	})
	t.Run("a Manager at TK can, up to the Manager's limit", func(t *testing.T) {
		atTK := decodeSession(t, tryPIN(t, tillTK, u.Username, testPIN)).Access
		open(atTK, tillTK)
		wantStatus(t, call(t, http.MethodPost, "/sales", atTK, discounted(tillTK)), http.StatusCreated)
		tooMuch := discounted(tillTK)
		tooMuch["discount_cents"] = 250
		wantDenied(t, call(t, http.MethodPost, "/sales", atTK, tooMuch), http.StatusForbidden, "DISCOUNT_LIMIT_EXCEEDED")
	})
	t.Run("/auth/me tells a till only about till work", func(t *testing.T) {
		atTK := decodeSession(t, tryPIN(t, tillTK, u.Username, testPIN)).Access
		r := call(t, http.MethodGet, "/auth/me", atTK, nil, atBKK()...)
		me := decodeSession(t, r)
		if me.User.RoleName != "Manager" || !slices.Contains(me.Permissions, "pos.discount") || slices.Contains(me.Permissions, "expense.create") {
			t.Fatalf("the TK till is told %s with %v", me.User.RoleName, me.Permissions)
		}
		if tk := sessionBranches(t, r)[e.branchTK]; slices.Contains(tk.Permissions, "expense.create") {
			t.Fatalf("the TK till is told TK allows %v", tk.Permissions)
		}
	})
}

func TestATillSessionEndsWhenItsBranchIsTakenAway(t *testing.T) {
	e := setup(t)
	u := managerHereCashierThere(t)
	till := newTill(t, e.branchBKK)
	s := decodeSession(t, tryPIN(t, till, u.Username, testPIN))
	wantStatus(t, call(t, http.MethodGet, "/customers", s.Access, nil), http.StatusOK)

	env.db.Exec("DELETE FROM user_branches WHERE user_id = ? AND branch_id = ?", u.ID, e.branchBKK)
	wantStatus(t, call(t, http.MethodGet, "/customers", s.Access, nil), http.StatusUnauthorized)
	wantDenied(t, tryRefresh(t, s.Refresh), http.StatusForbidden, "TILL_NOT_ALLOWED")
}

func TestPerBranchRolesAreSaved(t *testing.T) {
	e := setup(t)
	owner := login(t, e.owner).Access
	withRoles := func(name string, main uint64, branches []uint64, perBranch ...gin.H) gin.H {
		body := updateBody(name, main, true, branches, "password", testPassword)
		body["branch_roles"] = perBranch
		return body
	}
	at := func(branch, role uint64) gin.H { return gin.H{"branch_id": branch, "role_id": role} }
	roleAt := func(user uint64, branch uint64) int64 {
		return dbInt(t, "SELECT COALESCE(role_id, 0) FROM user_branches WHERE user_id = ? AND branch_id = ?", user, branch)
	}

	t.Run("a role at one branch", func(t *testing.T) {
		r := call(t, http.MethodPost, "/users", owner, withRoles(unique("x"), e.roleManager, []uint64{e.branchTK, e.branchBKK}, at(e.branchBKK, e.roleCashier)))
		wantStatus(t, r, http.StatusCreated)
		u := r.id(t)
		t.Cleanup(func() { env.db.Exec("UPDATE users SET active = 0, deleted_at = NOW() WHERE id = ?", u) })
		if roleAt(u, e.branchBKK) != int64(e.roleCashier) || roleAt(u, e.branchTK) != 0 {
			t.Fatalf("saved as %s", r)
		}
		roles, _ := r.field(t, "branch_roles").([]interface{})
		if len(roles) != 1 || roles[0].(map[string]interface{})["role_name"] != "Cashier" {
			t.Fatalf("answered with %v", roles)
		}

		// Saving it with the main role there clears it.
		name, _ := r.field(t, "username").(string)
		wantStatus(t, call(t, http.MethodPut, "/users/"+id(u), owner, withRoles(name, e.roleManager, []uint64{e.branchTK, e.branchBKK}, at(e.branchBKK, e.roleManager))), http.StatusOK)
		if roleAt(u, e.branchBKK) != 0 {
			t.Fatal("the main role was saved as a per-branch one")
		}
	})
	t.Run("only at one of their branches", func(t *testing.T) {
		both := []uint64{e.branchTK, e.branchBKK}
		for name, body := range map[string]gin.H{
			"a branch they're not at":   withRoles(unique("x"), e.roleManager, []uint64{e.branchTK}, at(e.branchBKK, e.roleCashier)),
			"two for one branch":        withRoles(unique("x"), e.roleManager, both, at(e.branchBKK, e.roleCashier), at(e.branchBKK, e.roleCashier)),
			"a role that doesn't exist": withRoles(unique("x"), e.roleManager, both, at(e.branchBKK, 99999999)),
			"no role":                   withRoles(unique("x"), e.roleManager, both, at(e.branchBKK, 0)),
		} {
			r := call(t, http.MethodPost, "/users", owner, body)
			wantDenied(t, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
			if r.Error.Fields["branch_roles"] == "" {
				t.Errorf("%s: want it blamed on branch_roles, got %s", name, r)
			}
		}
	})
	t.Run("the main role has to apply somewhere", func(t *testing.T) {
		r := call(t, http.MethodPost, "/users", owner, withRoles(unique("x"), e.roleManager, []uint64{e.branchTK}, at(e.branchTK, e.roleCashier)))
		wantDenied(t, r, http.StatusUnprocessableEntity, "MAIN_ROLE_UNUSED")
	})
	t.Run("Owner is never per branch", func(t *testing.T) {
		r := call(t, http.MethodPost, "/users", owner, withRoles(unique("x"), e.roleManager, []uint64{e.branchTK, e.branchBKK}, at(e.branchBKK, e.roleOwner)))
		wantDenied(t, r, http.StatusUnprocessableEntity, "OWNER_ROLE_GLOBAL")
		r = call(t, http.MethodPost, "/users", owner, withRoles(unique("x"), e.roleOwner, []uint64{e.branchTK, e.branchBKK}, at(e.branchBKK, e.roleCashier)))
		wantDenied(t, r, http.StatusUnprocessableEntity, "OWNER_ROLE_GLOBAL")
	})
	t.Run("a branch's role only at that branch", func(t *testing.T) {
		tkOnly := atBranch(t, newRole(t, "pos.sell"), e.branchTK)
		r := call(t, http.MethodPost, "/users", owner, withRoles(unique("x"), tkOnly, []uint64{e.branchBKK}))
		wantDenied(t, r, http.StatusUnprocessableEntity, "ROLE_WRONG_BRANCH")
		r = call(t, http.MethodPost, "/users", owner, withRoles(unique("x"), e.roleCashier, []uint64{e.branchTK, e.branchBKK}, at(e.branchBKK, tkOnly)))
		wantDenied(t, r, http.StatusUnprocessableEntity, "ROLE_WRONG_BRANCH")
		r = call(t, http.MethodPost, "/users", owner, withRoles(unique("x"), tkOnly, nil))
		wantDenied(t, r, http.StatusUnprocessableEntity, "ROLE_WRONG_BRANCH")

		r = call(t, http.MethodPost, "/users", owner, withRoles(unique("x"), e.roleCashier, []uint64{e.branchTK, e.branchBKK}, at(e.branchTK, tkOnly)))
		wantStatus(t, r, http.StatusCreated)
		hired := r.id(t)
		t.Cleanup(func() { env.db.Exec("UPDATE users SET active = 0, deleted_at = NOW() WHERE id = ?", hired) })
	})
}

func TestPerBranchRolesAreHeldToTheCeilings(t *testing.T) {
	e := setup(t)
	// A user admin at both branches, with a Cashier's limits.
	adminRole := newRoleWithDiscount(t, 5, append([]string{"user.view", "user.create", "user.update", "user.delete"}, cashierPermissions...)...)
	admin := newUser(t, adminRole, e.branchTK, e.branchBKK)
	token := login(t, admin).Access
	discounter := newRoleWithDiscount(t, 50, "pos.sell")
	stronger := newRole(t, "system.reset", "pos.sell")

	t.Run("can't hand out a bigger role at one branch", func(t *testing.T) {
		for role, code := range map[uint64]string{discounter: "DISCOUNT_ABOVE_YOURS", stronger: "ROLE_ABOVE_YOURS", e.roleOwner: "OWNER_ONLY"} {
			body := updateBody(unique("x"), e.roleCashier, true, []uint64{e.branchTK, e.branchBKK}, "password", testPassword)
			body["branch_roles"] = []gin.H{{"branch_id": e.branchBKK, "role_id": role}}
			wantDenied(t, call(t, http.MethodPost, "/users", token, body), http.StatusForbidden, code)
		}
	})
	t.Run("can't take over someone with a bigger role at one branch", func(t *testing.T) {
		strong := newUser(t, e.roleCashier, e.branchTK, e.branchBKK)
		holdAt(t, strong, e.branchBKK, discounter)
		body := updateBody(strong.Username, e.roleCashier, true, []uint64{e.branchTK, e.branchBKK}, "password", "attacker-password")
		body["branch_roles"] = []gin.H{{"branch_id": e.branchBKK, "role_id": discounter}}
		wantDenied(t, call(t, http.MethodPut, "/users/"+id(strong.ID), token, body), http.StatusForbidden, "DISCOUNT_ABOVE_YOURS")
		wantDenied(t, call(t, http.MethodPost, "/users/"+id(strong.ID)+"/reset-pin", token, gin.H{}), http.StatusForbidden, "DISCOUNT_ABOVE_YOURS")
		wantDenied(t, call(t, http.MethodDelete, "/users/"+id(strong.ID), token, nil), http.StatusForbidden, "DISCOUNT_ABOVE_YOURS")
	})
	t.Run("can't change their own role at a branch", func(t *testing.T) {
		body := updateBody(admin.Username, adminRole, true, []uint64{e.branchTK, e.branchBKK})
		body["branch_roles"] = []gin.H{{"branch_id": e.branchBKK, "role_id": e.roleCashier}}
		wantDenied(t, call(t, http.MethodPut, "/users/"+id(admin.ID), token, body), http.StatusForbidden, "CANNOT_CHANGE_OWN_ACCESS")
	})
	t.Run("can give a smaller role at one branch", func(t *testing.T) {
		body := updateBody(unique("x"), e.roleCashier, true, []uint64{e.branchTK, e.branchBKK}, "password", testPassword)
		body["branch_roles"] = []gin.H{{"branch_id": e.branchBKK, "role_id": newRoleWithDiscount(t, 5, "pos.sell")}}
		r := call(t, http.MethodPost, "/users", token, body)
		wantStatus(t, r, http.StatusCreated)
		hired := r.id(t)
		t.Cleanup(func() { env.db.Exec("UPDATE users SET active = 0, deleted_at = NOW() WHERE id = ?", hired) })
	})
}

// The ceilings are the role the admin holds where they're working.
func TestAUserAdminsCeilingIsTheirRoleThere(t *testing.T) {
	e := setup(t)
	small := newRoleWithDiscount(t, 5, append([]string{"user.view", "user.create", "user.update", "user.delete"}, cashierPermissions...)...)
	big := newRoleWithDiscount(t, 20, append([]string{"user.view", "user.create", "user.update", "user.delete"}, cashierPermissions...)...)
	admin := newUser(t, small, e.branchTK, e.branchBKK)
	holdAt(t, admin, e.branchBKK, big)
	token := login(t, admin).Access
	at15 := newRoleWithDiscount(t, 15, "pos.sell")

	hire := func(branch uint64, header []string) response {
		return call(t, http.MethodPost, "/users", token, updateBody(unique("x"), at15, true, []uint64{branch}, "password", testPassword), header...)
	}
	wantDenied(t, hire(e.branchTK, atTK()), http.StatusForbidden, "DISCOUNT_ABOVE_YOURS")
	// The big role only reaches BKK.
	wantDenied(t, hire(e.branchTK, atBKK()), http.StatusForbidden, "BRANCH_OUT_OF_SCOPE")
	r := hire(e.branchBKK, atBKK())
	wantStatus(t, r, http.StatusCreated)
	hired := r.id(t)
	t.Cleanup(func() { env.db.Exec("UPDATE users SET active = 0, deleted_at = NOW() WHERE id = ?", hired) })
}

// Saving a user refuses these, so they can only come from rows written some
// other way. They mustn't count.
func TestRolesThatCantBeHeldThereAreIgnored(t *testing.T) {
	e := setup(t)
	t.Run("a per-branch Owner", func(t *testing.T) {
		u := newUser(t, e.roleCashier, e.branchTK, e.branchBKK)
		holdAt(t, u, e.branchBKK, e.roleOwner)
		token := login(t, u).Access
		wantStatus(t, call(t, http.MethodGet, "/users", token, nil, atBKK()...), http.StatusForbidden)
		wantStatus(t, call(t, http.MethodGet, "/expenses?branch_id="+id(e.branchBKK), token, nil), http.StatusForbidden)
	})
	t.Run("another branch's role", func(t *testing.T) {
		tkManager := atBranch(t, newRoleWithDiscount(t, 20, managerPermissions...), e.branchTK)
		u := newUser(t, e.roleCashier, e.branchTK, e.branchBKK)
		holdAt(t, u, e.branchBKK, tkManager)
		token := login(t, u).Access
		wantStatus(t, call(t, http.MethodGet, "/expenses?branch_id="+id(e.branchBKK), token, nil), http.StatusForbidden)
		wantStatus(t, call(t, http.MethodGet, "/expenses?branch_id="+id(e.branchTK), token, nil), http.StatusForbidden)
	})
}

func TestTheStaffReportUsesTheRoleAtTheBranch(t *testing.T) {
	e := setup(t)
	helper := atBranch(t, newRole(t, "pos.sell"), e.branchBKK)
	u := newUser(t, e.roleManager, e.branchTK, e.branchBKK)
	holdAt(t, u, e.branchBKK, helper)
	owner := login(t, e.owner).Access

	report := func(branch uint64) (staff map[uint64]string, roles map[uint64]bool) {
		t.Helper()
		r := call(t, http.MethodGet, "/reports/staff?branch_id="+id(branch), owner, nil)
		wantStatus(t, r, http.StatusOK)
		var body struct {
			Staff []struct {
				ID       uint64 `json:"id"`
				RoleName string `json:"role_name"`
			} `json:"staff"`
			Roles []struct {
				ID uint64 `json:"id"`
			} `json:"roles"`
		}
		if err := json.Unmarshal(r.Data, &body); err != nil {
			t.Fatal(err)
		}
		staff, roles = map[uint64]string{}, map[uint64]bool{}
		for _, s := range body.Staff {
			staff[s.ID] = s.RoleName
		}
		for _, r := range body.Roles {
			roles[r.ID] = true
		}
		return staff, roles
	}
	staff, roles := report(e.branchTK)
	if staff[u.ID] != "Manager" || roles[helper] {
		t.Errorf("at TK they're a Manager and BKK's role isn't listed: %v %v", staff, roles)
	}
	staff, roles = report(e.branchBKK)
	if _, listed := staff[u.ID]; listed || !roles[helper] || !roles[e.roleCashier] {
		t.Errorf("at BKK they're a helper, not staff, and BKK's role is listed: %v %v", staff, roles)
	}
}
