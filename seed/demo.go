package main

import (
	"fmt"
	"math/rand"
	"sort"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"com-mart/backend/internal/models"
	"com-mart/backend/internal/utils"
)

// Demo data: catalog, customers, purchase orders, expenses, payment methods
// and 14 days of shifts + sales. Only written into an empty database (no
// products yet), so re-running the seed never duplicates or overwrites work
// done in the app. Sales are generated with the same rules the real POST
// /sales uses (prices from the catalog, change via utils.CalculateChange,
// stock booked through stock_movements) so the reports agree with stock.

type demoProduct struct {
	sku, barcode, name string
	category           string // key into categories
	supplier           string
	unit               string
	priceCents, cost   int64
	stock              map[string]int64 // branch code -> qty on hand *now*
	reorder            int64
	// parent, when set, makes this row a variant of the product with that
	// SKU (which must appear earlier in demoProducts) — category, supplier,
	// unit and image are then inherited from the parent, same as a real
	// variant created through the app, and variant is appended to the
	// parent's name (English and Khmer) the same way composeVariantName
	// does in internal/handlers/catalog.go.
	parent, variant string
}

// Khmer product names, keyed by SKU.
var demoProductKm = map[string]string{
	"DRK-001": "ទឹកបរិសុទ្ធ 500ml", "DRK-002": "កាហ្វេកំប៉ុង 240ml", "DRK-003": "ភេសជ្ជៈថាមពល 250ml", "DRK-004": "ទឹកដោះសណ្តែក 250ml",
	"PAN-001": "មីកំប៉ុងរសមាន់", "PAN-002": "អង្ករម្លិះ 5kg", "PAN-003": "ទឹកត្រីស្រស់ 700ml", "PAN-004": "ស៊ុតមាន់ 10 គ្រាប់", "PAN-005": "នំបុ័ង",
	"SNK-001": "ដំឡូងបំពង់ 60g", "HHD-001": "ទឹកលាងចាន 750ml", "PSC-001": "ថ្នាំដុសធ្មេញ 150g",
	"HHD-002": "ថង់សំរាម",
}

var demoCategories = []struct{ en, km string }{
	{"Drinks", "ភេសជ្ជៈ"}, {"Pantry", "គ្រឿងទេស"}, {"Snacks", "អាហារសម្រន់"},
	{"Household", "របស់ប្រើប្រាស់ក្នុងផ្ទះ"}, {"Personal care", "ថែទាំសុខភាព"},
}

var demoSuppliers = []struct{ name, phone, contact, terms string }{
	{"Mekong FMCG Supply", "012 345 678", "Mr. Vibol", "Net 30"},
	{"Angkor Beverage Co.", "017 222 333", "Ms. Sreypov", "Net 15"},
	{"Golden Delta Distribution", "096 888 111", "Mr. Sokha", "COD"},
}

var demoProducts = []demoProduct{
	{sku: "DRK-001", barcode: "8850001001", name: "Drinking water 500ml", category: "Drinks", supplier: "Angkor Beverage Co.", unit: "bottle", priceCents: 30, cost: 18, stock: map[string]int64{"TK": 240, "BKK1": 96, "SS": 12}, reorder: 48},
	{sku: "DRK-002", barcode: "8850001002", name: "Iced coffee can 240ml", category: "Drinks", supplier: "Angkor Beverage Co.", unit: "can", priceCents: 75, cost: 48, stock: map[string]int64{"TK": 84, "BKK1": 40, "SS": 6}, reorder: 24},
	{sku: "DRK-003", barcode: "8850001003", name: "Energy drink 250ml", category: "Drinks", supplier: "Angkor Beverage Co.", unit: "can", priceCents: 70, cost: 44, stock: map[string]int64{"TK": 60, "BKK1": 18, "SS": 30}, reorder: 24},
	{sku: "DRK-004", barcode: "8850001004", name: "Soy milk 250ml", category: "Drinks", supplier: "Angkor Beverage Co.", unit: "box", priceCents: 55, cost: 35, stock: map[string]int64{"TK": 72, "BKK1": 20, "SS": 18}, reorder: 24},
	{sku: "PAN-001", barcode: "8850002001", name: "Instant noodles chicken", category: "Pantry", supplier: "Mekong FMCG Supply", unit: "pack", priceCents: 45, cost: 28, stock: map[string]int64{"TK": 150, "BKK1": 60, "SS": 40}, reorder: 60},
	{sku: "PAN-002", barcode: "8850002002", name: "Jasmine rice 5kg", category: "Pantry", supplier: "Golden Delta Distribution", unit: "bag", priceCents: 650, cost: 510, stock: map[string]int64{"TK": 22, "BKK1": 9, "SS": 4}, reorder: 10},
	{sku: "PAN-003", barcode: "8850002003", name: "Fish sauce 700ml", category: "Pantry", supplier: "Golden Delta Distribution", unit: "bottle", priceCents: 160, cost: 110, stock: map[string]int64{"TK": 34, "BKK1": 12, "SS": 8}, reorder: 15},
	{sku: "PAN-004", barcode: "8850002004", name: "Eggs 10 pack", category: "Pantry", supplier: "Golden Delta Distribution", unit: "pack", priceCents: 180, cost: 135, stock: map[string]int64{"TK": 26, "BKK1": 6, "SS": 14}, reorder: 20},
	{sku: "PAN-005", barcode: "8850002005", name: "Bread loaf", category: "Pantry", supplier: "Mekong FMCG Supply", unit: "pcs", priceCents: 120, cost: 80, stock: map[string]int64{"TK": 18, "BKK1": 8, "SS": 5}, reorder: 15},
	{sku: "SNK-001", barcode: "8850003001", name: "Potato chips 60g", category: "Snacks", supplier: "Mekong FMCG Supply", unit: "pack", priceCents: 90, cost: 55, stock: map[string]int64{"TK": 96, "BKK1": 40, "SS": 20}, reorder: 30},
	{sku: "HHD-001", barcode: "8850004001", name: "Dish soap 750ml", category: "Household", supplier: "Mekong FMCG Supply", unit: "bottle", priceCents: 195, cost: 140, stock: map[string]int64{"TK": 28, "BKK1": 10, "SS": 6}, reorder: 12},
	{sku: "PSC-001", barcode: "8850005001", name: "Toothpaste 150g", category: "Personal care", supplier: "Mekong FMCG Supply", unit: "tube", priceCents: 140, cost: 95, stock: map[string]int64{"TK": 20, "BKK1": 7, "SS": 5}, reorder: 12},

	// A product with variants, to exercise/showcase that feature in the demo
	// data — HHD-002 is the parent (Sort/reorder point set at the family
	// level: 3 sizes, each independently priced/stocked/barcoded), with
	// three S/M/L variants. category/supplier/unit are inherited from the
	// parent below, same as a real variant.
	{sku: "HHD-002", barcode: "8850004002", name: "Trash bags", category: "Household", supplier: "Mekong FMCG Supply", unit: "roll", priceCents: 220, cost: 140, stock: map[string]int64{"TK": 40, "BKK1": 15, "SS": 8}, reorder: 15},
	{sku: "HHD-002-S", barcode: "8850004003", parent: "HHD-002", variant: "Small", priceCents: 180, cost: 110, stock: map[string]int64{"TK": 30, "BKK1": 10, "SS": 6}, reorder: 10},
	{sku: "HHD-002-M", barcode: "8850004004", parent: "HHD-002", variant: "Medium", priceCents: 220, cost: 140, stock: map[string]int64{"TK": 35, "BKK1": 12, "SS": 7}, reorder: 12},
	{sku: "HHD-002-L", barcode: "8850004005", parent: "HHD-002", variant: "Large", priceCents: 260, cost: 170, stock: map[string]int64{"TK": 25, "BKK1": 9, "SS": 5}, reorder: 10},
}

func seedDemoData(db *gorm.DB, branches map[string]models.Branch, users map[string]models.User) {
	var existing int64
	db.Model(&models.Product{}).Count(&existing)
	if existing > 0 {
		log.Info().Msg("catalog already present — skipping demo data")
		return
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return seedDemoTx(tx, branches, users) }); err != nil {
		log.Fatal().Err(err).Msg("failed to seed demo data")
	}
	log.Info().Msg("demo data seeded")
}

func seedDemoTx(tx *gorm.DB, branches map[string]models.Branch, users map[string]models.User) error {
	owner := users["sok.dara"]

	// Extra tills so the other branches can have shift history too.
	extra := []struct{ branch, name, key string }{{"BKK1", "Till 1", "BKK1-TILL-1"}, {"SS", "Till 1", "SS-TILL-1"}}
	for _, e := range extra {
		var d models.Device
		if err := tx.Where("device_key = ?", e.key).First(&d).Error; err == gorm.ErrRecordNotFound {
			if err := tx.Create(&models.Device{BranchID: branches[e.branch].ID, Name: e.name, DeviceKey: e.key, Active: true}).Error; err != nil {
				return err
			}
		}
	}

	for _, s := range []models.Setting{
		{Key: "receipt_header", Value: "Com Mart"},
		{Key: "receipt_footer", Value: "Thank you for shopping with us! សូមអរគុណ"},
	} {
		// Save() on an existing `key` issues a full-column UPDATE using this
		// freshly-built struct's zero-value CreatedAt, which MySQL's strict
		// mode rejects as an invalid '0000-00-00' date. OnConflict only
		// touches the columns actually being seeded, leaving created_at (and
		// the row entirely, if it's untouched otherwise) alone — same
		// pattern as BackupService.SaveActivityLogRetentionMonths.
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
		}).Create(&s).Error; err != nil {
			return err
		}
	}
	for _, p := range []models.PaymentMethodRow{
		{Name: "Cash", Type: "CASH", FeePercent: 0, Enabled: true},
		{Name: "KHQR (Bakong)", Type: "KHQR", FeePercent: 0, Enabled: true},
		{Name: "Card — Visa / Mastercard", Type: "CARD", FeePercent: 2.5, Enabled: true},
		{Name: "Card — UPI", Type: "CARD", FeePercent: 1.8, Enabled: false},
	} {
		if err := tx.Create(&p).Error; err != nil {
			return err
		}
	}

	catID := map[string]uint64{}
	for i, c := range demoCategories {
		cat := models.Category{NameEn: c.en, NameKm: c.km, Sort: i}
		if err := tx.Create(&cat).Error; err != nil {
			return err
		}
		catID[c.en] = cat.ID
	}
	supID := map[string]uint64{}
	for _, s := range demoSuppliers {
		sup := models.Supplier{Name: s.name, Phone: s.phone, Contact: s.contact, PaymentTerms: s.terms}
		if err := tx.Create(&sup).Error; err != nil {
			return err
		}
		supID[s.name] = sup.ID
	}

	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, -13)
	prodID := map[string]uint64{}
	byID := map[uint64]demoProduct{}
	bySKU := map[string]models.Product{}
	for _, dp := range demoProducts {
		bc := dp.barcode
		p := models.Product{SKU: dp.sku, Barcode: &bc, Active: true}
		if dp.parent != "" {
			// A variant: category/supplier/unit/type/image are inherited
			// from the parent (already created earlier in this slice), same
			// rule as a real variant created through the app — see
			// composeVariantName in internal/handlers/catalog.go.
			parent := bySKU[dp.parent]
			p.ParentProductID, p.VariantName = &parent.ID, dp.variant
			p.NameEn = parent.NameEn + " — " + dp.variant
			if parent.NameKm != "" {
				p.NameKm = parent.NameKm + " — " + dp.variant
			}
			p.CategoryID, p.SupplierID, p.Unit, p.Type, p.ImageURL = parent.CategoryID, parent.SupplierID, parent.Unit, parent.Type, parent.ImageURL
		} else {
			cid, sid := catID[dp.category], supID[dp.supplier]
			p.NameEn, p.NameKm, p.CategoryID, p.SupplierID, p.Unit, p.Type = dp.name, demoProductKm[dp.sku], &cid, &sid, dp.unit, models.ProductStandard
		}
		if err := tx.Create(&p).Error; err != nil {
			return err
		}
		bySKU[dp.sku] = p
		if err := tx.Create(&models.ProductPrice{ProductID: p.ID, PriceCents: dp.priceCents, CostCents: dp.cost, EffectiveAt: start.AddDate(0, 0, -30), CreatedAt: start}).Error; err != nil {
			return err
		}
		prodID[dp.sku] = p.ID
		byID[p.ID] = dp
	}

	customers := []struct {
		name, phone, tier string
		points            int64
	}{
		{"Chea Sopheak", "012 111 222", "GOLD", 1280}, {"Ly Dalin", "096 222 333", "MEMBER", 340}, {"Nguon Panha", "070 333 444", "MEMBER", 95},
		{"Sok Chenda", "017 444 555", "GOLD", 2110}, {"Vong Ratana", "087 555 666", "MEMBER", 60}, {"Khun Sreymom", "093 666 777", "MEMBER", 410},
	}
	var custIDs []uint64
	for _, c := range customers {
		cu := models.Customer{Name: c.name, Phone: c.phone, Tier: models.CustomerTier(c.tier), Points: c.points}
		if err := tx.Create(&cu).Error; err != nil {
			return err
		}
		custIDs = append(custIDs, cu.ID)
	}

	// ---- Sales history ------------------------------------------------------
	type saleLine struct {
		pid   uint64
		qty   int64
		price int64
		cost  int64
	}
	type saleSpec struct {
		branch   string
		day      time.Time
		at       time.Time
		lines    []saleLine
		method   string
		cust     *uint64
		voided   bool
		tillIdx  int
		cashierU string
	}
	rng := rand.New(rand.NewSource(42))
	weights := map[string]float64{"TK": 1, "BKK1": 0.58, "SS": 0.35}
	hourCurve := []int{0, 0, 0, 0, 0, 0, 1, 3, 6, 9, 11, 13, 15, 13, 10, 9, 8, 11, 15, 17, 13, 7, 3, 1}
	var hourPool []int
	for h, w := range hourCurve {
		for i := 0; i < w; i++ {
			hourPool = append(hourPool, h)
		}
	}
	tillCount := map[string]int{"TK": 3, "BKK1": 1, "SS": 1}
	cashiers := map[string][]string{"TK": {"kim.vannak", "lim.sophea", "pov.malis"}, "BKK1": {"ouk.sothea"}, "SS": {"sok.dara"}}
	var specs []*saleSpec
	pids := make([]uint64, 0, len(prodID))
	for _, dp := range demoProducts {
		pids = append(pids, prodID[dp.sku])
	}
	for d := 0; d < 14; d++ {
		day := start.AddDate(0, 0, d)
		weekend := day.Weekday() == time.Saturday || day.Weekday() == time.Sunday
		for code := range branches {
			n := int(float64(28+rng.Intn(14)) * weights[code])
			if weekend {
				n = int(float64(n) * 1.15)
			}
			for i := 0; i < n; i++ {
				at := day.Add(time.Duration(hourPool[rng.Intn(len(hourPool))])*time.Hour + time.Duration(rng.Intn(60))*time.Minute + time.Duration(rng.Intn(60))*time.Second)
				if at.After(now) {
					continue // nothing rung up in the future
				}
				spec := &saleSpec{branch: code, day: day, at: at, tillIdx: rng.Intn(tillCount[code])}
				spec.cashierU = cashiers[code][spec.tillIdx%len(cashiers[code])]
				used := map[uint64]bool{}
				for k := 0; k < 1+rng.Intn(5); k++ {
					pid := pids[rng.Intn(len(pids))]
					if used[pid] {
						continue
					}
					used[pid] = true
					dp := byID[pid]
					spec.lines = append(spec.lines, saleLine{pid, int64(1 + rng.Intn(3)), dp.priceCents, dp.cost})
				}
				switch r := rng.Float64(); {
				case r < 0.52:
					spec.method = "CASH"
				case r < 0.88:
					spec.method = "KHQR"
				default:
					spec.method = "CARD"
				}
				if rng.Float64() < 0.12 {
					c := custIDs[rng.Intn(len(custIDs))]
					spec.cust = &c
				}
				spec.voided = rng.Float64() < 0.025
				specs = append(specs, spec)
			}
		}
	}
	sort.SliceStable(specs, func(i, j int) bool { return specs[i].at.Before(specs[j].at) })

	// Opening stock = what's on hand now + everything sold since.
	sold := map[string]int64{} // "branchCode|pid"
	for _, s := range specs {
		if s.voided {
			continue
		}
		for _, l := range s.lines {
			sold[fmt.Sprintf("%s|%d", s.branch, l.pid)] += l.qty
		}
	}
	balance := map[string]int64{}
	stockStart := start.Add(-time.Hour)
	for code, br := range branches {
		for _, dp := range demoProducts {
			pid := prodID[dp.sku]
			key := fmt.Sprintf("%s|%d", code, pid)
			open := dp.stock[code] + sold[key]
			balance[key] = open
			if err := tx.Create(&models.BranchStock{BranchID: br.ID, ProductID: pid, Qty: dp.stock[code] + 0, ReorderPoint: dp.reorder}).Error; err != nil {
				return err
			}
			if err := tx.Create(&models.StockMovement{BranchID: br.ID, ProductID: pid, Type: models.StockMovementReceive, QtyChange: open, BalanceAfter: open, UnitCostCents: dp.cost, ReferenceType: "OPENING", UserID: owner.ID, Note: "Opening stock", CreatedAt: stockStart}).Error; err != nil {
				return err
			}
		}
	}

	// Shifts: one per till per day, cash totals derived from the sales.
	type shiftKey struct {
		branch string
		day    string
		till   int
	}
	shiftIDs := map[shiftKey]uint64{}
	type shiftAcc struct{ cashUSD, cashKHR int64 }
	acc := map[uint64]*shiftAcc{}
	devices := map[string][]models.Device{}
	for code, br := range branches {
		var ds []models.Device
		tx.Where("branch_id = ?", br.ID).Order("id").Find(&ds)
		devices[code] = ds
	}
	getShift := func(s *saleSpec) (uint64, error) {
		k := shiftKey{s.branch, s.day.Format("2006-01-02"), s.tillIdx}
		if id, ok := shiftIDs[k]; ok {
			return id, nil
		}
		dev := devices[s.branch][s.tillIdx%len(devices[s.branch])]
		cashier := users[cashiers[s.branch][s.tillIdx%len(cashiers[s.branch])]]
		sh := models.Shift{BranchID: branches[s.branch].ID, DeviceID: dev.ID, UserID: cashier.ID, Status: models.ShiftClosed,
			OpenedAt: s.day.Add(7 * time.Hour), ExchangeRate: 4100, OpeningUSDCents: 10000, OpeningKHRRiel: 200000}
		if err := tx.Omit("Device", "User").Create(&sh).Error; err != nil {
			return 0, err
		}
		shiftIDs[k] = sh.ID
		acc[sh.ID] = &shiftAcc{}
		return sh.ID, nil
	}

	seqByDay := map[string]int64{}
	custPoints := map[uint64]int64{}
	voidReasons := []string{"Wrong items rung up", "Customer changed mind", "Duplicate scan"}
	for i, s := range specs {
		shiftID, err := getShift(s)
		if err != nil {
			return err
		}
		br := branches[s.branch]
		dev := devices[s.branch][s.tillIdx%len(devices[s.branch])]
		cashier := users[s.cashierU]
		var subtotal, cost int64
		for _, l := range s.lines {
			subtotal += l.price * l.qty
			cost += l.cost * l.qty
		}
		total := subtotal
		seqKey := s.branch + s.day.Format("0102")
		seqByDay[seqKey]++
		status := models.SalePaid
		if s.voided {
			status = models.SaleVoided
		}
		sale := models.Sale{
			BranchID: br.ID, ShiftID: shiftID, DeviceID: dev.ID, CashierID: cashier.ID, CustomerID: s.cust,
			ReceiptNo: fmt.Sprintf("#%s-%03d", s.day.Format("0102"), seqByDay[seqKey]), BusinessDate: s.day, Status: status,
			SubtotalCents: subtotal, TotalCents: total, TotalRiel: utils.USDCentsToRiel(total, 4100), ExchangeRate: 4100,
			CostTotalCents: cost, IdempotencyKey: fmt.Sprintf("seed-%d", i), SoldAt: s.at,
		}
		if err := tx.Omit("Branch", "Cashier", "Customer", "Items", "Payments").Create(&sale).Error; err != nil {
			return err
		}
		for _, l := range s.lines {
			if err := tx.Omit("Product").Create(&models.SaleItem{SaleID: sale.ID, ProductID: l.pid, NameSnapshot: byID[l.pid].name, Qty: l.qty, UnitPriceCents: l.price, UnitCostCents: l.cost, LineTotalCents: l.price * l.qty}).Error; err != nil {
				return err
			}
		}
		pay := models.Payment{SaleID: sale.ID, Method: models.PaymentMethod(s.method), Currency: models.CurrencyUSD, AmountCents: total, AmountRiel: utils.USDCentsToRiel(total, 4100), Status: models.PaymentConfirmed, CreatedAt: s.at}
		if s.method == "CASH" {
			// Mostly USD notes rounded up; sometimes part/all in riel.
			recvUSD := ((total + 99) / 100) * 100
			var recvKHR int64
			if rng.Float64() < 0.35 {
				recvKHR = utils.RoundRielTo100(utils.USDCentsToRiel(total, 4100)+int64(rng.Intn(3))*1000) + 0
				recvUSD = 0
			}
			res, err := utils.CalculateChange(total, recvUSD, recvKHR, 4100)
			if err != nil || res.ShortCents > 0 {
				recvUSD, recvKHR = ((total+99)/100)*100+100, 0
				res, _ = utils.CalculateChange(total, recvUSD, recvKHR, 4100)
			}
			pay.ReceivedUSDCents, pay.ReceivedKHRRiel, pay.ChangeUSDCents, pay.ChangeKHRRiel = recvUSD, recvKHR, res.ChangeUSDCents, res.ChangeKHRRiel
			if status == models.SalePaid {
				acc[shiftID].cashUSD += recvUSD - res.ChangeUSDCents
				acc[shiftID].cashKHR += recvKHR - res.ChangeKHRRiel
			}
		}
		if err := tx.Create(&pay).Error; err != nil {
			return err
		}
		if s.voided {
			mgr := users["chan.sreyneang"]
			if err := tx.Create(&models.VoidRefund{SaleID: sale.ID, Type: models.VoidType, AmountCents: total, Reason: voidReasons[i%len(voidReasons)], CashierID: cashier.ID, ApprovedByID: &mgr.ID, CreatedAt: s.at.Add(3 * time.Minute)}).Error; err != nil {
				return err
			}
			continue
		}
		for _, l := range s.lines {
			key := fmt.Sprintf("%s|%d", s.branch, l.pid)
			balance[key] -= l.qty
			ref := sale.ID
			if err := tx.Create(&models.StockMovement{BranchID: br.ID, ProductID: l.pid, Type: models.StockMovementSale, QtyChange: -l.qty, BalanceAfter: balance[key], UnitCostCents: l.cost, ReferenceType: "SALE", ReferenceID: &ref, UserID: cashier.ID, CreatedAt: s.at}).Error; err != nil {
				return err
			}
		}
		if s.cust != nil {
			pts := total / 100
			custPoints[*s.cust] += pts
			if pts > 0 {
				sid := sale.ID
				if err := tx.Create(&models.LoyaltyTransaction{CustomerID: *s.cust, SaleID: &sid, PointsChange: pts, Reason: "Sale " + sale.ReceiptNo, CreatedAt: s.at}).Error; err != nil {
					return err
				}
			}
		}
	}

	// Close every seeded shift with a counted amount (mostly balanced).
	diffs := []int64{0, 0, 0, 0, 0, 0, -1200, 300, 0, -500}
	i := 0
	for k, id := range shiftIDs {
		var sh models.Shift
		if err := tx.First(&sh, id).Error; err != nil {
			return err
		}
		expUSD := sh.OpeningUSDCents + acc[id].cashUSD
		expKHR := sh.OpeningKHRRiel + acc[id].cashKHR
		d := diffs[i%len(diffs)]
		i++
		closeAt := time.Date(sh.OpenedAt.Year(), sh.OpenedAt.Month(), sh.OpenedAt.Day(), 21, 0, 0, 0, time.Local)
		if closeAt.After(now) {
			closeAt = now
		}
		_ = k
		if err := tx.Model(&models.Shift{}).Where("id = ?", id).Updates(map[string]interface{}{
			"closed_at": closeAt, "expected_usd_cents": expUSD, "expected_khr_riel": expKHR,
			"counted_usd_cents": expUSD + d, "counted_khr_riel": expKHR, "diff_usd_cents": d, "diff_khr_riel": 0,
		}).Error; err != nil {
			return err
		}
	}
	for cid, pts := range custPoints {
		if err := tx.Model(&models.Customer{}).Where("id = ?", cid).Update("points", gorm.Expr("points + ?", pts)).Error; err != nil {
			return err
		}
	}

	// Final branch_stock quantities = last balance (already set to "now" above).

	// ---- Purchase orders ----------------------------------------------------
	type poDef struct {
		branch, supplier, status, note string
		shipping                       int64
		daysAgo                        int
		items                          []struct {
			sku       string
			qty, cost int64
		}
	}
	pos := []poDef{
		{"TK", "Golden Delta Distribution", "RECEIVED", "", 1500, 4, []struct {
			sku       string
			qty, cost int64
		}{{"PAN-002", 20, 510}, {"PAN-003", 30, 110}, {"PAN-004", 20, 135}}},
		{"TK", "Angkor Beverage Co.", "PARTIAL", "Split delivery, second half next week.", 2500, 2, []struct {
			sku       string
			qty, cost int64
		}{{"DRK-001", 200, 18}, {"DRK-002", 100, 48}, {"DRK-003", 100, 44}, {"DRK-004", 100, 35}}},
		{"BKK1", "Mekong FMCG Supply", "SENT", "", 800, 1, []struct {
			sku       string
			qty, cost int64
		}{{"PAN-001", 100, 28}, {"SNK-001", 100, 55}}},
		{"TK", "Golden Delta Distribution", "DRAFT", "", 0, 0, []struct {
			sku       string
			qty, cost int64
		}{{"PAN-002", 10, 510}}},
	}
	for _, pd := range pos {
		po := models.PurchaseOrder{BranchID: branches[pd.branch].ID, SupplierID: supID[pd.supplier], Status: models.PurchaseOrderStatus(pd.status), Note: pd.note, ShippingCents: pd.shipping, UserID: users["heng.rithy"].ID}
		po.CreatedAt = now.AddDate(0, 0, -pd.daysAgo)
		po.UpdatedAt = po.CreatedAt
		if err := tx.Omit("Supplier", "Items").Create(&po).Error; err != nil {
			return err
		}
		for _, it := range pd.items {
			recv := int64(0)
			switch pd.status {
			case "RECEIVED":
				recv = it.qty
			case "PARTIAL":
				recv = it.qty / 2
			}
			if err := tx.Omit("Product").Create(&models.PurchaseOrderItem{PurchaseOrderID: po.ID, ProductID: prodID[it.sku], QtyOrdered: it.qty, QtyReceived: recv, UnitCostCents: it.cost}).Error; err != nil {
				return err
			}
		}
	}

	// ---- Expenses ---------------------------------------------------------------
	tk := branches["TK"]
	for _, e := range []struct {
		daysAgo int
		cat     models.ExpenseCategory
		cents   int64
		note    string
	}{
		{3, models.ExpenseRent, 65000, "Monthly rent"}, {8, models.ExpenseElectricity, 8200, "EDC bill"}, {10, models.ExpenseWages, 42000, "First half wages"},
		{11, models.ExpensePackaging, 3100, "Plastic bags restock"}, {13, models.ExpenseInternet, 2500, "Fiber internet"},
	} {
		day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, -e.daysAgo)
		if err := tx.Create(&models.Expense{BranchID: tk.ID, Category: e.cat, AmountCents: e.cents, ExpenseDate: day, Note: e.note, UserID: users["meas.bopha"].ID}).Error; err != nil {
			return err
		}
	}
	return nil
}
