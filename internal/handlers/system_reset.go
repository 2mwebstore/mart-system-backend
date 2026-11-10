package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

	"com-mart/backend/internal/utils"
)

// resetConfirmPhrase is the exact text the caller must type and send back —
// a typed confirmation (same idea as GitHub's "type the repo name to
// delete it"), not just a click, given what this does.
const resetConfirmPhrase = "RESET"

type resetForProductionReq struct {
	Confirm string `json:"confirm" binding:"required"`
}

// ResetForProduction wipes every company and everything in it: branches,
// products, categories, suppliers, customers, sales, shifts, stock
// movements, purchase orders, expenses, invoices, roles, staff accounts,
// company settings and numbering — plus every backup record, activity log
// and exchange rate. Only the permissions, the Super admin role, the super
// admin accounts and the install's own settings (backups and the platform's
// alerts) survive, so the super admin can sign straight back in and create
// the real companies, each with its own first Owner, completely from
// scratch rather than inheriting whatever demo/test data happened to exist
// before. Every table this leaves empty also has its
// AUTO_INCREMENT counter reset to 1 (see resetAutoIncrement below), so the
// first product/sale/etc. created after a reset really is id 1, not a
// continuation of whatever numbering the wiped demo/test data left behind.
// This is for standing up a brand-new production instance, never for
// clearing out test data on a system that's already serving real customers.
//
// It covers every company at once, so it's the super admin's alone (the
// /platform routes); no company account, the Owner neither, can reach it.
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
		// any row still pointing at a device blocks it otherwise. A super
		// admin never signs in at a till, so their own sessions stay.
		//
		// Staff accounts go before the roles they hold (users.role_id and
		// user_branches.role_id both RESTRICT), and the roles before the
		// branches some of them belong to (roles.branch_id). branches comes
		// after every other table with a RESTRICT FK into it (devices,
		// branch_stock, stock_movements, purchase_orders, stock_transfers,
		// stock_counts, shifts, sales, expenses, daily_sales_summary), and the
		// companies themselves go last.
		statements := []string{
			"DELETE FROM table_orders", // before sales and products (its items reference products)
			"DELETE FROM invoices",     // before customers, products and branches (items/payments cascade)
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
			"DELETE FROM refresh_tokens WHERE device_id IS NOT NULL OR user_id IN (SELECT id FROM users WHERE company_id IS NOT NULL)",
			"DELETE FROM login_attempts WHERE device_id IS NOT NULL OR user_id IN (SELECT id FROM users WHERE company_id IS NOT NULL)",
			"DELETE FROM devices",
			"DELETE FROM dining_tables",
			"DELETE FROM payment_methods",
			"DELETE FROM tax_rates", // after everything that used one
			"DELETE FROM backups",   // the DB record only — files already on disk are untouched, see docs/DEPLOY.md
			"DELETE FROM activity_logs",
			"DELETE FROM exchange_rates",
			"DELETE FROM daily_sales_summary",
			"DELETE FROM users WHERE company_id IS NOT NULL",
			"DELETE FROM roles WHERE company_id IS NOT NULL", // their permissions and limits cascade
			"DELETE FROM branches",
			"DELETE FROM company_settings",
			"DELETE FROM number_sequences",
			"DELETE FROM sku_counters",
			"DELETE FROM companies",
		}
		for _, stmt := range statements {
			if err := tx.Exec(stmt).Error; err != nil {
				return err
			}
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
	a.audit(c, "platform", "system.reset_for_production", "system", 0, nil, nil)
	utils.OK(c, http.StatusOK, gin.H{"reset": true, "at": time.Now()})
}

// resetAutoIncrement covers every AUTO_INCREMENT table ResetForProduction
// leaves empty: the tables it deletes from directly, plus every table that
// only cascades from one of those (sale_items, payments, voids_refunds from
// sales; shift_counts, cash_movements from shifts; purchase_order_items from
// purchase_orders; stock_transfer_items from stock_transfers;
// stock_count_items from stock_counts; product_prices from products;
// loyalty_transactions from customers), and the companies. roles and users
// aren't here: the Super admin role and the super admins survive, so their
// counters stay put.
func resetAutoIncrement(db *gorm.DB) {
	tables := []string{
		"branches", "devices", "exchange_rates", "categories", "suppliers", "products", "product_prices",
		"stock_movements", "shifts", "shift_counts", "cash_movements",
		"purchase_orders", "purchase_order_items", "stock_transfers", "stock_transfer_items",
		"stock_counts", "stock_count_items", "sales", "sale_items", "payments", "voids_refunds",
		"expenses", "payment_methods", "backups", "customers", "loyalty_transactions",
		"activity_logs", "refresh_tokens", "login_attempts",
		"dining_tables", "table_orders", "table_order_items",
		"invoices", "invoice_items", "invoice_payments", "companies", "tax_rates",
	}
	for _, t := range tables {
		if err := db.Exec("ALTER TABLE " + t + " AUTO_INCREMENT = 1").Error; err != nil {
			log.Error().Err(err).Str("table", t).Msg("failed to reset AUTO_INCREMENT after production reset")
		}
	}
}
