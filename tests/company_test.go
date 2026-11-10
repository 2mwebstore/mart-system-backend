package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"com-mart/backend/internal/models"
)

// Companies are fully separate: nothing of one is listed, changed or used by
// another, and the super admin above them sees none of it.

// listedIDs is every id a list endpoint answers with, whether it sends a
// plain list or a page of rows.
func listedIDs(t *testing.T, r response) []uint64 {
	t.Helper()
	wantStatus(t, r, http.StatusOK)
	var rows []map[string]interface{}
	if err := json.Unmarshal(r.Data, &rows); err != nil {
		var page struct {
			Rows []map[string]interface{} `json:"rows"`
		}
		if err := json.Unmarshal(r.Data, &page); err != nil {
			t.Fatalf("not a list: %s", r)
		}
		rows = page.Rows
	}
	ids := []uint64{}
	for _, row := range rows {
		if v, ok := row["id"].(float64); ok {
			ids = append(ids, uint64(v))
		}
	}
	return ids
}

// sessionCompany is the code of the company a sign-in answered with, "" for none.
func sessionCompany(t *testing.T, r response) string {
	t.Helper()
	company, _ := r.field(t, "company").(map[string]interface{})
	code, _ := company["code"].(string)
	return code
}

func companyCode() string { return fmt.Sprintf("C%d", uniqueSeq.Add(1)) }

func deviceID(t *testing.T, company uint64, key string) uint64 {
	t.Helper()
	return uint64(dbInt(t, "SELECT id FROM devices WHERE company_id = ? AND device_key = ?", company, key))
}

// madeCompany is a company the super admin made in a test, with its first Owner.
type madeCompany struct {
	id    uint64
	code  string
	owner namedUser
}

func makeCompany(t *testing.T) madeCompany {
	t.Helper()
	code := companyCode()
	username := unique("co.owner")
	r := call(t, http.MethodPost, "/platform/companies", login(t, env.superAdmin).Access, gin.H{
		"name": "Company " + code, "code": strings.ToLower(code),
		"owner_full_name": "Owner " + code, "owner_username": username, "owner_password": testPassword,
	})
	wantStatus(t, r, http.StatusCreated)
	if got := r.field(t, "code"); got != code {
		t.Fatalf("code stored as %v, want %s", got, code)
	}
	owners, _ := r.field(t, "owners").([]interface{})
	if len(owners) != 1 {
		t.Fatalf("want one Owner, got %s", r)
	}
	ownerID := uint64(owners[0].(map[string]interface{})["id"].(float64))
	return madeCompany{id: r.id(t), code: code, owner: namedUser{ID: ownerID, Username: username}}
}

// companyACategory is a category of company A's, made for the test.
func companyACategory(t *testing.T) uint64 {
	t.Helper()
	category := models.Category{CompanyID: env.companyA, NameEn: unique("A category")}
	if err := env.db.Create(&category).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.db.Unscoped().Delete(&models.Category{}, category.ID) })
	return category.ID
}

func TestEachCompanyListsOnlyItsOwnRows(t *testing.T) {
	e := setup(t)
	// Company A gets the kinds of rows the fixtures don't give it, so every
	// list below has something of the other company's to leave out.
	tag := unique("only-a")
	category := models.Category{CompanyID: e.companyA, NameEn: tag}
	method := models.PaymentMethodRow{CompanyID: e.companyA, Name: tag, Type: "CASH", Enabled: true}
	if err := e.db.Create(&category).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.db.Create(&method).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		e.db.Unscoped().Delete(&models.Category{}, category.ID)
		e.db.Delete(&models.PaymentMethodRow{}, method.ID)
	})

	tables := map[string]string{
		"/products": "products", "/categories": "categories", "/suppliers": "suppliers", "/customers": "customers",
		"/payment-methods": "payment_methods", "/branches": "branches", "/devices": "devices", "/users": "users", "/roles": "roles",
	}
	for _, side := range []struct {
		name    string
		owner   namedUser
		company uint64
	}{{"company A", e.owner, e.companyA}, {"company B", e.b.owner, e.b.id}} {
		token := login(t, side.owner).Access
		for path, table := range tables {
			ids := listedIDs(t, call(t, http.MethodGet, path, token, nil))
			if len(ids) == 0 {
				t.Errorf("%s's %s is empty; the fixtures give it some", side.name, path)
				continue
			}
			if n := dbInt(t, "SELECT COUNT(*) FROM "+table+" WHERE id IN ? AND (company_id IS NULL OR company_id <> ?)", ids, side.company); n != 0 {
				t.Errorf("%s's %s lists %d rows of another company's", side.name, path, n)
			}
		}
	}
}

func TestACompanyCantChangeAnotherCompanysRows(t *testing.T) {
	e := setup(t)
	token := login(t, e.b.owner).Access
	aCategory := companyACategory(t)

	// Each change goes through for company B's own row, and not for A's,
	// which is left as it was.
	cases := []struct {
		name          string
		method, path  string
		own, other    string
		body          gin.H
		unchangedSQL  string
		unchangedWant int64
	}{
		{"supplier", http.MethodPut, "/suppliers/%d", id(e.b.supplier), id(e.supplier), gin.H{"name": "B Supplier"},
			"SELECT COUNT(*) FROM suppliers WHERE id = ? AND name = 'Test Supplier'", 1},
		{"customer", http.MethodPut, "/customers/%d", id(e.b.customer), id(e.customer), gin.H{"name": "B Customer", "phone": "012000000", "tier": "MEMBER"},
			"SELECT COUNT(*) FROM customers WHERE id = ? AND name = 'Test Customer'", 1},
		{"category", http.MethodPut, "/categories/%d", id(e.b.category), id(aCategory), gin.H{"name_en": "B category"},
			"SELECT COUNT(*) FROM categories WHERE id = ? AND name_en <> 'B category'", 1},
		{"branch", http.MethodPut, "/branches/%d", id(e.b.branch), id(e.branchTK), gin.H{"code": "TK", "name": "B Toul Kork", "active": true},
			"SELECT COUNT(*) FROM branches WHERE id = ? AND name = 'Toul Kork'", 1},
		{"till", http.MethodPut, "/devices/%d", id(deviceID(t, e.b.id, e.b.till)), id(deviceID(t, e.companyA, e.tillTK2)),
			gin.H{"branch_id": e.b.branch, "name": "B till", "device_key": e.b.till, "active": true},
			"SELECT COUNT(*) FROM devices WHERE id = ? AND branch_id = " + id(e.branchTK), 1},
		{"user", http.MethodPut, "/users/%d", id(e.b.manager.ID), id(e.managerTK.ID), updateBody(e.b.manager.Username, e.b.roleManager, true, []uint64{e.b.branch}),
			"SELECT COUNT(*) FROM users WHERE id = ? AND role_id = " + id(e.roleManager) + " AND company_id = " + id(e.companyA), 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			own := call(t, c.method, strings.Replace(c.path, "%d", c.own, 1), token, c.body)
			if own.Status != http.StatusOK {
				t.Fatalf("company B's own %s: want 200, got %s", c.name, own)
			}
			other := call(t, c.method, strings.Replace(c.path, "%d", c.other, 1), token, c.body)
			if other.Status < 400 {
				t.Fatalf("company A's %s was accepted: %s", c.name, other)
			}
			if got := dbInt(t, c.unchangedSQL, c.other); got != c.unchangedWant {
				t.Fatalf("company A's %s changed", c.name)
			}
		})
	}

	t.Run("role delete", func(t *testing.T) {
		mine, err := e.createRoleIn(e.b.id, unique("B spare"), false, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		theirs := newRole(t, "pos.sell")
		if r := call(t, http.MethodDelete, "/roles/"+id(theirs), token, nil); r.Status < 400 {
			t.Fatalf("company A's role was deleted: %s", r)
		}
		if dbInt(t, "SELECT COUNT(*) FROM roles WHERE id = ?", theirs) != 1 {
			t.Fatal("company A's role is gone")
		}
		wantStatus(t, call(t, http.MethodDelete, "/roles/"+id(mine), token, nil), http.StatusOK)
	})
}

func TestACompanyCantUseAnotherCompanysRows(t *testing.T) {
	e := setup(t)
	token := login(t, e.b.owner).Access
	wantField := func(t *testing.T, r response, field string) {
		t.Helper()
		if r.Status != http.StatusUnprocessableEntity || r.Error == nil || r.Error.Fields[field] == "" {
			t.Fatalf("want 422 on %s, got %s", field, r)
		}
	}

	t.Run("a user with the other company's role or branch", func(t *testing.T) {
		body := updateBody(unique("x"), e.roleManager, true, []uint64{e.b.branch}, "password", testPassword)
		wantField(t, call(t, http.MethodPost, "/users", token, body), "role_id")
		body = updateBody(unique("x"), e.b.roleManager, true, []uint64{e.branchTK}, "password", testPassword)
		wantField(t, call(t, http.MethodPost, "/users", token, body), "branch_ids")
	})
	t.Run("a till in the other company's branch", func(t *testing.T) {
		r := call(t, http.MethodPost, "/devices", token, gin.H{"branch_id": e.branchTK, "name": "x", "device_key": unique("K"), "active": true})
		wantField(t, r, "branch_id")
	})
	t.Run("a product in the other company's category or from its supplier", func(t *testing.T) {
		aCategory := companyACategory(t)
		wantField(t, call(t, http.MethodPost, "/products", token, gin.H{"name_en": unique("p"), "category_id": aCategory, "active": true}), "category_id")
		wantField(t, call(t, http.MethodPost, "/products", token, gin.H{"name_en": unique("p"), "supplier_id": e.supplier, "active": true}), "supplier_id")
	})
	t.Run("a purchase order from the other company's supplier or for its product", func(t *testing.T) {
		item := func(product uint64) []gin.H {
			return []gin.H{{"product_id": product, "qty": 1, "unit_cost_cents": 100}}
		}
		wantField(t, call(t, http.MethodPost, "/purchase-orders", token, gin.H{"branch_id": e.b.branch, "supplier_id": e.supplier, "items": item(e.b.product)}), "supplier_id")
		wantField(t, call(t, http.MethodPost, "/purchase-orders", token, gin.H{"branch_id": e.b.branch, "supplier_id": e.b.supplier, "items": item(e.product)}), "items")
		wantStatus(t, call(t, http.MethodPost, "/purchase-orders", token, gin.H{"branch_id": e.b.branch, "supplier_id": e.b.supplier, "items": item(e.b.product)}), http.StatusCreated)
	})
	t.Run("an invoice to the other company's customer", func(t *testing.T) {
		body := func(customer, product uint64) gin.H {
			return gin.H{"branch_id": e.b.branch, "customer_id": customer, "invoice_date": today(), "items": []gin.H{{"product_id": product, "qty": 1, "unit_price_cents": 100}}}
		}
		if r := call(t, http.MethodPost, "/invoices", token, body(e.customer, e.b.product)); r.Status < 400 {
			t.Fatalf("invoiced company A's customer: %s", r)
		}
		if r := call(t, http.MethodPost, "/invoices", token, body(e.b.customer, e.product)); r.Status < 400 {
			t.Fatalf("invoiced company A's product: %s", r)
		}
		wantStatus(t, call(t, http.MethodPost, "/invoices", token, body(e.b.customer, e.b.product)), http.StatusCreated)
	})
	t.Run("a sale of the other company's product or to its customer", func(t *testing.T) {
		till := newTill(t, e.b.branch)
		wantStatus(t, call(t, http.MethodPost, "/shifts/open", token, gin.H{"device_key": till, "opening_usd_cents": 0, "opening_khr_riel": 0}), http.StatusCreated)
		wantDenied(t, call(t, http.MethodPost, "/sales", token, saleBody(till, unique("key"), e.product)), http.StatusUnprocessableEntity, "PRODUCT_UNAVAILABLE")
		toTheirs := saleBody(till, unique("key"), e.b.product)
		toTheirs["customer_id"] = e.customer
		wantField(t, call(t, http.MethodPost, "/sales", token, toTheirs), "customer_id")
		toOurs := saleBody(till, unique("key"), e.b.product)
		toOurs["customer_id"] = e.b.customer
		sale := call(t, http.MethodPost, "/sales", token, toOurs)
		wantStatus(t, sale, http.StatusCreated)
		// Nor can an edit add company A's product.
		edit := gin.H{"items": []gin.H{{"product_id": e.b.product, "qty": 1}, {"product_id": e.product, "qty": 1}}, "discount_cents": 0}
		wantDenied(t, call(t, http.MethodPut, "/sales/"+id(sale.id(t)), token, edit), http.StatusUnprocessableEntity, "PRODUCT_UNAVAILABLE")
	})
	t.Run("a till of the other company", func(t *testing.T) {
		if r := call(t, http.MethodPost, "/shifts/open", token, gin.H{"device_key": e.tillTK2, "opening_usd_cents": 0, "opening_khr_riel": 0}); r.Status < 400 {
			t.Fatalf("opened a shift on company A's till: %s", r)
		}
		// A key both companies have is company B's own till here.
		shift := call(t, http.MethodPost, "/shifts/open", token, gin.H{"device_key": e.b.till, "opening_usd_cents": 0, "opening_khr_riel": 0})
		wantStatus(t, shift, http.StatusCreated)
		t.Cleanup(func() {
			call(t, http.MethodPost, "/shifts/"+id(shift.id(t))+"/close", token, gin.H{"counted_usd_cents": 0, "counted_khr_riel": 0})
		})
		if n := dbInt(t, "SELECT COUNT(*) FROM shifts s JOIN devices d ON d.id = s.device_id WHERE s.id = ? AND d.company_id = ?", shift.id(t), e.b.id); n != 1 {
			t.Fatal("company B's shift is on company A's till")
		}
	})
}

func TestCodesAndNamesArePerCompany(t *testing.T) {
	e := setup(t)
	a, b := login(t, e.owner).Access, login(t, e.b.owner).Access

	t.Run("role names", func(t *testing.T) {
		name := unique("Supervisor")
		body := gin.H{"name": name, "permissions": []string{"pos.sell"}}
		wantStatus(t, call(t, http.MethodPost, "/roles", a, body), http.StatusCreated)
		wantStatus(t, call(t, http.MethodPost, "/roles", b, body), http.StatusCreated)
		if r := call(t, http.MethodPost, "/roles", b, body); r.Status != http.StatusUnprocessableEntity {
			t.Fatalf("a second %q in company B: want 422, got %s", name, r)
		}
	})
	t.Run("customer phones", func(t *testing.T) {
		phone := fmt.Sprintf("09%07d", uniqueSeq.Add(1))
		body := gin.H{"name": "Same phone", "phone": phone, "tier": "MEMBER"}
		wantStatus(t, call(t, http.MethodPost, "/customers", a, body), http.StatusCreated)
		wantStatus(t, call(t, http.MethodPost, "/customers", b, body), http.StatusCreated)
		if r := call(t, http.MethodPost, "/customers", b, body); r.Status != http.StatusUnprocessableEntity {
			t.Fatalf("a second %s in company B: want 422, got %s", phone, r)
		}
	})
	t.Run("branch codes", func(t *testing.T) {
		// Company A has BKK1 already.
		r := call(t, http.MethodPost, "/branches", b, gin.H{"code": "BKK1", "name": "B BKK1", "active": true})
		wantStatus(t, r, http.StatusCreated)
		t.Cleanup(func() { call(t, http.MethodDelete, "/branches/"+id(r.id(t)), b, nil) })
		if r := call(t, http.MethodPost, "/branches", b, gin.H{"code": "BKK1", "name": "Again", "active": true}); r.Status != http.StatusUnprocessableEntity {
			t.Fatalf("a second BKK1 in company B: want 422, got %s", r)
		}
	})
	t.Run("the fixtures share SKU, branch code, till key and role names", func(t *testing.T) {
		if n := dbInt(t, "SELECT COUNT(DISTINCT company_id) FROM products WHERE sku = 'TEST-1'"); n != 2 {
			t.Fatalf("SKU TEST-1 in %d companies", n)
		}
		if n := dbInt(t, "SELECT COUNT(DISTINCT company_id) FROM devices WHERE device_key = 'TEST-TK-1'"); n != 2 {
			t.Fatalf("till TEST-TK-1 in %d companies", n)
		}
	})
}

func TestNumberingIsPerCompany(t *testing.T) {
	e := setup(t)
	c := makeCompany(t)
	token := login(t, c.owner).Access
	before := dbInt(t, "SELECT next_number FROM number_sequences WHERE company_id = ? AND doc_type = 'EXPENSE'", e.companyA)

	branch := call(t, http.MethodPost, "/branches", token, gin.H{"code": "HQ", "name": "Head office", "active": true})
	wantStatus(t, branch, http.StatusCreated)
	expense := call(t, http.MethodPost, "/expenses", token, gin.H{"branch_id": branch.id(t), "category": "OTHER", "amount_cents": 100, "expense_date": today()})
	wantStatus(t, expense, http.StatusCreated)
	if got := expense.field(t, "code"); got != "EXP-000001" {
		t.Fatalf("a new company's first expense is %v, want EXP-000001", got)
	}
	if after := dbInt(t, "SELECT next_number FROM number_sequences WHERE company_id = ? AND doc_type = 'EXPENSE'", e.companyA); after != before {
		t.Fatalf("company A's numbering moved from %d to %d", before, after)
	}
}

func TestTheLastOwnerIsCountedPerCompany(t *testing.T) {
	e := setup(t)
	// Company A has active Owners; company B has just the one, who can't
	// step down for that.
	token := login(t, e.b.owner).Access
	r := call(t, http.MethodPut, "/users/"+id(e.b.owner.ID), token, updateBody(e.b.owner.Username, e.b.roleManager, true, []uint64{e.b.branch}))
	t.Cleanup(func() {
		e.db.Exec("UPDATE users SET role_id = ?, active = 1 WHERE id = ?", e.b.roleOwner, e.b.owner.ID)
		e.db.Exec("DELETE FROM user_branches WHERE user_id = ?", e.b.owner.ID)
	})
	wantDenied(t, r, http.StatusConflict, "LAST_OWNER")
}

func TestTheSuperAdminWorksInNoCompany(t *testing.T) {
	e := setup(t)
	r := tryLogin(t, e.superAdmin.Username, testPassword)
	wantStatus(t, r, http.StatusOK)
	if r.field(t, "is_super_admin") != true || r.field(t, "company") != nil {
		t.Fatalf("super admin session: %s", r)
	}
	token := decodeSession(t, r).Access
	for _, path := range []string{"/products", "/users", "/roles", "/settings", "/exchange-rate", "/branches", "/reports/dashboard", "/reports/activity-log"} {
		wantDenied(t, call(t, http.MethodGet, path, token, nil), http.StatusForbidden, "NO_COMPANY")
	}
	me := call(t, http.MethodGet, "/auth/me", token, nil)
	wantStatus(t, me, http.StatusOK)
	if me.field(t, "is_super_admin") != true {
		t.Fatalf("/auth/me: %s", me)
	}
	wantStatus(t, call(t, http.MethodGet, "/platform/companies", token, nil), http.StatusOK)

	t.Run("and no company user reaches the platform", func(t *testing.T) {
		for _, user := range []namedUser{e.owner, e.b.owner, e.managerTK} {
			token := login(t, user).Access
			for _, path := range []string{"/platform/companies", "/platform/backups", "/platform/settings/notifications"} {
				wantDenied(t, call(t, http.MethodGet, path, token, nil), http.StatusForbidden, "NOT_SUPER_ADMIN")
			}
			wantDenied(t, call(t, http.MethodPost, "/platform/reset-for-production", token, gin.H{"confirm": "RESET"}), http.StatusForbidden, "NOT_SUPER_ADMIN")
		}
	})
}

func TestTheSuperAdminCreatesACompanyReadyToUse(t *testing.T) {
	e := setup(t)
	token := login(t, e.superAdmin).Access
	c := makeCompany(t)

	r := tryLogin(t, c.owner.Username, testPassword)
	wantStatus(t, r, http.StatusOK)
	if got := sessionCompany(t, r); got != c.code {
		t.Fatalf("the Owner's session is for %q, want %s", got, c.code)
	}
	owner := decodeSession(t, r)
	if owner.User.RoleName != "Owner" {
		t.Fatalf("first account is a %s", owner.User.RoleName)
	}

	roles := call(t, http.MethodGet, "/roles", owner.Access, nil)
	wantStatus(t, roles, http.StatusOK)
	for _, name := range []string{"Owner", "Manager", "Cashier", "Stock keeper", "Accountant"} {
		if !strings.Contains(roles.Raw, `"name":"`+name+`"`) {
			t.Errorf("the new company has no %s role", name)
		}
	}
	if n := len(listedIDs(t, call(t, http.MethodGet, "/payment-methods", owner.Access, nil))); n != 3 {
		t.Errorf("the new company has %d payment methods, want 3", n)
	}
	if n := len(listedIDs(t, call(t, http.MethodGet, "/branches", owner.Access, nil))); n != 0 {
		t.Errorf("the new company has %d branches, want none until its Owner adds them", n)
	}
	numbering := call(t, http.MethodGet, "/settings/numbering", owner.Access, nil)
	wantStatus(t, numbering, http.StatusOK)
	for _, docType := range []string{"SALE", "EXPENSE", "PURCHASE_ORDER", "INVOICE"} {
		if !strings.Contains(numbering.Raw, docType) {
			t.Errorf("the new company has no %s numbering", docType)
		}
	}

	t.Run("a code that's taken or malformed is refused", func(t *testing.T) {
		body := func(code string) gin.H {
			return gin.H{"name": "Dup", "code": code, "owner_full_name": "x", "owner_username": unique("dup"), "owner_password": testPassword}
		}
		wantDenied(t, call(t, http.MethodPost, "/platform/companies", token, body(testCompanyCode)), http.StatusUnprocessableEntity, "COMPANY_CODE_TAKEN")
		wantDenied(t, call(t, http.MethodPost, "/platform/companies", token, body(strings.ToLower(c.code))), http.StatusUnprocessableEntity, "COMPANY_CODE_TAKEN")
		for _, bad := range []string{"X", "TOO-LONG", "ABCDEFGHIJKLM", "A B"} {
			r := call(t, http.MethodPost, "/platform/companies", token, body(bad))
			if r.Status != http.StatusUnprocessableEntity || r.Error == nil || r.Error.Fields["code"] == "" {
				t.Errorf("code %q: want 422 on code, got %s", bad, r)
			}
		}
	})
	t.Run("an Owner username that's taken leaves no company behind", func(t *testing.T) {
		code := companyCode()
		r := call(t, http.MethodPost, "/platform/companies", token, gin.H{
			"name": "Orphan", "code": code, "owner_full_name": "x", "owner_username": e.owner.Username, "owner_password": testPassword,
		})
		if r.Status != http.StatusUnprocessableEntity || r.Error == nil || r.Error.Fields["owner_username"] == "" {
			t.Fatalf("want 422 on owner_username, got %s", r)
		}
		if n := dbInt(t, "SELECT COUNT(*) FROM companies WHERE code = ?", code); n != 0 {
			t.Fatal("the company was created without its Owner")
		}
	})
	t.Run("a short Owner password is refused", func(t *testing.T) {
		r := call(t, http.MethodPost, "/platform/companies", token, gin.H{
			"name": "Short", "code": companyCode(), "owner_full_name": "x", "owner_username": unique("short"), "owner_password": "short",
		})
		if r.Status != http.StatusUnprocessableEntity || r.Error == nil || r.Error.Fields["owner_password"] == "" {
			t.Fatalf("want 422 on owner_password, got %s", r)
		}
	})
}

func TestDisablingACompanyLocksItsStaffOut(t *testing.T) {
	setup(t)
	super := login(t, env.superAdmin).Access
	c := makeCompany(t)
	owner := login(t, c.owner)
	branch := call(t, http.MethodPost, "/branches", owner.Access, gin.H{"code": "HQ", "name": "HQ", "active": true})
	wantStatus(t, branch, http.StatusCreated)
	till := unique("TILL")
	wantStatus(t, call(t, http.MethodPost, "/devices", owner.Access, gin.H{"branch_id": branch.id(t), "name": "Till", "device_key": till, "active": true}), http.StatusCreated)
	wantStatus(t, call(t, http.MethodGet, "/auth/tills?company="+c.code, "", nil), http.StatusOK)

	update := func(active bool) {
		t.Helper()
		r := call(t, http.MethodPut, "/platform/companies/"+id(c.id), super, gin.H{"name": "Company " + c.code, "code": c.code, "active": active, "retention_months": 3})
		wantStatus(t, r, http.StatusOK)
	}
	bystander := login(t, env.b.owner)
	update(false)

	wantStatus(t, call(t, http.MethodGet, "/branches", owner.Access, nil), http.StatusUnauthorized)
	// Nobody else's sessions end.
	wantStatus(t, call(t, http.MethodGet, "/branches", bystander.Access, nil), http.StatusOK)
	wantStatus(t, tryRefresh(t, bystander.Refresh), http.StatusOK)
	wantStatus(t, tryRefresh(t, owner.Refresh), http.StatusUnauthorized)
	wantDenied(t, tryLogin(t, c.owner.Username, testPassword), http.StatusForbidden, "COMPANY_DISABLED")
	wantDenied(t, call(t, http.MethodGet, "/auth/tills?company="+c.code, "", nil), http.StatusNotFound, "UNKNOWN_COMPANY")
	wantDenied(t, tryPINAt(t, c.code, till, c.owner.Username, testPIN), http.StatusNotFound, "UNKNOWN_COMPANY")

	update(true)
	// Turning it back on lets staff sign in again, but the sessions it ended
	// stay ended.
	wantStatus(t, call(t, http.MethodGet, "/branches", owner.Access, nil), http.StatusUnauthorized)
	fresh := login(t, c.owner)
	wantStatus(t, call(t, http.MethodGet, "/branches", fresh.Access, nil), http.StatusOK)

	t.Run("a company switched off in the database is locked out too", func(t *testing.T) {
		// Without the platform route, so no session was ended for it: the
		// company's own flag has to be enough.
		env.db.Exec("UPDATE companies SET active = 0 WHERE id = ?", c.id)
		t.Cleanup(func() { env.db.Exec("UPDATE companies SET active = 1 WHERE id = ?", c.id) })
		wantStatus(t, call(t, http.MethodGet, "/branches", fresh.Access, nil), http.StatusUnauthorized)
	})
}

// A user whose main role is another company's has no access at all, however
// the row came to be (saving a user refuses it).
func TestARoleFromAnotherCompanyGivesNoAccess(t *testing.T) {
	e := setup(t)
	session := login(t, e.b.manager)
	wantStatus(t, call(t, http.MethodGet, "/auth/me", session.Access, nil), http.StatusOK)
	e.db.Exec("UPDATE users SET role_id = ? WHERE id = ?", e.roleManager, e.b.manager.ID)
	t.Cleanup(func() { e.db.Exec("UPDATE users SET role_id = ? WHERE id = ?", e.b.roleManager, e.b.manager.ID) })
	wantStatus(t, call(t, http.MethodGet, "/auth/me", session.Access, nil), http.StatusUnauthorized)
}

func TestTheSuperAdminLooksAfterOwnerAccounts(t *testing.T) {
	e := setup(t)
	super := login(t, e.superAdmin).Access
	c := makeCompany(t)
	old := login(t, c.owner)

	t.Run("resets an Owner's password and ends their sessions", func(t *testing.T) {
		r := call(t, http.MethodPost, "/platform/companies/"+id(c.id)+"/owner", super, gin.H{"user_id": c.owner.ID, "password": "brand-new-password"})
		wantStatus(t, r, http.StatusOK)
		wantStatus(t, call(t, http.MethodGet, "/branches", old.Access, nil), http.StatusUnauthorized)
		wantStatus(t, tryLogin(t, c.owner.Username, testPassword), http.StatusUnauthorized)
		wantStatus(t, tryLogin(t, c.owner.Username, "brand-new-password"), http.StatusOK)
	})
	t.Run("but not another company's", func(t *testing.T) {
		r := call(t, http.MethodPost, "/platform/companies/"+id(c.id)+"/owner", super, gin.H{"user_id": e.owner.ID, "password": "brand-new-password"})
		if r.Status != http.StatusUnprocessableEntity || r.Error == nil || r.Error.Fields["user_id"] == "" {
			t.Fatalf("want 422 on user_id, got %s", r)
		}
		wantStatus(t, tryLogin(t, e.owner.Username, testPassword), http.StatusOK)
	})
	t.Run("adds another Owner", func(t *testing.T) {
		username := unique("second.owner")
		r := call(t, http.MethodPost, "/platform/companies/"+id(c.id)+"/owner", super, gin.H{"full_name": "Second", "username": username, "password": testPassword})
		wantStatus(t, r, http.StatusOK)
		s := tryLogin(t, username, testPassword)
		if sessionCompany(t, s) != c.code || decodeSession(t, s).User.RoleName != "Owner" {
			t.Fatalf("the new Owner: %s", s)
		}
	})
	t.Run("needs a name and username for a new one", func(t *testing.T) {
		r := call(t, http.MethodPost, "/platform/companies/"+id(c.id)+"/owner", super, gin.H{"password": testPassword})
		wantStatus(t, r, http.StatusUnprocessableEntity)
	})
}

func TestTillSignInNeedsTheCompanyCode(t *testing.T) {
	e := setup(t)

	r := tryPINAt(t, testCompanyCode, e.tillTK, e.cashierTK.Username, testPIN)
	wantStatus(t, r, http.StatusOK)
	if sessionCompany(t, r) != testCompanyCode {
		t.Fatalf("session company: %s", r)
	}
	wantStatus(t, tryPINAt(t, strings.ToLower(testCompanyCode), e.tillTK, e.cashierTK.Username, testPIN), http.StatusOK)

	// Company B has a till with the same key; company A's cashier isn't known there.
	wantDenied(t, tryPINAt(t, e.b.code, e.b.till, e.cashierTK.Username, testPIN), http.StatusUnauthorized, "UNAUTHORIZED")
	// A key only company A has.
	wantDenied(t, tryPINAt(t, e.b.code, e.tillTK2, e.b.manager.Username, testPIN), http.StatusUnauthorized, "UNKNOWN_DEVICE")
	wantDenied(t, tryPINAt(t, "NOPE99", e.tillTK, e.cashierTK.Username, testPIN), http.StatusNotFound, "UNKNOWN_COMPANY")
	if r := tryPINAt(t, "", e.tillTK, e.cashierTK.Username, testPIN); r.Status == http.StatusOK {
		t.Fatal("signed in at a till with no company code")
	}

	b := tryPINAt(t, e.b.code, e.b.till, e.b.manager.Username, testPIN)
	wantStatus(t, b, http.StatusOK)
	if sessionCompany(t, b) != e.b.code {
		t.Fatalf("company B's till session: %s", b)
	}

	t.Run("the tills listed are that company's", func(t *testing.T) {
		for _, side := range []struct {
			code    string
			company uint64
		}{{testCompanyCode, e.companyA}, {e.b.code, e.b.id}} {
			r := call(t, http.MethodGet, "/auth/tills?company="+side.code, "", nil)
			wantStatus(t, r, http.StatusOK)
			var tills []struct {
				BranchID uint64 `json:"branch_id"`
			}
			if err := json.Unmarshal(r.Data, &tills); err != nil || len(tills) == 0 {
				t.Fatalf("tills for %s: %s", side.code, r)
			}
			for _, till := range tills {
				if dbInt(t, "SELECT company_id FROM branches WHERE id = ?", till.BranchID) != int64(side.company) {
					t.Fatalf("%s lists a till of branch %d", side.code, till.BranchID)
				}
			}
		}
		wantDenied(t, call(t, http.MethodGet, "/auth/tills", "", nil), http.StatusNotFound, "UNKNOWN_COMPANY")
		// The code is typed at the till, in any case.
		if ids := call(t, http.MethodGet, "/auth/tills?company="+strings.ToLower(e.b.code), "", nil); ids.Status != http.StatusOK || !strings.Contains(ids.Raw, e.b.till) {
			t.Fatalf("a lower-case code: %s", ids)
		}
	})
}

func TestBrandingAndTheTableMenuAreEachCompanysOwn(t *testing.T) {
	e := setup(t)
	name := func(code string) interface{} {
		t.Helper()
		r := call(t, http.MethodGet, "/auth/branding?company="+code, "", nil)
		wantStatus(t, r, http.StatusOK)
		return r.field(t, "company_name")
	}
	if got := name(testCompanyCode); got != "Test Company A" {
		t.Fatalf("company A's branding: %v", got)
	}
	if got := name(e.b.code); got != "Test Company B" {
		t.Fatalf("company B's branding: %v", got)
	}
	if got := name(""); got != "" {
		t.Fatalf("no company's branding: %v", got)
	}

	b := login(t, e.b.owner).Access
	wantStatus(t, call(t, http.MethodPut, "/settings/branding", b, gin.H{"company_name": "Bee Mart"}), http.StatusOK)
	t.Cleanup(func() {
		e.db.Exec("DELETE FROM company_settings WHERE company_id = ? AND `key` = 'company_name'", e.b.id)
	})
	if got := name(e.b.code); got != "Bee Mart" {
		t.Fatalf("company B's new name: %v", got)
	}
	if got := name(testCompanyCode); got != "Test Company A" {
		t.Fatalf("company B's change reached company A: %v", got)
	}

	table := call(t, http.MethodPost, "/dining-tables", b, gin.H{"branch_id": e.b.branch, "name": unique("T"), "active": true})
	wantStatus(t, table, http.StatusCreated)
	token, _ := table.field(t, "token").(string)
	menu := call(t, http.MethodGet, "/public/tables/"+token+"/menu", "", nil)
	wantStatus(t, menu, http.StatusOK)
	if !strings.Contains(menu.Raw, "B product") || strings.Contains(menu.Raw, "Test product") {
		t.Fatalf("company B's menu: %s", menu)
	}
}

func TestSettingsAndTheActivityLogAreEachCompanysOwn(t *testing.T) {
	e := setup(t)
	a, b := login(t, e.owner).Access, login(t, e.b.owner).Access

	t.Run("exchange rate", func(t *testing.T) {
		rate := func(token string) float64 {
			r := call(t, http.MethodGet, "/exchange-rate", token, nil)
			wantStatus(t, r, http.StatusOK)
			v, _ := r.field(t, "rate").(float64)
			return v
		}
		if rate(b) != 4000 || rate(a) == 4000 {
			t.Fatalf("rates: A %v, B %v", rate(a), rate(b))
		}
	})
	t.Run("alerts", func(t *testing.T) {
		secret := unique("b-token")
		body := gin.H{"enabled": false, "bot_token": secret, "chat_id": "1", "alert_types": []string{"low_stock"}}
		wantStatus(t, call(t, http.MethodPut, "/settings/notifications", b, body), http.StatusOK)
		r := call(t, http.MethodGet, "/settings/notifications", a, nil)
		wantStatus(t, r, http.StatusOK)
		if strings.Contains(r.Raw, secret) {
			t.Fatal("company A sees company B's bot token")
		}
		// Backup alerts are the super admin's.
		body["alert_types"] = []string{"backup_failed"}
		wantStatus(t, call(t, http.MethodPut, "/settings/notifications", b, body), http.StatusUnprocessableEntity)
	})
	t.Run("numbering", func(t *testing.T) {
		wantStatus(t, call(t, http.MethodPut, "/settings/numbering", b, gin.H{"doc_type": "EXPENSE", "prefix": "BEX", "next_number": 7}), http.StatusOK)
		t.Cleanup(func() {
			e.db.Exec("UPDATE number_sequences SET prefix = 'EXP' WHERE company_id = ? AND doc_type = 'EXPENSE'", e.b.id)
		})
		if n := dbInt(t, "SELECT COUNT(*) FROM number_sequences WHERE company_id = ? AND prefix = 'BEX'", e.companyA); n != 0 {
			t.Fatal("company B's prefix reached company A")
		}
	})
	t.Run("activity log", func(t *testing.T) {
		created := call(t, http.MethodPost, "/customers", b, gin.H{"name": unique("Logged"), "phone": fmt.Sprintf("08%07d", uniqueSeq.Add(1)), "tier": "MEMBER"})
		wantStatus(t, created, http.StatusCreated)
		customer := float64(created.id(t))
		// logged reports whether a log lists B's new customer, and whether it
		// lists anything B's Owner did.
		logged := func(token string) (theCustomer, byB bool) {
			r := call(t, http.MethodGet, "/reports/activity-log", token, nil)
			wantStatus(t, r, http.StatusOK)
			var rows []map[string]interface{}
			if err := json.Unmarshal(r.Data, &rows); err != nil {
				t.Fatalf("not a list: %s", r)
			}
			for _, row := range rows {
				if row["entity_type"] == "customer" && row["entity_id"] == customer {
					theCustomer = true
				}
				if row["user"] == e.b.owner.Username {
					byB = true
				}
			}
			return theCustomer, byB
		}
		if theCustomer, _ := logged(b); !theCustomer {
			t.Fatal("company B's own log is missing its new customer")
		}
		if theCustomer, byB := logged(a); theCustomer || byB {
			t.Fatal("company A's activity log shows company B's changes")
		}
	})
	t.Run("the platform's alerts are apart from every company's", func(t *testing.T) {
		super := login(t, e.superAdmin).Access
		body := gin.H{"enabled": false, "bot_token": "platform-token", "chat_id": "1", "alert_types": []string{"low_stock"}}
		wantStatus(t, call(t, http.MethodPut, "/platform/settings/notifications", super, body), http.StatusUnprocessableEntity)
		body["alert_types"] = []string{"backup_failed", "backup_success"}
		wantStatus(t, call(t, http.MethodPut, "/platform/settings/notifications", super, body), http.StatusOK)
		for _, token := range []string{a, b} {
			if r := call(t, http.MethodGet, "/settings/notifications", token, nil); strings.Contains(r.Raw, "platform-token") {
				t.Fatal("a company sees the platform's bot token")
			}
		}
	})
}

// Runs the reset for real, so it rebuilds the whole test database afterwards
// for the tests that follow.
func TestTheProductionResetWipesEveryCompanyAndKeepsTheSuperAdmins(t *testing.T) {
	e := setup(t)
	t.Cleanup(func() {
		rebuilt, err := buildEnv()
		if err != nil {
			t.Errorf("rebuilding the test database after the reset: %v", err)
			return
		}
		env = rebuilt
	})
	super := login(t, e.superAdmin)
	owner := login(t, e.owner).Access

	// Things the reset has to get past: a role that belongs to a branch, a
	// till session, a company setting, a document.
	wantStatus(t, call(t, http.MethodPost, "/roles", owner, gin.H{"name": unique("Branch role"), "branch_id": e.branchBKK, "permissions": []string{"pos.sell"}}), http.StatusCreated)
	wantStatus(t, tryPIN(t, e.tillTK, e.cashierTK.Username, testPIN), http.StatusOK)
	wantStatus(t, call(t, http.MethodPut, "/settings/branding", owner, gin.H{"company_name": "Before reset"}), http.StatusOK)
	wantStatus(t, call(t, http.MethodPost, "/expenses", owner, gin.H{"branch_id": e.branchTK, "category": "OTHER", "amount_cents": 100, "expense_date": today()}), http.StatusCreated)

	wantStatus(t, call(t, http.MethodPost, "/platform/reset-for-production", super.Access, gin.H{"confirm": "reset"}), http.StatusUnprocessableEntity)
	wantStatus(t, call(t, http.MethodPost, "/platform/reset-for-production", super.Access, gin.H{"confirm": "RESET"}), http.StatusOK)

	for _, query := range []string{
		"SELECT COUNT(*) FROM companies", "SELECT COUNT(*) FROM branches", "SELECT COUNT(*) FROM products",
		"SELECT COUNT(*) FROM users WHERE company_id IS NOT NULL", "SELECT COUNT(*) FROM roles WHERE company_id IS NOT NULL",
		"SELECT COUNT(*) FROM company_settings", "SELECT COUNT(*) FROM number_sequences", "SELECT COUNT(*) FROM devices",
		"SELECT COUNT(*) FROM expenses", "SELECT COUNT(*) FROM customers",
	} {
		if n := dbInt(t, query); n != 0 {
			t.Errorf("%s: %d left", query, n)
		}
	}
	if n := dbInt(t, "SELECT COUNT(*) FROM users WHERE id = ? AND active = 1", e.superAdmin.ID); n != 1 {
		t.Fatal("the super admin is gone")
	}
	if n := dbInt(t, "SELECT COUNT(*) FROM roles WHERE company_id IS NULL AND name = 'Super admin'"); n != 1 {
		t.Fatal("the Super admin role is gone")
	}
	wantStatus(t, tryLogin(t, e.owner.Username, testPassword), http.StatusUnauthorized)

	// The super admin is still signed in and starts over.
	companies := call(t, http.MethodGet, "/platform/companies", super.Access, nil)
	if len(listedIDs(t, companies)) != 0 {
		t.Fatalf("companies after the reset: %s", companies)
	}
	wantStatus(t, tryRefresh(t, super.Refresh), http.StatusOK)
	c := makeCompany(t)
	if c.id != 1 {
		t.Errorf("the first company after a reset is #%d, want #1", c.id)
	}
}
