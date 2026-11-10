package models

// What every install and every new company starts with. The seed and the
// super admin's "new company" both use these, so a company made either way
// looks the same.

// PermissionDefs is every permission key the app checks. Records people keep
// (products, purchase orders, users, roles…) have view, create, update and
// delete each; the rest are actions (sell, void, adjust stock, see reports).
// Migration 000033 moved existing roles from the older keys (…manage,
// inventory.product_edit…) to these without changing what anyone can do.
var PermissionDefs = []Permission{
	{Key: "pos.sell", Group: "pos", Description: "Sell at the till"},
	{Key: "pos.discount", Group: "pos", Description: "Apply a discount"},
	{Key: "pos.void", Group: "pos", Description: "Void a sale"},
	{Key: "pos.edit_sale", Group: "pos", Description: "Edit a sale's items (still-open shift only)"},
	{Key: "pos.refund", Group: "pos", Description: "Refund a sale"},
	{Key: "pos.open_drawer", Group: "pos", Description: "Open the cash drawer without a sale"},
	{Key: "shift.own", Group: "shift", Description: "Open/close own shift"},
	{Key: "shift.close_others", Group: "shift", Description: "Close another cashier's shift"},
	{Key: "shift.see_expected", Group: "shift", Description: "See expected cash before counting"},
	{Key: "product.view", Group: "product", Description: "View products"},
	{Key: "product.create", Group: "product", Description: "Create products"},
	{Key: "product.update", Group: "product", Description: "Edit products"},
	{Key: "product.delete", Group: "product", Description: "Delete products"},
	{Key: "category.view", Group: "category", Description: "View categories"},
	{Key: "category.create", Group: "category", Description: "Create categories"},
	{Key: "category.update", Group: "category", Description: "Edit categories"},
	{Key: "category.delete", Group: "category", Description: "Delete categories"},
	{Key: "supplier.view", Group: "supplier", Description: "View suppliers"},
	{Key: "supplier.create", Group: "supplier", Description: "Create suppliers"},
	{Key: "supplier.update", Group: "supplier", Description: "Edit suppliers"},
	{Key: "supplier.delete", Group: "supplier", Description: "Delete suppliers"},
	{Key: "inventory.view", Group: "inventory", Description: "View stock movements"},
	{Key: "inventory.adjust", Group: "inventory", Description: "Adjust/write off/count stock"},
	{Key: "inventory.transfer", Group: "inventory", Description: "Transfer stock between branches"},
	{Key: "purchase_order.view", Group: "purchase_order", Description: "View purchase orders"},
	{Key: "purchase_order.create", Group: "purchase_order", Description: "Create purchase orders"},
	{Key: "purchase_order.update", Group: "purchase_order", Description: "Edit purchase orders"},
	{Key: "purchase_order.delete", Group: "purchase_order", Description: "Delete purchase orders"},
	{Key: "purchase_order.receive", Group: "purchase_order", Description: "Receive stock on a purchase order"},
	{Key: "customer.view", Group: "customer", Description: "View customers"},
	{Key: "customer.create", Group: "customer", Description: "Create customers"},
	{Key: "customer.update", Group: "customer", Description: "Edit customers"},
	{Key: "customer.delete", Group: "customer", Description: "Delete customers"},
	{Key: "invoice.view", Group: "invoice", Description: "View invoices"},
	{Key: "invoice.create", Group: "invoice", Description: "Create invoices"},
	{Key: "invoice.update", Group: "invoice", Description: "Edit invoices and record payments"},
	{Key: "invoice.delete", Group: "invoice", Description: "Cancel invoices and remove payments"},
	{Key: "expense.view", Group: "expense", Description: "View expenses"},
	{Key: "expense.create", Group: "expense", Description: "Create expenses"},
	{Key: "expense.update", Group: "expense", Description: "Edit expenses"},
	{Key: "expense.delete", Group: "expense", Description: "Delete expenses"},
	{Key: "report.sales", Group: "report", Description: "View sales reports"},
	{Key: "report.profit_loss", Group: "report", Description: "View profit & loss"},
	{Key: "report.export", Group: "report", Description: "Export reports"},
	{Key: "report.activity_log", Group: "report", Description: "View the activity log"},
	{Key: "user.view", Group: "user", Description: "View users"},
	{Key: "user.create", Group: "user", Description: "Create users"},
	{Key: "user.update", Group: "user", Description: "Edit users and reset their PINs"},
	{Key: "user.delete", Group: "user", Description: "Delete users"},
	{Key: "role.view", Group: "role", Description: "View roles & permissions"},
	{Key: "role.create", Group: "role", Description: "Create roles"},
	{Key: "role.update", Group: "role", Description: "Edit roles and their permissions"},
	{Key: "role.delete", Group: "role", Description: "Delete roles"},
	{Key: "branch.view", Group: "branch", Description: "View every branch and its tills"},
	{Key: "branch.create", Group: "branch", Description: "Create branches and register tills"},
	{Key: "branch.update", Group: "branch", Description: "Edit branches and tills"},
	{Key: "branch.delete", Group: "branch", Description: "Delete branches and tills"},
	{Key: "table.view", Group: "table", Description: "View dining tables"},
	{Key: "table.create", Group: "table", Description: "Create dining tables"},
	{Key: "table.update", Group: "table", Description: "Edit dining tables and their QR codes"},
	{Key: "table.delete", Group: "table", Description: "Delete dining tables"},
	{Key: "payment_method.create", Group: "payment_method", Description: "Create payment methods"},
	{Key: "payment_method.update", Group: "payment_method", Description: "Edit payment methods"},
	{Key: "payment_method.delete", Group: "payment_method", Description: "Delete payment methods"},
	{Key: "settings.rate", Group: "settings", Description: "Change the exchange rate"},
	{Key: "settings.company", Group: "settings", Description: "Change company, receipt and loyalty settings"},
	// Listing tax rates needs nothing (the till and the forms pick from them).
	{Key: "tax_rate.create", Group: "tax_rate", Description: "Create tax rates"},
	{Key: "tax_rate.update", Group: "tax_rate", Description: "Edit tax rates and where they start"},
	{Key: "tax_rate.delete", Group: "tax_rate", Description: "Delete tax rates"},
	{Key: "system.manage", Group: "system", Description: "Manage the company's alerts, numbering and activity-log retention"},
	// Not part of any role template below but the Owner's (who gets every
	// key). Wiping data is the super admin's, on the platform pages; this key
	// is kept so existing roles that hold it stay valid.
	{Key: "system.reset", Group: "system", Description: "Wipe all data for a fresh production setup"},
}

// RoleTemplate is one of the roles a new company starts with.
type RoleTemplate struct {
	Name                       string
	Description                string
	Locked                     bool
	Permissions                []string // nil with AllPermissions
	AllPermissions             bool
	MaxDiscountPercent         int
	RefundWithoutApprovalCents int64
}

func joinKeys(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

// crud is a record's view, create, update and delete keys.
func crud(record string) []string {
	return []string{record + ".view", record + ".create", record + ".update", record + ".delete"}
}

var (
	posAll   = []string{"pos.sell", "pos.discount", "pos.void", "pos.edit_sale", "pos.refund", "pos.open_drawer"}
	shiftAll = []string{"shift.own", "shift.close_others", "shift.see_expected"}
	// Running the stock: the catalog, counts and transfers, purchase orders.
	inventoryOps = joinKeys(
		crud("product"), crud("category"), crud("supplier"), crud("purchase_order"),
		[]string{"purchase_order.receive", "inventory.view", "inventory.adjust", "inventory.transfer"},
	)
	reportAll = []string{"report.sales", "report.profit_loss", "report.export", "report.activity_log"}
)

// DefaultRoles are a new company's roles, all shared by its branches. The
// Owner is locked and holds every permission.
var DefaultRoles = []RoleTemplate{
	{
		Name: RoleOwner, Description: "Full access to all branches and settings.", Locked: true,
		AllPermissions: true, MaxDiscountPercent: 100, RefundWithoutApprovalCents: 1 << 40,
	},
	{
		Name: "Manager", Description: "Runs a branch: approvals, shifts, stock.",
		Permissions: joinKeys(posAll, shiftAll, inventoryOps, []string{"report.sales", "report.export", "report.activity_log"},
			crud("customer"), crud("expense"), crud("invoice")),
		MaxDiscountPercent: 20, RefundWithoutApprovalCents: 2000,
	},
	{
		Name: "Cashier", Description: "Sells at the till and runs own shift.",
		Permissions: []string{
			"pos.sell", "shift.own", "product.view", "category.view", "inventory.view",
			"customer.view", "customer.create", "customer.update",
		},
		MaxDiscountPercent: 5, RefundWithoutApprovalCents: 500,
	},
	{
		Name: "Stock keeper", Description: "Receives, counts and moves stock.",
		Permissions: inventoryOps,
	},
	{
		Name: "Accountant", Description: "Reads reports and profit & loss.",
		Permissions: joinKeys(
			[]string{"product.view", "category.view", "supplier.view", "purchase_order.view", "inventory.view", "invoice.view"},
			reportAll, crud("expense"),
		),
	},
}

// DefaultPaymentMethods are what a new company's tills can take.
var DefaultPaymentMethods = []PaymentMethodRow{
	{Name: "Cash", Type: "CASH", FeePercent: 0, Enabled: true},
	{Name: "KHQR (Bakong)", Type: "KHQR", FeePercent: 0, Enabled: true},
	{Name: "Card — Visa / Mastercard", Type: "CARD", FeePercent: 2.5, Enabled: true},
}

// DefaultNumberSequences is each document type's prefix: a new company's
// numbers all start at 1.
var DefaultNumberSequences = map[string]string{
	"SALE":           "INV",
	"EXPENSE":        "EXP",
	"PURCHASE_ORDER": "PO",
	"INVOICE":        "SI",
}
