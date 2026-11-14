package tests

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"com-mart/backend/internal/models"
)

// Whether a company uses tables: the super admin turns it on or off on the
// company. Off, the company's staff can't reach its tables or table orders,
// and its QR codes order nothing; back on, everything is as it was.
func TestTablesAreTurnedOnAndOffByTheSuperAdmin(t *testing.T) {
	e := setup(t)
	super := login(t, e.superAdmin).Access
	owner := login(t, e.owner).Access
	// Company A is everyone's fixture: it ends with tables on.
	t.Cleanup(func() {
		e.db.Where("company_id = ? AND `key` = ?", e.companyA, "tables_enabled").Delete(&models.CompanySetting{})
	})

	usesTables := func(token string) bool {
		t.Helper()
		r := call(t, http.MethodGet, "/settings", token, nil)
		wantStatus(t, r, http.StatusOK)
		v, ok := r.field(t, "uses_tables").(bool)
		if !ok {
			t.Fatalf("no uses_tables in %s", r)
		}
		return v
	}
	listed := func() map[string]interface{} {
		t.Helper()
		r := call(t, http.MethodGet, "/platform/companies", super, nil)
		wantStatus(t, r, http.StatusOK)
		for _, m := range r.rows(t) {
			if uint64(m["id"].(float64)) == e.companyA {
				return m
			}
		}
		t.Fatalf("company %d not listed", e.companyA)
		return nil
	}
	retention := listed()["retention_months"]
	update := func(extra gin.H) response {
		t.Helper()
		body := gin.H{"name": "Test Company A", "code": testCompanyCode, "active": true, "retention_months": retention}
		for k, v := range extra {
			body[k] = v
		}
		return call(t, http.MethodPut, "/platform/companies/"+id(e.companyA), super, body)
	}

	if !usesTables(owner) {
		t.Fatal("a company starts without tables")
	}
	if listed()["uses_tables"] != true {
		t.Fatalf("listed with uses_tables %v", listed()["uses_tables"])
	}

	table := call(t, http.MethodPost, "/dining-tables", owner, gin.H{"branch_id": e.branchTK, "name": unique("T"), "active": true})
	wantStatus(t, table, http.StatusCreated)
	tableID := table.id(t)
	token, _ := table.field(t, "token").(string)
	menu := "/public/tables/" + token + "/menu"
	wantStatus(t, call(t, http.MethodGet, menu, "", nil), http.StatusOK)
	wantStatus(t, call(t, http.MethodPost, "/public/tables/"+token+"/orders", "", gin.H{"items": []gin.H{{"product_id": e.product, "qty": 1}}}), http.StatusCreated)

	// Other tests leave orders open at company A's tables: they're set aside
	// (cancelled) for this one, and opened again after it.
	var othersOpen []uint64
	e.db.Raw(`SELECT o.id FROM table_orders o JOIN branches b ON b.id = o.branch_id
		WHERE b.company_id = ? AND o.status IN ('NEW', 'ACCEPTED') AND (o.table_id IS NULL OR o.table_id <> ?)`, e.companyA, tableID).Scan(&othersOpen)
	var setAside []models.TableOrder
	if len(othersOpen) > 0 {
		e.db.Where("id IN ?", othersOpen).Find(&setAside)
		e.db.Model(&models.TableOrder{}).Where("id IN ?", othersOpen).Update("status", models.TableOrderCancelled)
		t.Cleanup(func() {
			for _, o := range setAside {
				e.db.Model(&models.TableOrder{}).Where("id = ?", o.ID).Update("status", o.Status)
			}
		})
	}

	t.Run("not while a table has an order open", func(t *testing.T) {
		for _, status := range []models.TableOrderStatus{models.TableOrderNew, models.TableOrderAccepted} {
			if err := e.db.Model(&models.TableOrder{}).Where("table_id = ?", tableID).Update("status", status).Error; err != nil {
				t.Fatal(err)
			}
			r := update(gin.H{"uses_tables": false})
			wantDenied(t, r, http.StatusUnprocessableEntity, "TABLE_ORDERS_OPEN")
			if !strings.Contains(r.Raw, `"uses_tables"`) {
				t.Errorf("the refusal names no field: %s", r)
			}
			if !usesTables(owner) {
				t.Fatalf("a refused change turned tables off (%s order)", status)
			}
		}
		if err := e.db.Model(&models.TableOrder{}).Where("table_id = ?", tableID).Update("status", models.TableOrderCancelled).Error; err != nil {
			t.Fatal(err)
		}
	})

	r := update(gin.H{"uses_tables": false})
	wantStatus(t, r, http.StatusOK)
	if v := r.field(t, "uses_tables"); v != false {
		t.Fatalf("answered uses_tables %v", v)
	}
	if usesTables(owner) {
		t.Fatal("the company still reads tables on")
	}
	if listed()["uses_tables"] != false {
		t.Fatal("still listed with tables")
	}

	t.Run("off, the staff can't reach them", func(t *testing.T) {
		for _, c := range []struct{ method, path string }{
			{http.MethodGet, "/dining-tables"},
			{http.MethodPost, "/dining-tables"},
			{http.MethodPut, "/dining-tables/" + id(tableID)},
			{http.MethodDelete, "/dining-tables/" + id(tableID)},
			{http.MethodGet, "/table-orders?branch_id=" + id(e.branchTK)},
			{http.MethodGet, "/table-board?branch_id=" + id(e.branchTK)},
			{http.MethodPost, "/table-orders/move"},
		} {
			wantDenied(t, call(t, c.method, c.path, owner, gin.H{}), http.StatusForbidden, "TABLES_OFF")
		}
	})

	t.Run("off, its QR codes order nothing", func(t *testing.T) {
		wantDenied(t, call(t, http.MethodGet, menu, "", nil), http.StatusNotFound, "TABLE_NOT_FOUND")
		wantDenied(t, call(t, http.MethodPost, "/public/tables/"+token+"/orders", "", gin.H{"items": []gin.H{{"product_id": e.product, "qty": 1}}}),
			http.StatusNotFound, "TABLE_NOT_FOUND")
	})

	t.Run("left out, it's kept", func(t *testing.T) {
		wantStatus(t, update(nil), http.StatusOK)
		if usesTables(owner) {
			t.Fatal("an update without it turned tables back on")
		}
	})

	t.Run("each company has its own", func(t *testing.T) {
		other := login(t, e.b.owner).Access
		if !usesTables(other) {
			t.Fatal("another company lost its tables")
		}
		wantStatus(t, call(t, http.MethodGet, "/dining-tables", other, nil), http.StatusOK)
	})

	t.Run("the company can't turn them on", func(t *testing.T) {
		wantDenied(t, call(t, http.MethodPut, "/platform/companies/"+id(e.companyA), owner, gin.H{
			"name": "Test Company A", "code": testCompanyCode, "active": true, "retention_months": retention, "uses_tables": true,
		}), http.StatusForbidden, "NOT_SUPER_ADMIN")
		if usesTables(owner) {
			t.Fatal("the company turned its tables on")
		}
	})

	t.Run("back on, they're as they were", func(t *testing.T) {
		wantStatus(t, update(gin.H{"uses_tables": true}), http.StatusOK)
		if !usesTables(owner) {
			t.Fatal("still off")
		}
		wantStatus(t, call(t, http.MethodGet, menu, "", nil), http.StatusOK)
		r := call(t, http.MethodGet, "/dining-tables?branch_id="+id(e.branchTK), owner, nil)
		wantStatus(t, r, http.StatusOK)
		if !strings.Contains(r.Raw, token) {
			t.Fatal("the table is gone")
		}
	})

	t.Run("one company reads as the list has it", func(t *testing.T) {
		r := call(t, http.MethodGet, "/platform/companies/"+id(e.companyA), super, nil)
		wantStatus(t, r, http.StatusOK)
		if r.field(t, "code") != testCompanyCode || r.field(t, "uses_tables") != true {
			t.Fatalf("got %s", r)
		}
		wantStatus(t, call(t, http.MethodGet, "/platform/companies/999999999", super, nil), http.StatusNotFound)
		wantDenied(t, call(t, http.MethodGet, "/platform/companies/"+id(e.companyA), owner, nil), http.StatusForbidden, "NOT_SUPER_ADMIN")
	})
}

// A company can be made without tables from the start.
func TestNewCompanyCanBeWithoutTables(t *testing.T) {
	setup(t)
	super := login(t, env.superAdmin).Access
	code := companyCode()
	username := unique("notables.owner")
	r := call(t, http.MethodPost, "/platform/companies", super, gin.H{
		"name": "Company " + code, "code": strings.ToLower(code), "uses_tables": false,
		"owner_full_name": "Owner " + code, "owner_username": username, "owner_password": testPassword,
	})
	wantStatus(t, r, http.StatusCreated)
	if v := r.field(t, "uses_tables"); v != false {
		t.Fatalf("made with uses_tables %v", v)
	}
	owner := login(t, namedUser{Username: username}).Access
	if v := call(t, http.MethodGet, "/settings", owner, nil).field(t, "uses_tables"); v != false {
		t.Fatalf("the company reads uses_tables %v", v)
	}
	wantDenied(t, call(t, http.MethodGet, "/dining-tables", owner, nil), http.StatusForbidden, "TABLES_OFF")

	// Left out, a new company has them.
	if v := makeCompanyField(t, "uses_tables"); v != true {
		t.Fatalf("a company made without saying has uses_tables %v", v)
	}
}

// makeCompanyField is one field of a company made with nothing but its name,
// code and Owner.
func makeCompanyField(t *testing.T, name string) interface{} {
	t.Helper()
	c := makeCompany(t)
	r := call(t, http.MethodGet, "/platform/companies/"+id(c.id), login(t, env.superAdmin).Access, nil)
	wantStatus(t, r, http.StatusOK)
	return r.field(t, name)
}
