package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"com-mart/backend/internal/models"
)

// readCurrency is the main currency a company's staff read from /settings.
func readCurrency(t *testing.T, token string) string {
	t.Helper()
	r := call(t, http.MethodGet, "/settings", token, nil)
	wantStatus(t, r, http.StatusOK)
	v, _ := r.field(t, "main_currency").(string)
	return v
}

// companyRefundLimit is a role's refund limit in a company, in its main currency.
func companyRefundLimit(t *testing.T, company uint64, role string) int64 {
	t.Helper()
	return dbInt(t, `SELECT rl.refund_without_approval_cents FROM role_limits rl JOIN roles r ON r.id = rl.role_id
		WHERE r.company_id = ? AND r.name = ?`, company, role)
}

// A company's main currency: the super admin sets it on the company, its
// staff read it, and it can't change once the company has amounts stored.
func TestMainCurrencyIsSetByTheSuperAdmin(t *testing.T) {
	setup(t)
	super := login(t, env.superAdmin).Access
	c := makeCompany(t)
	owner := login(t, c.owner).Access

	listed := func() map[string]interface{} {
		t.Helper()
		r := call(t, http.MethodGet, "/platform/companies", super, nil)
		wantStatus(t, r, http.StatusOK)
		for _, m := range r.rows(t) {
			if uint64(m["id"].(float64)) == c.id {
				return m
			}
		}
		t.Fatalf("company %d not listed", c.id)
		return nil
	}
	update := func(extra gin.H) response {
		t.Helper()
		body := gin.H{"name": "Company " + c.code, "code": c.code, "active": true, "retention_months": 3}
		for k, v := range extra {
			body[k] = v
		}
		return call(t, http.MethodPut, "/platform/companies/"+id(c.id), super, body)
	}

	if got := readCurrency(t, owner); got != "USD" {
		t.Fatalf("a new company starts in %q, want USD", got)
	}
	if m := listed(); m["main_currency"] != "USD" || m["main_currency_locked"] != false {
		t.Fatalf("listed as %v (locked %v), want USD and not locked", m["main_currency"], m["main_currency_locked"])
	}
	if got := companyRefundLimit(t, c.id, "Manager"); got != 2000 {
		t.Fatalf("a dollar company's Manager refunds up to %d, want 2000 cents", got)
	}

	r := update(gin.H{"main_currency": "KHR"})
	wantStatus(t, r, http.StatusOK)
	if v, _ := r.field(t, "main_currency").(string); v != "KHR" {
		t.Fatalf("answered %q, want KHR", v)
	}
	if got := readCurrency(t, owner); got != "KHR" {
		t.Fatalf("the company reads %q, want KHR", got)
	}

	t.Run("refund limits move to riel with it", func(t *testing.T) {
		// $20 and $5 at the default ៛4,100.
		for role, want := range map[string]int64{"Manager": 82000, "Cashier": 20500, models.RoleOwner: 1 << 40} {
			if got := companyRefundLimit(t, c.id, role); got != want {
				t.Errorf("%s refunds up to %d, want %d", role, got, want)
			}
		}
	})

	t.Run("left out, it's kept", func(t *testing.T) {
		wantStatus(t, update(nil), http.StatusOK)
		if got := readCurrency(t, owner); got != "KHR" {
			t.Fatalf("an update without it left %q", got)
		}
	})

	t.Run("only USD or KHR", func(t *testing.T) {
		for _, bad := range []string{"khr", "EUR", "RIEL"} {
			wantStatus(t, update(gin.H{"main_currency": bad}), http.StatusUnprocessableEntity)
		}
		if got := readCurrency(t, owner); got != "KHR" {
			t.Fatalf("a refused change left %q", got)
		}
	})

	t.Run("the company can't change it", func(t *testing.T) {
		r := call(t, http.MethodPut, "/settings", owner, gin.H{"receipt_header": "Shop", "receipt_footer": "", "loyalty_points_per_usd": 1, "main_currency": "USD"})
		wantStatus(t, r, http.StatusOK)
		if got := readCurrency(t, owner); got != "KHR" {
			t.Fatalf("the company's own settings save changed it to %q", got)
		}
		wantDenied(t, call(t, http.MethodPut, "/platform/companies/"+id(c.id), owner, gin.H{
			"name": "Company " + c.code, "code": c.code, "active": true, "retention_months": 3, "main_currency": "USD",
		}), http.StatusForbidden, "NOT_SUPER_ADMIN")
		wantStatus(t, call(t, http.MethodPut, "/settings/cash-currency", owner, gin.H{"cash_currency": "USD"}), http.StatusNotFound)
	})

	t.Run("back and forth while empty", func(t *testing.T) {
		wantStatus(t, update(gin.H{"main_currency": "USD"}), http.StatusOK)
		if got := companyRefundLimit(t, c.id, "Manager"); got != 2000 {
			t.Fatalf("back in dollars the Manager refunds up to %d, want 2000", got)
		}
		wantStatus(t, update(gin.H{"main_currency": "KHR"}), http.StatusOK)
	})

	t.Run("locked once a product has a price", func(t *testing.T) {
		product := models.Product{CompanyID: c.id, SKU: unique("SKU"), NameEn: "Iced coffee", Unit: "cup", Type: models.ProductStandard, Active: true}
		if err := env.db.Create(&product).Error; err != nil {
			t.Fatal(err)
		}
		if err := env.db.Create(&models.ProductPrice{ProductID: product.ID, PriceCents: 0, CostCents: 0, EffectiveAt: time.Now()}).Error; err != nil {
			t.Fatal(err)
		}
		if listed()["main_currency_locked"] != false {
			t.Fatal("a product priced at nothing locked it")
		}
		if err := env.db.Create(&models.ProductPrice{ProductID: product.ID, PriceCents: 6200, CostCents: 3000, EffectiveAt: time.Now()}).Error; err != nil {
			t.Fatal(err)
		}
		if listed()["main_currency_locked"] != true {
			t.Fatal("not listed as locked")
		}
		r := update(gin.H{"main_currency": "USD"})
		wantDenied(t, r, http.StatusUnprocessableEntity, "MAIN_CURRENCY_LOCKED")
		if r.Error.Fields["main_currency"] == "" {
			t.Fatalf("no field message: %s", r)
		}
		if got := readCurrency(t, owner); got != "KHR" {
			t.Fatalf("a locked change left %q", got)
		}
		if got := companyRefundLimit(t, c.id, "Manager"); got != 82000 {
			t.Fatalf("a locked change moved the Manager's limit to %d", got)
		}
		// Saving it as it is still works.
		wantStatus(t, update(gin.H{"main_currency": "KHR", "shift_mode": "SKIP"}), http.StatusOK)
	})

	t.Run("each company has its own", func(t *testing.T) {
		if got := readCurrency(t, login(t, env.b.owner).Access); got != "USD" {
			t.Fatalf("another company reads %q, want USD", got)
		}
	})
}

// rielCompany is a company made in riel, with a branch, a till, two
// products and a customer, ready to sell.
type rielCompany struct {
	id, branch, product, odd, customer uint64
	till, owner, code                  string
	ownerUser                          namedUser
}

func makeRielCompany(t *testing.T) rielCompany {
	t.Helper()
	code := companyCode()
	username := unique("riel.owner")
	r := call(t, http.MethodPost, "/platform/companies", login(t, env.superAdmin).Access, gin.H{
		"name": "Company " + code, "code": strings.ToLower(code), "main_currency": "KHR",
		"owner_full_name": "Owner " + code, "owner_username": username, "owner_password": testPassword,
	})
	wantStatus(t, r, http.StatusCreated)
	if v, _ := r.field(t, "main_currency").(string); v != "KHR" {
		t.Fatalf("answered %q, want KHR", v)
	}
	rc := rielCompany{id: r.id(t), code: code, ownerUser: namedUser{Username: username}}
	branch := models.Branch{CompanyID: rc.id, Name: "Riel branch", Code: "RB", Active: true}
	if err := env.db.Create(&branch).Error; err != nil {
		t.Fatal(err)
	}
	rc.branch = branch.ID
	rc.till = newTill(t, branch.ID)
	for _, p := range []struct {
		into  *uint64
		price int64
	}{{&rc.product, 15600}, {&rc.odd, 17050}} {
		product := models.Product{CompanyID: rc.id, SKU: unique("SKU"), NameEn: fmt.Sprintf("Item ៛%d", p.price), Unit: "pcs", Type: models.ProductStandard, Active: true}
		if err := env.db.Create(&product).Error; err != nil {
			t.Fatal(err)
		}
		if err := env.db.Create(&models.ProductPrice{ProductID: product.ID, PriceCents: p.price, CostCents: 9000, EffectiveAt: time.Now().Add(-time.Hour)}).Error; err != nil {
			t.Fatal(err)
		}
		if err := env.db.Create(&models.BranchStock{BranchID: branch.ID, ProductID: product.ID, Qty: 1000}).Error; err != nil {
			t.Fatal(err)
		}
		*p.into = product.ID
	}
	customer := models.Customer{CompanyID: rc.id, Name: "Riel customer", Phone: unique("tel"), Tier: "MEMBER"}
	if err := env.db.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	rc.customer = customer.ID
	rc.owner = login(t, rc.ownerUser).Access
	return rc
}

// A riel company prices in whole riel and gives all change in riel: the cash
// due is rounded to ៛100 and dollars handed over count at the rate.
func TestSellingInRiel(t *testing.T) {
	setup(t)
	rc := makeRielCompany(t)
	if got := readCurrency(t, rc.owner); got != "KHR" {
		t.Fatalf("reads %q, want KHR", got)
	}
	for role, want := range map[string]int64{"Manager": 82000, "Cashier": 20500} {
		if got := companyRefundLimit(t, rc.id, role); got != want {
			t.Errorf("%s refunds up to %d, want %d riel", role, got, want)
		}
	}
	open := call(t, http.MethodPost, "/shifts/open", rc.owner, gin.H{"device_key": rc.till, "opening_usd_cents": 0, "opening_khr_riel": 0})
	wantStatus(t, open, http.StatusCreated)
	shift := open.id(t)

	sell := func(product uint64, usd, khr int64, extra gin.H) response {
		t.Helper()
		body := saleBody(rc.till, unique("key"), product)
		body["payment"] = gin.H{"method": "CASH", "received_usd_cents": usd, "received_khr_riel": khr}
		for k, v := range extra {
			body[k] = v
		}
		return call(t, http.MethodPost, "/sales", rc.owner, body)
	}
	// payment is a sale's payment row: currency, riel amount, change.
	payment := func(sale uint64) (string, int64, int64, int64) {
		t.Helper()
		var p models.Payment
		if err := env.db.Where("sale_id = ?", sale).First(&p).Error; err != nil {
			t.Fatal(err)
		}
		return string(p.Currency), p.AmountRiel, p.ChangeUSDCents, p.ChangeKHRRiel
	}

	for _, tc := range []struct {
		name          string
		product       uint64
		usd, khr      int64
		total, change int64
	}{
		{"riel", rc.product, 0, 20000, 15600, 4400},
		{"dollars, change in riel", rc.product, 500, 0, 15600, 4900},
		{"both", rc.product, 300, 4000, 15600, 700},
		{"the cash due is rounded to ៛100", rc.odd, 0, 17100, 17050, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := sell(tc.product, tc.usd, tc.khr, nil)
			wantStatus(t, r, http.StatusCreated)
			wantNum(t, "total", r.field(t, "total_cents"), tc.total)
			wantNum(t, "riel total", r.field(t, "total_riel"), tc.total)
			currency, amountRiel, changeUSD, changeKHR := payment(r.id(t))
			if currency != "KHR" || amountRiel != tc.total || changeUSD != 0 || changeKHR != tc.change {
				t.Fatalf("payment %s ៛%d, change $%d + ៛%d; want KHR ៛%d, change ៛%d", currency, amountRiel, changeUSD, changeKHR, tc.total, tc.change)
			}
		})
	}

	t.Run("short is refused", func(t *testing.T) {
		wantDenied(t, sell(rc.product, 0, 15500, nil), http.StatusUnprocessableEntity, "INSUFFICIENT_CASH")
		wantDenied(t, sell(rc.odd, 0, 17000, nil), http.StatusUnprocessableEntity, "INSUFFICIENT_CASH")
		wantDenied(t, sell(rc.product, 300, 3200, nil), http.StatusUnprocessableEntity, "INSUFFICIENT_CASH") // ៛15,500
	})

	t.Run("loyalty points per ៛1,000", func(t *testing.T) {
		wantStatus(t, sell(rc.product, 0, 20000, gin.H{"customer_id": rc.customer}), http.StatusCreated)
		if got := dbInt(t, "SELECT points FROM customers WHERE id = ?", rc.customer); got != 15 {
			t.Fatalf("៛15,600 earned %d points, want 15", got)
		}
	})

	t.Run("the cashier report adds the drawer difference up in riel", func(t *testing.T) {
		usd := dbInt(t, "SELECT COALESCE(SUM(p.received_usd_cents - p.change_usd_cents),0) FROM payments p JOIN sales s ON s.id = p.sale_id WHERE s.shift_id = ?", shift)
		khr := dbInt(t, "SELECT COALESCE(SUM(p.received_khr_riel - p.change_khr_riel),0) FROM payments p JOIN sales s ON s.id = p.sale_id WHERE s.shift_id = ?", shift)
		// $1 over in the drawer is ៛4,100 over.
		wantStatus(t, call(t, http.MethodPost, "/shifts/"+id(shift)+"/close", rc.owner, gin.H{"counted_usd_cents": usd + 100, "counted_khr_riel": khr}), http.StatusOK)
		at := fmt.Sprintf("branch_id=%d&date_from=%s&date_to=%s", rc.branch, today(), today())
		r := call(t, http.MethodGet, "/reports/by-cashier?"+at, rc.owner, nil)
		wantStatus(t, r, http.StatusOK)
		rows := r.rows(t)
		if len(rows) != 1 {
			t.Fatalf("by-cashier: %s", r)
		}
		wantNum(t, "cash difference", rows[0]["cash_diff_cents"], 4100)
	})

	t.Run("the table menu says what its prices are in", func(t *testing.T) {
		table := models.DiningTable{BranchID: rc.branch, Name: "T1", Token: unique("tok"), Active: true}
		if err := env.db.Create(&table).Error; err != nil {
			t.Fatal(err)
		}
		r := call(t, http.MethodGet, "/public/tables/"+table.Token+"/menu", "", nil)
		wantStatus(t, r, http.StatusOK)
		if v, _ := r.field(t, "currency").(string); v != "KHR" {
			t.Fatalf("menu currency %q, want KHR", v)
		}
	})

	t.Run("the company is now locked to riel", func(t *testing.T) {
		r := call(t, http.MethodPut, "/platform/companies/"+id(rc.id), login(t, env.superAdmin).Access, gin.H{
			"name": "Riel", "code": rc.code, "active": true, "retention_months": 3, "main_currency": "USD",
		})
		wantDenied(t, r, http.StatusUnprocessableEntity, "MAIN_CURRENCY_LOCKED")
	})
}

// A dollar company is untouched: change is whole dollars plus riel.
func TestSellingInDollarsIsUnchanged(t *testing.T) {
	e := setup(t)
	owner := login(t, e.owner).Access
	if got := readCurrency(t, owner); got != "USD" {
		t.Fatalf("reads %q, want USD", got)
	}
	till := newTill(t, e.branchTK)
	wantStatus(t, call(t, http.MethodPost, "/shifts/open", owner, gin.H{"device_key": till, "opening_usd_cents": 0, "opening_khr_riel": 0}), http.StatusCreated)
	body := saleBody(till, unique("key"), e.product) // $10.00
	body["payment"] = gin.H{"method": "CASH", "received_usd_cents": 1250, "received_khr_riel": 0}
	r := call(t, http.MethodPost, "/sales", owner, body)
	wantStatus(t, r, http.StatusCreated)
	wantNum(t, "riel total", r.field(t, "total_riel"), 41000)
	var p models.Payment
	if err := env.db.Where("sale_id = ?", r.id(t)).First(&p).Error; err != nil {
		t.Fatal(err)
	}
	if p.Currency != models.CurrencyUSD || p.AmountRiel != 41000 || p.ChangeUSDCents != 200 || p.ChangeKHRRiel != 2100 {
		t.Fatalf("payment %+v, want USD ៛41000, change $2 + ៛2100", p)
	}
}

// Handing over exactly the riel amount the till shows pays a dollar sale:
// $3.15 at ៛4,100 is ៛12,915, shown as ៛12,900, a cent under the dollars.
func TestPayingTheRielTotal(t *testing.T) {
	e := setup(t)
	owner := login(t, e.owner).Access
	till := newTill(t, e.branchTK)
	wantStatus(t, call(t, http.MethodPost, "/shifts/open", owner, gin.H{"device_key": till, "opening_usd_cents": 0, "opening_khr_riel": 0}), http.StatusCreated)
	sale := func(usd, khr int64) response {
		body := saleBody(till, unique("key"), e.product)
		body["discount_cents"] = 685 // $10.00 down to $3.15
		body["payment"] = gin.H{"method": "CASH", "received_usd_cents": usd, "received_khr_riel": khr}
		return call(t, http.MethodPost, "/sales", owner, body)
	}

	paid := sale(0, 12900)
	wantStatus(t, paid, http.StatusCreated)
	if total, _ := paid.field(t, "total_cents").(float64); total != 315 {
		t.Fatalf("total is %v, want 315", total)
	}
	got := dbInt(t, "SELECT received_khr_riel * 1000000 + change_usd_cents * 1000 + change_khr_riel FROM payments WHERE sale_id = ?", paid.id(t))
	if got != 12900*1000000 {
		t.Fatalf("payment received ៛%d with change $%d + ៛%d, want ៛12900 and no change", got/1000000, got/1000%1000, got%1000)
	}

	wantStatus(t, sale(300, 600), http.StatusCreated)
	wantDenied(t, sale(0, 12800), http.StatusUnprocessableEntity, "INSUFFICIENT_CASH")
	wantDenied(t, sale(314, 0), http.StatusUnprocessableEntity, "INSUFFICIENT_CASH")
}
