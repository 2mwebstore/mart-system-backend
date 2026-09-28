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
}

func Setup(deps Dependencies) *gin.Engine {
	if deps.Config.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(cors.New(cors.Config{
		AllowOrigins:     deps.Config.CORSAllowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Request-Id"},
		ExposeHeaders:    []string{"X-Request-Id"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	r.GET("/healthz", handlers.Health)

	v1 := r.Group("/api/v1")
	{
		auth := v1.Group("/auth")
		{
			auth.POST("/login", deps.AuthHandler.Login)
			auth.POST("/pin-login", deps.AuthHandler.PINLogin)
			auth.POST("/refresh", deps.AuthHandler.Refresh)
			auth.POST("/logout", deps.AuthHandler.Logout)
			auth.GET("/me", middleware.RequireAuth(deps.Config.JWTAccessSecret), deps.AuthHandler.Me)
			// The POS sign-in screen lists tills before anyone is signed in.
			auth.GET("/tills", deps.API.PublicTills)
		}
	}

	api := deps.API
	authed := v1.Group("")
	authed.Use(middleware.RequireAuth(deps.Config.JWTAccessSecret), middleware.BranchScope())
	{
		perm := middleware.RequirePermission
		anyPerm := middleware.RequireAnyPermission
		catalogWrite := anyPerm("inventory.adjust", "inventory.edit_price")

		// Reference data & settings
		authed.GET("/branches", api.ListBranches)
		authed.POST("/branches", perm("branch.manage"), api.CreateBranch)
		authed.PUT("/branches/:id", perm("branch.manage"), api.UpdateBranch)
		authed.DELETE("/branches/:id", perm("branch.manage"), api.DeleteBranch)

		authed.GET("/devices", perm("branch.manage"), api.ListDevices)
		authed.POST("/devices", perm("branch.manage"), api.CreateDevice)
		authed.PUT("/devices/:id", perm("branch.manage"), api.UpdateDevice)
		authed.DELETE("/devices/:id", perm("branch.manage"), api.DeleteDevice)

		authed.GET("/payment-methods", api.ListPaymentMethods)
		authed.POST("/payment-methods", perm("branch.manage"), api.CreatePaymentMethod)
		authed.PUT("/payment-methods/:id", perm("branch.manage"), api.UpdatePaymentMethod)
		authed.DELETE("/payment-methods/:id", perm("branch.manage"), api.DeletePaymentMethod)

		authed.GET("/settings", api.GetSettings)
		authed.PUT("/settings", perm("branch.manage"), api.UpdateSettings)
		authed.GET("/exchange-rate", api.GetExchangeRate)
		authed.POST("/exchange-rate", perm("settings.rate"), api.SetExchangeRate)

		// Catalog
		authed.GET("/categories", perm("inventory.view"), api.ListCategories)
		authed.POST("/categories", catalogWrite, api.CreateCategory)
		authed.PUT("/categories/:id", catalogWrite, api.UpdateCategory)
		authed.DELETE("/categories/:id", catalogWrite, api.DeleteCategory)

		authed.GET("/suppliers", perm("inventory.view"), api.ListSuppliers)
		authed.POST("/suppliers", catalogWrite, api.CreateSupplier)
		authed.PUT("/suppliers/:id", catalogWrite, api.UpdateSupplier)
		authed.DELETE("/suppliers/:id", catalogWrite, api.DeleteSupplier)

		authed.GET("/products", perm("inventory.view"), api.ListProducts)
		authed.POST("/products", catalogWrite, api.CreateProduct)
		authed.PUT("/products/:id", catalogWrite, api.UpdateProduct)
		authed.DELETE("/products/:id", catalogWrite, api.DeleteProduct)

		authed.GET("/stock-movements", perm("inventory.view"), api.ListStockMovements)

		authed.GET("/purchase-orders", perm("inventory.view"), api.ListPurchaseOrders)
		authed.POST("/purchase-orders", perm("inventory.purchase_order"), api.CreatePurchaseOrder)
		authed.PUT("/purchase-orders/:id", perm("inventory.purchase_order"), api.UpdatePurchaseOrder)
		authed.DELETE("/purchase-orders/:id", perm("inventory.purchase_order"), api.DeletePurchaseOrder)
		authed.POST("/purchase-orders/:id/receive", perm("inventory.receive"), api.ReceivePurchaseOrder)

		// People
		authed.GET("/customers", perm("customer.view"), api.ListCustomers)
		authed.POST("/customers", perm("customer.manage"), api.CreateCustomer)
		authed.PUT("/customers/:id", perm("customer.manage"), api.UpdateCustomer)
		authed.DELETE("/customers/:id", perm("customer.manage"), api.DeleteCustomer)

		authed.GET("/expenses", perm("expense.manage"), api.ListExpenses)
		authed.POST("/expenses", perm("expense.manage"), api.CreateExpense)
		authed.PUT("/expenses/:id", perm("expense.manage"), api.UpdateExpense)
		authed.DELETE("/expenses/:id", perm("expense.manage"), api.DeleteExpense)

		authed.GET("/users", anyPerm("user.manage", "role.manage"), api.ListUsers)
		authed.POST("/users", perm("user.manage"), api.CreateUser)
		authed.PUT("/users/:id", perm("user.manage"), api.UpdateUser)
		authed.POST("/users/:id/reset-pin", perm("user.manage"), api.ResetUserPIN)
		authed.GET("/roles", anyPerm("user.manage", "role.manage"), api.ListRoles)
		authed.GET("/permissions", anyPerm("user.manage", "role.manage"), api.ListPermissions)
		authed.POST("/roles", perm("role.manage"), api.CreateRole)
		authed.PUT("/roles/:id", perm("role.manage"), api.UpdateRole)
		authed.DELETE("/roles/:id", perm("role.manage"), api.DeleteRole)

		// Shifts & sales
		authed.GET("/shifts/current", perm("shift.own"), api.GetCurrentShift)
		authed.POST("/shifts/open", perm("shift.own"), api.OpenShift)
		authed.POST("/shifts/:id/cash-movements", perm("shift.own"), api.AddCashMovement)
		authed.POST("/shifts/:id/close", perm("shift.own"), api.CloseShift)
		authed.GET("/shifts", anyPerm("shift.own", "report.sales"), api.ListShifts)

		authed.GET("/sales", anyPerm("pos.sell", "report.sales"), api.ListSales)
		authed.POST("/sales", perm("pos.sell"), api.CreateSale)
		authed.POST("/sales/:id/void", perm("pos.void"), api.VoidSale)

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

	return r
}
