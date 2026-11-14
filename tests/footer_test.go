package tests

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// The footer: one line under every page for every company, which only the
// super admin writes, read by anyone with the branding.
func TestFooterIsTheSuperAdminsForEveryCompany(t *testing.T) {
	e := setup(t)
	t.Cleanup(func() { e.db.Exec("DELETE FROM settings WHERE `key` = 'footer_text'") })
	e.db.Exec("DELETE FROM settings WHERE `key` = 'footer_text'")
	super := login(t, e.superAdmin).Access

	shown := func(code string) string {
		t.Helper()
		r := call(t, http.MethodGet, "/auth/branding?company="+code, "", nil)
		wantStatus(t, r, http.StatusOK)
		v, _ := r.field(t, "footer_text").(string)
		return v
	}
	const byDefault = "© {year} Com Mart. All rights reserved."
	for _, code := range []string{testCompanyCode, e.b.code, ""} {
		if got := shown(code); got != byDefault {
			t.Fatalf("footer before it's set, company %q: %q", code, got)
		}
	}
	r := call(t, http.MethodGet, "/platform/settings/footer", super, nil)
	wantStatus(t, r, http.StatusOK)
	if got := r.field(t, "text"); got != byDefault {
		t.Fatalf("the super admin reads %v", got)
	}

	t.Run("only the super admin writes it", func(t *testing.T) {
		owner := login(t, e.owner).Access
		wantStatus(t, call(t, http.MethodPut, "/platform/settings/footer", owner, gin.H{"text": "Mine"}), http.StatusForbidden)
		wantStatus(t, call(t, http.MethodGet, "/platform/settings/footer", owner, nil), http.StatusForbidden)
		wantStatus(t, call(t, http.MethodPut, "/platform/settings/footer", "", gin.H{"text": "Mine"}), http.StatusUnauthorized)
		if got := shown(testCompanyCode); got != byDefault {
			t.Fatalf("a refused write changed it: %q", got)
		}
	})

	t.Run("saved as one line, for every company", func(t *testing.T) {
		r := call(t, http.MethodPut, "/platform/settings/footer", super, gin.H{"text": "  © {year} Bee Soft\n  Phnom   Penh "})
		wantStatus(t, r, http.StatusOK)
		const want = "© {year} Bee Soft Phnom Penh"
		if got := r.field(t, "text"); got != want {
			t.Fatalf("saved %v", got)
		}
		for _, code := range []string{testCompanyCode, e.b.code, ""} {
			if got := shown(code); got != want {
				t.Fatalf("company %q shows %q", code, got)
			}
		}
		// A company saving its own name and logo gets it back too.
		b := login(t, e.b.owner).Access
		r = call(t, http.MethodPut, "/settings/branding", b, gin.H{"company_name": "Test Company B"})
		wantStatus(t, r, http.StatusOK)
		if got := r.field(t, "footer_text"); got != want {
			t.Fatalf("branding saved by a company: %v", got)
		}
	})

	t.Run("empty means no footer, not the default", func(t *testing.T) {
		wantStatus(t, call(t, http.MethodPut, "/platform/settings/footer", super, gin.H{"text": "   "}), http.StatusOK)
		if got := shown(testCompanyCode); got != "" {
			t.Fatalf("emptied footer shows %q", got)
		}
	})

	t.Run("200 characters at most", func(t *testing.T) {
		wantStatus(t, call(t, http.MethodPut, "/platform/settings/footer", super, gin.H{"text": strings.Repeat("ក", 200)}), http.StatusOK)
		wantDenied(t, call(t, http.MethodPut, "/platform/settings/footer", super, gin.H{"text": strings.Repeat("ក", 201)}), http.StatusUnprocessableEntity, "VALIDATION_ERROR")
		if got := shown(""); got != strings.Repeat("ក", 200) {
			t.Fatalf("a refused write changed it: %q", got)
		}
	})
}
