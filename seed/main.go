// Command seed populates dev/demo data (§10 of the build spec). It is
// idempotent — safe to run multiple times — matching on each table's natural
// unique key.
//
// Every install gets the permissions and a super admin, who manages the
// companies and sees none of their data. In development it also gets two
// companies to work in:
//   - the main demo company ("Com Mart", code CM; on a database that already
//     had data before companies existed, that data's company is used as is):
//     three branches, tills, the default roles, staff, a VAT 10% tax rate,
//     and 14 days of demo catalog, sales, shifts, purchase orders and
//     expenses;
//   - "Demo Two" (code D2): one branch, an Owner, a cashier, a till and a few
//     products, to check by hand that the two companies never see each
//     other's data.
//
// With APP_ENV=production there are no companies, no demo staff, no known
// passwords and no demo data: it creates the super admin only, whose
// password comes from SEED_SUPERADMIN_PASSWORD (and username from
// SEED_SUPERADMIN_USERNAME, "superadmin" by default). The super admin signs
// in and creates each company with its first Owner.
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"com-mart/backend/internal/config"
	"com-mart/backend/internal/database"
	"com-mart/backend/internal/models"
	"com-mart/backend/internal/services"
)

const devPassword = "Password123!"
const devPIN = "1234"

// devSuperAdminUsername is the super admin a development seed creates, with
// devPassword.
const devSuperAdminUsername = "root.admin"

// defaultSuperAdminUsername is the production super admin's username when
// SEED_SUPERADMIN_USERNAME isn't set.
const defaultSuperAdminUsername = "superadmin"

// seedOwnerUsername is the main demo company's Owner.
const seedOwnerUsername = "sok.dara"

// productionSuperAdmin reads the super admin's username and password for a
// production seed.
func productionSuperAdmin() (string, string, error) {
	password := os.Getenv("SEED_SUPERADMIN_PASSWORD")
	if len(password) < services.MinSuperAdminPasswordLen {
		return "", "", fmt.Errorf("SEED_SUPERADMIN_PASSWORD must be set to at least %d characters when APP_ENV=production", services.MinSuperAdminPasswordLen)
	}
	username := os.Getenv("SEED_SUPERADMIN_USERNAME")
	if username == "" {
		username = defaultSuperAdminUsername
	}
	return username, password, nil
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	// Checked before anything is written, so a refused seed leaves no trace.
	superUsername, superPassword := devSuperAdminUsername, devPassword
	if cfg.IsProduction() {
		if superUsername, superPassword, err = productionSuperAdmin(); err != nil {
			log.Fatal().Err(err).Msg("refusing to seed production")
		}
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

	if err := services.EnsurePermissions(db); err != nil {
		log.Fatal().Err(err).Msg("failed to seed permissions")
	}
	// An existing super admin keeps their password: running the seed again
	// can't reset a login (cmd/superadmin does that, on purpose).
	if _, err := services.UpsertSuperAdmin(db, superUsername, "Super admin", superPassword, false); err != nil {
		log.Fatal().Err(err).Msg("failed to seed the super admin")
	}
	if cfg.IsProduction() {
		log.Info().Str("username", superUsername).Msg("seed complete: sign in as the super admin and create the first company")
		return
	}

	company := seedMainCompany(db)
	roles := setUpCompany(db, company.ID)
	branches := seedBranches(db, company.ID)
	seedDevices(db, company.ID, branches["TK"])
	seedSettings(db, company.ID)
	seedTaxRates(db, company.ID)
	users := seedUsers(db, company.ID, roles)
	seedExchangeRate(db, company.ID, users[seedOwnerUsername], 4100)
	seedUserBranches(db, users, branches)
	seedDemoData(db, company.ID, branches, users)
	seedDemoTwo(db)

	log.Info().Msg("seed complete")
}

// seedMainCompany is the company the main demo data belongs to: the first
// company there is (on a database upgraded from before companies existed,
// the one migration 000030 made for its data), or a new "Com Mart".
func seedMainCompany(db *gorm.DB) models.Company {
	var company models.Company
	err := db.Order("id").Take(&company).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		company = models.Company{Name: "Com Mart", Code: "CM", Active: true}
		if err := db.Create(&company).Error; err != nil {
			log.Fatal().Err(err).Msg("failed to seed the main company")
		}
	} else if err != nil {
		log.Fatal().Err(err).Msg("failed to query companies")
	}
	return company
}

// companyByCode finds or creates a company.
func companyByCode(db *gorm.DB, name, code string) models.Company {
	var company models.Company
	err := db.Where("code = ?", code).Take(&company).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		company = models.Company{Name: name, Code: code, Active: true}
		if err := db.Create(&company).Error; err != nil {
			log.Fatal().Err(err).Str("company", code).Msg("failed to seed company")
		}
	} else if err != nil {
		log.Fatal().Err(err).Msg("failed to query company")
	}
	return company
}

// setUpCompany gives the company its default roles, numbering and payment
// methods, the same way the super admin's "new company" does.
func setUpCompany(db *gorm.DB, companyID uint64) map[string]models.Role {
	var roles map[string]models.Role
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		roles, err = services.SetUpCompany(tx, companyID)
		return err
	})
	if err != nil {
		log.Fatal().Err(err).Uint64("company", companyID).Msg("failed to set up company")
	}
	return roles
}

func seedBranches(db *gorm.DB, companyID uint64) map[string]models.Branch {
	defs := []models.Branch{
		{Name: "Toul Kork", Code: "TK", Address: "Toul Kork, Phnom Penh", Phone: "023 555 0101", Active: true},
		{Name: "BKK1", Code: "BKK1", Address: "Boeung Keng Kang 1, Phnom Penh", Phone: "023 555 0102", Active: true},
		{Name: "Sen Sok", Code: "SS", Address: "Sen Sok, Phnom Penh", Phone: "023 555 0103", Active: true},
	}
	return seedBranchList(db, companyID, defs)
}

func seedBranchList(db *gorm.DB, companyID uint64, defs []models.Branch) map[string]models.Branch {
	out := make(map[string]models.Branch, len(defs))
	for _, b := range defs {
		b.CompanyID = companyID
		var existing models.Branch
		err := db.Where("company_id = ? AND code = ?", companyID, b.Code).First(&existing).Error
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

func seedDevices(db *gorm.DB, companyID uint64, toulKork models.Branch) {
	for i := 1; i <= 3; i++ {
		seedDevice(db, companyID, toulKork, tillName(i), deviceKey(i))
	}
}

func seedDevice(db *gorm.DB, companyID uint64, branch models.Branch, name, key string) {
	var existing models.Device
	err := db.Where("company_id = ? AND device_key = ?", companyID, key).First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		d := models.Device{CompanyID: companyID, BranchID: branch.ID, Name: name, DeviceKey: key, Active: true}
		if err := db.Create(&d).Error; err != nil {
			log.Fatal().Err(err).Str("device", key).Msg("failed to seed device")
		}
	} else if err != nil {
		log.Fatal().Err(err).Msg("failed to query device")
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

func seedSettings(db *gorm.DB, companyID uint64) {
	seedCompanySettings(db, companyID, map[string]string{"loyalty_points_per_usd": "1"}, false)
}

// seedTaxRates gives the demo company one rate to pick, VAT 10%. No place
// starts on it (Settings › Tax rates sets that), so demo sales stay tax-free.
// A rate someone deleted stays deleted.
func seedTaxRates(db *gorm.DB, companyID uint64) {
	var count int64
	db.Unscoped().Model(&models.TaxRate{}).Where("company_id = ? AND name = ?", companyID, "VAT 10%").Count(&count)
	if count > 0 {
		return
	}
	if err := db.Create(&models.TaxRate{CompanyID: companyID, Name: "VAT 10%", Percent: 10, Active: true}).Error; err != nil {
		log.Fatal().Err(err).Msg("failed to seed tax rate")
	}
}

// seedCompanySettings writes a company's settings. Unless overwrite is set,
// a setting the company already has is left as it is.
func seedCompanySettings(db *gorm.DB, companyID uint64, values map[string]string, overwrite bool) {
	for k, v := range values {
		row := models.CompanySetting{CompanyID: companyID, Key: k, Value: v}
		onConflict := clause.OnConflict{DoNothing: true}
		if overwrite {
			onConflict = clause.OnConflict{DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"})}
		}
		if err := db.Clauses(onConflict).Create(&row).Error; err != nil {
			log.Fatal().Err(err).Str("key", k).Msg("failed to seed setting")
		}
	}
}

func seedExchangeRate(db *gorm.DB, companyID uint64, owner models.User, rate int64) {
	var count int64
	db.Model(&models.ExchangeRate{}).Where("company_id = ?", companyID).Count(&count)
	if count > 0 {
		return
	}
	if err := db.Create(&models.ExchangeRate{
		CompanyID:      companyID,
		RateRielPerUSD: rate,
		SetByUserID:    owner.ID,
		EffectiveAt:    time.Now(),
	}).Error; err != nil {
		log.Fatal().Err(err).Msg("failed to seed exchange rate")
	}
}

type userDef struct {
	fullName string
	username string
	roleName string
	active   bool
}

func seedUsers(db *gorm.DB, companyID uint64, roles map[string]models.Role) map[string]models.User {
	return seedUserList(db, companyID, roles, []userDef{
		{"Sok Dara", "sok.dara", "Owner", true},
		{"Chan Sreyneang", "chan.sreyneang", "Manager", true},
		{"Ouk Sothea", "ouk.sothea", "Manager", true},
		{"Kim Vannak", "kim.vannak", "Cashier", true},
		{"Lim Sophea", "lim.sophea", "Cashier", true},
		{"Pov Malis", "pov.malis", "Cashier", true},
		{"Ros Chenda", "ros.chenda", "Cashier", false},
		{"Heng Rithy", "heng.rithy", "Stock keeper", true},
		{"Meas Bopha", "meas.bopha", "Accountant", true},
	})
}

// seedUserList creates the company's demo staff, all with devPassword and
// devPIN. A username that already exists is left alone.
func seedUserList(db *gorm.DB, companyID uint64, roles map[string]models.Role, defs []userDef) map[string]models.User {
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
			company := companyID
			user = models.User{
				CompanyID:    &company,
				FullName:     d.fullName,
				Username:     d.username,
				RoleID:       roles[d.roleName].ID,
				PasswordHash: string(passwordHash),
				PINHash:      &pinHashStr,
				Active:       d.active,
			}
			if err := db.Omit("Role", "Branches").Create(&user).Error; err != nil {
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
	seedUserBranchList(db, users, branches, map[string]string{
		"chan.sreyneang": "TK",
		"ouk.sothea":     "BKK1",
		"kim.vannak":     "TK",
		"lim.sophea":     "TK",
		"pov.malis":      "TK",
		"ros.chenda":     "TK",
		"heng.rithy":     "TK",
		"meas.bopha":     "TK",
	})
}

func seedUserBranchList(db *gorm.DB, users map[string]models.User, branches map[string]models.Branch, assignments map[string]string) {
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
