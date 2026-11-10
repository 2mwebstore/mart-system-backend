package tests

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

// putCompany saves a company as the super admin: what it has now, with
// changes on top (PUT /platform/companies/:id takes every field).
func putCompany(t *testing.T, companyID uint64, changes gin.H) response {
	t.Helper()
	super := login(t, env.superAdmin).Access
	body := gin.H{}
	for k, v := range companyRow(t, companyID) {
		switch k {
		case "name", "code", "active", "max_branches", "max_users", "retention_months":
			body[k] = v
		}
	}
	for k, v := range changes {
		body[k] = v
	}
	return call(t, http.MethodPut, "/platform/companies/"+id(companyID), super, body)
}

// companyRow is the company as the super admin's list shows it.
func companyRow(t *testing.T, companyID uint64) map[string]interface{} {
	t.Helper()
	r := call(t, http.MethodGet, "/platform/companies", login(t, env.superAdmin).Access, nil)
	wantStatus(t, r, http.StatusOK)
	var rows []map[string]interface{}
	if err := json.Unmarshal(r.Data, &rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if uint64(row["id"].(float64)) == companyID {
			return row
		}
	}
	t.Fatalf("no company %d in %s", companyID, r)
	return nil
}

// wantCounts checks what the super admin's list says a company has.
func wantCounts(t *testing.T, companyID uint64, want map[string]interface{}) {
	t.Helper()
	row := companyRow(t, companyID)
	for k, v := range want {
		if row[k] != v {
			t.Fatalf("company %d has %s %v, want %v (%v)", companyID, k, row[k], v, row)
		}
	}
}

func cashierRole(t *testing.T, companyID uint64) uint64 {
	t.Helper()
	return uint64(dbInt(t, "SELECT id FROM roles WHERE company_id = ? AND name = 'Cashier'", companyID))
}

// A company has at most as many active branches and user accounts as the
// super admin allows. Switched-off ones don't count.
func TestACompanyKeepsToItsLimits(t *testing.T) {
	setup(t)
	c := makeCompany(t)
	owner := login(t, c.owner).Access
	wantCounts(t, c.id, map[string]interface{}{"max_branches": nil, "max_users": nil, "active_user_count": float64(1)})

	r := putCompany(t, c.id, gin.H{"max_branches": 2, "max_users": 2})
	wantStatus(t, r, http.StatusOK)
	if r.field(t, "max_branches") != float64(2) || r.field(t, "max_users") != float64(2) {
		t.Fatalf("limits: %s", r)
	}
	branch := func(code string, active bool) response {
		return call(t, http.MethodPost, "/branches", owner, gin.H{"code": code, "name": code, "active": active})
	}
	setBranch := func(branchID uint64, code string, active bool) response {
		return call(t, http.MethodPut, "/branches/"+id(branchID), owner, gin.H{"code": code, "name": code, "active": active})
	}
	var hq, rs, km uint64
	t.Run("branches", func(t *testing.T) {
		hq = branch("HQ", true).id(t)
		rs = branch("RS", true).id(t)
		wantDenied(t, branch("TK", true), http.StatusConflict, "BRANCH_LIMIT_REACHED")
		// A switched-off branch doesn't take a place, until it's switched on.
		off := branch("KM", false)
		wantStatus(t, off, http.StatusCreated)
		km = off.id(t)
		wantDenied(t, setBranch(km, "KM", true), http.StatusConflict, "BRANCH_LIMIT_REACHED")
		// An active branch saved as it is keeps its place.
		wantStatus(t, setBranch(rs, "RS", true), http.StatusOK)
		wantStatus(t, setBranch(rs, "RS", false), http.StatusOK)
		wantStatus(t, setBranch(km, "KM", true), http.StatusOK)
		wantCounts(t, c.id, map[string]interface{}{"branch_count": float64(3), "active_branch_count": float64(2)})
	})

	role := cashierRole(t, c.id)
	user := func(name string, active bool) response {
		return call(t, http.MethodPost, "/users", owner, updateBody(name, role, active, []uint64{hq}, "password", testPassword))
	}
	var first, second uint64
	t.Run("user accounts, the Owner's among them", func(t *testing.T) {
		first = user(unique("one"), true).id(t)
		wantDenied(t, user(unique("two"), true), http.StatusConflict, "USER_LIMIT_REACHED")
		name := unique("two")
		off := user(name, false)
		wantStatus(t, off, http.StatusCreated)
		second = off.id(t)
		wantDenied(t, call(t, http.MethodPut, "/users/"+id(second), owner, updateBody(name, role, true, []uint64{hq})), http.StatusConflict, "USER_LIMIT_REACHED")
		firstName := dbText(t, "SELECT username FROM users WHERE id = ?", first)
		wantStatus(t, call(t, http.MethodPut, "/users/"+id(first), owner, updateBody(firstName, role, false, []uint64{hq})), http.StatusOK)
		wantStatus(t, call(t, http.MethodPut, "/users/"+id(second), owner, updateBody(name, role, true, []uint64{hq})), http.StatusOK)
		// An active account saved as it is keeps its place.
		wantStatus(t, call(t, http.MethodPut, "/users/"+id(second), owner, updateBody(name, role, true, []uint64{hq})), http.StatusOK)
		wantCounts(t, c.id, map[string]interface{}{"user_count": float64(3), "active_user_count": float64(2)})
	})

	super := login(t, env.superAdmin).Access
	t.Run("the super admin keeps to them too", func(t *testing.T) {
		wantDenied(t, call(t, http.MethodPut, "/platform/companies/"+id(c.id)+"/branches/"+id(rs), super, gin.H{"active": true}), http.StatusConflict, "BRANCH_LIMIT_REACHED")
		wantStatus(t, call(t, http.MethodPut, "/platform/companies/"+id(c.id)+"/branches/"+id(hq), super, gin.H{"active": true}), http.StatusOK)
		wantDenied(t, call(t, http.MethodPut, "/platform/companies/"+id(c.id)+"/users/"+id(first), super, gin.H{"active": true}), http.StatusConflict, "USER_LIMIT_REACHED")
		wantDenied(t, call(t, http.MethodPost, "/platform/companies/"+id(c.id)+"/owner", super, gin.H{"full_name": "Second", "username": unique("second.owner"), "password": testPassword}), http.StatusConflict, "USER_LIMIT_REACHED")
		// Resetting a switched-off Owner switches them back on, which takes a
		// place: the Owner off, and both others on, fills the company.
		env.db.Exec("UPDATE users SET active = 0 WHERE id = ?", c.owner.ID)
		env.db.Exec("UPDATE users SET active = 1 WHERE id = ?", first)
		reset := gin.H{"user_id": c.owner.ID, "password": testPassword}
		wantDenied(t, call(t, http.MethodPost, "/platform/companies/"+id(c.id)+"/owner", super, reset), http.StatusConflict, "USER_LIMIT_REACHED")
		env.db.Exec("UPDATE users SET active = 0 WHERE id = ?", first)
		wantStatus(t, call(t, http.MethodPost, "/platform/companies/"+id(c.id)+"/owner", super, reset), http.StatusOK)
		wantCounts(t, c.id, map[string]interface{}{"active_user_count": float64(2)})
		// An Owner already on is reset at the limit.
		wantStatus(t, call(t, http.MethodPost, "/platform/companies/"+id(c.id)+"/owner", super, reset), http.StatusOK)
		// The reset signed the Owner out.
		owner = login(t, c.owner).Access
	})

	t.Run("lowering a limit switches nothing off; no limit is no limit", func(t *testing.T) {
		wantStatus(t, putCompany(t, c.id, gin.H{"max_branches": 1}), http.StatusOK)
		wantCounts(t, c.id, map[string]interface{}{"max_branches": float64(1), "active_branch_count": float64(2)})
		wantDenied(t, setBranch(rs, "RS", true), http.StatusConflict, "BRANCH_LIMIT_REACHED")
		wantStatus(t, putCompany(t, c.id, gin.H{"max_branches": nil, "max_users": nil}), http.StatusOK)
		wantStatus(t, setBranch(rs, "RS", true), http.StatusOK)
		wantStatus(t, branch("TK", true), http.StatusCreated)
		wantStatus(t, user(unique("three"), true), http.StatusCreated)
		wantCounts(t, c.id, map[string]interface{}{"max_branches": nil, "max_users": nil, "active_branch_count": float64(4)})
	})

	t.Run("a limit is a whole number from 1", func(t *testing.T) {
		for _, bad := range []gin.H{{"max_branches": 0}, {"max_users": 0}, {"max_branches": -1}, {"max_users": 1.5}, {"max_users": 100001}} {
			if r := putCompany(t, c.id, bad); r.Status != http.StatusUnprocessableEntity {
				t.Errorf("%v: want 422, got %s", bad, r)
			}
		}
	})

	t.Run("a deleted branch doesn't count", func(t *testing.T) {
		d := makeCompany(t)
		token := login(t, d.owner).Access
		wantStatus(t, putCompany(t, d.id, gin.H{"max_branches": 1}), http.StatusOK)
		gone := call(t, http.MethodPost, "/branches", token, gin.H{"code": "OLD", "name": "Old", "active": true})
		wantStatus(t, gone, http.StatusCreated)
		wantStatus(t, call(t, http.MethodDelete, "/branches/"+id(gone.id(t)), token, nil), http.StatusOK)
		wantStatus(t, call(t, http.MethodPost, "/branches", token, gin.H{"code": "NEW", "name": "New", "active": true}), http.StatusCreated)
		wantCounts(t, d.id, map[string]interface{}{"branch_count": float64(1), "active_branch_count": float64(1)})
	})

	t.Run("a new company can start with limits", func(t *testing.T) {
		r := call(t, http.MethodPost, "/platform/companies", super, gin.H{
			"name": "Small shop", "code": companyCode(), "owner_full_name": "Small owner", "owner_username": unique("small.owner"),
			"owner_password": testPassword, "max_branches": 1, "max_users": 1,
		})
		wantStatus(t, r, http.StatusCreated)
		if r.field(t, "max_branches") != float64(1) || r.field(t, "max_users") != float64(1) || r.field(t, "active_user_count") != float64(1) {
			t.Fatalf("new company: %s", r)
		}
		small := r.id(t)
		var ownerName string
		for _, o := range r.field(t, "owners").([]interface{}) {
			ownerName = o.(map[string]interface{})["username"].(string)
		}
		token := login(t, namedUser{Username: ownerName}).Access
		first := call(t, http.MethodPost, "/branches", token, gin.H{"code": "HQ", "name": "HQ", "active": true})
		wantStatus(t, first, http.StatusCreated)
		wantDenied(t, call(t, http.MethodPost, "/branches", token, gin.H{"code": "RS", "name": "RS", "active": true}), http.StatusConflict, "BRANCH_LIMIT_REACHED")
		// The Owner is the one account it may have.
		wantDenied(t, call(t, http.MethodPost, "/users", token, updateBody(unique("staff"), cashierRole(t, small), true, []uint64{first.id(t)}, "password", testPassword)),
			http.StatusConflict, "USER_LIMIT_REACHED")
		if r := call(t, http.MethodPost, "/platform/companies", super, gin.H{
			"name": "Bad", "code": companyCode(), "owner_full_name": "Bad", "owner_username": unique("bad.owner"), "owner_password": testPassword, "max_users": 0,
		}); r.Status != http.StatusUnprocessableEntity {
			t.Fatalf("a limit of 0 users: want 422, got %s", r)
		}
	})
}

// Two requests at the same moment can't both take a company's last place.
func TestALimitHoldsForRequestsAtOnce(t *testing.T) {
	setup(t)
	c := makeCompany(t)
	owner := login(t, c.owner).Access
	wantStatus(t, putCompany(t, c.id, gin.H{"max_branches": 3, "max_users": 3}), http.StatusOK)
	hq := call(t, http.MethodPost, "/branches", owner, gin.H{"code": "HQ", "name": "HQ", "active": true})
	wantStatus(t, hq, http.StatusCreated)
	role := cashierRole(t, c.id)

	const n = 8
	branches, users := make([]response, n), make([]response, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(2)
		go func() {
			defer wg.Done()
			branches[i] = call(t, http.MethodPost, "/branches", owner, gin.H{"code": unique("B"), "name": "Branch", "active": true})
		}()
		go func() {
			defer wg.Done()
			users[i] = call(t, http.MethodPost, "/users", owner, updateBody(unique("rush"), role, true, []uint64{hq.id(t)}, "password", testPassword))
		}()
	}
	wg.Wait()
	for what, rows := range map[string][]response{"branches": branches, "users": users} {
		made := 0
		for _, r := range rows {
			switch r.Status {
			case http.StatusCreated:
				made++
			case http.StatusConflict:
			default:
				t.Fatalf("%s: %s", what, r)
			}
		}
		// HQ and the Owner already took one place each.
		if made != 2 {
			t.Errorf("%d %s made at once under a limit of 3, want 2", made, what)
		}
	}
	wantCounts(t, c.id, map[string]interface{}{"active_branch_count": float64(3), "active_user_count": float64(3)})
}

// The super admin sees each company's branches and accounts, switches them on
// and off, and nobody else's through that company.
func TestTheSuperAdminSwitchesACompanysBranchesAndUsers(t *testing.T) {
	e := setup(t)
	super := login(t, e.superAdmin).Access
	c := makeCompany(t)
	owner := login(t, c.owner).Access
	hq := call(t, http.MethodPost, "/branches", owner, gin.H{"code": "HQ", "name": "Head office", "active": true})
	wantStatus(t, hq, http.StatusCreated)
	cashierName := unique("cashier")
	cashier := call(t, http.MethodPost, "/users", owner, updateBody(cashierName, cashierRole(t, c.id), true, []uint64{hq.id(t)}, "password", testPassword))
	wantStatus(t, cashier, http.StatusCreated)
	base := "/platform/companies/" + id(c.id)

	t.Run("lists the company's own", func(t *testing.T) {
		branches := call(t, http.MethodGet, base+"/branches", super, nil)
		wantStatus(t, branches, http.StatusOK)
		if got := listedIDs(t, branches); len(got) != 1 || got[0] != hq.id(t) {
			t.Fatalf("branches: %s", branches)
		}
		users := call(t, http.MethodGet, base+"/users", super, nil)
		wantStatus(t, users, http.StatusOK)
		if got := listedIDs(t, users); len(got) != 2 || got[0] != c.owner.ID || got[1] != cashier.id(t) {
			t.Fatalf("users: %s", users)
		}
		var rows []struct {
			Username string `json:"username"`
			RoleName string `json:"role_name"`
			Active   bool   `json:"active"`
		}
		if err := json.Unmarshal(users.Data, &rows); err != nil {
			t.Fatal(err)
		}
		if rows[0].RoleName != "Owner" || rows[1].RoleName != "Cashier" || rows[1].Username != cashierName || !rows[1].Active {
			t.Fatalf("users: %s", users)
		}
		wantStatus(t, call(t, http.MethodGet, "/platform/companies/999999/branches", super, nil), http.StatusNotFound)
		wantStatus(t, call(t, http.MethodGet, "/platform/companies/999999/users", super, nil), http.StatusNotFound)
	})

	t.Run("a user switched off is signed out and kept out", func(t *testing.T) {
		session := login(t, namedUser{Username: cashierName})
		r := call(t, http.MethodPut, base+"/users/"+id(cashier.id(t)), super, gin.H{"active": false})
		wantStatus(t, r, http.StatusOK)
		if r.field(t, "active") != false {
			t.Fatalf("after: %s", r)
		}
		wantStatus(t, call(t, http.MethodGet, "/auth/me", session.Access, nil), http.StatusUnauthorized)
		wantStatus(t, tryRefresh(t, session.Refresh), http.StatusUnauthorized)
		if r := tryLogin(t, cashierName, testPassword); r.Status == http.StatusOK {
			t.Fatalf("a switched-off account signed in: %s", r)
		}
		if dbInt(t, "SELECT active FROM users WHERE id = ?", cashier.id(t)) != 0 {
			t.Fatal("still active")
		}
		wantStatus(t, call(t, http.MethodPut, base+"/users/"+id(cashier.id(t)), super, gin.H{"active": true}), http.StatusOK)
		wantStatus(t, tryLogin(t, cashierName, testPassword), http.StatusOK)
		// Switched back on, the sessions it ended stay ended.
		wantStatus(t, call(t, http.MethodGet, "/auth/me", session.Access, nil), http.StatusUnauthorized)
		wantStatus(t, tryRefresh(t, session.Refresh), http.StatusUnauthorized)
	})

	t.Run("the company's only active Owner stays on", func(t *testing.T) {
		wantDenied(t, call(t, http.MethodPut, base+"/users/"+id(c.owner.ID), super, gin.H{"active": false}), http.StatusConflict, "LAST_OWNER")
		if dbInt(t, "SELECT active FROM users WHERE id = ?", c.owner.ID) != 1 {
			t.Fatal("the Owner was switched off")
		}
	})

	t.Run("a branch switched off and on", func(t *testing.T) {
		r := call(t, http.MethodPut, base+"/branches/"+id(hq.id(t)), super, gin.H{"active": false})
		wantStatus(t, r, http.StatusOK)
		if r.field(t, "active") != false || dbInt(t, "SELECT active FROM branches WHERE id = ?", hq.id(t)) != 0 {
			t.Fatalf("after: %s", r)
		}
		wantStatus(t, call(t, http.MethodPut, base+"/branches/"+id(hq.id(t)), super, gin.H{"active": true}), http.StatusOK)
		if dbInt(t, "SELECT active FROM branches WHERE id = ?", hq.id(t)) != 1 {
			t.Fatal("still off")
		}
	})

	t.Run("another company's branch or user isn't this company's", func(t *testing.T) {
		theirBranch := uint64(dbInt(t, "SELECT MIN(id) FROM branches WHERE company_id = ?", e.companyA))
		wantStatus(t, call(t, http.MethodPut, base+"/branches/"+id(theirBranch), super, gin.H{"active": false}), http.StatusNotFound)
		wantStatus(t, call(t, http.MethodPut, base+"/users/"+id(e.managerTK.ID), super, gin.H{"active": false}), http.StatusNotFound)
		if dbInt(t, "SELECT active FROM branches WHERE id = ?", theirBranch) != 1 || dbInt(t, "SELECT active FROM users WHERE id = ?", e.managerTK.ID) != 1 {
			t.Fatal("another company's row was switched off")
		}
		wantStatus(t, call(t, http.MethodPut, "/platform/companies/999999/branches/"+id(hq.id(t)), super, gin.H{"active": false}), http.StatusNotFound)
		// The super admin can't reach the super admin, either.
		wantStatus(t, call(t, http.MethodPut, base+"/users/"+id(e.superAdmin.ID), super, gin.H{"active": false}), http.StatusNotFound)
	})

	t.Run("on or off must be said", func(t *testing.T) {
		if r := call(t, http.MethodPut, base+"/branches/"+id(hq.id(t)), super, gin.H{}); r.Status != http.StatusUnprocessableEntity {
			t.Fatalf("no active: %s", r)
		}
		if r := call(t, http.MethodPut, base+"/users/"+id(cashier.id(t)), super, gin.H{}); r.Status != http.StatusUnprocessableEntity {
			t.Fatalf("no active: %s", r)
		}
	})

	t.Run("the company's log shows what the super admin did", func(t *testing.T) {
		for _, action := range []string{"user.update", "branch.update"} {
			if n := dbInt(t, "SELECT COUNT(*) FROM activity_logs WHERE company_id = ? AND module = 'platform' AND action = ? AND user_id = ?", c.id, action, e.superAdmin.ID); n != 2 {
				t.Errorf("%d %s entries in the company's log, want 2", n, action)
			}
		}
		log := call(t, http.MethodGet, "/reports/activity-log", owner, nil)
		wantStatus(t, log, http.StatusOK)
		var rows []struct {
			Module string `json:"module"`
			Action string `json:"action"`
		}
		if err := json.Unmarshal(log.Data, &rows); err != nil {
			t.Fatal(err)
		}
		seen := 0
		for _, r := range rows {
			if r.Module == "platform" {
				seen++
			}
		}
		if seen == 0 {
			t.Fatalf("the Owner's activity log has none of the super admin's changes: %s", log)
		}
	})
}

// The super admin sets how long each company keeps its activity log; the
// company's own staff, its Owner included, can only read it.
func TestOnlyTheSuperAdminSetsLogRetention(t *testing.T) {
	e := setup(t)
	c := makeCompany(t)
	owner := login(t, c.owner).Access
	wantCounts(t, c.id, map[string]interface{}{"retention_months": float64(3)})

	wantStatus(t, putCompany(t, c.id, gin.H{"retention_months": 1}), http.StatusOK)
	wantCounts(t, c.id, map[string]interface{}{"retention_months": float64(1)})
	r := call(t, http.MethodGet, "/settings/activity-log-retention", owner, nil)
	wantStatus(t, r, http.StatusOK)
	if r.field(t, "retention_months") != float64(1) {
		t.Fatalf("the Owner reads %s", r)
	}
	// The other companies keep theirs.
	if got := companyRow(t, e.companyA)["retention_months"]; got != float64(3) {
		t.Fatalf("company A's retention is %v", got)
	}

	for _, token := range []string{owner, login(t, e.owner).Access} {
		if r := call(t, http.MethodPut, "/settings/activity-log-retention", token, gin.H{"retention_months": 2}); r.Status != http.StatusNotFound {
			t.Fatalf("an Owner saved retention: %s", r)
		}
		if r := call(t, http.MethodPost, "/settings/activity-log-retention/clear", token, nil); r.Status != http.StatusNotFound {
			t.Fatalf("an Owner cleared the log: %s", r)
		}
	}
	if got := companyRow(t, c.id)["retention_months"]; got != float64(1) {
		t.Fatalf("retention is %v after the Owner's tries", got)
	}

	for _, bad := range []gin.H{{"retention_months": 0}, {"retention_months": 4}, {"retention_months": nil}} {
		if r := putCompany(t, c.id, bad); r.Status != http.StatusUnprocessableEntity {
			t.Errorf("%v: want 422, got %s", bad, r)
		}
	}

	// The change is in the company's log, with what it was before.
	got := dbText(t, `SELECT COALESCE(JSON_UNQUOTE(JSON_EXTRACT(before_json, '$.retention_months')), '') FROM activity_logs
		WHERE company_id = ? AND module = 'platform' AND action = 'company.update' ORDER BY id DESC LIMIT 1`, c.id)
	if got != "3" {
		t.Fatalf("the log has retention %q before the change, want 3", got)
	}

	wantStatus(t, call(t, http.MethodPost, "/platform/companies/999999/activity-log/clear", login(t, e.superAdmin).Access, nil), http.StatusNotFound)
}
