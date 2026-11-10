package tests

import (
	"net/http"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"com-mart/backend/internal/models"
)

// Each company numbers its SKUs on its own: the category's prefix and the
// company's next number under it, from 001.
func TestSKUsAreNumberedPerCompany(t *testing.T) {
	e := setup(t)
	one, two := makeCompany(t), makeCompany(t)
	tokenOne, tokenTwo := login(t, one.owner).Access, login(t, two.owner).Access
	category := func(t *testing.T, token, name string) uint64 {
		t.Helper()
		r := call(t, http.MethodPost, "/categories", token, gin.H{"name_en": name})
		wantStatus(t, r, http.StatusCreated)
		return r.id(t)
	}
	made := func(t *testing.T, token string, body gin.H) response {
		t.Helper()
		body["price_cents"], body["active"] = 100, true
		r := call(t, http.MethodPost, "/products", token, body)
		wantStatus(t, r, http.StatusCreated)
		return r
	}
	wantSKU := func(t *testing.T, r response, want string) {
		t.Helper()
		if got := r.field(t, "sku"); got != want {
			t.Fatalf("SKU %v, want %s", got, want)
		}
	}
	skuOf := func(t *testing.T, product uint64) string {
		t.Helper()
		return dbText(t, "SELECT sku FROM products WHERE id = ?", product)
	}
	drinksOne, drinksTwo := category(t, tokenOne, "Drinks"), category(t, tokenTwo, "Drinks")
	snacksOne := category(t, tokenOne, "Snacks")

	var water response
	t.Run("each company counts from 001, per prefix", func(t *testing.T) {
		water = made(t, tokenOne, gin.H{"name_en": "Water", "category_id": drinksOne})
		wantSKU(t, water, "DRI-001")
		wantSKU(t, made(t, tokenTwo, gin.H{"name_en": "Water", "category_id": drinksTwo}), "DRI-001")
		wantSKU(t, made(t, tokenOne, gin.H{"name_en": "Juice", "category_id": drinksOne}), "DRI-002")
		wantSKU(t, made(t, tokenOne, gin.H{"name_en": "Chips", "category_id": snacksOne}), "SNA-001")
		wantSKU(t, made(t, tokenTwo, gin.H{"name_en": "Tea", "category_id": drinksTwo}), "DRI-002")
		wantSKU(t, made(t, tokenOne, gin.H{"name_en": "Gift card"}), "GEN-001")
	})
	t.Run("a variant and an import take the next number", func(t *testing.T) {
		wantSKU(t, made(t, tokenOne, gin.H{"parent_product_id": water.id(t), "variant_name": "Large"}), "DRI-003")
		r := importCSV(t, "/products/import", tokenOne, "name_en,category,price\nSoda,Drinks,1.00\n")
		wantStatus(t, r, http.StatusOK)
		soda := uint64(dbInt(t, "SELECT id FROM products WHERE company_id = ? AND name_en = 'Soda'", one.id))
		if got := skuOf(t, soda); got != "DRI-004" {
			t.Fatalf("the imported product is %s, want DRI-004", got)
		}
	})
	t.Run("a deleted product's number isn't given out again", func(t *testing.T) {
		last := made(t, tokenTwo, gin.H{"name_en": "Coffee", "category_id": drinksTwo})
		wantSKU(t, last, "DRI-003")
		wantStatus(t, call(t, http.MethodDelete, "/products/"+id(last.id(t)), tokenTwo, nil), http.StatusOK)
		wantSKU(t, made(t, tokenTwo, gin.H{"name_en": "Cocoa", "category_id": drinksTwo}), "DRI-004")
	})
	t.Run("counting goes on from the SKUs a company already has", func(t *testing.T) {
		// SKUs from before counting by company (the product's row id), or a
		// seeded catalogue: none of them is handed out again.
		legacy := category(t, tokenOne, "Legacy")
		existing := func(sku string) {
			p := models.Product{CompanyID: one.id, SKU: sku, NameEn: sku, CategoryID: &legacy, Unit: "pcs", Type: models.ProductStandard, Active: true}
			if err := e.db.Create(&p).Error; err != nil {
				t.Fatal(err)
			}
		}
		existing("LEG-041")
		wantSKU(t, made(t, tokenOne, gin.H{"name_en": "New", "category_id": legacy}), "LEG-042")
		existing("LEG-050")
		wantSKU(t, made(t, tokenOne, gin.H{"name_en": "Newer", "category_id": legacy}), "LEG-051")
	})
	t.Run("products made at the same moment get different SKUs", func(t *testing.T) {
		const n = 8
		var wg sync.WaitGroup
		results := make([]response, n)
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i] = call(t, http.MethodPost, "/products", tokenTwo, gin.H{"name_en": unique("Rush"), "category_id": drinksTwo, "price_cents": 100, "active": true})
			}()
		}
		wg.Wait()
		seen := map[interface{}]bool{}
		for _, r := range results {
			wantStatus(t, r, http.StatusCreated)
			sku := r.field(t, "sku")
			if seen[sku] {
				t.Fatalf("two products got SKU %v", sku)
			}
			seen[sku] = true
		}
		if dbInt(t, "SELECT last_number FROM sku_counters WHERE company_id = ? AND prefix = 'DRI'", two.id) != 4+n {
			t.Fatalf("company %s's DRI count is off after %d at once", two.code, n)
		}
	})
}
