package tests

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPublicOrderLimitCantBeResetWithHeaders(t *testing.T) {
	e := setup(t)
	owner := login(t, e.owner).Access
	table := call(t, http.MethodPost, "/dining-tables", owner, gin.H{"branch_id": e.branchTK, "name": unique("T"), "active": true})
	wantStatus(t, table, http.StatusCreated)
	token, _ := table.field(t, "token").(string)
	path := "/public/tables/" + token + "/orders"
	order := gin.H{"items": []gin.H{{"product_id": e.product, "qty": 1}}}

	for i := 0; i < 5; i++ {
		wantStatus(t, call(t, http.MethodPost, path, "", order), http.StatusCreated)
	}
	wantDenied(t, call(t, http.MethodPost, path, "", order), http.StatusTooManyRequests, "TOO_MANY_ORDERS")
	spoofed := call(t, http.MethodPost, path, "", order, "X-Forwarded-For", "198.51.100.7", "X-Real-IP", "198.51.100.7")
	wantDenied(t, spoofed, http.StatusTooManyRequests, "TOO_MANY_ORDERS")
}

func TestPublicMenuShowsNoCosts(t *testing.T) {
	e := setup(t)
	owner := login(t, e.owner).Access
	table := call(t, http.MethodPost, "/dining-tables", owner, gin.H{"branch_id": e.branchTK, "name": unique("T"), "active": true})
	wantStatus(t, table, http.StatusCreated)
	token, _ := table.field(t, "token").(string)

	menu := call(t, http.MethodGet, "/public/tables/"+token+"/menu", "", nil)
	wantStatus(t, menu, http.StatusOK)
	for _, leak := range []string{`"cost`, `"qty"`, `"margin`} {
		if strings.Contains(menu.Raw, leak) {
			t.Errorf("menu exposes %s", leak)
		}
	}
	wantStatus(t, call(t, http.MethodGet, "/public/tables/does-not-exist/menu", "", nil), http.StatusNotFound)
	wantStatus(t, call(t, http.MethodPost, "/public/tables/does-not-exist/orders", "", gin.H{"items": []gin.H{{"product_id": e.product, "qty": 1}}}), http.StatusNotFound)
}

func TestTillRules(t *testing.T) {
	e := setup(t)
	till := newTill(t, e.branchTK)
	cashier := login(t, e.cashierTK).Access
	other := login(t, newUser(t, e.roleCashier, e.branchTK)).Access
	manager := login(t, e.managerTK).Access

	opened := call(t, http.MethodPost, "/shifts/open", cashier, gin.H{"device_key": till, "opening_usd_cents": 0, "opening_khr_riel": 0})
	wantStatus(t, opened, http.StatusCreated)
	shift := id(opened.id(t))

	sale := func(discount int) gin.H {
		body := saleBody(till, unique("key"), e.product)
		body["discount_cents"] = discount
		return body
	}
	lineDiscount := func(discount int) gin.H {
		body := saleBody(till, unique("key"), e.product)
		body["items"] = []gin.H{{"product_id": e.product, "qty": 1, "discount_cents": discount}}
		return body
	}

	t.Run("a cashier can't see the expected cash", func(t *testing.T) {
		r := call(t, http.MethodGet, "/shifts/current?device_key="+till, cashier, nil)
		wantStatus(t, r, http.StatusOK)
		if v := r.field(t, "expected_usd_cents"); v != nil {
			t.Fatalf("expected cash is %v", v)
		}
	})
	t.Run("a cashier without pos.discount can't discount", func(t *testing.T) {
		for _, body := range []gin.H{sale(1), lineDiscount(1)} {
			r := call(t, http.MethodPost, "/sales", cashier, body)
			if r.Status != http.StatusForbidden && r.Status != http.StatusConflict {
				t.Fatalf("got %s", r)
			}
		}
	})
	t.Run("a manager can't go past their role's limit", func(t *testing.T) {
		r := call(t, http.MethodPost, "/sales", manager, lineDiscount(9999999))
		if r.Status == http.StatusCreated || r.Status == http.StatusOK {
			t.Fatalf("sale went through: %s", r)
		}
	})
	t.Run("prices sent by the client are ignored", func(t *testing.T) {
		body := sale(0)
		body["unit_price_cents"], body["total_cents"], body["price_cents"] = 1, 1, 1
		r := call(t, http.MethodPost, "/sales", cashier, body)
		wantStatus(t, r, http.StatusCreated)
		if total, _ := r.field(t, "total_cents").(float64); total != 1000 {
			t.Fatalf("total is %v, the catalog price is 1000", total)
		}
	})
	t.Run("another cashier can't touch my shift", func(t *testing.T) {
		for path, body := range map[string]gin.H{
			"/shifts/" + shift + "/cash-movements": {"type": "PAYOUT", "amount_usd_cents": 1, "reason": "x"},
			"/shifts/" + shift + "/close":          {"counted_usd_cents": 0, "counted_khr_riel": 0},
		} {
			r := call(t, http.MethodPost, path, other, body)
			if r.Status != http.StatusForbidden && r.Status != http.StatusNotFound {
				t.Errorf("%s: %s", path, r)
			}
		}
	})
}
