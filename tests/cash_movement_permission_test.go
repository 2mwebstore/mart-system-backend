package tests

import (
	"database/sql"
	"net/http"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"

	"com-mart/backend/internal/models"
	"com-mart/backend/migrations"
)

// A pay-in or pay-out needs pos.cash_movement as well as running your own
// shift: without it the cashier still sells and closes the shift, but moves
// no cash in or out of the drawer.
func TestCashMovementNeedsItsPermission(t *testing.T) {
	e := setup(t)
	move := gin.H{"type": "PAYOUT", "amount_usd_cents": 500, "reason": "ice delivery"}
	openShift := func(token string) string {
		t.Helper()
		r := call(t, http.MethodPost, "/shifts/open", token, gin.H{"device_key": newTill(t, e.branchTK), "opening_usd_cents": 0, "opening_khr_riel": 0})
		wantStatus(t, r, http.StatusCreated)
		return id(r.id(t))
	}

	t.Run("without it", func(t *testing.T) {
		cashier := login(t, newUser(t, newRole(t, "pos.sell", "shift.own"), e.branchTK)).Access
		shift := openShift(cashier)
		for _, kind := range []string{"PAYOUT", "PAYIN"} {
			body := gin.H{"type": kind, "amount_usd_cents": 500, "reason": "x"}
			wantDenied(t, call(t, http.MethodPost, "/shifts/"+shift+"/cash-movements", cashier, body), http.StatusForbidden, "FORBIDDEN")
		}
		if n := dbInt(t, "SELECT COUNT(*) FROM cash_movements WHERE shift_id = ?", shift); n != 0 {
			t.Fatalf("%d cash movements were saved", n)
		}
		wantStatus(t, call(t, http.MethodPost, "/shifts/"+shift+"/close", cashier, gin.H{"counted_usd_cents": 0, "counted_khr_riel": 0}), http.StatusOK)
	})

	t.Run("with it", func(t *testing.T) {
		cashier := login(t, newUser(t, newRole(t, "pos.sell", "shift.own", "pos.cash_movement"), e.branchTK)).Access
		shift := openShift(cashier)
		wantStatus(t, call(t, http.MethodPost, "/shifts/"+shift+"/cash-movements", cashier, move), http.StatusCreated)
		if n := dbInt(t, "SELECT COUNT(*) FROM cash_movements WHERE shift_id = ? AND amount_usd_cents = 500", shift); n != 1 {
			t.Fatalf("%d cash movements were saved, want 1", n)
		}
	})

	t.Run("it alone doesn't open a shift's drawer", func(t *testing.T) {
		shift := openShift(login(t, e.cashierTK).Access)
		keyOnly := login(t, newUser(t, newRole(t, "pos.cash_movement"), e.branchTK)).Access
		wantDenied(t, call(t, http.MethodPost, "/shifts/"+shift+"/cash-movements", keyOnly, move), http.StatusForbidden, "FORBIDDEN")
	})
}

// A new company's roles match an upgraded one's: every default role that runs
// its own shift can move cash, as migration 000035 gives existing roles.
func TestDefaultRolesThatRunAShiftMoveCash(t *testing.T) {
	for _, r := range models.DefaultRoles {
		if r.AllPermissions {
			continue
		}
		if slices.Contains(r.Permissions, "shift.own") != slices.Contains(r.Permissions, "pos.cash_movement") {
			t.Errorf("%s: shift.own and pos.cash_movement should go together", r.Name)
		}
	}
	for _, name := range []string{"Manager", "Cashier"} {
		i := slices.IndexFunc(models.DefaultRoles, func(r models.RoleTemplate) bool { return r.Name == name })
		if i < 0 || !slices.Contains(models.DefaultRoles[i].Permissions, "pos.cash_movement") {
			t.Errorf("the default %s role can't move cash", name)
		}
	}
}

// Migration 000035 gives the new key to every role that runs its own shift,
// so nobody who records cash movements today loses them. It's safe to run
// again, so it is replayed here.
func TestCashMovementMigrationKeepsAccess(t *testing.T) {
	e := setup(t)
	runsShift := newRole(t, "pos.sell", "shift.own")
	sellsOnly := newRole(t, "pos.sell")

	// The replay also reaches other tests' roles: take its grants back after.
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

	script, err := migrations.FS.ReadFile("000035_cash_movement_permission.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("mysql", e.cfg.DSN()+"&multiStatements=true")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for range 2 {
		if _, err := db.Exec(string(script)); err != nil {
			t.Fatalf("replaying the migration: %v", err)
		}
	}

	held := func(role uint64) []string {
		var keys []string
		e.db.Table("role_permissions rp").Joins("JOIN permissions p ON p.id = rp.permission_id").
			Where("rp.role_id = ?", role).Order("p.`key`").Pluck("p.`key`", &keys)
		return keys
	}
	if got, want := held(runsShift), []string{"pos.cash_movement", "pos.sell", "shift.own"}; !slices.Equal(got, want) {
		t.Errorf("a role that runs a shift holds %v, want %v", got, want)
	}
	if got, want := held(sellsOnly), []string{"pos.sell"}; !slices.Equal(got, want) {
		t.Errorf("a role that doesn't run a shift holds %v, want %v", got, want)
	}
	var desc string
	e.db.Raw("SELECT description FROM permissions WHERE `key` = 'pos.cash_movement'").Scan(&desc)
	if desc != "Record cash pay-ins and pay-outs" {
		t.Errorf("description is %q", desc)
	}
}
