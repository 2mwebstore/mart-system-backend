package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

	"com-mart/backend/internal/middleware"
	"com-mart/backend/internal/utils"
)

// resetConfirmPhrase is the exact text the caller must type and send back —
// a typed confirmation (same idea as GitHub's "type the repo name to
// delete it"), not just a click, given what this does.
const resetConfirmPhrase = "RESET"

type resetForProductionReq struct {
	Confirm string `json:"confirm" binding:"required"`
}

// ResetForProduction wipes every branch, product, category, supplier,
// customer, sale, shift, stock movement, purchase order, expense, backup
// record, activity log, exchange rate and setting — and every user account
// except the one making this request. Only roles, permissions and the
// caller's own account survive, so the caller can sign back in and start
// configuring a real deployment — including its branches — completely from
// scratch, rather than inheriting whatever demo/test branches happened to
// exist before. (CreateBranch auto-grants the caller access to the first
// branch they create afterward, since this reset leaves them with none —
// see its own comment.) Every table this leaves empty also has its
// AUTO_INCREMENT counter reset to 1 (see resetAutoIncrement below), so the
// first product/sale/etc. created after a reset really is id 1, not a
// continuation of whatever numbering the wiped demo/test data left behind.
// This is for standing up a brand-new production instance, never for
// clearing out test data on a system that's already serving real customers.
//
// Gated by a dedicated system.reset permission — deliberately not part of
// system.manage or any role's default grant, so enabling Telegram alerts or
// backups for a Manager can never accidentally also hand them this. Only
// the Owner role gets it (see seed/main.go's allKeys).
//
// Everything below runs in one transaction: if any step fails (a
// foreign-key surprise from a table this comment missed, for instance) the
// whole thing rolls back and nothing is lost — a failed reset is a safe
// failure, not a half-wiped database.
func (a *API) ResetForProduction(c *gin.Context) {
	var req resetForProductionReq
	if !bind(c, &req) {
		return
	}
	if req.Confirm != resetConfirmPhrase {
		invalid(c, "confirm", "Type RESET exactly to confirm.")
		return
	}
	callerID := middleware.UserIDFrom(c)

	err := a.DB.Transaction(func(tx *gorm.DB) error {
		// Order matters: every RESTRICT foreign key (the default — see
		// docs/DECISIONS.md's soft-delete note for how that bit the app
		// before) must have its referencing rows gone before the row it
		// references. Tables not listed here (sale_items, payments,
		// voids_refunds, shift_counts, cash_movements, purchase_order_items,
		// stock_transfer_items, stock_count_items, product_prices,
		// loyalty_transactions, role_permissions, role_limits) all cascade
		// from a table that IS listed, so deleting the parent removes them
		// too — user_branches cascades from *both* users and branches, so it's
		// gone well before either of those two runs.
		//
		// refresh_tokens and login_attempts are explicitly listed rather than
		// left to cascade: their user_id FK cascades from users, but their
		// device_id FK does not (no ON DELETE clause — see migration 000002),
		// and DELETE FROM devices below runs before users is ever touched, so
		// any row still pointing at a device — including the caller's own,
		// since the caller is never deleted — blocks it otherwise.
		//
		// branches is deleted last of all: every other table with a RESTRICT
		// FK into it (devices, branch_stock, stock_movements, purchase_orders,
		// stock_transfers, stock_counts, shifts, sales, expenses,
		// daily_sales_summary) is already gone by the time it runs.
		statements := []string{
			"DELETE FROM sales",
			"DELETE FROM shifts",
			"DELETE FROM purchase_orders",
			"DELETE FROM stock_transfers",
			"DELETE FROM stock_counts",
			"DELETE FROM stock_movements",
			"DELETE FROM expenses",
			"DELETE FROM branch_stock",
			"DELETE FROM products",
			"DELETE FROM customers",
			"DELETE FROM suppliers",
			"DELETE FROM categories",
			"DELETE FROM refresh_tokens",
			"DELETE FROM login_attempts",
			"DELETE FROM devices",
			"DELETE FROM payment_methods",
			"DELETE FROM backups", // the DB record only — files already on disk are untouched, see docs/DEPLOY.md
			"DELETE FROM activity_logs",
			"DELETE FROM exchange_rates",
			"DELETE FROM settings",
			"DELETE FROM daily_sales_summary",
			"DELETE FROM branches",
		}
		for _, stmt := range statements {
			if err := tx.Exec(stmt).Error; err != nil {
				return err
			}
		}
		// Every user except whoever is doing this — cascades their
		// user_branches/refresh_tokens/login_attempts (the latter two are
		// already gone above; user_branches was already gone too, once
		// branches was deleted).
		if err := tx.Exec("DELETE FROM users WHERE id != ?", callerID).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		dbFail(c, err)
		return
	}
	// AUTO_INCREMENT resets are DDL, and DDL causes an implicit commit in
	// MySQL — mixed into the transaction above, the first one would have
	// silently committed everything before it regardless of what failed
	// later, breaking the all-or-nothing guarantee that transaction exists
	// for. So this only ever runs after that transaction has already
	// committed in full, as a separate, best-effort pass: every table that
	// the wipe above leaves at zero rows (explicitly, or by cascade — see
	// the statement list's comment), so the next sale/product/etc. after a
	// reset is really id 1, not a continuation of the old numbering. MySQL
	// never lowers a table's counter below what its current rows need, so
	// this is safe to run unconditionally; a failure here (which shouldn't
	// happen) doesn't undo the wipe above, so it's logged, not fatal.
	resetAutoIncrement(a.DB)
	// Written after commit, deliberately: this is meant to be the first
	// entry in the fresh activity log, not something the reset itself wipes.
	a.audit(c, "settings", "system.reset_for_production", "system", 0, nil, nil)
	utils.OK(c, http.StatusOK, gin.H{"reset": true, "at": time.Now()})
}

// resetAutoIncrement covers every AUTO_INCREMENT table ResetForProduction
// leaves empty: the tables it deletes from directly, plus every table that
// only cascades from one of those (sale_items, payments, voids_refunds from
// sales; shift_counts, cash_movements from shifts; purchase_order_items from
// purchase_orders; stock_transfer_items from stock_transfers;
// stock_count_items from stock_counts; product_prices from products;
// loyalty_transactions from customers). roles/permissions/users aren't here
// — they're not wiped (or, for users, not wiped *empty*: the caller's own
// row survives, so its counter must stay put).
func resetAutoIncrement(db *gorm.DB) {
	tables := []string{
		"branches", "devices", "exchange_rates", "categories", "suppliers", "products", "product_prices",
		"stock_movements", "shifts", "shift_counts", "cash_movements",
		"purchase_orders", "purchase_order_items", "stock_transfers", "stock_transfer_items",
		"stock_counts", "stock_count_items", "sales", "sale_items", "payments", "voids_refunds",
		"expenses", "payment_methods", "backups", "customers", "loyalty_transactions",
		"activity_logs", "refresh_tokens", "login_attempts",
	}
	for _, t := range tables {
		if err := db.Exec("ALTER TABLE " + t + " AUTO_INCREMENT = 1").Error; err != nil {
			log.Error().Err(err).Str("table", t).Msg("failed to reset AUTO_INCREMENT after production reset")
		}
	}
}
