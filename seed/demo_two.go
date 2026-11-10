package main

import (
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

	"com-mart/backend/internal/models"
)

// demoTwoCode is the second demo company's code, typed at its till.
const demoTwoCode = "D2"

// seedDemoTwo is a small second company, a café, for checking by hand that
// two companies never see each other's data. Some of its codes match the
// main company's on purpose (SKU DRK-001, a customer's phone), since codes
// only have to be unique within a company.
func seedDemoTwo(db *gorm.DB) {
	company := companyByCode(db, "Demo Two", demoTwoCode)
	roles := setUpCompany(db, company.ID)
	branches := seedBranchList(db, company.ID, []models.Branch{
		{Name: "Riverside", Code: "RS", Address: "Sisowath Quay, Phnom Penh", Phone: "023 555 0201", Active: true},
	})
	riverside := branches["RS"]
	seedDevice(db, company.ID, riverside, "Till 1", "D2-TILL-1")
	seedCompanySettings(db, company.ID, map[string]string{
		"loyalty_points_per_usd": "2",
		"receipt_header":         "Demo Two Café",
		"receipt_footer":         "See you again soon!",
	}, false)
	users := seedUserList(db, company.ID, roles, []userDef{
		{"Keo Pisey", "demo2.owner", "Owner", true},
		{"Phan Rotha", "demo2.cashier", "Cashier", true},
	})
	seedExchangeRate(db, company.ID, users["demo2.owner"], 4050)
	seedUserBranchList(db, users, branches, map[string]string{"demo2.cashier": "RS"})

	var existing int64
	db.Model(&models.Product{}).Where("company_id = ?", company.ID).Count(&existing)
	if existing > 0 {
		return
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		return seedDemoTwoCatalog(tx, company.ID, riverside, users["demo2.owner"])
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to seed Demo Two")
	}
	log.Info().Msg("Demo Two seeded")
}

func seedDemoTwoCatalog(tx *gorm.DB, companyID uint64, branch models.Branch, owner models.User) error {
	categories := map[string]uint64{}
	for i, c := range []struct{ en, km string }{{"Coffee", "កាហ្វេ"}, {"Bakery", "នំ"}, {"Drinks", "ភេសជ្ជៈ"}} {
		cat := models.Category{CompanyID: companyID, NameEn: c.en, NameKm: c.km, Sort: i}
		if err := tx.Create(&cat).Error; err != nil {
			return err
		}
		categories[c.en] = cat.ID
	}
	supplier := models.Supplier{CompanyID: companyID, Name: "Riverside Roasters", Phone: "011 900 900", Contact: "Ms. Leakena", PaymentTerms: "Net 7"}
	if err := tx.Create(&supplier).Error; err != nil {
		return err
	}

	since := time.Now().AddDate(0, 0, -30)
	for _, p := range []struct {
		sku, name, km, category, unit string
		price, cost, stock            int64
	}{
		{"CF-001", "Iced latte", "ឡាតេទឹកកក", "Coffee", "cup", 250, 90, 200},
		{"CF-002", "Americano", "អាមេរិកាណូ", "Coffee", "cup", 200, 60, 200},
		{"BK-001", "Croissant", "នំក្រូសង់", "Bakery", "pcs", 180, 95, 40},
		// The same SKU as the main company's drinking water, priced its own way.
		{"DRK-001", "Bottled water", "ទឹកសុទ្ធ", "Drinks", "bottle", 50, 20, 120},
	} {
		cid, sid := categories[p.category], supplier.ID
		product := models.Product{
			CompanyID: companyID, SKU: p.sku, NameEn: p.name, NameKm: p.km, CategoryID: &cid, SupplierID: &sid,
			Unit: p.unit, Type: models.ProductStandard, Active: true,
		}
		if err := tx.Create(&product).Error; err != nil {
			return err
		}
		if err := tx.Create(&models.ProductPrice{ProductID: product.ID, PriceCents: p.price, CostCents: p.cost, EffectiveAt: since, CreatedAt: since}).Error; err != nil {
			return err
		}
		if err := tx.Create(&models.BranchStock{BranchID: branch.ID, ProductID: product.ID, Qty: p.stock, ReorderPoint: 10}).Error; err != nil {
			return err
		}
		if err := tx.Create(&models.StockMovement{
			BranchID: branch.ID, ProductID: product.ID, Type: models.StockMovementReceive, QtyChange: p.stock, BalanceAfter: p.stock,
			UnitCostCents: p.cost, ReferenceType: "OPENING", UserID: owner.ID, Note: "Opening stock", CreatedAt: since,
		}).Error; err != nil {
			return err
		}
	}
	// The same phone as one of the main company's customers.
	return tx.Create(&models.Customer{CompanyID: companyID, Name: "Sim Rachana", Phone: "012 111 222", Tier: models.CustomerTier("MEMBER")}).Error
}
