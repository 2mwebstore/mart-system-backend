// Command seed populates dev/demo data: branches, tills, roles &
// permissions, and users (§10 of the build spec). It is idempotent — safe
// to run multiple times — matching on each table's natural unique key.
//
// Products, customers, 14 days of sales/shifts/expenses (also listed in
// §10) are added by the seed commands of the phases that introduce those
// modules (Phase 3 onward), once the underlying tables have real write
// paths to seed through consistently with the app's own invariants
// (e.g. stock movements must back every branch_stock row).
package main

import (
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"com-mart/backend/internal/config"
	"com-mart/backend/internal/database"
	"com-mart/backend/internal/models"
)

const devPassword = "Password123!"
const devPIN = "1234"

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	// Seeding an unmigrated database fails halfway with confusing errors, so
	// bring the schema up to date first (a no-op when it already is).
	if err := database.Migrate(cfg); err != nil {
		log.Fatal().Err(err).Msg("failed to run database migrations")
	}

	db, err := database.Connect(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}

	branches := seedBranches(db)
	seedDevices(db, branches["TK"])
	seedSettings(db)

	roles := seedRolesAndPermissions(db)
	users := seedUsers(db, roles)
	seedExchangeRate(db, users["sok.dara"])
	seedUserBranches(db, users, branches)
	seedDemoData(db, branches, users)

	log.Info().Msg("seed complete")
}

func seedBranches(db *gorm.DB) map[string]models.Branch {
	defs := []models.Branch{
		{Name: "Toul Kork", Code: "TK", Address: "Toul Kork, Phnom Penh", Phone: "023 555 0101", Active: true},
		{Name: "BKK1", Code: "BKK1", Address: "Boeung Keng Kang 1, Phnom Penh", Phone: "023 555 0102", Active: true},
		{Name: "Sen Sok", Code: "SS", Address: "Sen Sok, Phnom Penh", Phone: "023 555 0103", Active: true},
	}

	out := make(map[string]models.Branch, len(defs))
	for _, b := range defs {
		var existing models.Branch
		err := db.Where("code = ?", b.Code).First(&existing).Error
		if err == gorm.ErrRecordNotFound {
			if err := db.Create(&b).Error; err != nil {
				log.Fatal().Err(err).Str("branch", b.Code).Msg("failed to seed branch")
			}
			out[b.Code] = b
		} else if err != nil {
			log.Fatal().Err(err).Msg("failed to query branch")
		} else {
			out[b.Code] = existing
		}
	}
	return out
}

func seedDevices(db *gorm.DB, toulKork models.Branch) {
	for i := 1; i <= 3; i++ {
		key := deviceKey(i)
		var existing models.Device
		err := db.Where("device_key = ?", key).First(&existing).Error
		if err == gorm.ErrRecordNotFound {
			d := models.Device{
				BranchID:  toulKork.ID,
				Name:      tillName(i),
				DeviceKey: key,
				Active:    true,
			}
			if err := db.Create(&d).Error; err != nil {
				log.Fatal().Err(err).Str("device", key).Msg("failed to seed device")
			}
		} else if err != nil {
			log.Fatal().Err(err).Msg("failed to query device")
		}
	}
}

func deviceKey(i int) string {
	names := []string{"", "TK-TILL-1", "TK-TILL-2", "TK-TILL-3"}
	return names[i]
}

func tillName(i int) string {
	names := []string{"", "Till 1", "Till 2", "Till 3"}
	return names[i]
}

func seedSettings(db *gorm.DB) {
	defaults := map[string]string{
		"loyalty_points_per_usd": "1",
		"currency_rounding_riel": "100",
	}
	for k, v := range defaults {
		var existing models.Setting
		err := db.Where("`key` = ?", k).First(&existing).Error
		if err == gorm.ErrRecordNotFound {
			if err := db.Create(&models.Setting{Key: k, Value: v}).Error; err != nil {
				log.Fatal().Err(err).Str("key", k).Msg("failed to seed setting")
			}
		}
	}
}

func seedExchangeRate(db *gorm.DB, owner models.User) {
	var count int64
	db.Model(&models.ExchangeRate{}).Count(&count)
	if count > 0 {
		return
	}
	if err := db.Create(&models.ExchangeRate{
		RateRielPerUSD: 4100,
		SetByUserID:    owner.ID,
		EffectiveAt:    time.Now(),
	}).Error; err != nil {
		log.Fatal().Err(err).Msg("failed to seed exchange rate")
	}
}

type roleDef struct {
	name          string
	description   string
	locked        bool
	permissions   []string
	maxDiscount   int
	refundNoApprovalCents int64
}

func seedRolesAndPermissions(db *gorm.DB) map[string]models.Role {
	permissionDefs := []models.Permission{
		{Key: "pos.sell", Group: "pos", Description: "Sell at the till"},
		{Key: "pos.discount", Group: "pos", Description: "Apply a discount"},
		{Key: "pos.void", Group: "pos", Description: "Void a sale"},
		{Key: "pos.edit_sale", Group: "pos", Description: "Edit a sale's items (still-open shift only)"},
		{Key: "pos.refund", Group: "pos", Description: "Refund a sale"},
		{Key: "pos.open_drawer", Group: "pos", Description: "Open the cash drawer without a sale"},
		{Key: "shift.own", Group: "shift", Description: "Open/close own shift"},
		{Key: "shift.close_others", Group: "shift", Description: "Close another cashier's shift"},
		{Key: "shift.see_expected", Group: "shift", Description: "See expected cash before counting"},
		{Key: "inventory.view", Group: "inventory", Description: "View inventory"},
		{Key: "inventory.receive", Group: "inventory", Description: "Receive stock"},
		{Key: "inventory.adjust", Group: "inventory", Description: "Adjust/write off/count stock"},
		{Key: "inventory.edit_price", Group: "inventory", Description: "Edit product price/cost"},
		// Split out from the general inventory.adjust/inventory.edit_price
		// catalog-write gate so a role can be allowed to add products but
		// not change existing ones, or vice versa — categories and suppliers
		// still just use inventory.adjust/inventory.edit_price (see
		// routes.go's catalogWrite).
		{Key: "inventory.product_create", Group: "inventory", Description: "Create new products (and add variants)"},
		{Key: "inventory.product_edit", Group: "inventory", Description: "Edit or delete existing products"},
		{Key: "inventory.purchase_order", Group: "inventory", Description: "Manage purchase orders"},
		{Key: "inventory.transfer", Group: "inventory", Description: "Transfer stock between branches"},
		{Key: "report.sales", Group: "report", Description: "View sales reports"},
		{Key: "report.profit_loss", Group: "report", Description: "View profit & loss"},
		{Key: "report.export", Group: "report", Description: "Export reports"},
		{Key: "report.activity_log", Group: "report", Description: "View the activity log"},
		{Key: "customer.view", Group: "customer", Description: "View customers"},
		{Key: "customer.manage", Group: "customer", Description: "Create/edit customers"},
		{Key: "user.manage", Group: "user", Description: "Manage users"},
		{Key: "role.manage", Group: "user", Description: "Manage roles & permissions"},
		{Key: "settings.rate", Group: "user", Description: "Change the exchange rate"},
		{Key: "branch.manage", Group: "user", Description: "Manage branches"},
		{Key: "expense.manage", Group: "user", Description: "Manage expenses"},
		{Key: "system.manage", Group: "user", Description: "Manage Telegram alerts and database backups"},
		// Deliberately not part of any grouped permission set below (posAll,
		// inventoryOps, etc.) — only allKeys (the Owner role) grants it, so
		// enabling something else for a Manager can never accidentally also
		// hand them a full data wipe. See ResetForProduction's doc comment.
		{Key: "system.reset", Group: "user", Description: "Wipe all data for a fresh production setup"},
	}

	permsByKey := make(map[string]models.Permission, len(permissionDefs))
	for _, p := range permissionDefs {
		var existing models.Permission
		err := db.Where("`key` = ?", p.Key).First(&existing).Error
		if err == gorm.ErrRecordNotFound {
			if err := db.Create(&p).Error; err != nil {
				log.Fatal().Err(err).Str("permission", p.Key).Msg("failed to seed permission")
			}
			permsByKey[p.Key] = p
		} else if err != nil {
			log.Fatal().Err(err).Msg("failed to query permission")
		} else {
			permsByKey[p.Key] = existing
		}
	}

	allKeys := make([]string, 0, len(permissionDefs))
	for _, p := range permissionDefs {
		allKeys = append(allKeys, p.Key)
	}

	posAll := []string{"pos.sell", "pos.discount", "pos.void", "pos.edit_sale", "pos.refund", "pos.open_drawer"}
	shiftAll := []string{"shift.own", "shift.close_others", "shift.see_expected"}
	inventoryOps := []string{
		"inventory.view", "inventory.receive", "inventory.adjust", "inventory.product_create", "inventory.product_edit",
		"inventory.purchase_order", "inventory.transfer",
	}
	reportAll := []string{"report.sales", "report.profit_loss", "report.export", "report.activity_log"}

	defs := []roleDef{
		{
			name: "Owner", description: "Full access to all branches and settings.", locked: true,
			permissions: allKeys, maxDiscount: 100, refundNoApprovalCents: 1 << 40,
		},
		{
			name: "Manager", description: "Runs a branch: approvals, shifts, stock.",
			permissions: concat(posAll, shiftAll, inventoryOps, []string{"report.sales", "report.export", "report.activity_log", "customer.view", "customer.manage", "expense.manage"}),
			maxDiscount: 20, refundNoApprovalCents: 2000,
		},
		{
			name: "Cashier", description: "Sells at the till and runs own shift.",
			permissions: []string{"pos.sell", "shift.own", "inventory.view", "customer.view", "customer.manage"},
			maxDiscount: 5, refundNoApprovalCents: 500,
		},
		{
			name: "Stock keeper", description: "Receives, counts and moves stock.",
			permissions: inventoryOps,
		},
		{
			name: "Accountant", description: "Reads reports and profit & loss.",
			permissions: concat([]string{"inventory.view"}, reportAll, []string{"expense.manage"}),
		},
	}

	out := make(map[string]models.Role, len(defs))
	for _, d := range defs {
		var role models.Role
		err := db.Where("name = ?", d.name).First(&role).Error
		if err == gorm.ErrRecordNotFound {
			role = models.Role{Name: d.name, Description: d.description, IsLocked: d.locked}
			if err := db.Create(&role).Error; err != nil {
				log.Fatal().Err(err).Str("role", d.name).Msg("failed to seed role")
			}
		} else if err != nil {
			log.Fatal().Err(err).Msg("failed to query role")
		}

		var perms []models.Permission
		for _, key := range d.permissions {
			perms = append(perms, permsByKey[key])
		}
		if err := db.Model(&role).Association("Permissions").Replace(perms); err != nil {
			log.Fatal().Err(err).Str("role", d.name).Msg("failed to assign role permissions")
		}

		var limit models.RoleLimit
		err = db.Where("role_id = ?", role.ID).First(&limit).Error
		if err == gorm.ErrRecordNotFound {
			limit = models.RoleLimit{RoleID: role.ID, MaxDiscountPercent: d.maxDiscount, RefundWithoutApprovalCents: d.refundNoApprovalCents}
			if err := db.Create(&limit).Error; err != nil {
				log.Fatal().Err(err).Str("role", d.name).Msg("failed to seed role limit")
			}
		}

		out[d.name] = role
	}
	return out
}

func concat(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

type userDef struct {
	fullName string
	username string
	roleName string
	active   bool
}

func seedUsers(db *gorm.DB, roles map[string]models.Role) map[string]models.User {
	defs := []userDef{
		{"Sok Dara", "sok.dara", "Owner", true},
		{"Chan Sreyneang", "chan.sreyneang", "Manager", true},
		{"Ouk Sothea", "ouk.sothea", "Manager", true},
		{"Kim Vannak", "kim.vannak", "Cashier", true},
		{"Lim Sophea", "lim.sophea", "Cashier", true},
		{"Pov Malis", "pov.malis", "Cashier", true},
		{"Ros Chenda", "ros.chenda", "Cashier", false},
		{"Heng Rithy", "heng.rithy", "Stock keeper", true},
		{"Meas Bopha", "meas.bopha", "Accountant", true},
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(devPassword), bcrypt.DefaultCost)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to hash dev password")
	}
	pinHash, err := bcrypt.GenerateFromPassword([]byte(devPIN), bcrypt.DefaultCost)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to hash dev pin")
	}
	pinHashStr := string(pinHash)

	out := make(map[string]models.User, len(defs))
	for _, d := range defs {
		var user models.User
		err := db.Where("username = ?", d.username).First(&user).Error
		if err == gorm.ErrRecordNotFound {
			user = models.User{
				FullName:     d.fullName,
				Username:     d.username,
				RoleID:       roles[d.roleName].ID,
				PasswordHash: string(passwordHash),
				PINHash:      &pinHashStr,
				Active:       d.active,
			}
			if err := db.Create(&user).Error; err != nil {
				log.Fatal().Err(err).Str("user", d.username).Msg("failed to seed user")
			}
		} else if err != nil {
			log.Fatal().Err(err).Msg("failed to query user")
		}
		out[d.username] = user
	}
	return out
}

func seedUserBranches(db *gorm.DB, users map[string]models.User, branches map[string]models.Branch) {
	assignments := map[string]string{
		"chan.sreyneang": "TK",
		"ouk.sothea":     "BKK1",
		"kim.vannak":     "TK",
		"lim.sophea":     "TK",
		"pov.malis":      "TK",
		"ros.chenda":     "TK",
		"heng.rithy":     "TK",
		"meas.bopha":     "TK",
	}
	for username, branchCode := range assignments {
		user := users[username]
		branch := branches[branchCode]
		var existing models.UserBranch
		err := db.Where("user_id = ? AND branch_id = ?", user.ID, branch.ID).First(&existing).Error
		if err == gorm.ErrRecordNotFound {
			if err := db.Create(&models.UserBranch{UserID: user.ID, BranchID: branch.ID}).Error; err != nil {
				log.Fatal().Err(err).Str("user", username).Msg("failed to seed user_branches")
			}
		}
	}
}
