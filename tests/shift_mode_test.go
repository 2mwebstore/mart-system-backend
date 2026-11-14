package tests

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Whether a company works in shifts: the super admin sets it on the company
// (REQUIRED, the cashier opens and closes shifts with a cash count, or SKIP, no
// shifts), and the company's staff read it from /settings.
func TestShiftModeIsSetByTheSuperAdmin(t *testing.T) {
	setup(t)
	super := login(t, env.superAdmin).Access
	c := makeCompany(t)
	owner := login(t, c.owner).Access

	readMode := func(token string) string {
		t.Helper()
		r := call(t, http.MethodGet, "/settings", token, nil)
		wantStatus(t, r, http.StatusOK)
		v, _ := r.field(t, "shift_mode").(string)
		return v
	}
	listed := func() string {
		t.Helper()
		r := call(t, http.MethodGet, "/platform/companies", super, nil)
		wantStatus(t, r, http.StatusOK)
		for _, m := range r.rows(t) {
			if uint64(m["id"].(float64)) == c.id {
				v, _ := m["shift_mode"].(string)
				return v
			}
		}
		t.Fatalf("company %d not listed", c.id)
		return ""
	}
	update := func(extra gin.H) response {
		t.Helper()
		body := gin.H{"name": "Company " + c.code, "code": c.code, "active": true, "retention_months": 3}
		for k, v := range extra {
			body[k] = v
		}
		return call(t, http.MethodPut, "/platform/companies/"+id(c.id), super, body)
	}

	if got := readMode(owner); got != "REQUIRED" {
		t.Fatalf("a new company starts with %q, want REQUIRED", got)
	}
	if got := listed(); got != "REQUIRED" {
		t.Fatalf("listed as %q, want REQUIRED", got)
	}

	r := update(gin.H{"shift_mode": "SKIP"})
	wantStatus(t, r, http.StatusOK)
	if v, _ := r.field(t, "shift_mode").(string); v != "SKIP" {
		t.Fatalf("answered %q, want SKIP", v)
	}
	if got := readMode(owner); got != "SKIP" {
		t.Fatalf("the company reads %q, want SKIP", got)
	}
	if got := listed(); got != "SKIP" {
		t.Fatalf("listed as %q, want SKIP", got)
	}

	t.Run("left out, it's kept", func(t *testing.T) {
		wantStatus(t, update(nil), http.StatusOK)
		if got := readMode(owner); got != "SKIP" {
			t.Fatalf("an update without it left %q", got)
		}
	})

	t.Run("only REQUIRED or SKIP", func(t *testing.T) {
		for _, bad := range []string{"skip", "NONE", "OFF"} {
			wantStatus(t, update(gin.H{"shift_mode": bad}), http.StatusUnprocessableEntity)
		}
		if got := readMode(owner); got != "SKIP" {
			t.Fatalf("a refused change left %q", got)
		}
	})

	t.Run("the company can't change it", func(t *testing.T) {
		r := call(t, http.MethodPut, "/settings", owner, gin.H{"receipt_header": "Shop", "receipt_footer": "", "loyalty_points_per_usd": 1, "shift_mode": "REQUIRED"})
		wantStatus(t, r, http.StatusOK)
		if got := readMode(owner); got != "SKIP" {
			t.Fatalf("the company's own settings save changed it to %q", got)
		}
		wantDenied(t, call(t, http.MethodPut, "/platform/companies/"+id(c.id), owner, gin.H{
			"name": "Company " + c.code, "code": c.code, "active": true, "retention_months": 3, "shift_mode": "REQUIRED",
		}), http.StatusForbidden, "NOT_SUPER_ADMIN")
	})

	t.Run("each company has its own", func(t *testing.T) {
		if got := readMode(login(t, env.b.owner).Access); got != "REQUIRED" {
			t.Fatalf("another company reads %q, want REQUIRED", got)
		}
	})

	t.Run("back to REQUIRED", func(t *testing.T) {
		wantStatus(t, update(gin.H{"shift_mode": "REQUIRED"}), http.StatusOK)
		if got := readMode(owner); got != "REQUIRED" {
			t.Fatalf("reads %q, want REQUIRED", got)
		}
	})
}

// A company can be made without shifts from the start.
func TestNewCompanyCanSkipShifts(t *testing.T) {
	setup(t)
	super := login(t, env.superAdmin).Access
	code := companyCode()
	username := unique("skip.owner")
	r := call(t, http.MethodPost, "/platform/companies", super, gin.H{
		"name": "Company " + code, "code": strings.ToLower(code), "shift_mode": "SKIP",
		"owner_full_name": "Owner " + code, "owner_username": username, "owner_password": testPassword,
	})
	wantStatus(t, r, http.StatusCreated)
	if v, _ := r.field(t, "shift_mode").(string); v != "SKIP" {
		t.Fatalf("answered %q, want SKIP", v)
	}
	owner := login(t, namedUser{Username: username}).Access
	s := call(t, http.MethodGet, "/settings", owner, nil)
	wantStatus(t, s, http.StatusOK)
	if v, _ := s.field(t, "shift_mode").(string); v != "SKIP" {
		t.Fatalf("the company reads %q, want SKIP", v)
	}

	bad := call(t, http.MethodPost, "/platform/companies", super, gin.H{
		"name": "Company X", "code": companyCode(), "shift_mode": "LATER",
		"owner_full_name": "Owner X", "owner_username": unique("bad.owner"), "owner_password": testPassword,
	})
	wantStatus(t, bad, http.StatusUnprocessableEntity)
}
