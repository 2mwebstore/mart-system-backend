package tests

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

// The POS's default customer: picked on the Customers page (customer.manage),
// read by everyone through GET /settings, one per company.
func TestDefaultCustomer(t *testing.T) {
	e := setup(t)
	owner := login(t, e.owner).Access
	t.Cleanup(func() { call(t, http.MethodPut, "/settings/default-customer", owner, gin.H{"customer_id": nil}) })

	defaultOf := func(token string) interface{} {
		t.Helper()
		r := call(t, http.MethodGet, "/settings", token, nil)
		wantStatus(t, r, http.StatusOK)
		return r.field(t, "default_customer_id")
	}

	t.Run("walk-in until one is picked", func(t *testing.T) {
		call(t, http.MethodPut, "/settings/default-customer", owner, gin.H{"customer_id": nil})
		if got := defaultOf(owner); got != nil {
			t.Fatalf("default_customer_id = %v, want null", got)
		}
	})

	t.Run("picked, then seen by everyone in the company, but not another", func(t *testing.T) {
		r := call(t, http.MethodPut, "/settings/default-customer", owner, gin.H{"customer_id": e.customer})
		wantStatus(t, r, http.StatusOK)
		if got := r.field(t, "default_customer_name"); got != "Test Customer" {
			t.Fatalf("default_customer_name = %v", got)
		}
		cashier := login(t, e.cashierTK).Access
		if got := defaultOf(cashier); got != float64(e.customer) {
			t.Fatalf("the cashier sees default %v, want %d", got, e.customer)
		}
		if got := defaultOf(login(t, e.b.owner).Access); got != nil {
			t.Fatalf("company B sees company A's default: %v", got)
		}
	})

	t.Run("not another company's customer", func(t *testing.T) {
		r := call(t, http.MethodPut, "/settings/default-customer", owner, gin.H{"customer_id": e.b.customer})
		wantDenied(t, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	})

	t.Run("needs customer.update", func(t *testing.T) {
		viewer := login(t, newUser(t, newRole(t, "customer.view"), e.branchTK)).Access
		r := call(t, http.MethodPut, "/settings/default-customer", viewer, gin.H{"customer_id": nil})
		wantStatus(t, r, http.StatusForbidden)
	})

	t.Run("deleting the customer goes back to walk-in", func(t *testing.T) {
		created := call(t, http.MethodPost, "/customers", owner, gin.H{"name": "Regular", "phone": unique("0")})
		wantStatus(t, created, http.StatusCreated)
		id := created.id(t)
		wantStatus(t, call(t, http.MethodPut, "/settings/default-customer", owner, gin.H{"customer_id": id}), http.StatusOK)
		wantStatus(t, call(t, http.MethodDelete, "/customers/"+fmt.Sprint(id), owner, nil), http.StatusOK)
		if got := defaultOf(owner); got != nil {
			t.Fatalf("default_customer_id = %v after deleting it, want null", got)
		}
		if n := dbInt(t, "SELECT COUNT(*) FROM company_settings WHERE company_id = ? AND `key` = 'pos_default_customer_id' AND value = ?", e.companyA, fmt.Sprint(id)); n != 0 {
			t.Fatalf("the setting still points at the deleted customer")
		}
	})
}
