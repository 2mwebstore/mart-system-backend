package routes

import (
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"com-mart/backend/internal/config"
	"com-mart/backend/internal/handlers"
	"com-mart/backend/internal/middleware"
)

// Dependencies bundles every handler the router needs to wire up. It grows
// as later phases add modules (inventory, POS, shifts, reports, ...).
type Dependencies struct {
	Config      *config.Config
	AuthHandler *handlers.AuthHandler
	API         *handlers.API
	// PrincipalLoader defaults to API.LoadPrincipal.
	PrincipalLoader middleware.PrincipalLoader
}

const (
	// A JSON API with a few base64 images in it. Anything bigger is refused
	// before it's read.
	maxBodyBytes = 10 << 20
	// Sign-in and the QR ordering endpoints are public and take small bodies.
	maxPublicBodyBytes = 64 << 10
)

func Setup(deps Dependencies) *gin.Engine {
	if deps.Config.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	loadPrincipal := deps.PrincipalLoader
	if loadPrincipal == nil {
		loadPrincipal = deps.API.LoadPrincipal
	}
	requireAuth := middleware.RequireAuth(deps.Config.JWTAccessSecret, loadPrincipal)

	r := gin.New()
	// Forwarded headers are never believed by default: anyone can send them,
	// and limits keyed on a spoofed address don't limit anything. A deployment
	// behind a proxy that sets a trustworthy header names it in CLIENT_IP_HEADER.
	if err := r.SetTrustedProxies(nil); err != nil {
		panic(err)
	}
	r.TrustedPlatform = deps.Config.ClientIPHeader

	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.SecurityHeaders(deps.Config.IsProduction()))
	r.Use(cors.New(cors.Config{
		AllowOrigins:     deps.Config.CORSAllowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Request-Id", middleware.BranchHeader},
		ExposeHeaders:    []string{"X-Request-Id"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	r.GET("/healthz", handlers.Health)

	// Per client address, on top of the per-account lockout. Refreshing gets
	// twice the room: every open tab and till does it on its own schedule.
	signInLimit := middleware.RateLimit(deps.Config.AuthRateLimitPerMinute)
	sessionLimit := middleware.RateLimit(deps.Config.AuthRateLimitPerMinute * 2)

	v1 := r.Group("/api/v1")
	{
		auth := v1.Group("/auth")
		auth.Use(middleware.MaxBodySize(maxPublicBodyBytes))
		{
			auth.POST("/login", signInLimit, deps.AuthHandler.Login)
			auth.POST("/pin-login", signInLimit, deps.AuthHandler.PINLogin)
			auth.POST("/refresh", sessionLimit, deps.AuthHandler.Refresh)
			auth.POST("/logout", sessionLimit, deps.AuthHandler.Logout)
			auth.GET("/me", requireAuth, deps.AuthHandler.Me)
			// The POS sign-in screen lists tills before anyone is signed in.
			auth.GET("/tills", deps.API.PublicTills)
			// Company name + logo, shown on the sign-in pages too.
			auth.GET("/branding", deps.API.GetBranding)
			auth.GET("/branding/logo", deps.API.GetBrandingLogo)
		}
		// QR table ordering: the customer's phone has no account. The table
		// token in the QR is the access check (see handlers/table_orders.go).
		public := v1.Group("/public")
		public.Use(middleware.MaxBodySize(maxPublicBodyBytes))
		{
			public.GET("/tables/:token/menu", deps.API.PublicTableMenu)
			public.POST("/tables/:token/orders", deps.API.PlacePublicTableOrder)
			public.GET("/table-orders/:token", deps.API.PublicTableOrderStatus)
		}
	}

	api := deps.API
	authed := v1.Group("")
	// Everything here is one company's data: a super admin (no company) is
	// refused, and every handler filters by the caller's company.
	authed.Use(middleware.MaxBodySize(maxBodyBytes), requireAuth, middleware.RequireCompany(), middleware.BranchScope())
	{
		perm := middleware.RequirePermission
		anyPerm := middleware.RequireAnyPermission
		// Reference data & settings
		authed.GET("/branches", api.ListBranches)
		authed.POST("/branches", perm("branch.create"), api.CreateBranch)
		authed.PUT("/branches/:id", perm("branch.update"), api.UpdateBranch)
		authed.DELETE("/branches/:id", perm("branch.delete"), api.DeleteBranch)

		authed.GET("/devices", perm("branch.view"), api.ListDevices)
		authed.POST("/devices", perm("branch.create"), api.CreateDevice)
		authed.PUT("/devices/:id", perm("branch.update"), api.UpdateDevice)
		authed.DELETE("/devices/:id", perm("branch.delete"), api.DeleteDevice)

		authed.GET("/dining-tables", perm("table.view"), api.ListDiningTables)
		authed.POST("/dining-tables", perm("table.create"), api.CreateDiningTable)
		authed.POST("/dining-tables/bulk", perm("table.create"), api.CreateDiningTablesBulk)
		authed.PUT("/dining-tables/:id", perm("table.update"), api.UpdateDiningTable)
		authed.DELETE("/dining-tables/:id", perm("table.delete"), api.DeleteDiningTable)

		authed.GET("/payment-methods", api.ListPaymentMethods)
		authed.POST("/payment-methods", perm("payment_method.create"), api.CreatePaymentMethod)
		authed.PUT("/payment-methods/:id", perm("payment_method.update"), api.UpdatePaymentMethod)
		authed.DELETE("/payment-methods/:id", perm("payment_method.delete"), api.DeletePaymentMethod)

		// Tax rates: everyone reads them (the till and the forms pick from
		// them); changing them, and where each place starts, is tax_rate.*.
		authed.GET("/tax-rates", api.ListTaxRates)
		authed.POST("/tax-rates", perm("tax_rate.create"), api.CreateTaxRate)
		authed.PUT("/tax-rates/defaults", perm("tax_rate.update"), api.UpdateTaxDefaults)
		authed.PUT("/tax-rates/:id", perm("tax_rate.update"), api.UpdateTaxRate)
		authed.DELETE("/tax-rates/:id", perm("tax_rate.delete"), api.DeleteTaxRate)

		authed.GET("/settings", api.GetSettings)
		authed.PUT("/settings", perm("settings.company"), api.UpdateSettings)
		authed.PUT("/settings/branding", perm("settings.company"), api.UpdateBranding)
		authed.GET("/exchange-rate", api.GetExchangeRate)
		authed.POST("/exchange-rate", perm("settings.rate"), api.SetExchangeRate)

		// The company's Telegram alerts and numbering (Settings > System) —
		// Owner-only by default. The company
		// reads how long it keeps its activity log; the super admin sets it.
		// Backups and the production reset cover every company, so they're
		// the super admin's (see /platform below).
		system := perm("system.manage")
		authed.GET("/settings/notifications", system, api.GetNotifySettings)
		authed.PUT("/settings/notifications", system, api.UpdateNotifySettings)
		authed.POST("/settings/notifications/test", system, api.SendTestAlert)
		authed.GET("/settings/numbering", system, api.GetNumberSequences)
		authed.PUT("/settings/numbering", system, api.UpdateNumberSequence)
		authed.GET("/settings/activity-log-retention", system, api.GetActivityLogRetention)
		authed.PUT("/settings/stock-policy", system, api.UpdateStockPolicy)

		// Catalog
		authed.GET("/categories", perm("category.view"), api.ListCategories)
		authed.POST("/categories", perm("category.create"), api.CreateCategory)
		authed.PUT("/categories/:id", perm("category.update"), api.UpdateCategory)
		authed.DELETE("/categories/:id", perm("category.delete"), api.DeleteCategory)
		authed.POST("/categories/import", perm("category.create"), api.ImportCategories)

		authed.GET("/suppliers", perm("supplier.view"), api.ListSuppliers)
		authed.POST("/suppliers", perm("supplier.create"), api.CreateSupplier)
		authed.PUT("/suppliers/:id", perm("supplier.update"), api.UpdateSupplier)
		authed.DELETE("/suppliers/:id", perm("supplier.delete"), api.DeleteSupplier)

		authed.GET("/products", perm("product.view"), api.ListProducts)
		authed.POST("/products", perm("product.create"), api.CreateProduct)
		authed.PUT("/products/:id", perm("product.update"), api.UpdateProduct)
		authed.DELETE("/products/:id", perm("product.delete"), api.DeleteProduct)
		authed.POST("/products/import", perm("product.create"), api.ImportProducts)

		authed.GET("/stock-movements", perm("inventory.view"), api.ListStockMovements)

		authed.GET("/purchase-orders", perm("purchase_order.view"), api.ListPurchaseOrders)
		authed.GET("/purchase-orders/:id", perm("purchase_order.view"), api.GetPurchaseOrder)
		authed.POST("/purchase-orders", perm("purchase_order.create"), api.CreatePurchaseOrder)
		authed.PUT("/purchase-orders/:id", perm("purchase_order.update"), api.UpdatePurchaseOrder)
		authed.DELETE("/purchase-orders/:id", perm("purchase_order.delete"), api.DeletePurchaseOrder)
		authed.POST("/purchase-orders/:id/receive", perm("purchase_order.receive"), api.ReceivePurchaseOrder)

		// People
		authed.GET("/customers", perm("customer.view"), api.ListCustomers)
		authed.POST("/customers", perm("customer.create"), api.CreateCustomer)
		authed.PUT("/customers/:id", perm("customer.update"), api.UpdateCustomer)
		authed.DELETE("/customers/:id", perm("customer.delete"), api.DeleteCustomer)
		authed.PUT("/settings/default-customer", perm("customer.update"), api.UpdateDefaultCustomer)

		authed.GET("/expenses", perm("expense.view"), api.ListExpenses)
		authed.POST("/expenses", perm("expense.create"), api.CreateExpense)
		authed.PUT("/expenses/:id", perm("expense.update"), api.UpdateExpense)
		authed.DELETE("/expenses/:id", perm("expense.delete"), api.DeleteExpense)

		authed.GET("/users", anyPerm("user.view", "role.view"), api.ListUsers)
		authed.GET("/users/:id", anyPerm("user.view", "role.view"), api.GetUser)
		authed.POST("/users", perm("user.create"), api.CreateUser)
		authed.PUT("/users/:id", perm("user.update"), api.UpdateUser)
		authed.DELETE("/users/:id", perm("user.delete"), api.DeleteUser)
		authed.POST("/users/:id/reset-pin", perm("user.update"), api.ResetUserPIN)
		authed.GET("/roles", anyPerm("user.view", "role.view"), api.ListRoles)
		authed.GET("/permissions", anyPerm("user.view", "role.view"), api.ListPermissions)
		authed.POST("/roles", perm("role.create"), api.CreateRole)
		authed.PUT("/roles/:id", perm("role.update"), api.UpdateRole)
		authed.DELETE("/roles/:id", perm("role.delete"), api.DeleteRole)

		// Shifts & sales
		authed.GET("/shifts/current", perm("shift.own"), api.GetCurrentShift)
		authed.POST("/shifts/open", perm("shift.own"), api.OpenShift)
		// A pay-in or pay-out: on your own shift, and only with its own permission.
		authed.POST("/shifts/:id/cash-movements", perm("shift.own"), perm("pos.cash_movement"), api.AddCashMovement)
		authed.POST("/shifts/:id/close", perm("shift.own"), api.CloseShift)
		authed.GET("/shifts", anyPerm("shift.own", "report.sales"), api.ListShifts)

		authed.GET("/sales", anyPerm("pos.sell", "report.sales"), api.ListSales)
		authed.GET("/sales/:id", anyPerm("pos.sell", "report.sales"), api.GetSale)
		authed.POST("/sales", perm("pos.sell"), api.CreateSale)
		authed.PUT("/sales/:id", perm("pos.edit_sale"), api.EditSale)
		authed.POST("/sales/:id/void", perm("pos.void"), api.VoidSale)
		// Back-office sales (invoices) — see handlers/invoices.go
		authed.GET("/invoices", perm("invoice.view"), api.ListInvoices)
		authed.GET("/invoices/:id", perm("invoice.view"), api.GetInvoice)
		authed.POST("/invoices", perm("invoice.create"), api.CreateInvoice)
		authed.PUT("/invoices/:id", perm("invoice.update"), api.UpdateInvoice)
		authed.POST("/invoices/:id/payments", perm("invoice.update"), api.AddInvoicePayment)
		authed.POST("/invoices/:id/cancel", perm("invoice.delete"), api.CancelInvoice)
		authed.DELETE("/invoices/:id/payments/:paymentId", perm("invoice.delete"), api.DeleteInvoicePayment)

		authed.GET("/table-orders", anyPerm("pos.sell", "report.sales"), api.ListTableOrders)
		authed.GET("/table-board", anyPerm("pos.sell", "report.sales"), api.ListTableBoard)
		authed.POST("/table-orders/:id/accept", perm("pos.sell"), api.AcceptTableOrder)
		authed.POST("/table-orders/:id/cancel", perm("pos.sell"), api.CancelTableOrder)
		authed.POST("/table-orders/move", perm("pos.sell"), api.MoveTableOrders)

		// Reports
		rep := authed.Group("/reports")
		{
			sales := perm("report.sales")
			rep.GET("/dashboard", sales, api.Dashboard)
			rep.GET("/staff", sales, api.StaffReport)
			rep.GET("/transactions", sales, api.TransactionsReport)
			rep.GET("/by-product", sales, api.ByProductReport)
			rep.GET("/by-cashier", sales, api.ByCashierReport)
			rep.GET("/payment-methods", sales, api.PaymentMethodsReport)
			rep.GET("/voids-refunds", sales, api.VoidsRefundsReport)
			rep.GET("/stock-movements", sales, api.StockMovementsReport)
			rep.GET("/profit-loss", perm("report.profit_loss"), api.ProfitLoss)
			rep.GET("/activity-log", perm("report.activity_log"), api.ActivityLogReport)
		}
	}

	// The super admin's: the companies themselves and the tools that cover
	// every company at once. No company user reaches these, the Owner neither.
	platform := v1.Group("/platform")
	platform.Use(middleware.MaxBodySize(maxBodyBytes), requireAuth, middleware.RequireSuperAdmin())
	{
		platform.GET("/companies", api.ListCompanies)
		platform.POST("/companies", api.CreateCompany)
		platform.PUT("/companies/:id", api.UpdateCompany)
		platform.POST("/companies/:id/owner", api.SetCompanyOwner)
		platform.POST("/companies/:id/activity-log/clear", api.ClearCompanyActivityLog)
		platform.GET("/companies/:id/branches", api.ListCompanyBranches)
		platform.PUT("/companies/:id/branches/:branch_id", api.SetCompanyBranchActive)
		platform.GET("/companies/:id/users", api.ListCompanyUsers)
		platform.PUT("/companies/:id/users/:user_id", api.SetCompanyUserActive)

		platform.GET("/settings/notifications", api.GetPlatformNotifySettings)
		platform.PUT("/settings/notifications", api.UpdatePlatformNotifySettings)
		platform.POST("/settings/notifications/test", api.SendTestAlert)
		// The line under every page, for every company (a copyright).
		platform.GET("/settings/footer", api.GetFooter)
		platform.PUT("/settings/footer", api.UpdateFooter)

		platform.GET("/backups", api.ListBackups)
		platform.PUT("/backups/settings", api.UpdateBackupSettings)
		platform.POST("/backups/run", api.RunBackupNow)
		platform.GET("/backups/:id/download", api.DownloadBackup)
		platform.DELETE("/backups/:id", api.DeleteBackup)

		platform.POST("/reset-for-production", api.ResetForProduction)
	}

	return r
}
