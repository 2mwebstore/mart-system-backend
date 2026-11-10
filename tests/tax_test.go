package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"com-mart/backend/internal/models"
)

// Tax rates are added on top of a POS sale, an invoice, a purchase order or
// an expense. Each keeps a snapshot of the rate it used; sales figures leave
// the tax out, and purchase tax is never part of the stock's cost.

// taxBranch is a branch of company A for one test, with stock of the test
// product, so its reports hold that test's documents alone. Make it before
// signing in: a session's branches are read at sign-in.
func taxBranch(t *testing.T) uint64 {
	t.Helper()
	b := models.Branch{CompanyID: env.companyA, Name: unique("Tax branch"), Code: fmt.Sprintf("TX%d", uniqueSeq.Add(1)), Active: true}
	if err := env.db.Create(&b).Error; err != nil {
		t.Fatal(err)
	}
	if err := env.db.Create(&models.BranchStock{BranchID: b.ID, ProductID: env.product, Qty: 100000}).Error; err != nil {
		t.Fatal(err)
	}
	return b.ID
}

// taxProduct is a product of company A for one test, priced 1000, costing 500.
func taxProduct(t *testing.T) uint64 {
	t.Helper()
	p := models.Product{CompanyID: env.companyA, SKU: unique("TAX"), NameEn: unique("Tax item"), Unit: "pcs", Type: models.ProductType("STANDARD"), Active: true}
	if err := env.db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	if err := env.db.Create(&models.ProductPrice{ProductID: p.ID, PriceCents: 1000, CostCents: 500, EffectiveAt: time.Now().Add(-time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func newTaxRate(t *testing.T, token, name string, percent float64) uint64 {
	t.Helper()
	r := call(t, http.MethodPost, "/tax-rates", token, gin.H{"name": name, "percent": percent, "active": true})
	wantStatus(t, r, http.StatusCreated)
	return r.id(t)
}

func setTaxRate(t *testing.T, token string, id uint64, name string, percent float64, active bool) {
	t.Helper()
	wantStatus(t, call(t, http.MethodPut, fmt.Sprintf("/tax-rates/%d", id), token, gin.H{"name": name, "percent": percent, "active": active}), http.StatusOK)
}

// num reads a number field of a response or row.
func num(t *testing.T, v interface{}) int64 {
	t.Helper()
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("not a number: %#v", v)
	}
	return int64(f)
}

func wantNum(t *testing.T, what string, got interface{}, want int64) {
	t.Helper()
	if n := num(t, got); n != want {
		t.Errorf("%s = %d, want %d", what, n, want)
	}
}

func wantField(t *testing.T, r response, field string) {
	t.Helper()
	if r.Status != http.StatusUnprocessableEntity || r.Error == nil || r.Error.Fields[field] == "" {
		t.Fatalf("want 422 on %s, got %s", field, r)
	}
}

// startingRates reads GET /settings' tax_defaults.
func startingRates(t *testing.T, token string) map[string]interface{} {
	t.Helper()
	r := call(t, http.MethodGet, "/settings", token, nil)
	wantStatus(t, r, http.StatusOK)
	d, ok := r.field(t, "tax_defaults").(map[string]interface{})
	if !ok {
		t.Fatalf("no tax_defaults: %s", r)
	}
	return d
}

func listedRates(t *testing.T, token string) map[uint64]bool {
	t.Helper()
	r := call(t, http.MethodGet, "/tax-rates", token, nil)
	wantStatus(t, r, http.StatusOK)
	out := map[uint64]bool{}
	for _, row := range r.rows(t) {
		out[uint64(num(t, row["id"]))] = true
	}
	return out
}

func TestTaxRates(t *testing.T) {
	e := setup(t)
	keepSettings(t)
	owner := login(t, e.owner).Access
	ownerB := login(t, e.b.owner).Access
	cashier := login(t, e.cashierTK).Access

	made := call(t, http.MethodPost, "/tax-rates", owner, gin.H{"name": "  VAT  ", "percent": 10.004, "active": true})
	wantStatus(t, made, http.StatusCreated)
	if made.field(t, "name") != "VAT" || made.field(t, "percent") != 10.0 {
		t.Fatalf("name and percent not cleaned: %s", made)
	}
	vat := made.id(t)
	rateB := newTaxRate(t, ownerB, "B's VAT", 10)

	t.Run("everyone signed in lists them; only tax_rate.* changes them", func(t *testing.T) {
		if !listedRates(t, cashier)[vat] {
			t.Fatal("a cashier doesn't see the company's rates")
		}
		wantDenied(t, call(t, http.MethodPost, "/tax-rates", cashier, gin.H{"name": "x", "percent": 1}), http.StatusForbidden, "FORBIDDEN")
		settings := login(t, newUser(t, newRole(t, "settings.company"))).Access
		wantDenied(t, call(t, http.MethodPut, fmt.Sprintf("/tax-rates/%d", vat), settings, gin.H{"name": "x", "percent": 1}), http.StatusForbidden, "FORBIDDEN")
		wantDenied(t, call(t, http.MethodDelete, fmt.Sprintf("/tax-rates/%d", vat), login(t, newUser(t, newRole(t, "tax_rate.create", "tax_rate.update"))).Access, nil), http.StatusForbidden, "FORBIDDEN")
		wantDenied(t, call(t, http.MethodPut, "/tax-rates/defaults", settings, gin.H{"pos": vat}), http.StatusForbidden, "FORBIDDEN")

		// Each key is its own: adding rates doesn't let you change them.
		creator := login(t, newUser(t, newRole(t, "tax_rate.create"))).Access
		same := gin.H{"name": "VAT", "percent": 10, "active": true}
		wantDenied(t, call(t, http.MethodPut, fmt.Sprintf("/tax-rates/%d", vat), creator, same), http.StatusForbidden, "FORBIDDEN")
		wantDenied(t, call(t, http.MethodPut, "/tax-rates/defaults", creator, gin.H{"pos": vat}), http.StatusForbidden, "FORBIDDEN")
		editor := login(t, newUser(t, newRole(t, "tax_rate.update"))).Access
		wantStatus(t, call(t, http.MethodPut, fmt.Sprintf("/tax-rates/%d", vat), editor, same), http.StatusOK)
		wantDenied(t, call(t, http.MethodPost, "/tax-rates", editor, gin.H{"name": "x", "percent": 1}), http.StatusForbidden, "FORBIDDEN")
	})

	t.Run("another company's rate is out of sight and reach", func(t *testing.T) {
		if listedRates(t, ownerB)[vat] || listedRates(t, owner)[rateB] {
			t.Fatal("a company sees another's rate")
		}
		wantStatus(t, call(t, http.MethodPut, fmt.Sprintf("/tax-rates/%d", vat), ownerB, gin.H{"name": "x", "percent": 1}), http.StatusNotFound)
		wantStatus(t, call(t, http.MethodDelete, fmt.Sprintf("/tax-rates/%d", vat), ownerB, nil), http.StatusNotFound)
	})

	t.Run("a percent is 0 to 100 and a name is needed", func(t *testing.T) {
		wantField(t, call(t, http.MethodPost, "/tax-rates", owner, gin.H{"name": "Too much", "percent": 100.5}), "percent")
		wantField(t, call(t, http.MethodPost, "/tax-rates", owner, gin.H{"name": "Negative", "percent": -1}), "percent")
		wantField(t, call(t, http.MethodPost, "/tax-rates", owner, gin.H{"name": "   ", "percent": 5}), "name")
	})

	t.Run("each place's starting rate", func(t *testing.T) {
		r := call(t, http.MethodPut, "/tax-rates/defaults", owner, gin.H{"pos": vat, "invoice": nil, "purchase": 0, "expense": vat})
		wantStatus(t, r, http.StatusOK)
		got := startingRates(t, cashier)
		if num(t, got["pos"]) != int64(vat) || num(t, got["expense"]) != int64(vat) || got["invoice"] != nil || got["purchase"] != nil {
			t.Fatalf("starting rates: %v", got)
		}
		wantField(t, call(t, http.MethodPut, "/tax-rates/defaults", owner, gin.H{"invoice": rateB}), "invoice")
		wantField(t, call(t, http.MethodPut, "/tax-rates/defaults", owner, gin.H{"purchase": 999999}), "purchase")

		// Switched off, it starts nowhere; on again, where it did.
		setTaxRate(t, owner, vat, "VAT", 10, false)
		if got := startingRates(t, owner); got["pos"] != nil || got["expense"] != nil {
			t.Fatalf("a rate switched off is still a starting rate: %v", got)
		}
		wantField(t, call(t, http.MethodPut, "/tax-rates/defaults", owner, gin.H{"pos": vat}), "pos")
		setTaxRate(t, owner, vat, "VAT", 10, true)
		if got := startingRates(t, owner); num(t, got["pos"]) != int64(vat) {
			t.Fatalf("switched back on, the starting rate is gone: %v", got)
		}
	})

	t.Run("deleting a rate takes it off the list and off every starting rate", func(t *testing.T) {
		wantStatus(t, call(t, http.MethodDelete, fmt.Sprintf("/tax-rates/%d", vat), owner, nil), http.StatusOK)
		if listedRates(t, owner)[vat] {
			t.Fatal("a deleted rate is still listed")
		}
		if got := startingRates(t, owner); got["pos"] != nil || got["expense"] != nil {
			t.Fatalf("a deleted rate is still a starting rate: %v", got)
		}
		if v := dbText(t, "SELECT value FROM company_settings WHERE company_id = ? AND `key` = 'tax_default_pos'", e.companyA); v != "" {
			t.Fatalf("tax_default_pos = %q after the rate was deleted", v)
		}
	})
}

func TestSaleTax(t *testing.T) {
	e := setup(t)
	branch := taxBranch(t)
	till := newTill(t, branch)
	owner := login(t, e.owner).Access
	ownerB := login(t, e.b.owner).Access
	wantStatus(t, call(t, http.MethodPost, "/shifts/open", owner, gin.H{"device_key": till, "opening_usd_cents": 0, "opening_khr_riel": 0}), http.StatusCreated)
	vat := newTaxRate(t, owner, unique("VAT 10%"), 10)

	sale := func(extra gin.H) response {
		t.Helper()
		body := saleBody(till, unique("key"), e.product)
		body["payment"] = gin.H{"method": "CASH", "received_usd_cents": 2000}
		for k, v := range extra {
			body[k] = v
		}
		return call(t, http.MethodPost, "/sales", owner, body)
	}
	payment := func(t *testing.T, r response) map[string]interface{} {
		t.Helper()
		p, ok := r.field(t, "payment").(map[string]interface{})
		if !ok {
			t.Fatalf("no payment: %s", r)
		}
		return p
	}

	t.Run("added on top of the discounted amount; change is on the taxed total", func(t *testing.T) {
		r := sale(gin.H{"tax_rate_id": vat, "discount_cents": 100})
		wantStatus(t, r, http.StatusCreated)
		wantNum(t, "subtotal", r.field(t, "subtotal_cents"), 1000)
		wantNum(t, "tax", r.field(t, "tax_cents"), 90)
		wantNum(t, "total", r.field(t, "total_cents"), 990)
		// $20.00 for $9.90: $10 in dollars and 10¢ in riel.
		wantNum(t, "change in dollars", payment(t, r)["change_usd_cents"], 1000)
		wantNum(t, "change in riel", payment(t, r)["change_khr_riel"], 400)
		if got := dbInt(t, "SELECT amount_cents FROM payments WHERE sale_id = ?", r.id(t)); got != 990 {
			t.Errorf("paid %d, want 990", got)
		}
		if r.field(t, "tax_percent") != 10.0 || num(t, r.field(t, "tax_rate_id")) != int64(vat) {
			t.Fatalf("rate not kept on the sale: %s", r)
		}
		// Enough before tax isn't enough.
		short := sale(gin.H{"tax_rate_id": vat, "discount_cents": 100, "payment": gin.H{"method": "CASH", "received_usd_cents": 950}})
		wantDenied(t, short, http.StatusUnprocessableEntity, "INSUFFICIENT_CASH")
	})

	t.Run("no rate is no tax", func(t *testing.T) {
		for _, extra := range []gin.H{{}, {"tax_rate_id": 0}, {"tax_rate_id": nil}} {
			r := sale(extra)
			wantStatus(t, r, http.StatusCreated)
			wantNum(t, "tax", r.field(t, "tax_cents"), 0)
			wantNum(t, "total", r.field(t, "total_cents"), 1000)
			if r.field(t, "tax_rate_id") != nil || r.field(t, "tax_name") != "" {
				t.Fatalf("a sale without tax names a rate: %s", r)
			}
		}
	})

	t.Run("a rate switched off, deleted or another company's is refused", func(t *testing.T) {
		off := newTaxRate(t, owner, unique("Off"), 5)
		setTaxRate(t, owner, off, "Off", 5, false)
		wantField(t, sale(gin.H{"tax_rate_id": off}), "tax_rate_id")
		gone := newTaxRate(t, owner, unique("Gone"), 5)
		wantStatus(t, call(t, http.MethodDelete, fmt.Sprintf("/tax-rates/%d", gone), owner, nil), http.StatusOK)
		wantField(t, sale(gin.H{"tax_rate_id": gone}), "tax_rate_id")
		wantField(t, sale(gin.H{"tax_rate_id": newTaxRate(t, ownerB, unique("B"), 5)}), "tax_rate_id")
	})

	t.Run("the note is rich text, cleaned; an edit changes or keeps it", func(t *testing.T) {
		r := sale(gin.H{"note": `<p>Wrap <strong>it</strong></p><script>alert(1)</script>`})
		wantStatus(t, r, http.StatusCreated)
		if got := r.field(t, "note"); got != "<p>Wrap <strong>it</strong></p>" {
			t.Fatalf("note = %q", got)
		}
		id := r.id(t)
		edit := func(body gin.H) response {
			t.Helper()
			body["items"] = []gin.H{{"product_id": e.product, "qty": 1}}
			return call(t, http.MethodPut, fmt.Sprintf("/sales/%d", id), owner, body)
		}
		kept := edit(gin.H{})
		wantStatus(t, kept, http.StatusOK)
		if got := kept.field(t, "note"); got != "<p>Wrap <strong>it</strong></p>" {
			t.Fatalf("an edit without a note changed it to %q", got)
		}
		changed := edit(gin.H{"note": `<p onclick="x()">Gift</p><img src=x onerror=y>`})
		wantStatus(t, changed, http.StatusOK)
		if got := changed.field(t, "note"); got != "<p>Gift</p>" {
			t.Fatalf("edited note = %q", got)
		}
	})

	t.Run("an edit keeps the sale's own rate, even after the rate changes", func(t *testing.T) {
		rate := newTaxRate(t, owner, unique("Kept"), 10)
		r := sale(gin.H{"tax_rate_id": rate})
		wantStatus(t, r, http.StatusCreated)
		setTaxRate(t, owner, rate, "Kept", 20, false)
		edit := func(received int64) response {
			return call(t, http.MethodPut, fmt.Sprintf("/sales/%d", r.id(t)), owner, gin.H{
				"items":   []gin.H{{"product_id": e.product, "qty": 2}},
				"payment": gin.H{"method": "CASH", "received_usd_cents": received},
			})
		}
		// 2000 would cover the two before tax, not after.
		wantDenied(t, edit(2000), http.StatusUnprocessableEntity, "INSUFFICIENT_CASH")
		edited := edit(2200)
		wantStatus(t, edited, http.StatusOK)
		wantNum(t, "tax", edited.field(t, "tax_cents"), 200)
		wantNum(t, "total", edited.field(t, "total_cents"), 2200)
		if got := dbInt(t, "SELECT amount_cents FROM payments WHERE sale_id = ?", r.id(t)); got != 2200 {
			t.Errorf("paid %d, want 2200", got)
		}
		if edited.field(t, "tax_percent") != 10.0 {
			t.Fatalf("the edit took the rate's new percent: %s", edited)
		}
	})
}

func TestDocumentTax(t *testing.T) {
	e := setup(t)
	branch := taxBranch(t)
	owner := login(t, e.owner).Access
	ownerB := login(t, e.b.owner).Access
	vat := newTaxRate(t, owner, unique("VAT 10%"), 10)
	rateB := newTaxRate(t, ownerB, unique("B"), 10)

	t.Run("invoice: on the lines after discounts plus shipping; an edit keeps its rate", func(t *testing.T) {
		body := gin.H{
			"branch_id": branch, "customer_id": e.customer, "invoice_date": today(), "shipping_cents": 200, "discount_cents": 100, "tax_rate_id": vat,
			"items": []gin.H{{"product_id": e.product, "qty": 2, "unit_price_cents": 1000, "discount_cents": 300}},
		}
		r := call(t, http.MethodPost, "/invoices", owner, body)
		wantStatus(t, r, http.StatusCreated)
		// 2000 - 300 - 100 + 200 = 1800; 10% is 180.
		wantNum(t, "tax", r.field(t, "tax_cents"), 180)
		wantNum(t, "total", r.field(t, "total_cents"), 1980)
		id := r.id(t)

		setTaxRate(t, owner, vat, "VAT 10%", 25, true)
		body["shipping_cents"] = 0
		edited := call(t, http.MethodPut, fmt.Sprintf("/invoices/%d", id), owner, body)
		wantStatus(t, edited, http.StatusOK)
		wantNum(t, "tax", edited.field(t, "tax_cents"), 160)
		wantNum(t, "total", edited.field(t, "total_cents"), 1760)
		body["tax_rate_id"] = nil
		untaxed := call(t, http.MethodPut, fmt.Sprintf("/invoices/%d", id), owner, body)
		wantStatus(t, untaxed, http.StatusOK)
		wantNum(t, "tax", untaxed.field(t, "tax_cents"), 0)
		wantNum(t, "total", untaxed.field(t, "total_cents"), 1600)
		// Taken off, it comes back at the rate's percent now.
		body["tax_rate_id"] = vat
		again := call(t, http.MethodPut, fmt.Sprintf("/invoices/%d", id), owner, body)
		wantStatus(t, again, http.StatusOK)
		wantNum(t, "tax", again.field(t, "tax_cents"), 400)
		setTaxRate(t, owner, vat, "VAT 10%", 10, true)
		body["tax_rate_id"] = nil
		wantStatus(t, call(t, http.MethodPut, fmt.Sprintf("/invoices/%d", id), owner, body), http.StatusOK)
		body["tax_rate_id"] = vat
		wantStatus(t, call(t, http.MethodPut, fmt.Sprintf("/invoices/%d", id), owner, body), http.StatusOK)

		// The whole taxed total can be paid, and no more.
		pay := func(cents int64) response {
			return call(t, http.MethodPost, fmt.Sprintf("/invoices/%d/payments", id), owner, gin.H{"paid_on": today(), "method_name": "Cash", "method_type": "CASH", "amount_cents": cents})
		}
		wantDenied(t, pay(1761), http.StatusUnprocessableEntity, "PAYMENT_OVER_BALANCE")
		if r := pay(1760); r.Status >= 300 {
			t.Fatalf("paying the taxed total: %s", r)
		}
		body["tax_rate_id"] = rateB
		wantField(t, call(t, http.MethodPost, "/invoices", owner, body), "tax_rate_id")
	})

	t.Run("purchase order: tax is owed the supplier, never the stock's cost", func(t *testing.T) {
		product := taxProduct(t)
		body := gin.H{
			"branch_id": branch, "supplier_id": e.supplier, "shipping_cents": 100, "tax_rate_id": vat,
			"items": []gin.H{{"product_id": product, "qty": 2, "unit_cost_cents": 300, "discount_cents": 50}},
		}
		r := call(t, http.MethodPost, "/purchase-orders", owner, body)
		wantStatus(t, r, http.StatusCreated)
		// 600 - 50 + 100 = 650; 10% is 65.
		wantNum(t, "tax", r.field(t, "tax_cents"), 65)
		id := r.id(t)

		setTaxRate(t, owner, vat, "VAT 10%", 20, true)
		body["shipping_cents"] = 0
		edited := call(t, http.MethodPut, fmt.Sprintf("/purchase-orders/%d", id), owner, body)
		wantStatus(t, edited, http.StatusOK)
		wantNum(t, "tax", edited.field(t, "tax_cents"), 55)
		setTaxRate(t, owner, vat, "VAT 10%", 10, true)

		wantStatus(t, call(t, http.MethodPost, fmt.Sprintf("/purchase-orders/%d/receive", id), owner, gin.H{}), http.StatusOK)
		// (2 × 300 - 50) / 2 = 275 a unit, before tax.
		if got := dbInt(t, "SELECT unit_cost_cents FROM stock_movements WHERE reference_type = 'PO' AND reference_id = ? LIMIT 1", id); got != 275 {
			t.Fatalf("received at %d a unit, want 275 (no tax)", got)
		}
		if got := dbInt(t, "SELECT cost_cents FROM product_prices WHERE product_id = ? ORDER BY effective_at DESC, id DESC LIMIT 1", product); got != 275 {
			t.Fatalf("product cost %d, want 275 (no tax)", got)
		}
		body["tax_rate_id"] = rateB
		wantField(t, call(t, http.MethodPost, "/purchase-orders", owner, body), "tax_rate_id")
	})

	t.Run("expense: on the amount; rich note; summary; an edit keeps its rate", func(t *testing.T) {
		r := call(t, http.MethodPost, "/expenses", owner, gin.H{
			"branch_id": branch, "category": "OTHER", "amount_cents": 505, "expense_date": today(), "tax_rate_id": vat,
			"note": `<p>Gas <em>bottle</em></p><iframe src="x"></iframe>`,
		})
		wantStatus(t, r, http.StatusCreated)
		// 10% of 505 is 50.5: half a cent rounds up.
		wantNum(t, "tax", r.field(t, "tax_cents"), 51)
		wantNum(t, "total", r.field(t, "total_cents"), 556)
		if got := r.field(t, "note"); got != "<p>Gas <em>bottle</em></p>" {
			t.Fatalf("note = %q", got)
		}
		id := r.id(t)
		plain := call(t, http.MethodPost, "/expenses", owner, gin.H{"branch_id": branch, "category": "RENT", "amount_cents": 1000, "expense_date": today()})
		wantStatus(t, plain, http.StatusCreated)
		wantNum(t, "tax", plain.field(t, "tax_cents"), 0)

		list := call(t, http.MethodGet, fmt.Sprintf("/expenses?branch_id=%d&page=1", branch), owner, nil)
		wantStatus(t, list, http.StatusOK)
		summary, _ := json.Marshal(list.Meta)
		var meta struct {
			Summary struct {
				TotalCents int64 `json:"total_cents"`
				TaxCents   int64 `json:"tax_cents"`
			} `json:"summary"`
		}
		_ = json.Unmarshal(summary, &meta)
		if meta.Summary.TotalCents != 1556 || meta.Summary.TaxCents != 51 {
			t.Fatalf("summary %s, want total 1556 and tax 51", summary)
		}

		setTaxRate(t, owner, vat, "VAT 10%", 20, false)
		put := func(rate interface{}) response {
			return call(t, http.MethodPut, fmt.Sprintf("/expenses/%d", id), owner, gin.H{"branch_id": branch, "category": "OTHER", "amount_cents": 1000, "expense_date": today(), "tax_rate_id": rate})
		}
		kept := put(vat)
		wantStatus(t, kept, http.StatusOK)
		wantNum(t, "tax", kept.field(t, "tax_cents"), 100)
		none := put(nil)
		wantStatus(t, none, http.StatusOK)
		wantNum(t, "tax", none.field(t, "tax_cents"), 0)
		// Once taken off, a rate switched off can't come back.
		wantField(t, put(vat), "tax_rate_id")
		setTaxRate(t, owner, vat, "VAT 10%", 10, true)
		wantField(t, put(rateB), "tax_rate_id")
	})
}

// Sales figures leave out the tax collected; Profit & Loss shows it, and the
// tax paid on expenses and received purchase orders, on their own.
func TestReportsLeaveTaxOut(t *testing.T) {
	e := setup(t)
	branch := taxBranch(t)
	till := newTill(t, branch)
	owner := login(t, e.owner).Access
	vat := newTaxRate(t, owner, unique("VAT 10%"), 10)
	wantStatus(t, call(t, http.MethodPost, "/shifts/open", owner, gin.H{"device_key": till, "opening_usd_cents": 0, "opening_khr_riel": 0}), http.StatusCreated)

	// A sale of 1000 (+100 tax), costing 500.
	sale := saleBody(till, unique("key"), e.product)
	sale["tax_rate_id"] = vat
	wantStatus(t, call(t, http.MethodPost, "/sales", owner, sale), http.StatusCreated)
	// An invoice of 1000 + 200 shipping (+120 tax), costing 500.
	wantStatus(t, call(t, http.MethodPost, "/invoices", owner, gin.H{
		"branch_id": branch, "customer_id": e.customer, "invoice_date": today(), "shipping_cents": 200, "tax_rate_id": vat,
		"items": []gin.H{{"product_id": e.product, "qty": 1, "unit_price_cents": 1000}},
	}), http.StatusCreated)
	// An expense of 500 (+50 tax).
	wantStatus(t, call(t, http.MethodPost, "/expenses", owner, gin.H{"branch_id": branch, "category": "OTHER", "amount_cents": 500, "expense_date": today(), "tax_rate_id": vat}), http.StatusCreated)
	// A purchase order of 600 + 100 shipping (+70 tax), received; and one not
	// received, whose tax isn't paid yet.
	po := func() uint64 {
		r := call(t, http.MethodPost, "/purchase-orders", owner, gin.H{
			"branch_id": branch, "supplier_id": e.supplier, "shipping_cents": 100, "tax_rate_id": vat,
			"items": []gin.H{{"product_id": taxProduct(t), "qty": 2, "unit_cost_cents": 300}},
		})
		wantStatus(t, r, http.StatusCreated)
		return r.id(t)
	}
	wantStatus(t, call(t, http.MethodPost, fmt.Sprintf("/purchase-orders/%d/receive", po()), owner, gin.H{}), http.StatusOK)
	po()

	at := fmt.Sprintf("branch_id=%d&date_from=%s&date_to=%s", branch, today(), today())
	dash := call(t, http.MethodGet, fmt.Sprintf("/reports/dashboard?branch_id=%d", branch), owner, nil)
	wantStatus(t, dash, http.StatusOK)
	wantNum(t, "dashboard net sales", dash.field(t, "net_sales_cents"), 1000)

	staff := call(t, http.MethodGet, "/reports/by-cashier?"+at, owner, nil)
	wantStatus(t, staff, http.StatusOK)
	cashiers := staff.rows(t)
	if len(cashiers) != 1 || uint64(num(t, cashiers[0]["id"])) != e.owner.ID {
		t.Fatalf("by-cashier: %s", staff)
	}
	wantNum(t, "cashier sales", cashiers[0]["sales_cents"], 1000)

	pl := call(t, http.MethodGet, "/reports/profit-loss?"+at, owner, nil)
	wantStatus(t, pl, http.StatusOK)
	for field, want := range map[string]int64{
		"net_sales_cents":      2200,
		"pos_sales_cents":      1000,
		"invoice_sales_cents":  1200,
		"cogs_cents":           1000,
		"gross_profit_cents":   1200,
		"total_expenses_cents": 500,
		"net_profit_cents":     700,
	} {
		wantNum(t, field, pl.field(t, field), want)
	}
	tax, ok := pl.field(t, "tax").(map[string]interface{})
	if !ok {
		t.Fatalf("no tax block: %s", pl)
	}
	for field, want := range map[string]int64{
		"collected_cents": 220, "pos_cents": 100, "invoice_cents": 120,
		"paid_cents": 120, "expense_cents": 50, "purchase_cents": 70,
	} {
		wantNum(t, "tax."+field, tax[field], want)
	}
	days, _ := pl.field(t, "net_profit_by_day").([]interface{})
	if len(days) != 1 {
		t.Fatalf("net_profit_by_day: %v", days)
	}
	wantNum(t, "today's net profit", days[0].(map[string]interface{})["net_profit_cents"], 700)

	// The dashboard's staff list (cashiers and managers): a cashier's sales
	// today, without the tax.
	cashierUser := newUser(t, cashierRole(t, env.companyA), branch)
	cashier := login(t, cashierUser).Access
	cashierTill := newTill(t, branch)
	wantStatus(t, call(t, http.MethodPost, "/shifts/open", cashier, gin.H{"device_key": cashierTill, "opening_usd_cents": 0, "opening_khr_riel": 0}), http.StatusCreated)
	cashierSale := saleBody(cashierTill, unique("key"), e.product)
	cashierSale["tax_rate_id"] = vat
	wantStatus(t, call(t, http.MethodPost, "/sales", cashier, cashierSale), http.StatusCreated)
	team := call(t, http.MethodGet, fmt.Sprintf("/reports/staff?branch_id=%d", branch), owner, nil)
	wantStatus(t, team, http.StatusOK)
	var body struct {
		Staff []struct {
			ID              uint64 `json:"id"`
			SalesTodayCents int64  `json:"sales_today_cents"`
		} `json:"staff"`
	}
	if err := json.Unmarshal(team.Data, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Staff) != 1 || body.Staff[0].ID != cashierUser.ID {
		t.Fatalf("staff: %s", team)
	}
	wantNum(t, "staff sales today", float64(body.Staff[0].SalesTodayCents), 1000)
}
