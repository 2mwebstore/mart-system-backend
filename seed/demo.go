package main

import (
	"fmt"
	"math/rand"
	"sort"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

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
}

// Khmer product names, keyed by SKU.
var demoProductKm = map[string]string{
	"DRK-001": "ទឹកបរិសុទ្ធ 500ml", "DRK-002": "កាហ្វេកំប៉ុង 240ml", "DRK-003": "ភេសជ្ជៈថាមពល 250ml", "DRK-004": "ទឹកដោះសណ្តែក 250ml",
	"PAN-001": "មីកំប៉ុងរសមាន់", "PAN-002": "អង្ករម្លិះ 5kg", "PAN-003": "ទឹកត្រីស្រស់ 700ml", "PAN-004": "ស៊ុតមាន់ 10 គ្រាប់", "PAN-005": "នំបុ័ង",
	"SNK-001": "ដំឡូងបំពង់ 60g", "HHD-001": "ទឹកលាងចាន 750ml", "PSC-001": "ថ្នាំដុសធ្មេញ 150g",
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
	{"DRK-001", "8850001001", "Drinking water 500ml", "Drinks", "Angkor Beverage Co.", "bottle", 30, 18, map[string]int64{"TK": 240, "BKK1": 96, "SS": 12}, 48},
	{"DRK-002", "8850001002", "Iced coffee can 240ml", "Drinks", "Angkor Beverage Co.", "can", 75, 48, map[string]int64{"TK": 84, "BKK1": 40, "SS": 6}, 24},
	{"DRK-003", "8850001003", "Energy drink 250ml", "Drinks", "Angkor Beverage Co.", "can", 70, 44, map[string]int64{"TK": 60, "BKK1": 18, "SS": 30}, 24},
	{"DRK-004", "8850001004", "Soy milk 250ml", "Drinks", "Angkor Beverage Co.", "box", 55, 35, map[string]int64{"TK": 72, "BKK1": 20, "SS": 18}, 24},
	{"PAN-001", "8850002001", "Instant noodles chicken", "Pantry", "Mekong FMCG Supply", "pack", 45, 28, map[string]int64{"TK": 150, "BKK1": 60, "SS": 40}, 60},
	{"PAN-002", "8850002002", "Jasmine rice 5kg", "Pantry", "Golden Delta Distribution", "bag", 650, 510, map[string]int64{"TK": 22, "BKK1": 9, "SS": 4}, 10},
	{"PAN-003", "8850002003", "Fish sauce 700ml", "Pantry", "Golden Delta Distribution", "bottle", 160, 110, map[string]int64{"TK": 34, "BKK1": 12, "SS": 8}, 15},
	{"PAN-004", "8850002004", "Eggs 10 pack", "Pantry", "Golden Delta Distribution", "pack", 180, 135, map[string]int64{"TK": 26, "BKK1": 6, "SS": 14}, 20},
	{"PAN-005", "8850002005", "Bread loaf", "Pantry", "Mekong FMCG Supply", "pcs", 120, 80, map[string]int64{"TK": 18, "BKK1": 8, "SS": 5}, 15},
	{"SNK-001", "8850003001", "Potato chips 60g", "Snacks", "Mekong FMCG Supply", "pack", 90, 55, map[string]int64{"TK": 96, "BKK1": 40, "SS": 20}, 30},
	{"HHD-001", "8850004001", "Dish soap 750ml", "Household", "Mekong FMCG Supply", "bottle", 195, 140, map[string]int64{"TK": 28, "BKK1": 10, "SS": 6}, 12},
	{"PSC-001", "8850005001", "Toothpaste 150g", "Personal care", "Mekong FMCG Supply", "tube", 140, 95, map[string]int64{"TK": 20, "BKK1": 7, "SS": 5}, 12},
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
		if err := tx.Save(&s).Error; err != nil {
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
	for _, dp := range demoProducts {
		cid, sid := catID[dp.category], supID[dp.supplier]
		bc := dp.barcode
		p := models.Product{SKU: dp.sku, Barcode: &bc, NameEn: dp.name, NameKm: demoProductKm[dp.sku], CategoryID: &cid, SupplierID: &sid, Unit: dp.unit, Active: true}
		if err := tx.Create(&p).Error; err != nil {
			return err
		}
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
