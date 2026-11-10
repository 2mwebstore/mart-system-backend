package tests

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"

	"com-mart/backend/internal/models"
	"com-mart/backend/migrations"
)

// Each kind of record has its own view, create, update and delete: holding
// one doesn't open the others.
func TestCrudPermissionsAreSeparate(t *testing.T) {
	e := setup(t)
	as := func(perms ...string) string { return login(t, newUser(t, newRole(t, perms...), e.branchTK)).Access }
	product := func() gin.H {
		return gin.H{"name_en": unique("crud item"), "price_cents": 250, "active": true}
	}

	t.Run("products", func(t *testing.T) {
		viewer := as("product.view")
		wantStatus(t, call(t, http.MethodGet, fmt.Sprintf("/products?branch_id=%d", e.branchTK), viewer, nil), http.StatusOK)
		wantDenied(t, call(t, http.MethodPost, "/products", viewer, product()), http.StatusForbidden, "FORBIDDEN")

		creator := as("product.view", "product.create")
		made := call(t, http.MethodPost, "/products", creator, product())
		wantStatus(t, made, http.StatusCreated)
		item := made.id(t)
		wantDenied(t, call(t, http.MethodPut, fmt.Sprintf("/products/%d", item), creator, product()), http.StatusForbidden, "FORBIDDEN")
		wantDenied(t, call(t, http.MethodDelete, fmt.Sprintf("/products/%d", item), creator, nil), http.StatusForbidden, "FORBIDDEN")

		editor := as("product.view", "product.update")
		wantStatus(t, call(t, http.MethodPut, fmt.Sprintf("/products/%d", item), editor, product()), http.StatusOK)
		wantDenied(t, call(t, http.MethodDelete, fmt.Sprintf("/products/%d", item), editor, nil), http.StatusForbidden, "FORBIDDEN")
		wantStatus(t, call(t, http.MethodDelete, fmt.Sprintf("/products/%d", item), as("product.delete"), nil), http.StatusOK)
	})

	t.Run("stock moves no longer open the catalog", func(t *testing.T) {
		stock := as("inventory.view")
		wantStatus(t, call(t, http.MethodGet, fmt.Sprintf("/stock-movements?branch_id=%d", e.branchTK), stock, nil), http.StatusOK)
		wantDenied(t, call(t, http.MethodGet, fmt.Sprintf("/products?branch_id=%d", e.branchTK), stock, nil), http.StatusForbidden, "FORBIDDEN")
		wantDenied(t, call(t, http.MethodGet, fmt.Sprintf("/purchase-orders?branch_id=%d", e.branchTK), stock, nil), http.StatusForbidden, "FORBIDDEN")
	})

	t.Run("users", func(t *testing.T) {
		viewer := as("user.view")
		wantStatus(t, call(t, http.MethodGet, "/users", viewer, nil), http.StatusOK)
		wantStatus(t, call(t, http.MethodGet, fmt.Sprintf("/users/%d", e.cashierTK.ID), viewer, nil), http.StatusOK)
		body := updateBody(unique("crud"), e.roleCashier, true, []uint64{e.branchTK}, "password", testPassword)
		wantDenied(t, call(t, http.MethodPost, "/users", viewer, body), http.StatusForbidden, "FORBIDDEN")
		wantDenied(t, call(t, http.MethodPost, fmt.Sprintf("/users/%d/reset-pin", e.cashierTK.ID), viewer, nil), http.StatusForbidden, "FORBIDDEN")
	})

	t.Run("roles", func(t *testing.T) {
		viewer := as("role.view")
		wantStatus(t, call(t, http.MethodGet, "/roles", viewer, nil), http.StatusOK)
		wantDenied(t, call(t, http.MethodPost, "/roles", viewer, gin.H{"name": unique("crud role"), "permissions": []string{"pos.sell"}}), http.StatusForbidden, "FORBIDDEN")
		wantDenied(t, call(t, http.MethodDelete, fmt.Sprintf("/roles/%d", newRole(t, "pos.sell")), as("role.view", "role.update"), nil), http.StatusForbidden, "FORBIDDEN")
	})
}

// GET /users/:id is the user as the list shows them, and only someone the
// caller could list: a user at a branch the caller doesn't have is 404.
func TestGetUser(t *testing.T) {
	e := setup(t)
	owner := login(t, e.owner).Access

	r := call(t, http.MethodGet, fmt.Sprintf("/users/%d", e.cashierTK.ID), owner, nil)
	wantStatus(t, r, http.StatusOK)
	var got struct {
		Username  string   `json:"username"`
		BranchIDs []uint64 `json:"branch_ids"`
	}
	if err := json.Unmarshal(r.Data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Username != e.cashierTK.Username || !slices.Contains(got.BranchIDs, e.branchTK) {
		t.Fatalf("got %+v", got)
	}

	bkkOnly := newUser(t, e.roleCashier, e.branchBKK)
	tkViewer := login(t, newUser(t, newRole(t, "user.view"), e.branchTK)).Access
	wantStatus(t, call(t, http.MethodGet, fmt.Sprintf("/users/%d", bkkOnly.ID), tkViewer, nil), http.StatusNotFound)
	wantStatus(t, call(t, http.MethodGet, fmt.Sprintf("/users/%d", e.b.owner.ID), owner, nil), http.StatusNotFound)
	wantStatus(t, call(t, http.MethodGet, "/users/999999999", owner, nil), http.StatusNotFound)
}

// Migration 000033 swaps each older key for the new ones covering the same
// routes. It's safe to run again, so it is replayed here on a role that holds
// the older keys.
func TestCrudPermissionMigrationKeepsAccess(t *testing.T) {
	e := setup(t)
	old := []string{"inventory.view", "inventory.edit_price", "inventory.product_edit", "inventory.receive", "user.manage", "branch.manage", "report.sales"}
	for _, k := range old {
		if err := e.db.Exec("INSERT IGNORE INTO permissions (`key`, `group`, description) VALUES (?, 'legacy', '')", k).Error; err != nil {
			t.Fatal(err)
		}
	}
	role := newRole(t, append([]string{"pos.sell"}, old...)...)
	// The replay gives the new keys to every role holding an older one, the
	// shared fixtures' roles too (the Cashier holds inventory.view): take
	// those grants back afterwards so later tests find the roles as seeded.
	type grant struct{ RoleID, PermissionID uint64 }
	grants := func() []grant {
		var rows []grant
		e.db.Raw("SELECT role_id, permission_id FROM role_permissions").Scan(&rows)
		return rows
	}
	had := map[grant]bool{}
	for _, g := range grants() {
		had[g] = true
	}
	t.Cleanup(func() {
		for _, g := range grants() {
			if !had[g] {
				e.db.Exec("DELETE FROM role_permissions WHERE role_id = ? AND permission_id = ?", g.RoleID, g.PermissionID)
			}
		}
	})

	script, err := migrations.FS.ReadFile("000033_crud_permissions.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("mysql", e.cfg.DSN()+"&multiStatements=true")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(string(script)); err != nil {
		t.Fatalf("replaying the migration: %v", err)
	}

	var held []string
	e.db.Table("role_permissions rp").Joins("JOIN permissions p ON p.id = rp.permission_id").
		Where("rp.role_id = ?", role).Order("p.`key`").Pluck("p.`key`", &held)
	want := []string{
		"branch.create", "branch.delete", "branch.update", "branch.view",
		"category.create", "category.delete", "category.update", "category.view",
		"inventory.view", "invoice.view",
		"payment_method.create", "payment_method.delete", "payment_method.update",
		"pos.sell", "product.delete", "product.update", "product.view",
		"purchase_order.receive", "purchase_order.view", "report.sales", "settings.company",
		"supplier.create", "supplier.delete", "supplier.update", "supplier.view",
		"table.create", "table.delete", "table.update", "table.view",
		"user.create", "user.delete", "user.update", "user.view",
	}
	if !slices.Equal(held, want) {
		t.Fatalf("after the migration the role holds\n %v\nwant\n %v", held, want)
	}

	var leftover int64
	e.db.Model(&models.Permission{}).Where("`key` IN ?", []string{"inventory.edit_price", "inventory.product_edit", "inventory.receive", "user.manage", "branch.manage"}).Count(&leftover)
	if leftover != 0 {
		t.Fatalf("%d older keys are still in the catalog", leftover)
	}
	// Every key the app checks is in the catalog, and nothing else.
	var catalog []string
	e.db.Model(&models.Permission{}).Order("`key`").Pluck("`key`", &catalog)
	for _, d := range models.PermissionDefs {
		if !slices.Contains(catalog, d.Key) {
			t.Errorf("%s is missing from the catalog", d.Key)
		}
	}
	if len(catalog) != len(models.PermissionDefs) {
		t.Errorf("catalog has %d keys, the app defines %d: %v", len(catalog), len(models.PermissionDefs), catalog)
	}
}
