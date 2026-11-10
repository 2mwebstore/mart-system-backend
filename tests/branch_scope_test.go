package tests

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// bkkResources is one of everything that belongs to the BKK1 branch.
type bkkResources struct {
	expense, purchaseOrder, invoice, shift, sale, table, order uint64
	till, tableToken, saleKey                                  string
}

func saleBody(till, key string, productID uint64) gin.H {
	return gin.H{
		"device_key": till, "idempotency_key": key,
		"items":   []gin.H{{"product_id": productID, "qty": 1}},
		"payment": gin.H{"method": "CASH", "received_usd_cents": 100000000},
	}
}

func makeBKKResources(t *testing.T) bkkResources {
	t.Helper()
	e := env
	o := login(t, e.owner).Access
	made := func(path string, body interface{}) response {
		t.Helper()
		r := call(t, http.MethodPost, path, o, body)
		if r.Status != http.StatusCreated && r.Status != http.StatusOK {
			t.Fatalf("POST %s: %s", path, r)
		}
		return r
	}
	var res bkkResources
	res.till = newTill(t, e.branchBKK)
	res.saleKey = unique("key")
	res.expense = made("/expenses", gin.H{"branch_id": e.branchBKK, "category": "OTHER", "amount_cents": 100, "expense_date": today()}).id(t)
	res.purchaseOrder = made("/purchase-orders", gin.H{"branch_id": e.branchBKK, "supplier_id": e.supplier, "items": []gin.H{{"product_id": e.product, "qty": 1, "unit_cost_cents": 100}}}).id(t)
	res.invoice = made("/invoices", gin.H{"branch_id": e.branchBKK, "customer_id": e.customer, "invoice_date": today(), "items": []gin.H{{"product_id": e.product, "qty": 1, "unit_price_cents": 100}}}).id(t)
	res.shift = made("/shifts/open", gin.H{"device_key": res.till, "opening_usd_cents": 0, "opening_khr_riel": 0}).id(t)
	res.sale = made("/sales", saleBody(res.till, res.saleKey, e.product)).id(t)
	table := made("/dining-tables", gin.H{"branch_id": e.branchBKK, "name": unique("T"), "active": true})
	res.table = table.id(t)
	res.tableToken, _ = table.field(t, "token").(string)
	wantStatus(t, call(t, http.MethodPost, "/public/tables/"+res.tableToken+"/orders", "", gin.H{"items": []gin.H{{"product_id": e.product, "qty": 1}}}), http.StatusCreated)
	res.order = uint64(dbInt(t, "SELECT id FROM table_orders WHERE table_id = ? ORDER BY id DESC LIMIT 1", res.table))
	return res
}

func TestManagerCantReachAnotherBranch(t *testing.T) {
	e := setup(t)
	res := makeBKKResources(t)
	token := login(t, e.managerTK).Access
	bkk := id(e.branchBKK)
	tk := e.branchTK

	t.Run("reads", func(t *testing.T) {
		for _, path := range []string{
			"/sales", "/invoices", "/expenses", "/shifts", "/purchase-orders", "/table-orders", "/stock-movements",
			"/products", "/reports/dashboard", "/reports/transactions", "/reports/by-product", "/reports/by-cashier",
		} {
			wantStatus(t, call(t, http.MethodGet, path+"?branch_id="+bkk, token, nil), http.StatusForbidden)
		}
		for _, path := range []string{"/sales/" + id(res.sale), "/invoices/" + id(res.invoice)} {
			r := call(t, http.MethodGet, path, token, nil)
			if r.Status != http.StatusForbidden && r.Status != http.StatusNotFound {
				t.Errorf("GET %s: %s", path, r)
			}
		}
	})

	t.Run("branch_id=all doesn't widen a list", func(t *testing.T) {
		r := call(t, http.MethodGet, "/sales?branch_id=all&per_page=200", token, nil)
		wantStatus(t, r, http.StatusOK)
		for _, row := range r.rows(t) {
			if uint64(row["branch_id"].(float64)) != tk {
				t.Fatalf("got a row from branch %v", row["branch_id"])
			}
		}
	})

	t.Run("writes", func(t *testing.T) {
		item := []gin.H{{"product_id": e.product, "qty": 1, "unit_cost_cents": 1}}
		invoiceItem := []gin.H{{"product_id": e.product, "qty": 1, "unit_price_cents": 1}}
		payment := gin.H{"paid_on": today(), "method_name": "Cash", "method_type": "CASH", "amount_cents": 1}
		attempts := []struct {
			method, path string
			body         interface{}
		}{
			{http.MethodPost, "/expenses", gin.H{"branch_id": e.branchBKK, "category": "OTHER", "amount_cents": 1, "expense_date": today()}},
			{http.MethodPut, "/expenses/" + id(res.expense), gin.H{"branch_id": tk, "category": "OTHER", "amount_cents": 1, "expense_date": today()}},
			{http.MethodDelete, "/expenses/" + id(res.expense), nil},
			{http.MethodPost, "/purchase-orders", gin.H{"branch_id": e.branchBKK, "supplier_id": e.supplier, "items": item}},
			{http.MethodPut, "/purchase-orders/" + id(res.purchaseOrder), gin.H{"branch_id": tk, "supplier_id": e.supplier, "items": item}},
			{http.MethodPost, "/purchase-orders/" + id(res.purchaseOrder) + "/receive", gin.H{}},
			{http.MethodDelete, "/purchase-orders/" + id(res.purchaseOrder), nil},
			{http.MethodPost, "/invoices", gin.H{"branch_id": e.branchBKK, "customer_id": e.customer, "invoice_date": today(), "items": invoiceItem}},
			{http.MethodPut, "/invoices/" + id(res.invoice), gin.H{"branch_id": tk, "customer_id": e.customer, "invoice_date": today(), "items": invoiceItem}},
			{http.MethodPost, "/invoices/" + id(res.invoice) + "/payments", payment},
			{http.MethodPost, "/invoices/" + id(res.invoice) + "/cancel", gin.H{"reason": "x"}},
			{http.MethodPost, "/shifts/" + id(res.shift) + "/cash-movements", gin.H{"type": "PAYOUT", "amount_usd_cents": 1, "reason": "x"}},
			{http.MethodPost, "/shifts/" + id(res.shift) + "/close", gin.H{"counted_usd_cents": 0, "counted_khr_riel": 0}},
			{http.MethodPost, "/shifts/open", gin.H{"device_key": e.tillBKK}},
			{http.MethodPost, "/sales", saleBody(res.till, unique("key"), e.product)},
			{http.MethodPost, "/sales/" + id(res.sale) + "/void", gin.H{"reason": "x"}},
			{http.MethodPut, "/sales/" + id(res.sale), gin.H{"items": []gin.H{{"product_id": e.product, "qty": 1}}}},
			{http.MethodPost, "/table-orders/" + id(res.order) + "/accept", gin.H{}},
			{http.MethodPost, "/table-orders/" + id(res.order) + "/cancel", gin.H{"reason": "x"}},
		}
		for _, a := range attempts {
			r := call(t, a.method, a.path, token, a.body)
			if r.Status != http.StatusForbidden && r.Status != http.StatusNotFound {
				t.Errorf("%s %s: want 403 or 404, got %s", a.method, a.path, r)
			}
		}
	})

	t.Run("the purchase order survived", func(t *testing.T) {
		if n := dbInt(t, "SELECT COUNT(*) FROM purchase_orders WHERE id = ?", res.purchaseOrder); n != 1 {
			t.Fatal("a manager from another branch deleted it")
		}
		wantStatus(t, call(t, http.MethodDelete, "/purchase-orders/"+id(res.purchaseOrder), login(t, e.managerBKK).Access, nil), http.StatusOK)
	})
}

func TestActivityLogStaysInsideTheBranch(t *testing.T) {
	e := setup(t)
	expenseBy := func(token string, branch uint64) uint64 {
		t.Helper()
		r := call(t, http.MethodPost, "/expenses", token, gin.H{"branch_id": branch, "category": "OTHER", "amount_cents": 1, "expense_date": today()})
		wantStatus(t, r, http.StatusCreated)
		return r.id(t)
	}
	bkkManager := login(t, e.managerBKK).Access
	tkManager := login(t, e.managerTK).Access
	owner := login(t, e.owner).Access
	bkkEvent := expenseBy(bkkManager, e.branchBKK)
	tkEvent := expenseBy(tkManager, e.branchTK)
	ownerEvent := expenseBy(owner, e.branchTK)

	sees := func(token string, event uint64) bool {
		t.Helper()
		r := call(t, http.MethodGet, "/reports/activity-log?per_page=200&date_from="+today()+"&date_to="+today(), token, nil)
		wantStatus(t, r, http.StatusOK)
		for _, row := range r.rows(t) {
			if row["entity_type"] == "expense" && row["entity_id"] == float64(event) {
				return true
			}
		}
		return false
	}
	if sees(tkManager, bkkEvent) {
		t.Error("a TK manager can see what a BKK1 manager did")
	}
	if !sees(tkManager, tkEvent) {
		t.Error("a TK manager should see what TK staff did")
	}
	if sees(tkManager, ownerEvent) {
		t.Error("a manager can see what the Owner did")
	}
	if !sees(owner, bkkEvent) || !sees(owner, tkEvent) {
		t.Error("the Owner should see everything")
	}

	t.Run("the totals don't count other branches either", func(t *testing.T) {
		totals := func(token string) float64 {
			r := call(t, http.MethodGet, "/reports/activity-log?per_page=1&date_from="+today()+"&date_to="+today(), token, nil)
			wantStatus(t, r, http.StatusOK)
			return r.Meta["total"].(float64)
		}
		if tk, all := totals(tkManager), totals(owner); tk >= all {
			t.Fatalf("manager's total %v should be below the Owner's %v", tk, all)
		}
	})
}

// usersListed is who GET /users gives this token, and the total it reports.
func usersListed(t *testing.T, token, filters string) (map[uint64]bool, float64) {
	t.Helper()
	r := call(t, http.MethodGet, "/users?per_page=200"+filters, token, nil)
	wantStatus(t, r, http.StatusOK)
	seen := map[uint64]bool{}
	for _, row := range r.rows(t) {
		seen[uint64(row["id"].(float64))] = true
	}
	return seen, r.Meta["total"].(float64)
}

func TestUserListStaysInsideTheBranch(t *testing.T) {
	e := setup(t)
	viewer := newUser(t, newRole(t, "user.view", "user.create", "user.update", "user.delete"), e.branchTK)
	token := login(t, viewer).Access

	sameBranch := newUser(t, e.roleCashier, e.branchTK)
	bothBranches := newUser(t, e.roleCashier, e.branchTK, e.branchBKK)
	otherBranch := newUser(t, e.roleCashier, e.branchBKK)
	noBranch := newUser(t, e.roleCashier)

	t.Run("lists who they share a branch with, and nobody else", func(t *testing.T) {
		seen, total := usersListed(t, token, "")
		for _, u := range []namedUser{viewer, sameBranch, bothBranches, e.managerTK} {
			if !seen[u.ID] {
				t.Errorf("%s should be listed", u.Username)
			}
		}
		for _, u := range []namedUser{otherBranch, noBranch, e.managerBKK} {
			if seen[u.ID] {
				t.Errorf("%s shouldn't be listed", u.Username)
			}
		}
		if total != float64(len(seen)) {
			t.Errorf("total says %v, but %d were listed", total, len(seen))
		}
	})

	t.Run("an Owner is on every branch", func(t *testing.T) {
		if seen, _ := usersListed(t, token, ""); !seen[e.owner.ID] {
			t.Error("the Owner should be listed")
		}
	})

	t.Run("the filters can't widen it", func(t *testing.T) {
		for _, filters := range []string{
			"&q=" + otherBranch.Username,
			"&q=" + noBranch.Username,
			"&role_id=" + id(e.roleCashier),
			"&active=true",
			"&branch_id=" + id(e.branchTK),
			"&branch_id=all",
		} {
			seen, total := usersListed(t, token, filters)
			if seen[otherBranch.ID] || seen[noBranch.ID] {
				t.Errorf("%s reaches past the branch: %v", filters, seen)
			}
			if total != float64(len(seen)) {
				t.Errorf("%s: total says %v, but %d were listed", filters, total, len(seen))
			}
		}
		if seen, _ := usersListed(t, token, "&q="+sameBranch.Username); !seen[sameBranch.ID] {
			t.Error("searching should still find someone at their branch")
		}
	})

	t.Run("another branch can't be asked for", func(t *testing.T) {
		wantStatus(t, call(t, http.MethodGet, "/users?branch_id="+id(e.branchBKK), token, nil), http.StatusForbidden)
	})

	t.Run("role.manage gets the same list", func(t *testing.T) {
		seen, _ := usersListed(t, login(t, newUser(t, newRole(t, "role.view", "role.create", "role.update", "role.delete"), e.branchTK)).Access, "")
		if !seen[sameBranch.ID] || seen[otherBranch.ID] || seen[noBranch.ID] {
			t.Errorf("got %v", seen)
		}
	})

	t.Run("someone at two branches gets both", func(t *testing.T) {
		both := login(t, newUser(t, newRole(t, "user.view", "user.create", "user.update", "user.delete"), e.branchTK, e.branchBKK)).Access
		seen, _ := usersListed(t, both, "")
		if !seen[sameBranch.ID] || !seen[otherBranch.ID] || seen[noBranch.ID] {
			t.Errorf("got %v", seen)
		}
		seen, _ = usersListed(t, both, "&branch_id="+id(e.branchBKK))
		if seen[sameBranch.ID] || !seen[otherBranch.ID] || !seen[bothBranches.ID] {
			t.Errorf("narrowed to BKK1, got %v", seen)
		}
	})

	t.Run("someone with no branch shares one with nobody", func(t *testing.T) {
		seen, total := usersListed(t, login(t, newUser(t, newRole(t, "user.view", "user.create", "user.update", "user.delete"))).Access, "")
		if len(seen) != 0 || total != 0 {
			t.Errorf("got %v, total %v", seen, total)
		}
	})

	t.Run("the Owner sees everyone", func(t *testing.T) {
		seen, _ := usersListed(t, login(t, e.owner).Access, "")
		for _, u := range []namedUser{viewer, sameBranch, bothBranches, otherBranch, noBranch, e.managerBKK, e.owner} {
			if !seen[u.ID] {
				t.Errorf("%s should be listed", u.Username)
			}
		}
	})
}

func TestAnIdempotencyKeyCantOpenAnotherBranchsSale(t *testing.T) {
	e := setup(t)
	res := makeBKKResources(t)
	till := newTill(t, e.branchTK)
	manager := login(t, e.managerTK).Access
	wantStatus(t, call(t, http.MethodPost, "/shifts/open", manager, gin.H{"device_key": till, "opening_usd_cents": 0, "opening_khr_riel": 0}), http.StatusCreated)

	r := call(t, http.MethodPost, "/sales", manager, saleBody(till, res.saleKey, e.product))
	wantDenied(t, r, http.StatusConflict, "IDEMPOTENCY_KEY_CONFLICT")
	if strings.Contains(r.Raw, "items") {
		t.Fatalf("the sale came back: %s", r)
	}

	t.Run("a retry in the same branch still gets its sale", func(t *testing.T) {
		replay := call(t, http.MethodPost, "/sales", login(t, e.managerBKK).Access, saleBody(res.till, res.saleKey, e.product))
		wantStatus(t, replay, http.StatusOK)
		if replay.id(t) != res.sale {
			t.Fatalf("got sale %d, want %d", replay.id(t), res.sale)
		}
	})
}

func TestDatabaseErrorsStayOnTheServer(t *testing.T) {
	e := setup(t)
	owner := login(t, e.owner).Access
	raw := regexp.MustCompile(`(?i)Error \d{4}|constraint|foreign key|com_mart|INSERT INTO|SQLSTATE|Duplicate entry|Data too long|gorm|\.go:`)
	long := strings.Repeat("x", 400)
	attempts := []struct {
		path string
		body gin.H
	}{
		{"/products", gin.H{"name_en": unique("p"), "category_id": 99999999}},
		{"/products", gin.H{"name_en": unique("p"), "supplier_id": 99999999}},
		{"/purchase-orders", gin.H{"branch_id": e.branchTK, "supplier_id": 99999999, "items": []gin.H{{"product_id": 99999999, "qty": 1, "unit_cost_cents": 1}}}},
		{"/categories", gin.H{"name_en": long, "name": long}},
		{"/suppliers", gin.H{"name": long}},
		{"/branches", gin.H{"name": long, "code": long}},
		{"/payment-methods", gin.H{"name": long, "type": "OTHER"}},
	}
	for _, a := range attempts {
		r := call(t, http.MethodPost, a.path, owner, a.body)
		if raw.MatchString(r.Raw) {
			t.Errorf("POST %s exposes a database error: %s", a.path, r)
		}
	}
}

// userRow is one user as GET /users gives it to this token.
func userRow(t *testing.T, token string, user uint64, filters string) map[string]interface{} {
	t.Helper()
	r := call(t, http.MethodGet, "/users?per_page=200"+filters, token, nil)
	wantStatus(t, r, http.StatusOK)
	for _, row := range r.rows(t) {
		if uint64(row["id"].(float64)) == user {
			return row
		}
	}
	return nil
}

func idsOf(v interface{}, key string) []uint64 {
	out := []uint64{}
	for _, x := range v.([]interface{}) {
		if key != "" {
			x = x.(map[string]interface{})[key]
		}
		out = append(out, uint64(x.(float64)))
	}
	return out
}

func TestOtherBranchesStayOutOfTheUserList(t *testing.T) {
	e := setup(t)
	viewer := login(t, newUser(t, newRole(t, "user.view", "user.create", "user.update", "user.delete"), e.branchTK)).Access
	both := newUser(t, e.roleCashier, e.branchTK, e.branchBKK)
	holdAt(t, both, e.branchBKK, e.roleManager)
	here := newUser(t, e.roleCashier, e.branchTK)

	row := userRow(t, viewer, both.ID, "")
	if got := idsOf(row["branch_ids"], ""); len(got) != 1 || got[0] != e.branchTK {
		t.Errorf("branch_ids should be TK only, got %v", got)
	}
	if roles := row["branch_roles"].([]interface{}); len(roles) != 0 {
		t.Errorf("the role at BKK should be left out, got %v", roles)
	}
	if row["has_other_branches"] != true {
		t.Error("should say they work elsewhere too")
	}
	if row := userRow(t, viewer, here.ID, ""); row["has_other_branches"] != false {
		t.Errorf("only at TK: %v", row)
	}
	if row := userRow(t, viewer, e.owner.ID, ""); row["has_other_branches"] != true || len(idsOf(row["branch_ids"], "")) != 1 {
		t.Errorf("the Owner is at BKK too: %v", row)
	}

	t.Run("the Owner sees all of it", func(t *testing.T) {
		row := userRow(t, login(t, e.owner).Access, both.ID, "")
		if len(idsOf(row["branch_ids"], "")) != 2 || row["has_other_branches"] != false {
			t.Errorf("got %v", row)
		}
		if got := idsOf(row["branch_roles"], "role_id"); len(got) != 1 || got[0] != e.roleManager {
			t.Errorf("branch_roles: %v", row["branch_roles"])
		}
	})
}

func TestRoleCountsAndFiltersAreForTheBranch(t *testing.T) {
	e := setup(t)
	role := newRole(t, "pos.sell")
	atTK := newUser(t, role, e.branchTK)
	atBKK := newUser(t, role, e.branchBKK)
	onlyAtTK := newUser(t, e.roleCashier, e.branchTK, e.branchBKK)
	holdAt(t, onlyAtTK, e.branchTK, role)

	owner := login(t, e.owner).Access
	viewer := login(t, newUser(t, newRole(t, "user.view", "user.create", "user.update", "user.delete"), e.branchTK)).Access
	count := func(token, query string) float64 {
		t.Helper()
		row := rolesListed(t, token, query)[role]
		if row == nil {
			t.Fatalf("role %d not listed", role)
		}
		return row["user_count"].(float64)
	}

	t.Run("user_count", func(t *testing.T) {
		for _, c := range []struct {
			name, query string
			token       string
			want        float64
		}{
			{"Owner, everywhere", "", owner, 3},
			{"Owner, at TK", "?branch_id=" + id(e.branchTK), owner, 2},
			{"Owner, at BKK", "?branch_id=" + id(e.branchBKK), owner, 1},
			{"TK viewer", "", viewer, 2},
		} {
			if got := count(c.token, c.query); got != c.want {
				t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			}
		}
	})

	t.Run("Owners count at every branch", func(t *testing.T) {
		var owners int64
		env.db.Raw("SELECT COUNT(*) FROM users WHERE role_id = ? AND deleted_at IS NULL", e.roleOwner).Scan(&owners)
		atTK := rolesListed(t, owner, "?branch_id="+id(e.branchTK))[e.roleOwner]["user_count"].(float64)
		if owners < 1 || atTK != float64(owners) {
			t.Errorf("Owner role at TK: %v, want all %d Owners", atTK, owners)
		}
	})

	t.Run("role_id matches the role held at the branch", func(t *testing.T) {
		seen, _ := usersListed(t, owner, "&role_id="+id(role)+"&branch_id="+id(e.branchTK))
		if !seen[atTK.ID] || !seen[onlyAtTK.ID] || seen[atBKK.ID] {
			t.Errorf("at TK: %v", seen)
		}
		seen, _ = usersListed(t, owner, "&role_id="+id(role)+"&branch_id="+id(e.branchBKK))
		if seen[atTK.ID] || seen[onlyAtTK.ID] || !seen[atBKK.ID] {
			t.Errorf("at BKK: %v", seen)
		}
		seen, _ = usersListed(t, owner, "&role_id="+id(role))
		if !seen[atTK.ID] || !seen[onlyAtTK.ID] || !seen[atBKK.ID] {
			t.Errorf("anywhere: %v", seen)
		}
		// Their Cashier role at BKK is out of the TK viewer's sight.
		seen, _ = usersListed(t, viewer, "&role_id="+id(e.roleCashier))
		if seen[onlyAtTK.ID] {
			t.Errorf("TK viewer, Cashier: %v", seen)
		}
		// An Owner is Owner at every branch.
		seen, _ = usersListed(t, owner, "&role_id="+id(e.roleOwner)+"&branch_id="+id(e.branchBKK))
		if !seen[e.owner.ID] {
			t.Errorf("Owners at BKK: %v", seen)
		}
	})
}
