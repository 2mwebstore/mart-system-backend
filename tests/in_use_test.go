package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"com-mart/backend/internal/models"
)

// inUseBody is error.details on a refused delete.
type inUseBody struct {
	Entity string `json:"entity"`
	Name   string `json:"name"`
	NameKm string `json:"name_km"`
	Uses   []struct {
		What  string `json:"what"`
		Count int64  `json:"count"`
	} `json:"uses"`
	Hint string `json:"hint"`
}

// wantInUse checks a delete was refused with 409 and code, and that the
// details name exactly these uses (and this entity and hint).
func wantInUse(t *testing.T, r response, code, entity, hint string, want map[string]int64) inUseBody {
	t.Helper()
	wantDenied(t, r, http.StatusConflict, code)
	var body struct {
		Error struct {
			Details inUseBody `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(r.Raw), &body); err != nil {
		t.Fatal(err)
	}
	d := body.Error.Details
	got := map[string]int64{}
	for _, u := range d.Uses {
		got[u.What] = u.Count
	}
	if d.Entity != entity || d.Hint != hint || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("want %s %s %v, got %s", entity, hint, want, r)
	}
	return d
}

// stockAt gives a product stock at a branch.
func stockAt(t *testing.T, branch, product uint64, qty int64) {
	t.Helper()
	if err := env.db.Create(&models.BranchStock{BranchID: branch, ProductID: product, Qty: qty}).Error; err != nil {
		t.Fatal(err)
	}
}

func del(t *testing.T, token, path string, id uint64) response {
	t.Helper()
	return call(t, http.MethodDelete, fmt.Sprintf("%s/%d", path, id), token, nil)
}

// A record other records point at (a product on a sale, a customer on an
// invoice, a till with shifts…) is never deleted: the delete says what still
// uses it. One nothing uses is deleted as before.
func TestUsedRecordsArentDeleted(t *testing.T) {
	e := setup(t)
	keepSettings(t)
	branch := taxBranch(t)
	till := newTill(t, branch)
	owner := login(t, e.owner).Access
	wantStatus(t, call(t, http.MethodPost, "/shifts/open", owner, gin.H{"device_key": till, "opening_usd_cents": 0, "opening_khr_riel": 0}), http.StatusCreated)
	sell := func(t *testing.T, product uint64, extra gin.H) {
		t.Helper()
		body := saleBody(till, unique("key"), product)
		for k, v := range extra {
			body[k] = v
		}
		wantStatus(t, call(t, http.MethodPost, "/sales", owner, body), http.StatusCreated)
	}

	t.Run("a branch with a sale, and its till", func(t *testing.T) {
		sell(t, e.product, nil)
		wantInUse(t, del(t, owner, "/branches", branch), "BRANCH_IN_USE", "branch", "switch_off",
			map[string]int64{"sales": 1, "shifts": 1, "stock_moves": 1, "stocked_products": 1, "tills": 1})
		tillID := uint64(dbInt(t, "SELECT id FROM devices WHERE device_key = ?", till))
		wantInUse(t, del(t, owner, "/devices", tillID), "TILL_IN_USE", "till", "switch_off", map[string]int64{"sales": 1, "shifts": 1})
	})

	t.Run("a branch with staff, and an empty one", func(t *testing.T) {
		staffed := models.Branch{CompanyID: env.companyA, Name: unique("Staffed"), Code: fmt.Sprintf("ST%d", uniqueSeq.Add(1)), Active: true}
		empty := models.Branch{CompanyID: env.companyA, Name: unique("Empty"), Code: fmt.Sprintf("EM%d", uniqueSeq.Add(1)), Active: true}
		for _, b := range []*models.Branch{&staffed, &empty} {
			if err := env.db.Create(b).Error; err != nil {
				t.Fatal(err)
			}
		}
		newUser(t, cashierRole(t, env.companyA), staffed.ID)
		wantInUse(t, del(t, owner, "/branches", staffed.ID), "BRANCH_IN_USE", "branch", "switch_off", map[string]int64{"staff": 1})
		// A branch role already deleted doesn't keep the branch.
		insert(t, "roles", gin.H{"company_id": env.companyA, "branch_id": empty.ID, "name": unique("Old role"), "deleted_at": time.Now()})
		wantStatus(t, del(t, owner, "/branches", empty.ID), http.StatusOK)
	})

	t.Run("a till someone only signed in at goes, with its sign-ins", func(t *testing.T) {
		signedIn := newTill(t, e.branchTK)
		wantStatus(t, tryPIN(t, signedIn, e.cashierTK.Username, testPIN), http.StatusOK)
		tillID := uint64(dbInt(t, "SELECT id FROM devices WHERE device_key = ?", signedIn))
		if dbInt(t, "SELECT COUNT(*) FROM refresh_tokens WHERE device_id = ?", tillID) == 0 {
			t.Fatal("the PIN sign-in left no session on the till")
		}
		wantStatus(t, del(t, owner, "/devices", tillID), http.StatusOK)
		if n := dbInt(t, "SELECT COUNT(*) FROM refresh_tokens WHERE device_id = ?", tillID); n != 0 {
			t.Fatalf("%d sessions left on a deleted till", n)
		}
	})

	t.Run("a sold product stays, and says so", func(t *testing.T) {
		sold := taxProduct(t)
		stockAt(t, branch, sold, 10)
		sell(t, sold, nil)
		r := del(t, owner, "/products", sold)
		d := wantInUse(t, r, "PRODUCT_IN_USE", "product", "switch_off", map[string]int64{"sales": 1, "stock_moves": 1, "stock": 1})
		want := fmt.Sprintf("%q can't be deleted: it's used in 1 POS sale, 1 stock movement and 1 branch with stock on hand. Switch it off instead.", d.Name)
		if r.Error.Message != want {
			t.Fatalf("message:\n got %q\nwant %q", r.Error.Message, want)
		}
		if dbInt(t, "SELECT COUNT(*) FROM products WHERE id = ? AND deleted_at IS NULL", sold) != 1 {
			t.Fatal("the product was deleted anyway")
		}
	})

	t.Run("a product whose variant was sold stays", func(t *testing.T) {
		parent := taxProduct(t)
		variant := taxProduct(t)
		env.db.Exec("UPDATE products SET parent_product_id = ?, variant_name = 'Large' WHERE id = ?", parent, variant)
		stockAt(t, branch, variant, 10)
		sell(t, variant, nil)
		wantInUse(t, del(t, owner, "/products", parent), "PRODUCT_IN_USE", "product", "switch_off", map[string]int64{"sales": 1, "stock_moves": 1, "stock": 1})
	})

	t.Run("a product only in stock stays; one never used goes", func(t *testing.T) {
		stocked := taxProduct(t)
		stockAt(t, branch, stocked, 5)
		wantInUse(t, del(t, owner, "/products", stocked), "PRODUCT_IN_USE", "product", "switch_off", map[string]int64{"stock": 1})
		wantStatus(t, del(t, owner, "/products", taxProduct(t)), http.StatusOK)
	})

	t.Run("a category with products: move them first", func(t *testing.T) {
		cat := models.Category{CompanyID: env.companyA, NameEn: unique("Drinks"), NameKm: "ភេសជ្ជៈ"}
		if err := env.db.Create(&cat).Error; err != nil {
			t.Fatal(err)
		}
		item := taxProduct(t)
		env.db.Exec("UPDATE products SET category_id = ? WHERE id = ?", cat.ID, item)
		d := wantInUse(t, del(t, owner, "/categories", cat.ID), "CATEGORY_IN_USE", "category", "move_products", map[string]int64{"products": 1})
		if d.NameKm != "ភេសជ្ជៈ" {
			t.Fatalf("no Khmer name: %+v", d)
		}
		if dbInt(t, "SELECT COUNT(*) FROM products WHERE id = ? AND category_id = ?", item, cat.ID) != 1 {
			t.Fatal("the product lost its category")
		}
		// Once its product is gone, the category goes.
		wantStatus(t, del(t, owner, "/products", item), http.StatusOK)
		wantStatus(t, del(t, owner, "/categories", cat.ID), http.StatusOK)
	})

	t.Run("a supplier with purchase orders or products", func(t *testing.T) {
		supplier := func() uint64 {
			s := models.Supplier{CompanyID: env.companyA, Name: unique("Supplier")}
			if err := env.db.Create(&s).Error; err != nil {
				t.Fatal(err)
			}
			return s.ID
		}
		withPO, withProduct, unused := supplier(), supplier(), supplier()
		wantStatus(t, call(t, http.MethodPost, "/purchase-orders", owner, gin.H{
			"branch_id": branch, "supplier_id": withPO, "items": []gin.H{{"product_id": e.product, "qty": 1, "unit_cost_cents": 100}},
		}), http.StatusCreated)
		env.db.Exec("UPDATE products SET supplier_id = ? WHERE id = ?", withProduct, taxProduct(t))
		// A product already deleted doesn't keep its supplier.
		env.db.Exec("UPDATE products SET supplier_id = ?, deleted_at = NOW() WHERE id = ?", unused, taxProduct(t))
		wantInUse(t, del(t, owner, "/suppliers", withPO), "SUPPLIER_IN_USE", "supplier", "", map[string]int64{"purchase_orders": 1})
		wantInUse(t, del(t, owner, "/suppliers", withProduct), "SUPPLIER_IN_USE", "supplier", "", map[string]int64{"products": 1})
		wantStatus(t, del(t, owner, "/suppliers", unused), http.StatusOK)
	})

	t.Run("a customer with a sale or an invoice", func(t *testing.T) {
		customer := func() uint64 {
			c := models.Customer{CompanyID: env.companyA, Name: unique("Customer"), Phone: unique("0")}
			if err := env.db.Create(&c).Error; err != nil {
				t.Fatal(err)
			}
			return c.ID
		}
		bought, invoiced, unused := customer(), customer(), customer()
		sell(t, e.product, gin.H{"customer_id": bought})
		wantStatus(t, call(t, http.MethodPost, "/invoices", owner, gin.H{
			"branch_id": branch, "customer_id": invoiced, "invoice_date": today(),
			"items": []gin.H{{"product_id": e.product, "qty": 1, "unit_price_cents": 100}},
		}), http.StatusCreated)
		wantInUse(t, del(t, owner, "/customers", bought), "CUSTOMER_IN_USE", "customer", "", map[string]int64{"sales": 1})
		wantInUse(t, del(t, owner, "/customers", invoiced), "CUSTOMER_IN_USE", "customer", "", map[string]int64{"invoices": 1})
		wantStatus(t, del(t, owner, "/customers", unused), http.StatusOK)
	})

	t.Run("a dining table that took orders", func(t *testing.T) {
		table := func() uint64 {
			r := call(t, http.MethodPost, "/dining-tables", owner, gin.H{"branch_id": branch, "name": unique("T"), "active": true})
			wantStatus(t, r, http.StatusCreated)
			return r.id(t)
		}
		ordered, unused := table(), table()
		order := models.TableOrder{BranchID: branch, TableID: &ordered, TableLabel: "T", PublicToken: unique("tok"), Status: models.TableOrderStatus("NEW")}
		if err := env.db.Create(&order).Error; err != nil {
			t.Fatal(err)
		}
		wantInUse(t, del(t, owner, "/dining-tables", ordered), "TABLE_IN_USE", "table", "switch_off", map[string]int64{"table_orders": 1})
		wantStatus(t, del(t, owner, "/dining-tables", unused), http.StatusOK)
	})

	t.Run("a tax rate an expense used", func(t *testing.T) {
		rate := newTaxRate(t, owner, unique("GST"), 5)
		wantStatus(t, call(t, http.MethodPost, "/expenses", owner, gin.H{"branch_id": branch, "category": "OTHER", "amount_cents": 100, "expense_date": today(), "tax_rate_id": rate}), http.StatusCreated)
		wantInUse(t, del(t, owner, "/tax-rates", rate), "TAX_RATE_IN_USE", "tax_rate", "switch_off", map[string]int64{"expenses": 1})
	})

	t.Run("a user whose only trace is the activity log", func(t *testing.T) {
		worker := newUser(t, newRole(t, "category.create"), e.branchTK)
		wantStatus(t, call(t, http.MethodPost, "/categories", login(t, worker).Access, gin.H{"name_en": unique("Snacks")}), http.StatusCreated)
		wantInUse(t, del(t, owner, "/users", worker.ID), "USER_IN_USE", "user", "switch_off", map[string]int64{"activity_log": 1})
		wantStatus(t, del(t, owner, "/users", newUser(t, cashierRole(t, env.companyA), e.branchTK).ID), http.StatusOK)
	})

	t.Run("a role someone holds", func(t *testing.T) {
		role := newRole(t, "pos.sell")
		newUser(t, role, e.branchTK)
		wantInUse(t, del(t, owner, "/roles", role), "ROLE_IN_USE", "role", "reassign_users", map[string]int64{"staff": 1})
	})
}

// insert adds one row by hand and removes it when the test ends (rows made
// later go first). For uses no other test makes through the API.
func insert(t *testing.T, table string, cols gin.H) uint64 {
	t.Helper()
	names := make([]string, 0, len(cols))
	for k := range cols {
		names = append(names, k)
	}
	sort.Strings(names)
	args := make([]interface{}, len(names))
	for i, k := range names {
		args[i] = cols[k]
	}
	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (?%s)", table, strings.Join(names, ", "), strings.Repeat(", ?", len(names)-1))
	var id uint64
	err := env.db.Connection(func(tx *gorm.DB) error {
		if err := tx.Exec(query, args...).Error; err != nil {
			return err
		}
		return tx.Raw("SELECT LAST_INSERT_ID()").Scan(&id).Error
	})
	if err != nil {
		t.Fatalf("insert into %s: %v", table, err)
	}
	t.Cleanup(func() { env.db.Exec("DELETE FROM "+table+" WHERE id = ?", id) })
	return id
}

// Every kind of record that points at a branch, product, user, tax rate,
// customer or supplier is counted: one of each is made against the same few
// records, and each delete names them all.
func TestEveryUseIsCounted(t *testing.T) {
	e := setup(t)
	branch, other := taxBranch(t), taxBranch(t)
	owner := login(t, e.owner).Access
	product := taxProduct(t)
	user := newUser(t, cashierRole(t, env.companyA), branch).ID
	rate := newTaxRate(t, owner, unique("VAT"), 10)
	customer := models.Customer{CompanyID: env.companyA, Name: unique("Customer"), Phone: unique("0")}
	supplier := models.Supplier{CompanyID: env.companyA, Name: unique("Supplier")}
	for _, row := range []interface{}{&customer, &supplier} {
		if err := env.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	till := uint64(dbInt(t, "SELECT id FROM devices WHERE device_key = ?", newTill(t, branch)))
	now, day := time.Now(), today()

	stockAt(t, branch, product, 3)
	t.Cleanup(func() {
		env.db.Exec("DELETE FROM branch_stock WHERE branch_id = ? AND product_id = ?", branch, product)
	})
	shift := insert(t, "shifts", gin.H{"branch_id": branch, "device_id": till, "user_id": user, "opened_at": now, "exchange_rate": 4000})
	sale := insert(t, "sales", gin.H{
		"branch_id": branch, "shift_id": shift, "device_id": till, "cashier_id": user, "customer_id": customer.ID, "tax_rate_id": rate,
		"receipt_no": unique("R"), "business_date": day, "subtotal_cents": 1000, "total_cents": 1000, "total_riel": 0,
		"exchange_rate": 4000, "cost_total_cents": 500, "idempotency_key": unique("key"), "sold_at": now,
	})
	insert(t, "sale_items", gin.H{"sale_id": sale, "product_id": product, "name_snapshot": "x", "qty": 1, "unit_price_cents": 1000, "unit_cost_cents": 500, "line_total_cents": 1000})
	// Cash moves and voids count the user who made them and the one who approved them.
	insert(t, "cash_movements", gin.H{"shift_id": shift, "type": "PAY_IN", "user_id": user})
	insert(t, "cash_movements", gin.H{"shift_id": shift, "type": "PAY_OUT", "user_id": e.owner.ID, "approved_by_id": user})
	insert(t, "voids_refunds", gin.H{"sale_id": sale, "type": "VOID", "amount_cents": 1000, "cashier_id": user})
	insert(t, "voids_refunds", gin.H{"sale_id": sale, "type": "REFUND", "amount_cents": 100, "cashier_id": e.owner.ID, "approved_by_id": user})
	invoice := insert(t, "invoices", gin.H{"code": unique("INV"), "branch_id": branch, "customer_id": customer.ID, "invoice_date": day, "note": "", "user_id": user, "tax_rate_id": rate})
	insert(t, "invoice_items", gin.H{"invoice_id": invoice, "product_id": product, "name_en": "x", "qty": 1, "unit_price_cents": 100, "line_total_cents": 100})
	insert(t, "invoice_payments", gin.H{"invoice_id": invoice, "paid_on": day, "method_name": "Cash", "method_type": "CASH", "amount_cents": 100, "user_id": user})
	po := insert(t, "purchase_orders", gin.H{"branch_id": branch, "supplier_id": supplier.ID, "note": "", "user_id": user, "tax_rate_id": rate})
	insert(t, "purchase_order_items", gin.H{"purchase_order_id": po, "product_id": product, "qty_ordered": 1, "unit_cost_cents": 100})
	insert(t, "expenses", gin.H{"branch_id": branch, "category": "OTHER", "amount_cents": 100, "expense_date": day, "user_id": user, "tax_rate_id": rate})
	table := insert(t, "dining_tables", gin.H{"branch_id": branch, "name": unique("T"), "token": unique("tok")})
	order := insert(t, "table_orders", gin.H{"branch_id": branch, "table_id": table, "table_name": "T", "public_token": unique("tok"), "handled_by": user})
	insert(t, "table_order_items", gin.H{"table_order_id": order, "product_id": product, "name_en": "x", "qty": 1, "unit_price_cents": 100})
	// A transfer counts for the branch it leaves and the one it reaches.
	out := insert(t, "stock_transfers", gin.H{"from_branch_id": branch, "to_branch_id": other, "user_id": user})
	insert(t, "stock_transfer_items", gin.H{"stock_transfer_id": out, "product_id": product, "qty": 1})
	insert(t, "stock_transfers", gin.H{"from_branch_id": other, "to_branch_id": branch, "user_id": e.owner.ID})
	count := insert(t, "stock_counts", gin.H{"branch_id": branch, "user_id": user})
	insert(t, "stock_count_items", gin.H{"stock_count_id": count, "product_id": product, "expected_qty": 3, "counted_qty": 3})
	insert(t, "stock_movements", gin.H{"branch_id": branch, "product_id": product, "type": "ADJUST", "qty_change": 3, "balance_after": 3, "unit_cost_cents": 500, "user_id": user})
	// Long past, so it is never the rate in force.
	insert(t, "exchange_rates", gin.H{"company_id": env.companyA, "rate_riel_per_usd": 4100, "set_by_user_id": user, "effective_at": now.AddDate(-20, 0, 0)})
	insert(t, "activity_logs", gin.H{"company_id": env.companyA, "user_id": user, "module": "test", "action": "test", "entity_type": "test"})
	insert(t, "roles", gin.H{"company_id": env.companyA, "branch_id": branch, "name": unique("Branch role")})

	wantInUse(t, del(t, owner, "/branches", branch), "BRANCH_IN_USE", "branch", "switch_off", map[string]int64{
		"sales": 1, "invoices": 1, "purchase_orders": 1, "expenses": 1, "shifts": 1, "table_orders": 1, "stock_transfers": 2,
		// taxBranch also stocks the shared test product.
		"stock_counts": 1, "stock_moves": 1, "stocked_products": 2, "tills": 1, "tables": 1, "staff": 1, "roles": 1,
	})
	wantInUse(t, del(t, owner, "/products", product), "PRODUCT_IN_USE", "product", "switch_off", map[string]int64{
		"sales": 1, "invoices": 1, "purchase_orders": 1, "table_orders": 1, "stock_transfers": 1, "stock_counts": 1, "stock_moves": 1, "stock": 1,
	})
	wantInUse(t, del(t, owner, "/users", user), "USER_IN_USE", "user", "switch_off", map[string]int64{
		"sales": 1, "shifts": 1, "invoices": 1, "invoice_payments": 1, "purchase_orders": 1, "expenses": 1, "stock_moves": 1,
		"stock_transfers": 1, "stock_counts": 1, "cash_movements": 2, "voids_refunds": 2, "exchange_rates": 1, "table_orders": 1, "activity_log": 1,
	})
	wantInUse(t, del(t, owner, "/tax-rates", rate), "TAX_RATE_IN_USE", "tax_rate", "switch_off", map[string]int64{
		"sales": 1, "invoices": 1, "purchase_orders": 1, "expenses": 1,
	})
	wantInUse(t, del(t, owner, "/customers", customer.ID), "CUSTOMER_IN_USE", "customer", "", map[string]int64{"sales": 1, "invoices": 1})
	wantInUse(t, del(t, owner, "/suppliers", supplier.ID), "SUPPLIER_IN_USE", "supplier", "", map[string]int64{"purchase_orders": 1})
}
