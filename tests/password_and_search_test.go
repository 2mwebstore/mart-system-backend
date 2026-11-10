package tests

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

// A user's password may be as short as six characters — a 6-digit number
// works — when the user is made and when it's changed; five is refused.
func TestSixCharacterPassword(t *testing.T) {
	e := setup(t)
	owner := login(t, e.owner).Access

	username := unique("six")
	body := updateBody(username, e.roleCashier, true, []uint64{e.branchTK}, "password", "123456")
	created := call(t, http.MethodPost, "/users", owner, body)
	wantStatus(t, created, http.StatusCreated)
	id := created.id(t)
	t.Cleanup(func() { call(t, http.MethodDelete, fmt.Sprintf("/users/%d", id), owner, nil) })
	decodeSession(t, tryLogin(t, username, "123456"))

	wantStatus(t, call(t, http.MethodPut, fmt.Sprintf("/users/%d", id), owner,
		updateBody(username, e.roleCashier, true, []uint64{e.branchTK}, "password", "654321")), http.StatusOK)
	decodeSession(t, tryLogin(t, username, "654321"))

	short := call(t, http.MethodPut, fmt.Sprintf("/users/%d", id), owner,
		updateBody(username, e.roleCashier, true, []uint64{e.branchTK}, "password", "12345"))
	wantDenied(t, short, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	if got := short.Error.Fields["password"]; got != "Password must be at least 6 characters." {
		t.Fatalf("password message = %q", got)
	}
}

// Expenses can be searched by code, note or who recorded them.
func TestExpenseSearch(t *testing.T) {
	e := setup(t)
	owner := login(t, e.owner).Access
	note := unique("generator diesel")
	for _, n := range []string{note, unique("printer paper")} {
		r := call(t, http.MethodPost, "/expenses", owner, gin.H{
			"branch_id": e.branchTK, "category": "OTHER", "amount_cents": 1500, "expense_date": today(), "note": n,
		})
		wantStatus(t, r, http.StatusCreated)
	}

	r := call(t, http.MethodGet, fmt.Sprintf("/expenses?branch_id=%d&page=1&per_page=20&q=%s", e.branchTK, "generator"), owner, nil)
	wantStatus(t, r, http.StatusOK)
	rows := r.rows(t)
	if len(rows) == 0 {
		t.Fatalf("no expense found for %q: %s", "generator", r)
	}
	for _, row := range rows {
		if got, _ := row["note"].(string); len(got) < 9 || got[:9] != "generator" {
			t.Fatalf("search for generator returned %q", got)
		}
	}
}
