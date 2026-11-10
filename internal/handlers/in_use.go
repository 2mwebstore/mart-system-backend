package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"com-mart/backend/internal/utils"
)

// A record that other records point at by id (a product on a sale, a customer
// on an invoice, a till with shifts…) is never deleted: the delete is refused
// with 409 and says what still uses it, so sales, POS history and reports
// never lose what they point at. The client lists the uses from `details`.
//
// Many of these rows are soft-deleted, so the database's own foreign keys
// wouldn't stop the delete; these checks are what does.

// use is one kind of record that still points at the one being deleted.
type use struct {
	What  string `json:"what"`
	Count int64  `json:"count"`
}

// usage counts one kind of use: `query` is a COUNT query with `?` for each
// arg (the record's id, or ids).
type usage struct {
	what  string
	query string
	args  []interface{}
}

// countWhere counts rows of table matching where. Deleted and voided rows
// count too (they still point at the record) unless where leaves them out.
func countWhere(what, table, where string, args ...interface{}) usage {
	return usage{what, "SELECT COUNT(*) FROM " + table + " WHERE " + where, args}
}

// countDistinct counts the distinct documents (column) that have lines
// matching where, e.g. the sales a product was sold on.
func countDistinct(what, table, column, where string, args ...interface{}) usage {
	return usage{what, "SELECT COUNT(DISTINCT " + column + ") FROM " + table + " WHERE " + where, args}
}

// usesOf runs each count and keeps those above zero, in order.
func usesOf(db *gorm.DB, checks ...usage) ([]use, error) {
	var out []use
	for _, ch := range checks {
		var n int64
		if err := db.Raw(ch.query, ch.args...).Scan(&n).Error; err != nil {
			return nil, err
		}
		if n > 0 {
			out = append(out, use{ch.what, n})
		}
	}
	return out, nil
}

// What a refused delete suggests instead, by key; the client says it in the
// user's language.
const (
	hintSwitchOff     = "switch_off"     // the record has an Active switch
	hintMoveProducts  = "move_products"  // a category: move its products first
	hintReassignUsers = "reassign_users" // a role: give its users another role
)

// inUseDetails is what the client gets in error.details.
type inUseDetails struct {
	Entity string `json:"entity"`
	Name   string `json:"name"`
	NameKm string `json:"name_km,omitempty"`
	Uses   []use  `json:"uses"`
	Hint   string `json:"hint,omitempty"`
}

// The English names of each use, for the message (one, many).
var useNouns = map[string][2]string{
	"sales":            {"POS sale", "POS sales"},
	"invoices":         {"invoice", "invoices"},
	"invoice_payments": {"invoice payment", "invoice payments"},
	"purchase_orders":  {"purchase order", "purchase orders"},
	"table_orders":     {"table order", "table orders"},
	"stock_transfers":  {"stock transfer", "stock transfers"},
	"stock_counts":     {"stock count", "stock counts"},
	"stock_moves":      {"stock movement", "stock movements"},
	"stock":            {"branch with stock on hand", "branches with stock on hand"},
	"stocked_products": {"product in stock", "products in stock"},
	"expenses":         {"expense", "expenses"},
	"shifts":           {"shift", "shifts"},
	"cash_movements":   {"cash pay-in or pay-out", "cash pay-ins and pay-outs"},
	"voids_refunds":    {"void or refund", "voids and refunds"},
	"exchange_rates":   {"exchange rate change", "exchange rate changes"},
	"activity_log":     {"activity log entry", "activity log entries"},
	"products":         {"product", "products"},
	"tills":            {"till", "tills"},
	"tables":           {"dining table", "dining tables"},
	"staff":            {"staff member", "staff members"},
	"roles":            {"branch role", "branch roles"},
}

var hintSentences = map[string]string{
	hintSwitchOff:     " Switch it off instead.",
	hintMoveProducts:  " Move its products to another category first.",
	hintReassignUsers: " Give these users another role first.",
}

// inUseError is the refusal: 409 with code, an English message naming the
// uses ("Coca-Cola" can't be deleted: it's used in 12 POS sales and 3
// invoices. Switch it off instead.), and the details for the client.
func inUseError(code string, d inUseDetails) *utils.AppError {
	parts := make([]string, 0, len(d.Uses))
	for _, u := range d.Uses {
		noun := useNouns[u.What]
		word := noun[1]
		if u.Count == 1 {
			word = noun[0]
		}
		if word == "" {
			word = strings.ReplaceAll(u.What, "_", " ")
		}
		parts = append(parts, fmt.Sprintf("%d %s", u.Count, word))
	}
	list := strings.Join(parts, ", ")
	if n := len(parts); n > 1 {
		list = strings.Join(parts[:n-1], ", ") + " and " + parts[n-1]
	}
	msg := fmt.Sprintf("%q can't be deleted: it's used in %s.%s", d.Name, list, hintSentences[d.Hint])
	err := utils.NewAppError(http.StatusConflict, code, msg)
	err.Details = d
	return err
}

// refuseIfUsed counts the uses and, when there are any, answers with the
// refusal and returns true. A failed count answers with that error.
func (a *API) refuseIfUsed(c *gin.Context, code string, d inUseDetails, checks ...usage) bool {
	uses, err := usesOf(a.DB, checks...)
	if err != nil {
		dbFail(c, err)
		return true
	}
	if len(uses) == 0 {
		return false
	}
	d.Uses = uses
	fail(c, inUseError(code, d))
	return true
}
