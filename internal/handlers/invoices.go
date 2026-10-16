package handlers

// Back-office sales ("invoices"): a customer, searched-in items with per-line
// discounts, an invoice discount, shipping and a note, paid over time in any
// number of payments. Separate from POS receipts (sales.go): no till, shift
// or cash drawer is involved. Stock leaves when the invoice is saved and
// comes back if it's edited or cancelled. See docs/DECISIONS.md.

import (
	"fmt"
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

// DocInvoice numbers invoices ("SI-000001"), separate from POS receipts.
const DocInvoice = "INVOICE"

func invoicePaymentStatus(total, paid int64) models.InvoicePaymentStatus {
	switch {
	case paid >= total:
		return models.InvoicePaid
	case paid <= 0:
		return models.InvoiceDue
	default:
		return models.InvoicePending
	}
}

// ---- Read ---------------------------------------------------------------------

type invoiceItemDTO struct {
	InvoiceID      uint64 `json:"-"`
	ProductID      uint64 `json:"product_id"`
	NameEn         string `json:"name_en"`
	NameKm         string `json:"name_km"`
	SKU            string `json:"sku"`
	Qty            int64  `json:"qty"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	DiscountCents  int64  `json:"discount_cents"`
	LineTotalCents int64  `json:"line_total_cents"`
	Note           string `json:"note"`
}

type invoicePaymentDTO struct {
	ID          uint64    `json:"id"`
	PaidOn      string    `json:"paid_on"`
	MethodName  string    `json:"method_name"`
	MethodType  string    `json:"method_type"`
	AmountCents int64     `json:"amount_cents"`
	Reference   string    `json:"reference"`
	Note        string    `json:"note"`
	Attachment  *string   `json:"attachment"`
	User        string    `json:"user"`
	CreatedAt   time.Time `json:"created_at"`
}

type invoiceDTO struct {
	ID                uint64              `json:"id"`
	Code              string              `json:"code"`
	BranchID          uint64              `json:"branch_id"`
	CustomerID        uint64              `json:"customer_id"`
	CustomerName      string              `json:"customer_name"`
	CustomerPhone     string              `json:"customer_phone"`
	InvoiceDate       string              `json:"invoice_date"`
	DueDate           *string             `json:"due_date"`
	Status            string              `json:"status"`
	PaymentStatus     string              `json:"payment_status"`
	SubtotalCents     int64               `json:"subtotal_cents"`
	LineDiscountCents int64               `json:"line_discount_cents"`
	DiscountCents     int64               `json:"discount_cents"`
	ShippingCents     int64               `json:"shipping_cents"`
	TotalCents        int64               `json:"total_cents"`
	PaidCents         int64               `json:"paid_cents"`
	BalanceCents      int64               `json:"balance_cents"`
	ExchangeRate      int64               `json:"exchange_rate"`
	ItemCount         int64               `json:"item_count"`
	Note              string              `json:"note"`
	CancelReason      string              `json:"cancel_reason"`
	CreatedBy         string              `json:"created_by"`
	CreatedAt         time.Time           `json:"created_at"`
	Items             []invoiceItemDTO    `json:"items,omitempty" gorm:"-"`
	Payments          []invoicePaymentDTO `json:"payments,omitempty" gorm:"-"`
}

func (a *API) invoiceBase() *gorm.DB {
	return a.DB.Table("invoices i").
		Joins("JOIN customers cu ON cu.id = i.customer_id").
		Joins("LEFT JOIN users u ON u.id = i.user_id")
}

const invoiceColumns = `i.id, i.code, i.branch_id, i.customer_id, cu.name AS customer_name, cu.phone AS customer_phone,
	DATE_FORMAT(i.invoice_date, '%Y-%m-%d') AS invoice_date, DATE_FORMAT(i.due_date, '%Y-%m-%d') AS due_date,
	i.status, i.payment_status, i.subtotal_cents, i.line_discount_cents, i.discount_cents, i.shipping_cents,
	i.total_cents, i.paid_cents, GREATEST(i.total_cents - i.paid_cents, 0) AS balance_cents, i.exchange_rate,
	(SELECT COALESCE(SUM(x.qty), 0) FROM invoice_items x WHERE x.invoice_id = i.id) AS item_count,
	i.note, i.cancel_reason, COALESCE(u.full_name, '') AS created_by, i.created_at`

// invoiceFilters: q (code, customer name or phone), customer_id, status
// (open | cancelled), payment_status (DUE | PENDING | PAID | unpaid = DUE +
// PENDING), date_from / date_to on the invoice date.
func invoiceFilters(c *gin.Context, q *gorm.DB) *gorm.DB {
	if s := strings.TrimSpace(c.Query("q")); s != "" {
		q = q.Where("i.code LIKE ? OR cu.name LIKE ? OR cu.phone LIKE ?", likeArg(s), likeArg(s), likeArg(s))
	}
	if cid := c.Query("customer_id"); cid != "" {
		q = q.Where("i.customer_id = ?", cid)
	}
	switch strings.ToUpper(c.Query("status")) {
	case "CANCELLED":
		q = q.Where("i.status = ?", models.InvoiceCancelled)
	case "ALL":
	default:
		q = q.Where("i.status = ?", models.InvoiceOpen)
	}
	switch ps := strings.ToUpper(c.Query("payment_status")); ps {
	case "":
	case "UNPAID":
		q = q.Where("i.payment_status IN ?", []string{string(models.InvoiceDue), string(models.InvoicePending)})
	case "OVERDUE":
		q = q.Where("i.payment_status <> ? AND i.due_date IS NOT NULL AND i.due_date < ?", models.InvoicePaid, today())
	default:
		q = q.Where("i.payment_status = ?", ps)
	}
	if from, to := rangeBounds(c); from != nil || to != nil {
		if from != nil {
			q = q.Where("i.invoice_date >= ?", *from)
		}
		if to != nil {
			q = q.Where("i.invoice_date < ?", *to)
		}
	}
	return q
}

type invoiceSummary struct {
	Count        int64 `json:"count"`
	TotalCents   int64 `json:"total_cents"`
	PaidCents    int64 `json:"paid_cents"`
	BalanceCents int64 `json:"balance_cents"`
	Overdue      int64 `json:"overdue"`
}

func (a *API) ListInvoices(c *gin.Context) {
	branchID := branchParam(c)
	pg := pagerOf(c)
	base := func() *gorm.DB { return invoiceFilters(c, a.invoiceBase().Where("i.branch_id = ?", branchID)) }
	var total int64
	if err := base().Count(&total).Error; err != nil {
		dbFail(c, err)
		return
	}
	rows := []invoiceDTO{}
	if err := pg.apply(base().Select(invoiceColumns).Order("i.invoice_date DESC, i.id DESC")).Scan(&rows).Error; err != nil {
		dbFail(c, err)
		return
	}
	var sum invoiceSummary
	base().Select(`COUNT(*) AS count, COALESCE(SUM(i.total_cents), 0) AS total_cents, COALESCE(SUM(i.paid_cents), 0) AS paid_cents,
		COALESCE(SUM(GREATEST(i.total_cents - i.paid_cents, 0)), 0) AS balance_cents,
		COALESCE(SUM(i.payment_status <> 'PAID' AND i.due_date IS NOT NULL AND i.due_date < ?), 0) AS overdue`, today()).Scan(&sum)
	pg.respond(c, rows, total, sum)
}

func (a *API) loadInvoice(id uint64) (*invoiceDTO, error) {
	rows := []invoiceDTO{}
	if err := a.invoiceBase().Select(invoiceColumns).Where("i.id = ?", id).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	inv := rows[0]
	inv.Items = []invoiceItemDTO{}
	if err := a.DB.Model(&models.InvoiceItem{}).
		Select("invoice_id, product_id, name_en, name_km, sku, qty, unit_price_cents, discount_cents, line_total_cents, note").
		Where("invoice_id = ?", id).Order("id").Scan(&inv.Items).Error; err != nil {
		return nil, err
	}
	inv.Payments = []invoicePaymentDTO{}
	if err := a.DB.Table("invoice_payments p").Joins("LEFT JOIN users u ON u.id = p.user_id").
		Select(`p.id, DATE_FORMAT(p.paid_on, '%Y-%m-%d') AS paid_on, p.method_name, p.method_type, p.amount_cents,
			p.reference, p.note, p.attachment, COALESCE(u.full_name, '') AS user, p.created_at`).
		Where("p.invoice_id = ?", id).Order("p.paid_on, p.id").Scan(&inv.Payments).Error; err != nil {
		return nil, err
	}
	return &inv, nil
}

// GetInvoice is one invoice with its items and payments (attachments included).
func (a *API) GetInvoice(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	inv, err := a.loadInvoice(id)
	if err != nil {
		dbFail(c, err)
		return
	}
	if !canAccessBranch(c, inv.BranchID) {
		fail(c, utils.ErrForbidden)
		return
	}
	utils.OK(c, http.StatusOK, inv)
}

// ---- Create / edit ------------------------------------------------------------

type invoiceItemReq struct {
	ProductID      uint64 `json:"product_id" binding:"required"`
	Qty            int64  `json:"qty" binding:"required,min=1,max=100000"`
	UnitPriceCents int64  `json:"unit_price_cents" binding:"min=0"`
	DiscountCents  int64  `json:"discount_cents" binding:"min=0"`
	Note           string `json:"note" binding:"max=255"`
}

type invoiceReq struct {
	BranchID      uint64           `json:"branch_id" binding:"required"`
	CustomerID    uint64           `json:"customer_id" binding:"required"`
	InvoiceDate   string           `json:"invoice_date" binding:"required"`
	DueDate       string           `json:"due_date"`
	Items         []invoiceItemReq `json:"items" binding:"required,min=1,max=200,dive"`
	DiscountCents int64            `json:"discount_cents" binding:"min=0"`
	ShippingCents int64            `json:"shipping_cents" binding:"min=0"`
	// Rich text (HTML from the RichTextEditor); sanitised before it's stored.
	Note string `json:"note" binding:"max=20000"`
	// Money received when the invoice is made (create only; optional). Saved
	// with the invoice in one transaction, as its first payment.
	Payment *invoicePaymentReq `json:"payment"`
}

// invoiceDraft is a validated, priced request ready to write.
type invoiceDraft struct {
	invoiceDate                                    time.Time
	dueDate                                        *time.Time
	lines                                          []models.InvoiceItem
	subtotal, lineDiscounts, discount, total, cost int64
	service                                        map[uint64]bool
}

// priceInvoice checks the request against the catalog (product exists and is
// active, not a family parent, this branch's cost) and works out the totals.
// The unit price is the one on the request — back-office sales can be priced
// for the customer (wholesale); the form fills in the catalog price.
func (a *API) priceInvoice(tx *gorm.DB, req invoiceReq) (*invoiceDraft, error) {
	d := &invoiceDraft{service: map[uint64]bool{}}
	day, ok := parseDay(req.InvoiceDate)
	if !ok {
		return nil, utils.NewValidationError(map[string]string{"invoice_date": "Pick the invoice date."})
	}
	d.invoiceDate = day
	if strings.TrimSpace(req.DueDate) != "" {
		due, ok := parseDay(req.DueDate)
		if !ok || due.Before(day) {
			return nil, utils.NewValidationError(map[string]string{"due_date": "The due date can't be before the invoice date."})
		}
		d.dueDate = &due
	}
	var cust models.Customer
	if err := tx.Where("id = ? AND deleted_at IS NULL", req.CustomerID).First(&cust).Error; err != nil {
		return nil, utils.NewValidationError(map[string]string{"customer_id": "Pick a customer."})
	}
	for _, it := range req.Items {
		var p productDTO
		if err := a.productQuery(req.BranchID).Where("p.id = ?", it.ProductID).Scan(&p).Error; err != nil {
			return nil, err
		}
		if p.ID == 0 || !p.Active || p.VariantCount > 0 {
			return nil, utils.NewAppError(http.StatusUnprocessableEntity, "PRODUCT_UNAVAILABLE", fmt.Sprintf("Product #%d isn't available for sale.", it.ProductID))
		}
		gross := it.UnitPriceCents * it.Qty
		disc := it.DiscountCents
		if disc > gross {
			disc = gross
		}
		if p.ProductType == string(models.ProductService) {
			d.service[p.ID] = true
		}
		d.subtotal += gross
		d.lineDiscounts += disc
		d.cost += p.CostCents * it.Qty
		d.lines = append(d.lines, models.InvoiceItem{
			ProductID: p.ID, NameEn: p.NameEn, NameKm: p.NameKm, SKU: p.SKU, Qty: it.Qty,
			UnitPriceCents: it.UnitPriceCents, DiscountCents: disc, LineTotalCents: gross - disc,
			UnitCostCents: p.CostCents, Note: strings.TrimSpace(it.Note),
		})
	}
	afterLines := d.subtotal - d.lineDiscounts
	d.discount = req.DiscountCents
	if d.discount > afterLines {
		d.discount = afterLines
	}
	d.total = afterLines - d.discount + req.ShippingCents
	return d, nil
}

// takeInvoiceStock moves stock out for an invoice's lines (SERVICE lines have
// none), honouring the store's out-of-stock policy like a POS sale does.
// Products that drop to (or below) their reorder point are appended to
// lowStock, to be alerted once the transaction commits — like a POS sale.
func (a *API) takeInvoiceStock(tx *gorm.DB, inv *models.Invoice, d *invoiceDraft, userID uint64, lowStock *[]lowStockHit) error {
	allowOOS := a.loadSettings().AllowOutOfStockSale
	need := map[uint64]int64{}
	for _, l := range d.lines {
		if !d.service[l.ProductID] {
			need[l.ProductID] += l.Qty
		}
	}
	for _, l := range d.lines {
		if d.service[l.ProductID] {
			continue
		}
		if q, ok := need[l.ProductID]; ok {
			var stock models.BranchStock
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("branch_id = ? AND product_id = ?", inv.BranchID, l.ProductID).First(&stock).Error
			if err != nil && err != gorm.ErrRecordNotFound {
				return err
			}
			if stock.Qty < q && !allowOOS {
				return utils.NewAppError(http.StatusUnprocessableEntity, "INSUFFICIENT_STOCK", fmt.Sprintf("Not enough stock for %s (%d left).", l.NameEn, stock.Qty))
			}
			if after := stock.Qty - q; stock.ReorderPoint > 0 && stock.Qty > stock.ReorderPoint && after <= stock.ReorderPoint {
				*lowStock = append(*lowStock, lowStockHit{name: l.NameEn, qty: after, reorderPoint: stock.ReorderPoint})
			}
			delete(need, l.ProductID) // checked once per product
		}
		ref := inv.ID
		if _, err := recordMovement(tx, inv.BranchID, l.ProductID, models.StockMovementSale, -l.Qty, l.UnitCostCents, "INVOICE", &ref, userID, "Invoice "+inv.Code); err != nil {
			return err
		}
	}
	return nil
}

// returnInvoiceStock puts an invoice's current lines back on the shelf.
func (a *API) returnInvoiceStock(tx *gorm.DB, inv *models.Invoice, userID uint64, why string) error {
	var items []models.InvoiceItem
	if err := tx.Where("invoice_id = ?", inv.ID).Find(&items).Error; err != nil {
		return err
	}
	ids := make([]uint64, len(items))
	for i, it := range items {
		ids[i] = it.ProductID
	}
	var service []uint64
	if len(ids) > 0 {
		tx.Model(&models.Product{}).Where("id IN ? AND product_type = ?", ids, models.ProductService).Pluck("id", &service)
	}
	isService := map[uint64]bool{}
	for _, id := range service {
		isService[id] = true
	}
	for _, it := range items {
		if isService[it.ProductID] {
			continue
		}
		ref := inv.ID
		if _, err := recordMovement(tx, inv.BranchID, it.ProductID, models.StockMovementRefundReturn, it.Qty, it.UnitCostCents, "INVOICE", &ref, userID, why+" "+inv.Code); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) alertLowStock(hits []lowStockHit) {
	for _, hit := range hits {
		a.Notify.Send(services.AlertLowStock, fmt.Sprintf("⚠️ <b>Low stock</b>\n%s: %d left (reorder point %d)", services.HTMLEscape(hit.name), hit.qty, hit.reorderPoint))
	}
}

func respondAppErr(c *gin.Context, err error) {
	if ae, ok := err.(*utils.AppError); ok {
		fail(c, ae)
		return
	}
	dbFail(c, err)
}

func (a *API) CreateInvoice(c *gin.Context) {
	var req invoiceReq
	if !bind(c, &req) {
		return
	}
	if !canAccessBranch(c, req.BranchID) {
		fail(c, utils.ErrForbidden)
		return
	}
	userID := middleware.UserIDFrom(c)
	var id uint64
	var lowStock []lowStockHit
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		d, err := a.priceInvoice(tx, req)
		if err != nil {
			return err
		}
		code, err := nextDocNumber(tx, DocInvoice)
		if err != nil {
			return err
		}
		inv := models.Invoice{
			Code: code, BranchID: req.BranchID, CustomerID: req.CustomerID, InvoiceDate: d.invoiceDate, DueDate: d.dueDate,
			Status: models.InvoiceOpen, PaymentStatus: invoicePaymentStatus(d.total, 0),
			SubtotalCents: d.subtotal, LineDiscountCents: d.lineDiscounts, DiscountCents: d.discount, ShippingCents: req.ShippingCents,
			TotalCents: d.total, CostTotalCents: d.cost, ExchangeRate: a.currentRate(), Note: utils.SanitizeRichText(req.Note), UserID: userID,
		}
		if err := tx.Create(&inv).Error; err != nil {
			return err
		}
		id = inv.ID
		for i := range d.lines {
			d.lines[i].InvoiceID = inv.ID
		}
		if err := tx.Create(&d.lines).Error; err != nil {
			return err
		}
		if err := a.takeInvoiceStock(tx, &inv, d, userID, &lowStock); err != nil {
			return err
		}
		if req.Payment == nil || req.Payment.AmountCents <= 0 {
			return nil
		}
		return a.firstInvoicePayment(tx, &inv, *req.Payment, userID)
	})
	if err != nil {
		respondAppErr(c, err)
		return
	}
	out, err := a.loadInvoice(id)
	if err != nil {
		dbFail(c, err)
		return
	}
	a.alertLowStock(lowStock)
	a.audit(c, "invoice", "invoice.create", "invoice", id, nil, out)
	utils.OK(c, http.StatusCreated, out)
}

// UpdateInvoice replaces an open invoice's customer, dates, lines, discounts,
// shipping and note. The old lines' stock goes back and the new lines' stock
// comes out in the same transaction; the new total can't be less than what's
// already been paid.
func (a *API) UpdateInvoice(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req invoiceReq
	if !bind(c, &req) {
		return
	}
	userID := middleware.UserIDFrom(c)
	before, _ := a.loadInvoice(id)
	var lowStock []lowStockHit
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var inv models.Invoice
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&inv, id).Error; err != nil {
			return err
		}
		if !canAccessBranch(c, inv.BranchID) {
			return utils.ErrForbidden
		}
		if inv.Status != models.InvoiceOpen {
			return utils.NewAppError(http.StatusConflict, "INVOICE_CANCELLED", "A cancelled invoice can't be changed.")
		}
		req.BranchID = inv.BranchID // an invoice stays in its branch (its stock moved there)
		d, err := a.priceInvoice(tx, req)
		if err != nil {
			return err
		}
		if d.total < inv.PaidCents {
			return utils.NewAppError(http.StatusUnprocessableEntity, "INVOICE_BELOW_PAID", "The new total is less than what has already been paid.")
		}
		if err := a.returnInvoiceStock(tx, &inv, userID, "Edit"); err != nil {
			return err
		}
		if err := tx.Where("invoice_id = ?", id).Delete(&models.InvoiceItem{}).Error; err != nil {
			return err
		}
		for i := range d.lines {
			d.lines[i].InvoiceID = id
		}
		if err := tx.Create(&d.lines).Error; err != nil {
			return err
		}
		if err := tx.Model(&inv).Updates(map[string]interface{}{
			"customer_id": req.CustomerID, "invoice_date": d.invoiceDate, "due_date": d.dueDate,
			"subtotal_cents": d.subtotal, "line_discount_cents": d.lineDiscounts, "discount_cents": d.discount,
			"shipping_cents": req.ShippingCents, "total_cents": d.total, "cost_total_cents": d.cost,
			"payment_status": invoicePaymentStatus(d.total, inv.PaidCents), "note": utils.SanitizeRichText(req.Note),
		}).Error; err != nil {
			return err
		}
		return a.takeInvoiceStock(tx, &inv, d, userID, &lowStock)
	})
	if err != nil {
		respondAppErr(c, err)
		return
	}
	out, err := a.loadInvoice(id)
	if err != nil {
		dbFail(c, err)
		return
	}
	a.alertLowStock(lowStock)
	a.audit(c, "invoice", "invoice.update", "invoice", id, before, out)
	utils.OK(c, http.StatusOK, out)
}

type cancelInvoiceReq struct {
	Reason string `json:"reason" binding:"max=255"`
}

// CancelInvoice voids an invoice that has no payments: its stock goes back.
// Payments must be removed first, so money already recorded is never lost
// silently with the invoice.
func (a *API) CancelInvoice(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req cancelInvoiceReq
	if !bind(c, &req) {
		return
	}
	userID := middleware.UserIDFrom(c)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var inv models.Invoice
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&inv, id).Error; err != nil {
			return err
		}
		if !canAccessBranch(c, inv.BranchID) {
			return utils.ErrForbidden
		}
		if inv.Status != models.InvoiceOpen {
			return utils.NewAppError(http.StatusConflict, "INVOICE_CANCELLED", "This invoice is already cancelled.")
		}
		if inv.PaidCents > 0 {
			return utils.NewAppError(http.StatusConflict, "INVOICE_HAS_PAYMENTS", "Remove this invoice's payments before cancelling it.")
		}
		if err := a.returnInvoiceStock(tx, &inv, userID, "Cancel"); err != nil {
			return err
		}
		now := time.Now()
		return tx.Model(&inv).Updates(map[string]interface{}{"status": models.InvoiceCancelled, "cancelled_at": now, "cancel_reason": strings.TrimSpace(req.Reason)}).Error
	})
	if err != nil {
		respondAppErr(c, err)
		return
	}
	out, err := a.loadInvoice(id)
	if err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "invoice", "invoice.cancel", "invoice", id, nil, out)
	utils.OK(c, http.StatusOK, out)
}

// firstInvoicePayment records the money received when an invoice is created,
// inside the same transaction (same rules as AddInvoicePayment).
func (a *API) firstInvoicePayment(tx *gorm.DB, inv *models.Invoice, req invoicePaymentReq, userID uint64) error {
	paidOn, ok := parseDay(req.PaidOn)
	if !ok {
		return utils.NewValidationError(map[string]string{"paid_on": "Pick the payment date."})
	}
	att := strings.TrimSpace(req.Attachment)
	if att != "" && !validAttachment(att) {
		return utils.NewValidationError(map[string]string{"attachment": "The proof must be an image or a web link (up to about 2 MB)."})
	}
	if req.AmountCents > inv.TotalCents {
		return utils.NewAppError(http.StatusUnprocessableEntity, "PAYMENT_OVER_BALANCE", fmt.Sprintf("That's more than the %s still owed.", formatCents(inv.TotalCents)))
	}
	p := models.InvoicePayment{
		InvoiceID: inv.ID, PaidOn: paidOn, MethodName: strings.TrimSpace(req.MethodName), MethodType: req.MethodType,
		AmountCents: req.AmountCents, Reference: strings.TrimSpace(req.Reference), Note: strings.TrimSpace(req.Note),
		UserID: userID, CreatedAt: time.Now(),
	}
	if att != "" {
		p.Attachment = &att
	}
	if err := tx.Create(&p).Error; err != nil {
		return err
	}
	return tx.Model(inv).Updates(map[string]interface{}{"paid_cents": req.AmountCents, "payment_status": invoicePaymentStatus(inv.TotalCents, req.AmountCents)}).Error
}

// ---- Payments -----------------------------------------------------------------

const maxAttachmentLen = 2_500_000 // ~1.8 MB image as a data URL; the browser shrinks photos first

type invoicePaymentReq struct {
	PaidOn      string `json:"paid_on" binding:"required"`
	MethodName  string `json:"method_name" binding:"required,max=60"`
	MethodType  string `json:"method_type" binding:"required,oneof=CASH KHQR CARD OTHER"`
	AmountCents int64  `json:"amount_cents" binding:"required,min=1"`
	Reference   string `json:"reference" binding:"max=120"`
	Note        string `json:"note" binding:"max=255"`
	Attachment  string `json:"attachment"`
}

func validAttachment(s string) bool {
	if len(s) > maxAttachmentLen {
		return false
	}
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "data:image/")
}

// AddInvoicePayment records one payment against an open invoice. A payment
// can't be more than what's still owed.
func (a *API) AddInvoicePayment(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req invoicePaymentReq
	if !bind(c, &req) {
		return
	}
	paidOn, ok := parseDay(req.PaidOn)
	if !ok {
		invalid(c, "paid_on", "Pick the payment date.")
		return
	}
	att := strings.TrimSpace(req.Attachment)
	if att != "" && !validAttachment(att) {
		invalid(c, "attachment", "The proof must be an image or a web link (up to about 2 MB).")
		return
	}
	userID := middleware.UserIDFrom(c)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var inv models.Invoice
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&inv, id).Error; err != nil {
			return err
		}
		if !canAccessBranch(c, inv.BranchID) {
			return utils.ErrForbidden
		}
		if inv.Status != models.InvoiceOpen {
			return utils.NewAppError(http.StatusConflict, "INVOICE_CANCELLED", "A cancelled invoice can't take payments.")
		}
		balance := inv.TotalCents - inv.PaidCents
		if balance <= 0 {
			return utils.NewAppError(http.StatusConflict, "INVOICE_PAID", "This invoice is already paid in full.")
		}
		if req.AmountCents > balance {
			return utils.NewAppError(http.StatusUnprocessableEntity, "PAYMENT_OVER_BALANCE", fmt.Sprintf("That's more than the %s still owed.", formatCents(balance)))
		}
		p := models.InvoicePayment{
			InvoiceID: id, PaidOn: paidOn, MethodName: strings.TrimSpace(req.MethodName), MethodType: req.MethodType,
			AmountCents: req.AmountCents, Reference: strings.TrimSpace(req.Reference), Note: strings.TrimSpace(req.Note),
			UserID: userID, CreatedAt: time.Now(),
		}
		if att != "" {
			p.Attachment = &att
		}
		if err := tx.Create(&p).Error; err != nil {
			return err
		}
		paid := inv.PaidCents + req.AmountCents
		return tx.Model(&inv).Updates(map[string]interface{}{"paid_cents": paid, "payment_status": invoicePaymentStatus(inv.TotalCents, paid)}).Error
	})
	if err != nil {
		respondAppErr(c, err)
		return
	}
	out, err := a.loadInvoice(id)
	if err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "invoice", "invoice.payment_add", "invoice", id, nil, gin.H{"amount_cents": req.AmountCents, "method": req.MethodName, "paid_on": req.PaidOn})
	utils.OK(c, http.StatusCreated, out)
}

// DeleteInvoicePayment removes a payment recorded by mistake.
func (a *API) DeleteInvoicePayment(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var payID uint64
	if _, err := fmt.Sscan(c.Param("paymentId"), &payID); err != nil || payID == 0 {
		fail(c, utils.ErrNotFound)
		return
	}
	var removed models.InvoicePayment
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var inv models.Invoice
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&inv, id).Error; err != nil {
			return err
		}
		if !canAccessBranch(c, inv.BranchID) {
			return utils.ErrForbidden
		}
		if err := tx.Where("id = ? AND invoice_id = ?", payID, id).First(&removed).Error; err != nil {
			return err
		}
		if err := tx.Delete(&removed).Error; err != nil {
			return err
		}
		paid := inv.PaidCents - removed.AmountCents
		if paid < 0 {
			paid = 0
		}
		return tx.Model(&inv).Updates(map[string]interface{}{"paid_cents": paid, "payment_status": invoicePaymentStatus(inv.TotalCents, paid)}).Error
	})
	if err != nil {
		respondAppErr(c, err)
		return
	}
	out, err := a.loadInvoice(id)
	if err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "invoice", "invoice.payment_delete", "invoice", id,
		gin.H{"amount_cents": removed.AmountCents, "method": removed.MethodName, "paid_on": removed.PaidOn.Format("2006-01-02")}, nil)
	utils.OK(c, http.StatusOK, out)
}

func formatCents(c int64) string {
	return fmt.Sprintf("$%d.%02d", c/100, c%100)
}
