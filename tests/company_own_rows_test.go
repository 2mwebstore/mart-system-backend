package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// What a company makes, changes or imports is its own, for company A as for
// company B. (company_test.go has what a company can't reach of another's.)

// side is one company, as its Owner sees it.
type side struct {
	name           string
	token          string
	company, other uint64
	otherToken     string
}

func bothSides(t *testing.T, e *testEnv) []side {
	t.Helper()
	a, b := login(t, e.owner).Access, login(t, e.b.owner).Access
	return []side{
		{name: "company A", token: a, company: e.companyA, other: e.b.id, otherToken: b},
		{name: "company B", token: b, company: e.b.id, other: e.companyA, otherToken: a},
	}
}

// ownRow wants a 201 whose row is the side's company's, and gives its id.
func (s side) ownRow(t *testing.T, table string, r response) uint64 {
	t.Helper()
	wantStatus(t, r, http.StatusCreated)
	rowID := r.id(t)
	if rowID == 0 {
		t.Fatalf("%s: no id in %s", table, r)
	}
	if got := dbInt(t, "SELECT company_id FROM "+table+" WHERE id = ?", rowID); got != int64(s.company) {
		t.Fatalf("%s %d went to company %d, want %d", table, rowID, got, s.company)
	}
	return rowID
}

// stockOnlyAtOwnBranches wants a product's stock rows at every branch of the
// side's company and none of another's.
func (s side) stockOnlyAtOwnBranches(t *testing.T, product uint64) {
	t.Helper()
	if n := dbInt(t, `SELECT COUNT(*) FROM branch_stock bs JOIN branches b ON b.id = bs.branch_id
		WHERE bs.product_id = ? AND b.company_id <> ?`, product, s.company); n != 0 {
		t.Fatalf("product %d has stock rows at %d branches of another company", product, n)
	}
	if n := dbInt(t, `SELECT COUNT(*) FROM branches b WHERE b.company_id = ? AND b.deleted_at IS NULL
		AND NOT EXISTS (SELECT 1 FROM branch_stock bs WHERE bs.branch_id = b.id AND bs.product_id = ?)`, s.company, product); n != 0 {
		t.Fatalf("product %d has no stock row at %d of its company's branches", product, n)
	}
}

// importCSV posts a CSV file the way the import pages do.
func importCSV(t *testing.T, path, token, csv string) response {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "import.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(csv)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return call(t, http.MethodPost, path, token, buf.Bytes(), "Content-Type", w.FormDataContentType())
}

// keepSettings puts every company's settings and exchange rate back as they
// were when the test ends.
func keepSettings(t *testing.T) {
	t.Helper()
	var rows []struct {
		CompanyID  uint64
		Key, Value string
	}
	if err := env.db.Raw("SELECT company_id, `key`, value FROM company_settings").Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	lastRate := dbInt(t, "SELECT COALESCE(MAX(id), 0) FROM exchange_rates")
	t.Cleanup(func() {
		env.db.Exec("DELETE FROM exchange_rates WHERE id > ?", lastRate)
		env.db.Exec("DELETE FROM company_settings")
		for _, r := range rows {
			env.db.Exec("INSERT INTO company_settings (company_id, `key`, value) VALUES (?, ?, ?)", r.CompanyID, r.Key, r.Value)
		}
	})
}

// dbText is dbInt for a text value.
func dbText(t *testing.T, query string, args ...interface{}) string {
	t.Helper()
	var s string
	if err := env.db.Raw(query, args...).Scan(&s).Error; err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEachCompanyWorksOnItsOwnRows(t *testing.T) {
	e := setup(t)
	for _, s := range bothSides(t, e) {
		t.Run(s.name, func(t *testing.T) {
			t.Run("catalog", func(t *testing.T) {
				category := s.ownRow(t, "categories", call(t, http.MethodPost, "/categories", s.token, gin.H{"name_en": unique("Cat")}))
				wantStatus(t, call(t, http.MethodPut, "/categories/"+id(category), s.token, gin.H{"name_en": unique("Cat")}), http.StatusOK)
				supplier := s.ownRow(t, "suppliers", call(t, http.MethodPost, "/suppliers", s.token, gin.H{"name": unique("Sup")}))
				wantStatus(t, call(t, http.MethodPut, "/suppliers/"+id(supplier), s.token, gin.H{"name": unique("Sup")}), http.StatusOK)

				name := unique("Prod")
				created := call(t, http.MethodPost, "/products", s.token, gin.H{"name_en": name, "category_id": category, "supplier_id": supplier, "price_cents": 100, "active": true})
				product := s.ownRow(t, "products", created)
				if created.field(t, "name_en") != name {
					t.Fatalf("the created product came back as %s", created)
				}
				s.stockOnlyAtOwnBranches(t, product)
				name = unique("Prod")
				updated := call(t, http.MethodPut, "/products/"+id(product), s.token, gin.H{"name_en": name, "category_id": category, "supplier_id": supplier, "price_cents": 150, "active": true})
				wantStatus(t, updated, http.StatusOK)
				if updated.field(t, "name_en") != name {
					t.Fatalf("the updated product came back as %s", updated)
				}
				// Saving fills in any missing stock rows: still only its own.
				s.stockOnlyAtOwnBranches(t, product)
				variant := s.ownRow(t, "products", call(t, http.MethodPost, "/products", s.token, gin.H{"parent_product_id": product, "variant_name": "Large", "price_cents": 200, "active": true}))

				wantStatus(t, call(t, http.MethodDelete, "/products/"+id(variant), s.token, nil), http.StatusOK)
				wantStatus(t, call(t, http.MethodDelete, "/products/"+id(product), s.token, nil), http.StatusOK)
				spare := s.ownRow(t, "categories", call(t, http.MethodPost, "/categories", s.token, gin.H{"name_en": unique("Cat")}))
				wantStatus(t, call(t, http.MethodDelete, "/categories/"+id(spare), s.token, nil), http.StatusOK)
				spareSupplier := s.ownRow(t, "suppliers", call(t, http.MethodPost, "/suppliers", s.token, gin.H{"name": unique("Sup")}))
				wantStatus(t, call(t, http.MethodDelete, "/suppliers/"+id(spareSupplier), s.token, nil), http.StatusOK)
			})
			t.Run("customers", func(t *testing.T) {
				name := unique("Cust")
				created := call(t, http.MethodPost, "/customers", s.token, gin.H{"name": name, "phone": fmt.Sprintf("07%07d", uniqueSeq.Add(1)), "tier": "MEMBER"})
				customer := s.ownRow(t, "customers", created)
				if created.field(t, "name") != name {
					t.Fatalf("the created customer came back as %s", created)
				}
				name = unique("Cust")
				updated := call(t, http.MethodPut, "/customers/"+id(customer), s.token, gin.H{"name": name, "phone": fmt.Sprintf("07%07d", uniqueSeq.Add(1)), "tier": "MEMBER"})
				wantStatus(t, updated, http.StatusOK)
				if updated.field(t, "name") != name {
					t.Fatalf("the updated customer came back as %s", updated)
				}
				wantStatus(t, call(t, http.MethodDelete, "/customers/"+id(customer), s.token, nil), http.StatusOK)
			})
			t.Run("payment methods and roles", func(t *testing.T) {
				method := s.ownRow(t, "payment_methods", call(t, http.MethodPost, "/payment-methods", s.token, gin.H{"name": unique("Pay"), "type": "OTHER", "enabled": true}))
				wantStatus(t, call(t, http.MethodPut, "/payment-methods/"+id(method), s.token, gin.H{"name": unique("Pay"), "type": "OTHER", "enabled": false}), http.StatusOK)
				wantStatus(t, call(t, http.MethodDelete, "/payment-methods/"+id(method), s.token, nil), http.StatusOK)
				role := s.ownRow(t, "roles", call(t, http.MethodPost, "/roles", s.token, gin.H{"name": unique("Role"), "permissions": []string{"pos.sell"}}))
				renamed := unique("Role")
				updated := call(t, http.MethodPut, "/roles/"+id(role), s.token, gin.H{"name": renamed, "permissions": []string{"pos.sell"}})
				wantStatus(t, updated, http.StatusOK)
				if updated.field(t, "name") != renamed {
					t.Fatalf("the updated role came back as %s", updated)
				}
				wantStatus(t, call(t, http.MethodDelete, "/roles/"+id(role), s.token, nil), http.StatusOK)
			})
			t.Run("imports", func(t *testing.T) {
				// A name and a SKU the other company has already don't stop
				// this one from having them.
				shared := unique("Shared cat")
				wantStatus(t, call(t, http.MethodPost, "/categories", s.otherToken, gin.H{"name_en": shared}), http.StatusCreated)
				r := importCSV(t, "/categories/import", s.token, "name_en\n"+shared+"\n")
				wantStatus(t, r, http.StatusOK)
				if r.field(t, "created") != float64(1) {
					t.Fatalf("category import: %s", r)
				}
				if n := dbInt(t, "SELECT COUNT(*) FROM categories WHERE company_id = ? AND name_en = ?", s.company, shared); n != 1 {
					t.Fatalf("the imported category isn't company %d's", s.company)
				}

				// A category name both have, the other company's made last, and
				// a product of theirs whose SKU this company doesn't have.
				later := unique("Later cat")
				category := s.ownRow(t, "categories", call(t, http.MethodPost, "/categories", s.token, gin.H{"name_en": later}))
				wantStatus(t, call(t, http.MethodPost, "/categories", s.otherToken, gin.H{"name_en": later}), http.StatusCreated)
				theirs := call(t, http.MethodPost, "/products", s.otherToken, gin.H{"name_en": unique("Theirs"), "price_cents": 100, "active": true})
				wantStatus(t, theirs, http.StatusCreated)
				// SKUs are numbered per company, so both could have theirs:
				// give it one this company can't have.
				sku := fmt.Sprintf("THR-%d", 900000+uniqueSeq.Add(1))
				e.db.Exec("UPDATE products SET sku = ? WHERE id = ?", sku, theirs.id(t))
				if dbInt(t, "SELECT COUNT(*) FROM products WHERE company_id = ? AND sku = ?", s.company, sku) != 0 {
					t.Fatalf("both companies have SKU %s already", sku)
				}
				ours := unique("Ours")
				r = importCSV(t, "/products/import", s.token, fmt.Sprintf("name_en,category,price,parent_sku,variant_name\n%s,%s,1.50,,\n,,2.00,%s,Large\n", ours, later, sku))
				wantStatus(t, r, http.StatusOK)
				if r.field(t, "created") != float64(1) {
					t.Fatalf("product import: want the product and not the variant of theirs, got %s", r)
				}
				if n := dbInt(t, "SELECT COUNT(*) FROM products WHERE parent_product_id = ?", theirs.id(t)); n != 0 {
					t.Fatal("the import made a variant of another company's product")
				}
				product := uint64(dbInt(t, "SELECT id FROM products WHERE company_id = ? AND name_en = ?", s.company, ours))
				if product == 0 {
					t.Fatalf("the imported product isn't company %d's", s.company)
				}
				if got := dbInt(t, "SELECT category_id FROM products WHERE id = ?", product); got != int64(category) {
					t.Fatalf("the imported product is in category %d, want company %d's %d", got, s.company, category)
				}
				s.stockOnlyAtOwnBranches(t, product)
			})
		})
	}
}

func TestEachCompanysDocumentsUseItsOwnNumbersAndRate(t *testing.T) {
	e := setup(t)
	type own struct{ branch, customer, product, supplier uint64 }
	rows := map[uint64]own{
		e.companyA: {e.branchTK, e.customer, e.product, e.supplier},
		e.b.id:     {e.b.branch, e.b.customer, e.b.product, e.b.supplier},
	}
	// Different rates, so a document that takes the other's shows.
	keepSettings(t)
	sides := bothSides(t, e)
	for i, s := range sides {
		wantStatus(t, call(t, http.MethodPost, "/exchange-rate", s.token, gin.H{"rate": 4300 + 100*i}), http.StatusOK)
	}
	for _, s := range sides {
		t.Run(s.name, func(t *testing.T) {
			o := rows[s.company]
			rate := call(t, http.MethodGet, "/exchange-rate", s.token, nil)
			wantStatus(t, rate, http.StatusOK)
			ownRate := rate.field(t, "rate")

			// numbered makes one document and wants only this company's
			// sequence for it to move, by one.
			numbered := func(docType string, create func() response) response {
				t.Helper()
				next := func(company uint64) int64 {
					return dbInt(t, "SELECT next_number FROM number_sequences WHERE company_id = ? AND doc_type = ?", company, docType)
				}
				mine, theirs := next(s.company), next(s.other)
				r := create()
				wantStatus(t, r, http.StatusCreated)
				if got := next(s.company); got != mine+1 {
					t.Fatalf("%s: own sequence went from %d to %d", docType, mine, got)
				}
				if got := next(s.other); got != theirs {
					t.Fatalf("%s: the other company's sequence moved from %d to %d", docType, theirs, got)
				}
				return r
			}
			numbered("EXPENSE", func() response {
				return call(t, http.MethodPost, "/expenses", s.token, gin.H{"branch_id": o.branch, "category": "OTHER", "amount_cents": 100, "expense_date": today()})
			})
			numbered("PURCHASE_ORDER", func() response {
				return call(t, http.MethodPost, "/purchase-orders", s.token, gin.H{"branch_id": o.branch, "supplier_id": o.supplier,
					"items": []gin.H{{"product_id": o.product, "qty": 1, "unit_cost_cents": 100}}})
			})
			invoice := numbered("INVOICE", func() response {
				return call(t, http.MethodPost, "/invoices", s.token, gin.H{"branch_id": o.branch, "customer_id": o.customer, "invoice_date": today(),
					"items": []gin.H{{"product_id": o.product, "qty": 1, "unit_price_cents": 100}}})
			})
			if got := invoice.field(t, "exchange_rate"); got != ownRate {
				t.Fatalf("the invoice took rate %v, want the company's %v", got, ownRate)
			}

			till := newTill(t, o.branch)
			shift := call(t, http.MethodPost, "/shifts/open", s.token, gin.H{"device_key": till, "opening_usd_cents": 0, "opening_khr_riel": 0})
			wantStatus(t, shift, http.StatusCreated)
			t.Cleanup(func() {
				call(t, http.MethodPost, "/shifts/"+id(shift.id(t))+"/close", s.token, gin.H{"counted_usd_cents": 0, "counted_khr_riel": 0})
			})
			if got := dbInt(t, "SELECT exchange_rate FROM shifts WHERE id = ?", shift.id(t)); float64(got) != ownRate {
				t.Fatalf("the shift took rate %d, want the company's %v", got, ownRate)
			}
			sale := numbered("SALE", func() response {
				return call(t, http.MethodPost, "/sales", s.token, saleBody(till, unique("key"), o.product))
			})
			if got := sale.field(t, "exchange_rate"); got != ownRate {
				t.Fatalf("the sale took rate %v, want the company's %v", got, ownRate)
			}
		})
	}
}

func TestEachCompanysStaffAndRolesStayInIt(t *testing.T) {
	e := setup(t)
	branchOf := map[uint64]uint64{e.companyA: e.branchTK, e.b.id: e.b.branch}
	managerOf := map[uint64]uint64{e.companyA: e.roleManager, e.b.id: e.b.roleManager}
	for _, s := range bothSides(t, e) {
		t.Run(s.name, func(t *testing.T) {
			t.Run("a staff account is the company's, from creation to deletion", func(t *testing.T) {
				name := unique("staff")
				created := call(t, http.MethodPost, "/users", s.token, updateBody(name, managerOf[s.company], true, []uint64{branchOf[s.company]}, "password", testPassword))
				user := s.ownRow(t, "users", created)
				if created.field(t, "username") != name {
					t.Fatalf("the created user came back as %s", created)
				}
				wantStatus(t, call(t, http.MethodPost, "/users/"+id(user)+"/reset-pin", s.token, nil), http.StatusOK)
				wantStatus(t, call(t, http.MethodDelete, "/users/"+id(user), s.token, nil), http.StatusOK)
				if n := dbInt(t, "SELECT COUNT(*) FROM users WHERE id = ?", user); n != 0 {
					t.Fatalf("user %d is still there", user)
				}
			})
			t.Run("the staff report lists the company's roles", func(t *testing.T) {
				r := call(t, http.MethodGet, "/reports/staff?branch_id="+id(branchOf[s.company]), s.token, nil)
				wantStatus(t, r, http.StatusOK)
				var out struct {
					Roles []struct {
						ID uint64 `json:"id"`
					} `json:"roles"`
				}
				if err := json.Unmarshal(r.Data, &out); err != nil || len(out.Roles) == 0 {
					t.Fatalf("staff report: %s", r)
				}
				for _, role := range out.Roles {
					if dbInt(t, "SELECT COALESCE(company_id, 0) FROM roles WHERE id = ?", role.ID) != int64(s.company) {
						t.Fatalf("the staff report lists role %d of another company", role.ID)
					}
				}
			})
			t.Run("the user list names only the company's branches", func(t *testing.T) {
				r := call(t, http.MethodGet, "/users", s.token, nil)
				wantStatus(t, r, http.StatusOK)
				var users []struct {
					Username  string   `json:"username"`
					BranchIDs []uint64 `json:"branch_ids"`
				}
				if err := json.Unmarshal(r.Data, &users); err != nil || len(users) == 0 {
					t.Fatalf("users: %s", r)
				}
				for _, u := range users {
					for _, b := range u.BranchIDs {
						if dbInt(t, "SELECT company_id FROM branches WHERE id = ?", b) != int64(s.company) {
							t.Fatalf("%s is listed at branch %d of another company", u.Username, b)
						}
					}
				}
			})
			t.Run("a branch role is for one of the company's branches", func(t *testing.T) {
				body := func(branch uint64) gin.H {
					return gin.H{"name": unique("Branch role"), "permissions": []string{"pos.sell"}, "branch_id": branch}
				}
				s.ownRow(t, "roles", call(t, http.MethodPost, "/roles", s.token, body(branchOf[s.company])))
				r := call(t, http.MethodPost, "/roles", s.token, body(branchOf[s.other]))
				if r.Status != http.StatusUnprocessableEntity || r.Error == nil || r.Error.Fields["branch_id"] == "" {
					t.Fatalf("a role for another company's branch: want 422 on branch_id, got %s", r)
				}
			})
		})
	}
}

// Each company reads back the settings it saved, whatever the other saved.
func TestEachCompanysSettingsAreItsOwn(t *testing.T) {
	e := setup(t)
	sides := bothSides(t, e)
	get := func(t *testing.T, s side, path, field string) interface{} {
		t.Helper()
		r := call(t, http.MethodGet, path, s.token, nil)
		wantStatus(t, r, http.StatusOK)
		return r.field(t, field)
	}
	// Both save, then both read: a mix-up shows whichever way round it is.
	t.Run("receipt and stock policy", func(t *testing.T) {
		headers := map[uint64]string{}
		for i, s := range sides {
			headers[s.company] = unique("Header")
			r := call(t, http.MethodPut, "/settings", s.token, gin.H{"receipt_header": headers[s.company], "receipt_footer": "", "loyalty_points_per_usd": 1})
			wantStatus(t, r, http.StatusOK)
			if r.field(t, "receipt_header") != headers[s.company] {
				t.Fatalf("%s: saving answered %s", s.name, r)
			}
			r = call(t, http.MethodPut, "/settings/stock-policy", s.token, gin.H{"allow_out_of_stock_sale": i == 0})
			wantStatus(t, r, http.StatusOK)
		}
		t.Cleanup(func() {
			for _, s := range sides {
				call(t, http.MethodPut, "/settings/stock-policy", s.token, gin.H{"allow_out_of_stock_sale": false})
			}
		})
		for i, s := range sides {
			if got := get(t, s, "/settings", "receipt_header"); got != headers[s.company] {
				t.Fatalf("%s reads receipt header %v", s.name, got)
			}
			if got := get(t, s, "/settings", "allow_out_of_stock_sale"); got != (i == 0) {
				t.Fatalf("%s reads stock policy %v", s.name, got)
			}
		}
	})
	t.Run("the receipt header follows the company's own name", func(t *testing.T) {
		keepSettings(t)
		// Each company is named, with that name as its receipt header; then
		// each renames itself, and the header follows.
		names := map[uint64]string{}
		for _, s := range sides {
			names[s.company] = unique("Name")
			wantStatus(t, call(t, http.MethodPut, "/settings/branding", s.token, gin.H{"company_name": names[s.company]}), http.StatusOK)
			wantStatus(t, call(t, http.MethodPut, "/settings", s.token, gin.H{"receipt_header": names[s.company], "receipt_footer": "", "loyalty_points_per_usd": 1}), http.StatusOK)
		}
		for _, s := range sides {
			renamed := unique("Name")
			wantStatus(t, call(t, http.MethodPut, "/settings/branding", s.token, gin.H{"company_name": renamed}), http.StatusOK)
			if got := get(t, s, "/settings", "receipt_header"); got != renamed {
				t.Fatalf("%s renamed itself %s; its receipt header is %v", s.name, renamed, got)
			}
		}
	})
	t.Run("alerts", func(t *testing.T) {
		bots := map[uint64]string{}
		for _, s := range sides {
			bots[s.company] = unique("bot")
			wantStatus(t, call(t, http.MethodPut, "/settings/notifications", s.token, gin.H{"enabled": false, "bot_token": bots[s.company], "chat_id": "1", "alert_types": []string{"low_stock"}}), http.StatusOK)
		}
		for _, s := range sides {
			if got := get(t, s, "/settings/notifications", "bot_token"); got != bots[s.company] {
				t.Fatalf("%s reads bot token %v", s.name, got)
			}
		}
	})
	t.Run("exchange rate", func(t *testing.T) {
		rates := map[uint64]float64{}
		for i, s := range sides {
			before := get(t, s, "/exchange-rate", "rate").(float64)
			rates[s.company] = float64(4300 + 100*i)
			wantStatus(t, call(t, http.MethodPost, "/exchange-rate", s.token, gin.H{"rate": rates[s.company]}), http.StatusOK)
			token := s.token
			t.Cleanup(func() { call(t, http.MethodPost, "/exchange-rate", token, gin.H{"rate": before}) })
		}
		for _, s := range sides {
			if got := get(t, s, "/exchange-rate", "rate"); got != rates[s.company] {
				t.Fatalf("%s reads rate %v, want %v", s.name, got, rates[s.company])
			}
			for _, report := range []string{"/reports/dashboard", "/reports/profit-loss"} {
				if got := get(t, s, report, "exchange_rate"); got != rates[s.company] {
					t.Fatalf("%s's %s uses rate %v, want %v", s.name, report, got, rates[s.company])
				}
			}
		}
	})
	t.Run("numbering", func(t *testing.T) {
		prefixes := map[uint64]string{}
		for i, s := range sides {
			prefixes[s.company] = fmt.Sprintf("PX%d", i)
			r := call(t, http.MethodPut, "/settings/numbering", s.token, gin.H{"doc_type": "PURCHASE_ORDER", "prefix": prefixes[s.company], "next_number": 50 + i})
			wantStatus(t, r, http.StatusOK)
			if !bytes.Contains([]byte(r.Raw), []byte(prefixes[s.company])) {
				t.Fatalf("%s: saving answered %s", s.name, r)
			}
			company := s.company
			t.Cleanup(func() {
				e.db.Exec("UPDATE number_sequences SET prefix = 'PO' WHERE company_id = ? AND doc_type = 'PURCHASE_ORDER'", company)
			})
		}
		for _, s := range sides {
			r := call(t, http.MethodGet, "/settings/numbering", s.token, nil)
			wantStatus(t, r, http.StatusOK)
			var rows []struct {
				DocType string `json:"doc_type"`
				Prefix  string `json:"prefix"`
			}
			if err := json.Unmarshal(r.Data, &rows); err != nil {
				t.Fatalf("numbering: %s", r)
			}
			for _, row := range rows {
				if row.DocType == "PURCHASE_ORDER" && row.Prefix != prefixes[s.company] {
					t.Fatalf("%s reads prefix %s, want %s", s.name, row.Prefix, prefixes[s.company])
				}
			}
			if len(rows) != 4 {
				t.Fatalf("%s reads %d sequences, want its own 4", s.name, len(rows))
			}
		}
	})
	t.Run("activity log retention", func(t *testing.T) {
		// The super admin sets each company's; the company reads its own.
		for i, s := range sides {
			wantStatus(t, putCompany(t, s.company, gin.H{"retention_months": i + 1}), http.StatusOK)
			company := s.company
			t.Cleanup(func() { putCompany(t, company, gin.H{"retention_months": 3}) })
		}
		for i, s := range sides {
			if got := get(t, s, "/settings/activity-log-retention", "retention_months"); got != float64(i+1) {
				t.Fatalf("%s reads retention %v", s.name, got)
			}
		}
		super := login(t, e.superAdmin).Access
		// Clearing now purges the company's own old entries and no one else's.
		old := func(company uint64) uint64 {
			row := map[string]interface{}{"company_id": company, "user_id": e.owner.ID, "module": "test", "action": "old.entry", "entity_type": "test", "created_at": time.Now().AddDate(-2, 0, 0)}
			if err := e.db.Table("activity_logs").Create(row).Error; err != nil {
				t.Fatal(err)
			}
			return uint64(dbInt(t, "SELECT MAX(id) FROM activity_logs WHERE action = 'old.entry' AND company_id = ?", company))
		}
		for _, s := range sides {
			mine, theirs := old(s.company), old(s.other)
			wantStatus(t, call(t, http.MethodPost, "/platform/companies/"+id(s.company)+"/activity-log/clear", super, nil), http.StatusOK)
			if dbInt(t, "SELECT COUNT(*) FROM activity_logs WHERE id = ?", mine) != 0 {
				t.Fatalf("%s's old entry is still there", s.name)
			}
			if dbInt(t, "SELECT COUNT(*) FROM activity_logs WHERE id = ?", theirs) != 1 {
				t.Fatalf("%s cleared the other company's log", s.name)
			}
			e.db.Exec("DELETE FROM activity_logs WHERE id = ?", theirs)
		}
	})
}

// Company B can't change or remove company A's setup rows.
func TestACompanyCantRemoveAnotherCompanysSetup(t *testing.T) {
	e := setup(t)
	a, b := login(t, e.owner).Access, login(t, e.b.owner).Access
	gone := func(t *testing.T, r response, table string, rowID uint64) {
		t.Helper()
		if r.Status < 400 {
			t.Fatalf("company B changed company A's %s: %s", table, r)
		}
		alive := "SELECT COUNT(*) FROM " + table + " WHERE id = ?"
		if dbInt(t, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = 'deleted_at'", table) == 1 {
			alive += " AND deleted_at IS NULL"
		}
		if n := dbInt(t, alive, rowID); n != 1 {
			t.Fatalf("company A's %s %d is gone", table, rowID)
		}
	}
	branch := call(t, http.MethodPost, "/branches", a, gin.H{"code": fmt.Sprintf("SP%d", uniqueSeq.Add(1)), "name": "Spare", "active": true})
	wantStatus(t, branch, http.StatusCreated)
	gone(t, call(t, http.MethodDelete, "/branches/"+id(branch.id(t)), b, nil), "branches", branch.id(t))

	till := deviceID(t, e.companyA, e.tillTK2)
	gone(t, call(t, http.MethodPut, "/devices/"+id(till), b, gin.H{"branch_id": e.b.branch, "name": "Moved", "device_key": unique("K"), "active": true}), "devices", till)
	if n := dbInt(t, "SELECT COUNT(*) FROM devices WHERE id = ? AND branch_id = ?", till, e.branchTK); n != 1 {
		t.Fatal("company A's till moved")
	}
	gone(t, call(t, http.MethodDelete, "/devices/"+id(till), b, nil), "devices", till)

	method := call(t, http.MethodPost, "/payment-methods", a, gin.H{"name": unique("Pay"), "type": "OTHER", "enabled": true})
	wantStatus(t, method, http.StatusCreated)
	r := call(t, http.MethodPut, "/payment-methods/"+id(method.id(t)), b, gin.H{"name": "Taken", "type": "OTHER", "enabled": false})
	if r.Status < 400 || dbInt(t, "SELECT COUNT(*) FROM payment_methods WHERE id = ? AND name = 'Taken'", method.id(t)) != 0 {
		t.Fatalf("company B changed company A's payment method: %s", r)
	}
	r = call(t, http.MethodDelete, "/payment-methods/"+id(method.id(t)), b, nil)
	if r.Status < 400 || dbInt(t, "SELECT COUNT(*) FROM payment_methods WHERE id = ?", method.id(t)) != 1 {
		t.Fatalf("company B deleted company A's payment method: %s", r)
	}

	category := companyACategory(t)
	gone(t, call(t, http.MethodDelete, "/categories/"+id(category), b, nil), "categories", category)
	// A supplier with no orders, which its own company could delete.
	supplier := call(t, http.MethodPost, "/suppliers", a, gin.H{"name": unique("Sup")})
	wantStatus(t, supplier, http.StatusCreated)
	gone(t, call(t, http.MethodDelete, "/suppliers/"+id(supplier.id(t)), b, nil), "suppliers", supplier.id(t))
	r = call(t, http.MethodPost, "/products", b, gin.H{"parent_product_id": e.product, "variant_name": "Theirs", "price_cents": 100, "active": true})
	if r.Status < 400 {
		t.Fatalf("company B made a variant of company A's product: %s", r)
	}
}

// The session says which company it is, and whether it's a super admin's.
func TestTheSessionSaysWhoseItIs(t *testing.T) {
	e := setup(t)
	for _, s := range []struct {
		who   namedUser
		code  string
		super bool
	}{{e.owner, testCompanyCode, false}, {e.b.owner, e.b.code, false}, {e.superAdmin, "", true}} {
		me := call(t, http.MethodGet, "/auth/me", login(t, s.who).Access, nil)
		wantStatus(t, me, http.StatusOK)
		if sessionCompany(t, me) != s.code || me.field(t, "is_super_admin") != s.super {
			t.Fatalf("%s's session: %s", s.who.Username, me)
		}
	}
}

// An Owner is on every branch of their company and none of another's, and
// branch rows that cross companies (saving a user refuses them) are ignored.
func TestBranchAccessStaysInsideTheCompany(t *testing.T) {
	e := setup(t)
	branchesOf := func(t *testing.T, token string) []struct {
		ID     uint64 `json:"id"`
		RoleID uint64 `json:"role_id"`
	} {
		t.Helper()
		me := call(t, http.MethodGet, "/auth/me", token, nil)
		wantStatus(t, me, http.StatusOK)
		var out struct {
			Branches []struct {
				ID     uint64 `json:"id"`
				RoleID uint64 `json:"role_id"`
			} `json:"branches"`
		}
		if err := json.Unmarshal(me.Data, &out); err != nil {
			t.Fatalf("me: %s", me)
		}
		return out.Branches
	}
	otherBranch := map[uint64]uint64{e.companyA: e.b.branch, e.b.id: e.branchTK}

	t.Run("an Owner's branches", func(t *testing.T) {
		for _, s := range bothSides(t, e) {
			branches := branchesOf(t, s.token)
			if want := dbInt(t, "SELECT COUNT(*) FROM branches WHERE company_id = ? AND active = 1 AND deleted_at IS NULL", s.company); int64(len(branches)) != want {
				t.Fatalf("%s's Owner has %d branches, want the company's %d", s.name, len(branches), want)
			}
			for _, b := range branches {
				if dbInt(t, "SELECT company_id FROM branches WHERE id = ?", b.ID) != int64(s.company) {
					t.Fatalf("%s's Owner has branch %d of another company", s.name, b.ID)
				}
			}
			wantStatus(t, call(t, http.MethodGet, "/sales?branch_id="+id(otherBranch[s.company]), s.token, nil), http.StatusForbidden)
		}
	})
	t.Run("a staff member's row at another company's branch", func(t *testing.T) {
		e.db.Exec("INSERT INTO user_branches (user_id, branch_id) VALUES (?, ?)", e.b.manager.ID, e.branchTK)
		t.Cleanup(func() {
			e.db.Exec("DELETE FROM user_branches WHERE user_id = ? AND branch_id = ?", e.b.manager.ID, e.branchTK)
		})
		token := login(t, e.b.manager).Access
		for _, b := range branchesOf(t, token) {
			if b.ID == e.branchTK {
				t.Fatal("company B's manager is at company A's branch")
			}
		}
		wantStatus(t, call(t, http.MethodGet, "/sales?branch_id="+id(e.branchTK), token, nil), http.StatusForbidden)
	})
	t.Run("a role at a branch that's another company's", func(t *testing.T) {
		e.db.Exec("UPDATE user_branches SET role_id = ? WHERE user_id = ? AND branch_id = ?", e.roleCashier, e.b.manager.ID, e.b.branch)
		t.Cleanup(func() {
			e.db.Exec("UPDATE user_branches SET role_id = NULL WHERE user_id = ? AND branch_id = ?", e.b.manager.ID, e.b.branch)
		})
		for _, b := range branchesOf(t, login(t, e.b.manager).Access) {
			if b.RoleID == e.roleCashier {
				t.Fatal("company B's manager holds company A's Cashier role")
			}
		}
	})
}

// A settings change goes into the company's activity log with the company's
// own value as "before", never the other company's.
func TestEachCompanysLogRecordsItsOwnSettings(t *testing.T) {
	e := setup(t)
	keepSettings(t)
	sides := bothSides(t, e)
	before := func(t *testing.T, s side, entityType, path string) string {
		t.Helper()
		return dbText(t, `SELECT COALESCE(JSON_UNQUOTE(JSON_EXTRACT(before_json, ?)), '') FROM activity_logs
			WHERE company_id = ? AND entity_type = ? ORDER BY id DESC LIMIT 1`, path, s.company, entityType)
	}
	sequence := dbInt(t, "SELECT MAX(next_number) FROM number_sequences WHERE doc_type = 'PURCHASE_ORDER'") + 1000
	prefixOf := func(s side) string {
		return dbText(t, "SELECT prefix FROM number_sequences WHERE company_id = ? AND doc_type = 'PURCHASE_ORDER'", s.company)
	}
	// Each company saves a value of its own (company B's differs from A's,
	// and from the common one); then both save the common one, in turn.
	for _, c := range []struct {
		name, method, path, entityType, field string
		own                                   func(s side, i int) (gin.H, string)
		common                                func(s side) gin.H
	}{
		{"receipt", http.MethodPut, "/settings", "settings", "$.receipt_header",
			func(s side, i int) (gin.H, string) {
				h := unique("Header")
				return gin.H{"receipt_header": h, "receipt_footer": "", "loyalty_points_per_usd": 1}, h
			},
			func(side) gin.H {
				return gin.H{"receipt_header": unique("Header"), "receipt_footer": "", "loyalty_points_per_usd": 1}
			}},
		{"stock policy", http.MethodPut, "/settings/stock-policy", "settings", "$.allow_out_of_stock_sale",
			func(s side, i int) (gin.H, string) {
				return gin.H{"allow_out_of_stock_sale": i == 0}, fmt.Sprint(i == 0)
			},
			func(side) gin.H { return gin.H{"allow_out_of_stock_sale": true} }},
		{"exchange rate", http.MethodPost, "/exchange-rate", "exchange_rate", "$.rate",
			func(s side, i int) (gin.H, string) { return gin.H{"rate": 4300 + 100*i}, fmt.Sprint(4300 + 100*i) },
			func(side) gin.H { return gin.H{"rate": 4250} }},
		{"numbering", http.MethodPut, "/settings/numbering", "number_sequence", "$.next_number",
			// Only ever forward, so no number is handed out twice.
			func(s side, i int) (gin.H, string) {
				n := sequence + int64(i)
				return gin.H{"doc_type": "PURCHASE_ORDER", "prefix": prefixOf(s), "next_number": n}, fmt.Sprint(n)
			},
			func(s side) gin.H {
				return gin.H{"doc_type": "PURCHASE_ORDER", "prefix": prefixOf(s), "next_number": sequence + 10}
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			want := map[uint64]string{}
			for i, s := range sides {
				body, value := c.own(s, i)
				want[s.company] = value
				wantStatus(t, call(t, c.method, c.path, s.token, body), http.StatusOK)
			}
			for _, s := range sides {
				wantStatus(t, call(t, c.method, c.path, s.token, c.common(s)), http.StatusOK)
				if got := before(t, s, c.entityType, c.field); got != want[s.company] {
					t.Fatalf("%s's log has %s %q before the change, want its own %q", s.name, c.field, got, want[s.company])
				}
			}
		})
	}
}

// A sale, and an edit of it, follow the selling company's loyalty rate and
// stock policy.
func TestEachCompanysSalesUseItsOwnSettings(t *testing.T) {
	e := setup(t)
	keepSettings(t)
	type own struct{ branch, customer, product uint64 }
	rows := map[uint64]own{
		e.companyA: {e.branchTK, e.customer, e.product},
		e.b.id:     {e.b.branch, e.b.customer, e.b.product},
	}
	for _, s := range bothSides(t, e) {
		t.Run(s.name, func(t *testing.T) {
			o := rows[s.company]
			// This company gives 10 points a dollar and sells past zero
			// stock; the other gives none and doesn't.
			for token, on := range map[string]bool{s.token: true, s.otherToken: false} {
				loyalty := 0
				if on {
					loyalty = 10
				}
				wantStatus(t, call(t, http.MethodPut, "/settings", token, gin.H{"receipt_header": "", "receipt_footer": "", "loyalty_points_per_usd": loyalty}), http.StatusOK)
				wantStatus(t, call(t, http.MethodPut, "/settings/stock-policy", token, gin.H{"allow_out_of_stock_sale": on}), http.StatusOK)
			}
			e.db.Exec("UPDATE branch_stock SET qty = 1, reorder_point = 0 WHERE branch_id = ? AND product_id = ?", o.branch, o.product)
			t.Cleanup(func() {
				e.db.Exec("UPDATE branch_stock SET qty = 1000, reorder_point = 0 WHERE branch_id = ? AND product_id = ?", o.branch, o.product)
			})
			till := newTill(t, o.branch)
			shift := call(t, http.MethodPost, "/shifts/open", s.token, gin.H{"device_key": till, "opening_usd_cents": 0, "opening_khr_riel": 0})
			wantStatus(t, shift, http.StatusCreated)
			t.Cleanup(func() {
				call(t, http.MethodPost, "/shifts/"+id(shift.id(t))+"/close", s.token, gin.H{"counted_usd_cents": 0, "counted_khr_riel": 0})
			})
			points := func(sale uint64) int64 {
				return dbInt(t, "SELECT COALESCE(SUM(points_change), 0) FROM loyalty_transactions WHERE sale_id = ?", sale)
			}
			earned := func(r response) int64 {
				t.Helper()
				total, _ := r.field(t, "total_cents").(float64)
				want := int64(math.Floor(total / 100 * 10))
				if want <= 0 {
					t.Fatalf("a total of %v earns no points: %s", total, r)
				}
				return want
			}

			body := saleBody(till, unique("key"), o.product)
			body["customer_id"] = o.customer
			sale := call(t, http.MethodPost, "/sales", s.token, body)
			wantStatus(t, sale, http.StatusCreated)
			if got, want := points(sale.id(t)), earned(sale); got != want {
				t.Fatalf("the sale earned %d points, want %d at the company's rate", got, want)
			}

			// Three when one is left: only this company's policy allows it.
			edit := call(t, http.MethodPut, "/sales/"+id(sale.id(t)), s.token, gin.H{"items": []gin.H{{"product_id": o.product, "qty": 3}}, "discount_cents": 0})
			wantStatus(t, edit, http.StatusOK)
			if got, want := points(sale.id(t)), earned(edit); got != want {
				t.Fatalf("after the edit the sale has %d points, want %d at the company's rate", got, want)
			}
		})
	}
}
