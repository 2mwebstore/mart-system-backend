package tests

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"
)

// Two clients, each with its own company. The super admin makes each company
// with one admin account (its Owner), who then builds the company up from
// nothing: branches, roles, staff, categories and products. Each client gets
// the same names and codes, and neither sees the other's.
func TestTwoClientsEachBuildTheirOwnCompany(t *testing.T) {
	setup(t)
	type client struct {
		company                     madeCompany
		token                       string
		branches                    []uint64
		role, staff, category, item uint64
		staffName                   string
	}
	clients := []*client{{company: makeCompany(t)}, {company: makeCompany(t)}}

	for _, cl := range clients {
		cl.token = login(t, cl.company.owner).Access
		own := side{name: cl.company.code, token: cl.token, company: cl.company.id}

		// A new company has no branches until its admin adds them.
		for _, b := range []gin.H{{"code": "HQ", "name": "Head office", "active": true}, {"code": "RS", "name": "Riverside", "active": true}} {
			cl.branches = append(cl.branches, own.ownRow(t, "branches", call(t, http.MethodPost, "/branches", cl.token, b)))
		}
		cl.role = own.ownRow(t, "roles", call(t, http.MethodPost, "/roles", cl.token, gin.H{
			"name": "Supervisor", "permissions": []string{"pos.sell", "inventory.view", "product.view", "category.view", "supplier.view", "purchase_order.view"},
		}))
		cl.staffName = unique("staff")
		cl.staff = own.ownRow(t, "users", call(t, http.MethodPost, "/users", cl.token,
			updateBody(cl.staffName, cl.role, true, cl.branches, "password", testPassword)))
		cl.category = own.ownRow(t, "categories", call(t, http.MethodPost, "/categories", cl.token, gin.H{"name_en": "Drinks"}))
		cl.item = own.ownRow(t, "products", call(t, http.MethodPost, "/products", cl.token, gin.H{
			"name_en": "Water", "category_id": cl.category, "price_cents": 100, "active": true,
		}))
		own.stockOnlyAtOwnBranches(t, cl.item)
	}

	for i, cl := range clients {
		other := clients[1-i]
		t.Run(cl.company.code, func(t *testing.T) {
			t.Run("the admin's lists hold its own rows and none of the other's", func(t *testing.T) {
				for _, l := range []struct {
					path       string
					own, other uint64
				}{
					{"/branches", cl.branches[0], other.branches[0]},
					{"/roles", cl.role, other.role},
					{"/users", cl.staff, other.staff},
					{"/categories", cl.category, other.category},
					{"/products", cl.item, other.item},
				} {
					ids := listedIDs(t, call(t, http.MethodGet, l.path, cl.token, nil))
					if !slices.Contains(ids, l.own) {
						t.Errorf("%s doesn't list the company's own %d", l.path, l.own)
					}
					if slices.Contains(ids, l.other) {
						t.Errorf("%s lists the other client's %d", l.path, l.other)
					}
				}
				wantStatus(t, call(t, http.MethodGet, "/products/"+id(other.item), cl.token, nil), http.StatusNotFound)
			})

			t.Run("the new staff member signs in to this company, at its branches", func(t *testing.T) {
				r := tryLogin(t, cl.staffName, testPassword)
				wantStatus(t, r, http.StatusOK)
				if got := sessionCompany(t, r); got != cl.company.code {
					t.Fatalf("the staff member's session is for %q, want %s", got, cl.company.code)
				}
				var me struct {
					Branches []struct {
						ID uint64 `json:"id"`
					} `json:"branches"`
				}
				if err := json.Unmarshal(r.Data, &me); err != nil {
					t.Fatal(err)
				}
				var got []uint64
				for _, b := range me.Branches {
					got = append(got, b.ID)
				}
				slices.Sort(got)
				if !slices.Equal(got, cl.branches) {
					t.Fatalf("the staff member is at branches %v, want the company's %v", got, cl.branches)
				}
			})

			t.Run("the admin can make another admin, who has the same reach", func(t *testing.T) {
				var ownerRole uint64
				var roles []struct {
					ID   uint64 `json:"id"`
					Name string `json:"name"`
				}
				r := call(t, http.MethodGet, "/roles", cl.token, nil)
				wantStatus(t, r, http.StatusOK)
				if err := json.Unmarshal(r.Data, &roles); err != nil {
					t.Fatalf("roles: %s", r)
				}
				for _, role := range roles {
					if role.Name == "Owner" {
						ownerRole = role.ID
					}
				}
				name := unique("admin")
				own := side{company: cl.company.id}
				own.ownRow(t, "users", call(t, http.MethodPost, "/users", cl.token,
					updateBody(name, ownerRole, true, nil, "password", testPassword)))
				second := decodeSession(t, tryLogin(t, name, testPassword)).Access
				own.ownRow(t, "branches", call(t, http.MethodPost, "/branches", second, gin.H{"code": "AP", "name": "Airport", "active": true}))
				own.ownRow(t, "products", call(t, http.MethodPost, "/products", second, gin.H{"name_en": "Juice", "category_id": cl.category, "price_cents": 150, "active": true}))
			})
		})
	}
}
