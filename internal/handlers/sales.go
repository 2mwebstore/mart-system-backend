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
		Select("si.sale_id, si.product_id, si.name_snapshot AS name, COALESCE(p.name_km,'') AS name_km, COALESCE(p.sku,'') AS sku, si.qty, si.unit_price_cents, si.discount_cents, si.line_total_cents").
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

type createSaleItem struct {
	ProductID     uint64 `json:"product_id" binding:"required"`
	Qty           int64  `json:"qty" binding:"required,min=1"`
	DiscountCents int64  `json:"discount_cents" binding:"min=0"`
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
	loyaltyRate := a.loadSettings().LoyaltyPointsPerUSD
	var saleID uint64

	err = a.DB.Transaction(func(tx *gorm.DB) error {
		// Serialise receipt numbering per branch.
		var branch models.Branch
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&branch, device.BranchID).Error; err != nil {
			return err
		}

		var subtotal, lineDiscounts, cost int64
		lines := make([]models.SaleItem, 0, len(req.Items))
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
				ID         uint64
				NameEn     string
				Active     bool
				PriceCents int64
				CostCents  int64
			}
			err := tx.Table("products p").
				Select("p.id, p.name_en, p.active, COALESCE(pp.price_cents,0) AS price_cents, COALESCE(pp.cost_cents,0) AS cost_cents").
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
			var stock models.BranchStock
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("branch_id = ? AND product_id = ?", device.BranchID, pid).First(&stock).Error; err != nil || stock.Qty < it.Qty {
				return utils.NewAppError(http.StatusUnprocessableEntity, "INSUFFICIENT_STOCK", fmt.Sprintf("Not enough stock for %s (%d left).", prod.NameEn, stock.Qty))
			}
			gross := prod.PriceCents * it.Qty
			disc := it.DiscountCents
			if disc > gross {
				disc = gross
			}
			subtotal += gross
			lineDiscounts += disc
			cost += prod.CostCents * it.Qty
			lines = append(lines, models.SaleItem{ProductID: pid, NameSnapshot: prod.NameEn, Qty: it.Qty, UnitPriceCents: prod.PriceCents, UnitCostCents: prod.CostCents, DiscountCents: disc, LineTotalCents: gross - disc})
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
		var seq int64
		tx.Model(&models.Sale{}).Where("branch_id = ? AND business_date = ?", device.BranchID, businessDate).Count(&seq)
		sale := models.Sale{
			BranchID: device.BranchID, ShiftID: shift.ID, DeviceID: device.ID, CashierID: userID, CustomerID: req.CustomerID,
			ReceiptNo: fmt.Sprintf("#%02d%02d-%03d", int(now.Month()), now.Day(), seq+1), BusinessDate: businessDate, Status: models.SalePaid,
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
	out, err := a.loadSales("s.id = ?", saleID)
	if err != nil || len(out) == 0 {
		dbFail(c, err)
		return
	}
	utils.OK(c, http.StatusCreated, out[0])
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
		for _, it := range items {
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
	utils.OK(c, http.StatusOK, out[0])
}
