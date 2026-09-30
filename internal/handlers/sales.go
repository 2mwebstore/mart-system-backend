package handlers

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"com-mart/backend/internal/middleware"
	"com-mart/backend/internal/models"
	"com-mart/backend/internal/services"
	"com-mart/backend/internal/utils"
)

type saleItemDTO struct {
	ProductID      uint64 `json:"product_id"`
	Name           string `json:"name"`
	NameKm         string `json:"name_km"`
	SKU            string `json:"sku"`
	Qty            int64  `json:"qty"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	DiscountCents  int64  `json:"discount_cents"`
	Note           string `json:"note"`
	LineTotalCents int64  `json:"line_total_cents"`
}

type salePaymentDTO struct {
	Method           string `json:"method"`
	ReceivedUSDCents int64  `json:"received_usd_cents"`
	ReceivedKHRRiel  int64  `json:"received_khr_riel"`
	ChangeUSDCents   int64  `json:"change_usd_cents"`
	ChangeKHRRiel    int64  `json:"change_khr_riel"`
	Reference        string `json:"reference"`
}

type saleDTO struct {
	ID            uint64         `json:"id"`
	ReceiptNo     string         `json:"receipt_no"`
	ShiftID       uint64         `json:"shift_id"`
	BranchID      uint64         `json:"branch_id"`
	Cashier       string         `json:"cashier"`
	CustomerID    *uint64        `json:"customer_id"`
	CustomerName  string         `json:"customer_name"`
	Status        string         `json:"status"`
	SubtotalCents int64          `json:"subtotal_cents"`
	DiscountCents int64          `json:"discount_cents"`
	TotalCents    int64          `json:"total_cents"`
	TotalRiel     int64          `json:"total_riel"`
	PointsEarned  int64          `json:"points_earned"`
	SoldAt        time.Time      `json:"sold_at"`
	VoidReason    string         `json:"void_reason,omitempty"`
	Items         []saleItemDTO  `json:"items" gorm:"-"`
	Payment       salePaymentDTO `json:"payment" gorm:"-"`
}

func (a *API) loadSales(where string, args ...interface{}) ([]saleDTO, error) {
	sales, _, err := a.loadSalesPage(pager{}, where, args...)
	return sales, err
}

// loadSalesPage returns one page of sales (with lines and payment) matching
// where, plus the total number of matches.
func (a *API) loadSalesPage(pg pager, where string, args ...interface{}) ([]saleDTO, int64, error) {
	var sales []saleDTO
	count := a.DB.Table("sales s")
	if where != "" {
		count = count.Where(where, args...)
	}
	var total int64
	if err := count.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	q := a.DB.Table("sales s").
		Select(`s.id, s.receipt_no, s.shift_id, s.branch_id, COALESCE(u.full_name,'') AS cashier, s.customer_id,
			COALESCE(cu.name,'') AS customer_name, s.status, s.subtotal_cents, s.discount_cents, s.total_cents, s.total_riel, s.sold_at`).
		Joins("LEFT JOIN users u ON u.id = s.cashier_id").
		Joins("LEFT JOIN customers cu ON cu.id = s.customer_id").
		Order("s.sold_at DESC, s.id DESC")
	if where != "" {
		q = q.Where(where, args...)
	}
	if err := pg.apply(q).Scan(&sales).Error; err != nil {
		return nil, 0, err
	}
	if len(sales) == 0 {
		return []saleDTO{}, total, nil
	}
	ids := make([]uint64, len(sales))
	idx := map[uint64]int{}
	for i, s := range sales {
		ids[i] = s.ID
		idx[s.ID] = i
		sales[i].Items = []saleItemDTO{}
	}
	type itemRow struct {
		Item   saleItemDTO `gorm:"embedded"`
		SaleID uint64
	}
	var items []itemRow
	if err := a.DB.Table("sale_items si").
		Select("si.sale_id, si.product_id, si.name_snapshot AS name, COALESCE(p.name_km,'') AS name_km, COALESCE(p.sku,'') AS sku, si.qty, si.unit_price_cents, si.discount_cents, si.note, si.line_total_cents").
		Joins("LEFT JOIN products p ON p.id = si.product_id").Where("si.sale_id IN ?", ids).Order("si.id").Scan(&items).Error; err != nil {
		return nil, 0, err
	}
	for _, it := range items {
		sales[idx[it.SaleID]].Items = append(sales[idx[it.SaleID]].Items, it.Item)
	}
	type payRow struct {
		Pay    salePaymentDTO `gorm:"embedded"`
		SaleID uint64
	}
	var pays []payRow
	if err := a.DB.Table("payments").Select("sale_id, method, received_usd_cents, received_khr_riel, change_usd_cents, change_khr_riel, reference").
		Where("sale_id IN ?", ids).Scan(&pays).Error; err != nil {
		return nil, 0, err
	}
	for _, p := range pays {
		sales[idx[p.SaleID]].Payment = p.Pay
	}
	var loyalty []struct {
		SaleID uint64
		Points int64
	}
	a.DB.Table("loyalty_transactions").Select("sale_id, SUM(points_change) AS points").Where("sale_id IN ? AND points_change > 0", ids).Group("sale_id").Scan(&loyalty)
	for _, l := range loyalty {
		sales[idx[l.SaleID]].PointsEarned = l.Points
	}
	var voids []struct {
		SaleID uint64
		Reason string
	}
	a.DB.Table("voids_refunds").Select("sale_id, reason").Where("sale_id IN ? AND type = 'VOID'", ids).Scan(&voids)
	for _, v := range voids {
		sales[idx[v.SaleID]].VoidReason = v.Reason
	}
	return sales, total, nil
}

// ListSales backs the POS "sales this shift" list (?shift_id=) and the
// branch sales history (date range). Optional filters: status, q (receipt no).
func (a *API) ListSales(c *gin.Context) {
	branchID := branchParam(c)
	pg := pagerOf(c)
	where := "s.branch_id = ?"
	args := []interface{}{branchID}
	if sid := c.Query("shift_id"); sid != "" {
		where += " AND s.shift_id = ?"
		args = append(args, sid)
	} else {
		from, to := dateRange(c)
		where += " AND s.sold_at >= ? AND s.sold_at < ?"
		args = append(args, from, to)
	}
	if st := strings.ToUpper(c.Query("status")); st != "" {
		where += " AND s.status = ?"
		args = append(args, st)
	}
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		where += " AND s.receipt_no LIKE ?"
		args = append(args, likeArg(q))
	}
	out, total, err := a.loadSalesPage(pg, where, args...)
	if err != nil {
		dbFail(c, err)
		return
	}
	pg.respond(c, out, total, nil)
}

// GetSale is one sale's full detail (items, payment) — used by the Report
// Details "Transactions" table's click-to-view-detail and by anything else
// that only has a sale id (e.g. a receipt lookup).
func (a *API) GetSale(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	branchID := branchParam(c)
	out, err := a.loadSales("s.id = ? AND s.branch_id = ?", id, branchID)
	if err != nil {
		dbFail(c, err)
		return
	}
	if len(out) == 0 {
		fail(c, utils.ErrNotFound)
		return
	}
	utils.OK(c, http.StatusOK, out[0])
}

type createSaleItem struct {
	ProductID     uint64 `json:"product_id" binding:"required"`
	Qty           int64  `json:"qty" binding:"required,min=1"`
	DiscountCents int64  `json:"discount_cents" binding:"min=0"`
	// A free-text remark on this line (e.g. "less sugar") — cosmetic only,
	// carried through to the printed receipt, never interpreted by the
	// server. When the same product_id appears more than once in a request,
	// the first occurrence's note wins (the frontend cart never actually
	// sends duplicates — see useCart.ts — this is just the same defensive
	// merge behavior CreateSale already had for qty/discount).
	Note string `json:"note" binding:"max=255"`
}

type createSaleReq struct {
	DeviceKey      string           `json:"device_key" binding:"required"`
	IdempotencyKey string           `json:"idempotency_key" binding:"required,max=80"`
	CustomerID     *uint64          `json:"customer_id"`
	Items          []createSaleItem `json:"items" binding:"required,min=1,dive"`
	DiscountCents  int64            `json:"discount_cents" binding:"min=0"`
	Payment        struct {
		Method           string `json:"method" binding:"required,oneof=CASH KHQR CARD"`
		ReceivedUSDCents int64  `json:"received_usd_cents" binding:"min=0"`
		ReceivedKHRRiel  int64  `json:"received_khr_riel" binding:"min=0"`
		Reference        string `json:"reference" binding:"max=120"`
	} `json:"payment" binding:"required"`
}

func (a *API) roleMaxDiscountPercent(c *gin.Context) int {
	var limit models.RoleLimit
	if err := a.DB.Where("role_id = ?", roleIDFrom(c)).First(&limit).Error; err != nil {
		return 0
	}
	return limit.MaxDiscountPercent
}

func roleIDFrom(c *gin.Context) uint64 {
	v, _ := c.Get(middleware.CtxRoleID)
	id, _ := v.(uint64)
	return id
}

// CreateSale rings up a sale. Everything that matters is decided here, not
// by the till: prices come from the catalog, stock is checked and decremented
// under a row lock, change is computed by the spec's change rule, and the
// whole thing (sale, lines, payment, stock movements, loyalty points) commits
// or rolls back together. A repeated idempotency_key returns the original
// sale instead of charging twice.
func (a *API) CreateSale(c *gin.Context) {
	var req createSaleReq
	if !bind(c, &req) {
		return
	}
	userID := middleware.UserIDFrom(c)

	if prior, err := a.loadSales("s.idempotency_key = ?", req.IdempotencyKey); err == nil && len(prior) > 0 {
		utils.OK(c, http.StatusOK, prior[0])
		return
	}
	device, ok := a.deviceByKey(c, req.DeviceKey)
	if !ok {
		return
	}
	shift, err := a.openShiftOn(device.ID)
	if err != nil {
		dbFail(c, err)
		return
	}
	if shift == nil {
		fail(c, utils.NewAppError(http.StatusConflict, "SHIFT_REQUIRED", "Open a shift on this till before selling."))
		return
	}

	rate := a.currentRate()
	appSettings := a.loadSettings()
	loyaltyRate := appSettings.LoyaltyPointsPerUSD
	// Store-wide, not per-product (see docs/DECISIONS.md) — read once here,
	// same as the rate and loyalty settings above, rather than per line.
	allowOOS := appSettings.AllowOutOfStockSale
	var saleID uint64
	// Collected during the transaction, alerted on after it commits (an
	// alert must never depend on / block a rollback-able DB call).
	var lowStock []lowStockHit

	err = a.DB.Transaction(func(tx *gorm.DB) error {
		var subtotal, lineDiscounts, cost int64
		lines := make([]models.SaleItem, 0, len(req.Items))
		serviceItems := map[uint64]bool{} // product_id -> true, for skipping stock movements below
		merged := map[uint64]*createSaleItem{}
		order := []uint64{}
		for i := range req.Items {
			it := req.Items[i]
			if m, ok := merged[it.ProductID]; ok {
				m.Qty += it.Qty
				m.DiscountCents += it.DiscountCents
			} else {
				merged[it.ProductID] = &it
				order = append(order, it.ProductID)
			}
		}
		for _, pid := range order {
			it := merged[pid]
			var prod struct {
				ID          uint64
				NameEn      string
				Active      bool
				ProductType string
				PriceCents  int64
				CostCents   int64
			}
			err := tx.Table("products p").
				Select("p.id, p.name_en, p.active, p.product_type, COALESCE(pp.price_cents,0) AS price_cents, COALESCE(pp.cost_cents,0) AS cost_cents").
				Joins(`LEFT JOIN product_prices pp ON pp.id = (
					SELECT x.id FROM product_prices x WHERE x.product_id = p.id AND (x.branch_id IS NULL OR x.branch_id = ?)
					ORDER BY (x.branch_id IS NULL), x.effective_at DESC, x.id DESC LIMIT 1)`, device.BranchID).
				Where("p.id = ? AND p.deleted_at IS NULL", pid).Scan(&prod).Error
			if err != nil {
				return err
			}
			if prod.ID == 0 || !prod.Active {
				return utils.NewAppError(http.StatusUnprocessableEntity, "PRODUCT_UNAVAILABLE", fmt.Sprintf("Product #%d isn't available for sale.", pid))
			}
			// A SERVICE item has no stock to check or decrement — it's
			// always sellable, same as if the till had infinite of it.
			if prod.ProductType == string(models.ProductService) {
				serviceItems[pid] = true
			} else {
				var stock models.BranchStock
				lockErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("branch_id = ? AND product_id = ?", device.BranchID, pid).First(&stock).Error
				if lockErr != nil && lockErr != gorm.ErrRecordNotFound {
					return lockErr
				}
				// The store-wide allowOOS setting lets the sale go through
				// even at zero/negative stock — e.g. a backorder — instead
				// of blocking here.
				if stock.Qty < it.Qty && !allowOOS {
					return utils.NewAppError(http.StatusUnprocessableEntity, "INSUFFICIENT_STOCK", fmt.Sprintf("Not enough stock for %s (%d left).", prod.NameEn, stock.Qty))
				}
				if newQty := stock.Qty - it.Qty; stock.ReorderPoint > 0 && stock.Qty > stock.ReorderPoint && newQty <= stock.ReorderPoint {
					// Just crossed the reorder point this sale — alert once, not
					// on every subsequent sale while it stays low.
					lowStock = append(lowStock, lowStockHit{name: prod.NameEn, qty: newQty, reorderPoint: stock.ReorderPoint})
				}
			}
			gross := prod.PriceCents * it.Qty
			disc := it.DiscountCents
			if disc > gross {
				disc = gross
			}
			subtotal += gross
			lineDiscounts += disc
			cost += prod.CostCents * it.Qty
			lines = append(lines, models.SaleItem{ProductID: pid, NameSnapshot: prod.NameEn, Qty: it.Qty, UnitPriceCents: prod.PriceCents, UnitCostCents: prod.CostCents, DiscountCents: disc, Note: it.Note, LineTotalCents: gross - disc})
		}
		afterLines := subtotal - lineDiscounts
		cartDiscount := req.DiscountCents
		if cartDiscount > afterLines {
			cartDiscount = afterLines
		}
		totalDiscount := lineDiscounts + cartDiscount
		total := afterLines - cartDiscount

		if totalDiscount > 0 {
			if !hasPerm(c, "pos.discount") {
				return utils.ErrForbidden
			}
			if subtotal > 0 && float64(totalDiscount)*100/float64(subtotal) > float64(a.roleMaxDiscountPercent(c))+1e-9 {
				return utils.NewAppError(http.StatusForbidden, "DISCOUNT_LIMIT_EXCEEDED", fmt.Sprintf("Your role can discount at most %d%% of a sale.", a.roleMaxDiscountPercent(c)))
			}
		}

		pay := models.Payment{Method: models.PaymentMethod(req.Payment.Method), Currency: models.CurrencyUSD, AmountCents: total, AmountRiel: utils.USDCentsToRiel(total, rate), Reference: req.Payment.Reference, Status: models.PaymentConfirmed, CreatedAt: time.Now()}
		if req.Payment.Method == "CASH" {
			res, err := utils.CalculateChange(total, req.Payment.ReceivedUSDCents, req.Payment.ReceivedKHRRiel, rate)
			if err != nil {
				return err
			}
			if res.ShortCents > 0 {
				return utils.ErrInsufficientCash
			}
			pay.ReceivedUSDCents, pay.ReceivedKHRRiel = req.Payment.ReceivedUSDCents, req.Payment.ReceivedKHRRiel
			pay.ChangeUSDCents, pay.ChangeKHRRiel = res.ChangeUSDCents, res.ChangeKHRRiel
		}

		now := time.Now()
		businessDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
		receiptNo, err := nextDocNumber(tx, DocSale)
		if err != nil {
			return err
		}
		sale := models.Sale{
			BranchID: device.BranchID, ShiftID: shift.ID, DeviceID: device.ID, CashierID: userID, CustomerID: req.CustomerID,
			ReceiptNo: receiptNo, BusinessDate: businessDate, Status: models.SalePaid,
			SubtotalCents: subtotal, DiscountCents: totalDiscount, TotalCents: total, TotalRiel: utils.USDCentsToRiel(total, rate),
			ExchangeRate: rate, CostTotalCents: cost, IdempotencyKey: req.IdempotencyKey, SoldAt: now,
		}
		if err := tx.Omit("Branch", "Cashier", "Customer", "Items", "Payments").Create(&sale).Error; err != nil {
			return err
		}
		saleID = sale.ID
		for i := range lines {
			lines[i].SaleID = sale.ID
			if err := tx.Omit("Product").Create(&lines[i]).Error; err != nil {
				return err
			}
			if serviceItems[lines[i].ProductID] {
				continue // nothing to move — a service has no stock
			}
			ref := sale.ID
			if _, err := recordMovement(tx, device.BranchID, lines[i].ProductID, models.StockMovementSale, -lines[i].Qty, lines[i].UnitCostCents, "SALE", &ref, userID, ""); err != nil {
				return err
			}
		}
		pay.SaleID = sale.ID
		if err := tx.Create(&pay).Error; err != nil {
			return err
		}
		if req.CustomerID != nil && loyaltyRate > 0 {
			points := int64(math.Floor(float64(total) / 100 * loyaltyRate))
			if points > 0 {
				if err := tx.Model(&models.Customer{}).Where("id = ?", *req.CustomerID).Update("points", gorm.Expr("points + ?", points)).Error; err != nil {
					return err
				}
				sid := sale.ID
				if err := tx.Create(&models.LoyaltyTransaction{CustomerID: *req.CustomerID, SaleID: &sid, PointsChange: points, Reason: "Sale " + sale.ReceiptNo, CreatedAt: now}).Error; err != nil {
					return err
				}
			}
		}
		return tx.Model(&models.Device{}).Where("id = ?", device.ID).Update("last_seen_at", now).Error
	})
	if err != nil {
		if ae, ok := err.(*utils.AppError); ok {
			fail(c, ae)
			return
		}
		dbFail(c, err)
		return
	}
	for _, hit := range lowStock {
		a.Notify.Send(services.AlertLowStock, fmt.Sprintf("⚠️ <b>Low stock</b>\n%s: %d left (reorder point %d)", services.HTMLEscape(hit.name), hit.qty, hit.reorderPoint))
	}
	out, err := a.loadSales("s.id = ?", saleID)
	if err != nil || len(out) == 0 {
		dbFail(c, err)
		return
	}
	sale := out[0]
	itemLines := make([]string, len(sale.Items))
	for i, it := range sale.Items {
		itemLines[i] = fmt.Sprintf("%d× %s", it.Qty, services.HTMLEscape(it.Name))
	}
	a.Notify.Send(services.AlertSaleCompleted, fmt.Sprintf(
		"🧾 <b>Sale completed</b>\n%s · %s\n%s\nTotal: %s",
		sale.ReceiptNo, services.HTMLEscape(sale.Cashier), services.FormatItemLines(itemLines, 12), services.FormatUSD(sale.TotalCents),
	))
	utils.OK(c, http.StatusCreated, out[0])
}

type lowStockHit struct {
	name         string
	qty          int64
	reorderPoint int64
}

type editSaleItem struct {
	ProductID uint64 `json:"product_id" binding:"required"`
	Qty       int64  `json:"qty" binding:"required,min=1"`
	Note      string `json:"note" binding:"max=255"`
}

type editSaleReq struct {
	Items []editSaleItem `json:"items" binding:"required,min=1,dive"`
}

// EditSale corrects what was rung up on a still-open-shift sale: the item
// list (which products, how many of each) only — not the payment method or
// amount tendered, not discounts. It's the same trust boundary as CreateSale
// (prices come from the catalog, not the request) applied to a correction
// instead of a fresh sale: old items' stock goes back, new items' stock is
// taken, in one transaction, so the shelf count is never briefly wrong or
// double-counted. Payment and loyalty points are recalculated to stay
// arithmetically consistent with the new total — this isn't "editing
// payment", the amount tendered is untouched, only what it owes/changes.
func (a *API) EditSale(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req editSaleReq
	if !bind(c, &req) {
		return
	}
	userID := middleware.UserIDFrom(c)

	before, err := a.loadSales("s.id = ?", id)
	if err != nil || len(before) == 0 {
		dbFail(c, gorm.ErrRecordNotFound)
		return
	}

	var lowStock []lowStockHit
	allowOOS := a.loadSettings().AllowOutOfStockSale // store-wide, see CreateSale
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		var sale models.Sale
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&sale, id).Error; err != nil {
			return err
		}
		if !canAccessBranch(c, sale.BranchID) {
			return utils.ErrForbidden
		}
		if sale.Status != models.SalePaid {
			return utils.NewAppError(http.StatusConflict, "SALE_NOT_EDITABLE", "Only a paid sale can be edited.")
		}
		var shift models.Shift
		if err := tx.First(&shift, sale.ShiftID).Error; err != nil {
			return err
		}
		if shift.Status != models.ShiftOpen {
			return utils.NewAppError(http.StatusConflict, "SHIFT_CLOSED", "This sale belongs to a closed shift and can't be edited.")
		}

		// Reverse every old line's stock (SERVICE products never had any —
		// same rule as CreateSale/VoidSale), then delete the old lines.
		var oldItems []models.SaleItem
		if err := tx.Where("sale_id = ?", id).Find(&oldItems).Error; err != nil {
			return err
		}
		oldServiceIDs, err := a.serviceProductIDs(tx, oldItems)
		if err != nil {
			return err
		}
		for _, it := range oldItems {
			if oldServiceIDs[it.ProductID] {
				continue
			}
			ref := id
			if _, err := recordMovement(tx, sale.BranchID, it.ProductID, models.StockMovementRefundReturn, it.Qty, it.UnitCostCents, "SALE", &ref, userID, "Edit "+sale.ReceiptNo); err != nil {
				return err
			}
		}
		if err := tx.Where("sale_id = ?", id).Delete(&models.SaleItem{}).Error; err != nil {
			return err
		}

		// Price and stock-check the new lines exactly like CreateSale does —
		// the request only ever says which products and how many.
		var subtotal, cost int64
		lines := make([]models.SaleItem, 0, len(req.Items))
		newServiceIDs := map[uint64]bool{}
		merged := map[uint64]int64{}
		notes := map[uint64]string{} // first occurrence wins, same as merged qty below
		order := []uint64{}
		for _, it := range req.Items {
			if _, ok := merged[it.ProductID]; !ok {
				order = append(order, it.ProductID)
				notes[it.ProductID] = it.Note
			}
			merged[it.ProductID] += it.Qty
		}
		for _, pid := range order {
			qty := merged[pid]
			var prod struct {
				ID          uint64
				NameEn      string
				Active      bool
				ProductType string
				PriceCents  int64
				CostCents   int64
			}
			err := tx.Table("products p").
				Select("p.id, p.name_en, p.active, p.product_type, COALESCE(pp.price_cents,0) AS price_cents, COALESCE(pp.cost_cents,0) AS cost_cents").
				Joins(`LEFT JOIN product_prices pp ON pp.id = (
					SELECT x.id FROM product_prices x WHERE x.product_id = p.id AND (x.branch_id IS NULL OR x.branch_id = ?)
					ORDER BY (x.branch_id IS NULL), x.effective_at DESC, x.id DESC LIMIT 1)`, sale.BranchID).
				Where("p.id = ? AND p.deleted_at IS NULL", pid).Scan(&prod).Error
			if err != nil {
				return err
			}
			if prod.ID == 0 || !prod.Active {
				return utils.NewAppError(http.StatusUnprocessableEntity, "PRODUCT_UNAVAILABLE", fmt.Sprintf("Product #%d isn't available for sale.", pid))
			}
			if prod.ProductType == string(models.ProductService) {
				newServiceIDs[pid] = true
			} else {
				var stock models.BranchStock
				lockErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("branch_id = ? AND product_id = ?", sale.BranchID, pid).First(&stock).Error
				if lockErr != nil && lockErr != gorm.ErrRecordNotFound {
					return lockErr
				}
				if stock.Qty < qty && !allowOOS {
					return utils.NewAppError(http.StatusUnprocessableEntity, "INSUFFICIENT_STOCK", fmt.Sprintf("Not enough stock for %s (%d left).", prod.NameEn, stock.Qty))
				}
				if newQty := stock.Qty - qty; stock.ReorderPoint > 0 && stock.Qty > stock.ReorderPoint && newQty <= stock.ReorderPoint {
					lowStock = append(lowStock, lowStockHit{name: prod.NameEn, qty: newQty, reorderPoint: stock.ReorderPoint})
				}
			}
			gross := prod.PriceCents * qty
			subtotal += gross
			cost += prod.CostCents * qty
			lines = append(lines, models.SaleItem{SaleID: id, ProductID: pid, NameSnapshot: prod.NameEn, Qty: qty, UnitPriceCents: prod.PriceCents, UnitCostCents: prod.CostCents, DiscountCents: 0, Note: notes[pid], LineTotalCents: gross})
		}
		for i := range lines {
			if err := tx.Omit("Product").Create(&lines[i]).Error; err != nil {
				return err
			}
			if newServiceIDs[lines[i].ProductID] {
				continue
			}
			ref := id
			if _, err := recordMovement(tx, sale.BranchID, lines[i].ProductID, models.StockMovementSale, -lines[i].Qty, lines[i].UnitCostCents, "SALE", &ref, userID, "Edit "+sale.ReceiptNo); err != nil {
				return err
			}
		}

		// Keep the sale's own exchange rate — an edit corrects what was rung
		// up, it doesn't re-price the sale at today's rate.
		total := subtotal
		totalRiel := utils.USDCentsToRiel(total, sale.ExchangeRate)
		if err := tx.Model(&models.Sale{}).Where("id = ?", id).Updates(map[string]interface{}{
			"subtotal_cents": subtotal, "discount_cents": 0, "total_cents": total, "total_riel": totalRiel, "cost_total_cents": cost,
		}).Error; err != nil {
			return err
		}

		// The amount tendered is untouched; only what it owes / what's owed
		// back is recalculated so the receipt stays arithmetically correct.
		var pay models.Payment
		if err := tx.Where("sale_id = ?", id).First(&pay).Error; err != nil {
			return err
		}
		payUpdates := map[string]interface{}{"amount_cents": total, "amount_riel": totalRiel}
		if pay.Method == models.PaymentCash {
			res, err := utils.CalculateChange(total, pay.ReceivedUSDCents, pay.ReceivedKHRRiel, sale.ExchangeRate)
			if err != nil {
				return err
			}
			if res.ShortCents > 0 {
				return utils.NewAppError(http.StatusUnprocessableEntity, "INSUFFICIENT_CASH", "This sale now totals more than the cash received — void it and ring up a new sale instead.")
			}
			payUpdates["change_usd_cents"], payUpdates["change_khr_riel"] = res.ChangeUSDCents, res.ChangeKHRRiel
		}
		if err := tx.Model(&models.Payment{}).Where("sale_id = ?", id).Updates(payUpdates).Error; err != nil {
			return err
		}

		// Loyalty points were earned on the old total; take those back and
		// grant fresh ones on the new total, same accounting VoidSale uses.
		if sale.CustomerID != nil {
			var earned int64
			tx.Model(&models.LoyaltyTransaction{}).Where("sale_id = ? AND points_change > 0", id).Select("COALESCE(SUM(points_change),0)").Scan(&earned)
			loyaltyRate := a.loadSettings().LoyaltyPointsPerUSD
			newPoints := int64(0)
			if loyaltyRate > 0 {
				newPoints = int64(math.Floor(float64(total) / 100 * loyaltyRate))
			}
			delta := newPoints - earned
			if delta != 0 {
				if err := tx.Model(&models.Customer{}).Where("id = ?", *sale.CustomerID).Update("points", gorm.Expr("GREATEST(points + ?, 0)", delta)).Error; err != nil {
					return err
				}
				sid := id
				if err := tx.Create(&models.LoyaltyTransaction{CustomerID: *sale.CustomerID, SaleID: &sid, PointsChange: delta, Reason: "Edit " + sale.ReceiptNo, CreatedAt: time.Now()}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		if ae, ok := err.(*utils.AppError); ok {
			fail(c, ae)
			return
		}
		dbFail(c, err)
		return
	}
	for _, hit := range lowStock {
		a.Notify.Send(services.AlertLowStock, fmt.Sprintf("⚠️ <b>Low stock</b>\n%s: %d left (reorder point %d)", services.HTMLEscape(hit.name), hit.qty, hit.reorderPoint))
	}
	out, err := a.loadSales("s.id = ?", id)
	if err != nil || len(out) == 0 {
		dbFail(c, err)
		return
	}
	a.audit(c, "pos", "sale.edit", "sale", id, before[0], out[0])
	utils.OK(c, http.StatusOK, out[0])
}

// serviceProductIDs returns which of the given sale items' products are
// SERVICE type — shared by EditSale (both the old-line reversal and the
// new-line application) and mirrors the same check in VoidSale.
func (a *API) serviceProductIDs(tx *gorm.DB, items []models.SaleItem) (map[uint64]bool, error) {
	out := map[uint64]bool{}
	if len(items) == 0 {
		return out, nil
	}
	ids := make([]uint64, len(items))
	for i, it := range items {
		ids[i] = it.ProductID
	}
	var serviceIDs []uint64
	if err := tx.Model(&models.Product{}).Where("id IN ? AND product_type = ?", ids, models.ProductService).Pluck("id", &serviceIDs).Error; err != nil {
		return nil, err
	}
	for _, id := range serviceIDs {
		out[id] = true
	}
	return out, nil
}

// VoidSale cancels a sale that is still on an open shift: stock goes back on
// the shelf, loyalty points are taken back, and a voids_refunds row records
// who approved it. Sales on a closed shift can't be voided — the cash count
// for that shift is already final.
func (a *API) VoidSale(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason" binding:"required,max=255"`
	}
	if !bind(c, &req) {
		return
	}
	userID := middleware.UserIDFrom(c)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var sale models.Sale
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&sale, id).Error; err != nil {
			return err
		}
		if !canAccessBranch(c, sale.BranchID) {
			return utils.ErrForbidden
		}
		if sale.Status != models.SalePaid {
			return utils.NewAppError(http.StatusConflict, "SALE_NOT_VOIDABLE", "Only a paid sale can be voided.")
		}
		var shift models.Shift
		if err := tx.First(&shift, sale.ShiftID).Error; err != nil {
			return err
		}
		if shift.Status != models.ShiftOpen {
			return utils.NewAppError(http.StatusConflict, "SHIFT_CLOSED", "This sale belongs to a closed shift and can't be voided.")
		}
		var items []models.SaleItem
		if err := tx.Where("sale_id = ?", id).Find(&items).Error; err != nil {
			return err
		}
		// A SERVICE item was never decremented on sale (see CreateSale), so
		// voiding one must not "restock" it either — that would fabricate a
		// branch_stock row (and a qty) for something that's never supposed
		// to have either.
		serviceIDs, err := a.serviceProductIDs(tx, items)
		if err != nil {
			return err
		}
		for _, it := range items {
			if serviceIDs[it.ProductID] {
				continue
			}
			ref := id
			if _, err := recordMovement(tx, sale.BranchID, it.ProductID, models.StockMovementRefundReturn, it.Qty, it.UnitCostCents, "SALE", &ref, userID, "Void "+sale.ReceiptNo); err != nil {
				return err
			}
		}
		var earned int64
		tx.Model(&models.LoyaltyTransaction{}).Where("sale_id = ? AND points_change > 0", id).Select("COALESCE(SUM(points_change),0)").Scan(&earned)
		if earned > 0 && sale.CustomerID != nil {
			if err := tx.Model(&models.Customer{}).Where("id = ?", *sale.CustomerID).Update("points", gorm.Expr("GREATEST(points - ?, 0)", earned)).Error; err != nil {
				return err
			}
			sid := id
			if err := tx.Create(&models.LoyaltyTransaction{CustomerID: *sale.CustomerID, SaleID: &sid, PointsChange: -earned, Reason: "Void " + sale.ReceiptNo, CreatedAt: time.Now()}).Error; err != nil {
				return err
			}
		}
		approver := userID
		if err := tx.Create(&models.VoidRefund{SaleID: id, Type: models.VoidType, AmountCents: sale.TotalCents, Reason: req.Reason, CashierID: sale.CashierID, ApprovedByID: &approver, CreatedAt: time.Now()}).Error; err != nil {
			return err
		}
		return tx.Model(&models.Sale{}).Where("id = ?", id).Update("status", models.SaleVoided).Error
	})
	if err != nil {
		if ae, ok := err.(*utils.AppError); ok {
			fail(c, ae)
			return
		}
		dbFail(c, err)
		return
	}
	a.audit(c, "pos", "sale.void", "sale", id, nil, gin.H{"reason": req.Reason})
	out, _ := a.loadSales("s.id = ?", id)
	if len(out) > 0 {
		sale := out[0]
		itemLines := make([]string, len(sale.Items))
		for i, it := range sale.Items {
			itemLines[i] = fmt.Sprintf("%d× %s", it.Qty, services.HTMLEscape(it.Name))
		}
		a.Notify.Send(services.AlertVoidRefund, fmt.Sprintf(
			"🚫 <b>Sale voided</b>\n%s · %s\n%s\nTotal: %s\nReason: %s",
			sale.ReceiptNo, services.HTMLEscape(sale.Cashier), services.FormatItemLines(itemLines, 12), services.FormatUSD(sale.TotalCents), services.HTMLEscape(req.Reason),
		))
	}
	utils.OK(c, http.StatusOK, out[0])
}
