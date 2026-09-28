package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"com-mart/backend/internal/middleware"
	"com-mart/backend/internal/models"
	"com-mart/backend/internal/utils"
)

type poItemDTO struct {
	ProductID     uint64 `json:"product_id"`
	ProductName   string `json:"product_name"`
	ProductNameKm string `json:"product_name_km"`
	SKU           string `json:"sku"`
	QtyOrdered    int64  `json:"qty_ordered"`
	QtyReceived   int64  `json:"qty_received"`
	UnitCostCents int64  `json:"unit_cost_cents"`
}

type poDTO struct {
	ID            uint64      `json:"id"`
	Code          string      `json:"code"`
	BranchID      uint64      `json:"branch_id"`
	SupplierID    uint64      `json:"supplier_id"`
	SupplierName  string      `json:"supplier_name"`
	Status        string      `json:"status"`
	Note          string      `json:"note"`
	ShippingCents int64       `json:"shipping_cents"`
	CreatedAt     string      `json:"created_at"`
	Items         []poItemDTO `json:"items" gorm:"-"`
}

// poSelect is the PO header query (joined to its supplier).
func (a *API) poSelect() *gorm.DB {
	return a.DB.Table("purchase_orders po").
		Joins("LEFT JOIN suppliers s ON s.id = po.supplier_id")
}

const poColumns = "po.id, po.branch_id, po.supplier_id, COALESCE(s.name,'') AS supplier_name, po.status, po.note, po.shipping_cents, DATE_FORMAT(po.created_at,'%Y-%m-%d') AS created_at"

// attachPOItems fills Code and Items on a set of PO headers.
func (a *API) attachPOItems(pos []poDTO) error {
	if len(pos) == 0 {
		return nil
	}
	ids := make([]uint64, len(pos))
	idx := map[uint64]int{}
	for i, p := range pos {
		ids[i] = p.ID
		idx[p.ID] = i
		pos[i].Code = fmt.Sprintf("PO-%d", p.ID)
		pos[i].Items = []poItemDTO{}
	}
	type itemRow struct {
		Item            poItemDTO `gorm:"embedded"`
		PurchaseOrderID uint64
	}
	var items []itemRow
	err := a.DB.Table("purchase_order_items i").
		Select("i.purchase_order_id, i.product_id, p.name_en AS product_name, p.name_km AS product_name_km, p.sku, i.qty_ordered, i.qty_received, i.unit_cost_cents").
		Joins("JOIN products p ON p.id = i.product_id").
		Where("i.purchase_order_id IN ?", ids).Order("i.id").Scan(&items).Error
	if err != nil {
		return err
	}
	for _, it := range items {
		pos[idx[it.PurchaseOrderID]].Items = append(pos[idx[it.PurchaseOrderID]].Items, it.Item)
	}
	return nil
}

// loadPOs returns one PO by id (with its lines).
func (a *API) loadPOs(branchID uint64, onlyID uint64) ([]poDTO, error) {
	var pos []poDTO
	q := a.poSelect().Select(poColumns).Order("po.id DESC")
	if onlyID != 0 {
		q = q.Where("po.id = ?", onlyID)
	} else {
		q = q.Where("po.branch_id = ?", branchID)
	}
	if err := q.Scan(&pos).Error; err != nil {
		return nil, err
	}
	return pos, a.attachPOItems(pos)
}

// ListPurchaseOrders filters (optional): status, supplier_id, date_from /
// date_to (created), q (PO number, supplier or note). Meta summary carries the branch's open-order count.
func (a *API) ListPurchaseOrders(c *gin.Context) {
	branchID := branchParam(c)
	pg := pagerOf(c)
	base := a.poSelect().Where("po.branch_id = ?", branchID)
	if st := strings.ToUpper(c.Query("status")); st != "" {
		base = base.Where("po.status = ?", st)
	}
	if sid := c.Query("supplier_id"); sid != "" {
		base = base.Where("po.supplier_id = ?", sid)
	}
	if from, to := rangeBounds(c); from != nil || to != nil {
		if from != nil {
			base = base.Where("po.created_at >= ?", *from)
		}
		if to != nil {
			base = base.Where("po.created_at < ?", *to)
		}
	}
	if search := strings.TrimSpace(c.Query("q")); search != "" {
		num := strings.TrimPrefix(strings.ToUpper(search), "PO-")
		base = base.Where("CAST(po.id AS CHAR) = ? OR s.name LIKE ? OR po.note LIKE ?", num, likeArg(search), likeArg(search))
	}
	base = base.Session(&gorm.Session{})
	var total int64
	if err := base.Count(&total).Error; err != nil {
		dbFail(c, err)
		return
	}
	var pos []poDTO
	if err := pg.apply(base.Select(poColumns).Order("po.id DESC")).Scan(&pos).Error; err != nil {
		dbFail(c, err)
		return
	}
	if err := a.attachPOItems(pos); err != nil {
		dbFail(c, err)
		return
	}
	var summary gin.H
	if pg.on {
		var open int64
		a.DB.Model(&models.PurchaseOrder{}).Where("branch_id = ? AND status NOT IN ('RECEIVED','CANCELLED')", branchID).Count(&open)
		summary = gin.H{"open_orders": open}
	}
	pg.respond(c, pos, total, summary)
}

type poItemReq struct {
	ProductID     uint64 `json:"product_id" binding:"required"`
	Qty           int64  `json:"qty" binding:"required,min=1"`
	UnitCostCents int64  `json:"unit_cost_cents" binding:"min=0"`
}

type poReq struct {
	BranchID      uint64      `json:"branch_id" binding:"required"`
	SupplierID    uint64      `json:"supplier_id" binding:"required"`
	Status        string      `json:"status" binding:"omitempty,oneof=DRAFT SENT PARTIAL CANCELLED"`
	Note          string      `json:"note" binding:"max=255"`
	ShippingCents int64       `json:"shipping_cents" binding:"min=0"`
	Items         []poItemReq `json:"items" binding:"required,min=1,dive"`
}

func (a *API) savePO(c *gin.Context, existingID uint64) {
	var req poReq
	if !bind(c, &req) {
		return
	}
	if !canAccessBranch(c, req.BranchID) {
		fail(c, utils.ErrForbidden)
		return
	}
	status := req.Status
	if status == "" {
		status = string(models.PurchaseOrderDraft)
	}
	var poID uint64
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if existingID == 0 {
			po := models.PurchaseOrder{BranchID: req.BranchID, SupplierID: req.SupplierID, Status: models.PurchaseOrderStatus(status), Note: req.Note, ShippingCents: req.ShippingCents, UserID: middleware.UserIDFrom(c)}
			if err := tx.Omit("Supplier", "Items").Create(&po).Error; err != nil {
				return err
			}
			poID = po.ID
		} else {
			var po models.PurchaseOrder
			if err := tx.First(&po, existingID).Error; err != nil {
				return err
			}
			if po.Status == models.PurchaseOrderReceived {
				return utils.NewAppError(http.StatusConflict, "PO_LOCKED", "A received purchase order can't be edited.")
			}
			if !canAccessBranch(c, po.BranchID) {
				return utils.ErrForbidden
			}
			if err := tx.Model(&po).Updates(map[string]interface{}{
				"branch_id": req.BranchID, "supplier_id": req.SupplierID, "status": status, "note": req.Note, "shipping_cents": req.ShippingCents,
			}).Error; err != nil {
				return err
			}
			if err := tx.Where("purchase_order_id = ?", existingID).Delete(&models.PurchaseOrderItem{}).Error; err != nil {
				return err
			}
			poID = existingID
		}
		for _, it := range req.Items {
			row := models.PurchaseOrderItem{PurchaseOrderID: poID, ProductID: it.ProductID, QtyOrdered: it.Qty, UnitCostCents: it.UnitCostCents}
			if err := tx.Omit("Product").Create(&row).Error; err != nil {
				return err
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
	out, err := a.loadPOs(0, poID)
	if err != nil || len(out) == 0 {
		dbFail(c, err)
		return
	}
	action, code := "po.update", http.StatusOK
	if existingID == 0 {
		action, code = "po.create", http.StatusCreated
	}
	a.audit(c, "inventory", action, "purchase_order", poID, nil, out[0])
	utils.OK(c, code, out[0])
}

func (a *API) CreatePurchaseOrder(c *gin.Context) { a.savePO(c, 0) }

func (a *API) UpdatePurchaseOrder(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	a.savePO(c, id)
}

func (a *API) DeletePurchaseOrder(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var po models.PurchaseOrder
	if err := a.DB.First(&po, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	if po.Status == models.PurchaseOrderReceived || po.Status == models.PurchaseOrderPartial {
		fail(c, utils.NewAppError(http.StatusConflict, "PO_LOCKED", "A purchase order that has received stock can't be deleted — cancel it instead."))
		return
	}
	if err := a.DB.Delete(&po).Error; err != nil { // items cascade
		dbFail(c, err)
		return
	}
	a.audit(c, "inventory", "po.delete", "purchase_order", id, gin.H{"code": fmt.Sprintf("PO-%d", id)}, nil)
	utils.OK(c, http.StatusOK, gin.H{"deleted": true})
}

// ReceivePurchaseOrder books every outstanding line into branch stock (one
// RECEIVE stock movement per line) and marks the order RECEIVED, all in one
// transaction. Each line's unit cost also becomes the product's current cost
// when it differs, so margins and stock value follow what was really paid.
func (a *API) ReceivePurchaseOrder(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	userID := middleware.UserIDFrom(c)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var po models.PurchaseOrder
		if err := tx.First(&po, id).Error; err != nil {
			return err
		}
		if !canAccessBranch(c, po.BranchID) {
			return utils.ErrForbidden
		}
		if po.Status == models.PurchaseOrderReceived || po.Status == models.PurchaseOrderCancelled {
			return utils.NewAppError(http.StatusConflict, "PO_NOT_RECEIVABLE", fmt.Sprintf("This purchase order is already %s.", po.Status))
		}
		var items []models.PurchaseOrderItem
		if err := tx.Where("purchase_order_id = ?", id).Find(&items).Error; err != nil {
			return err
		}
		for _, it := range items {
			remaining := it.QtyOrdered - it.QtyReceived
			if remaining <= 0 {
				continue
			}
			if _, err := recordMovement(tx, po.BranchID, it.ProductID, models.StockMovementReceive, remaining, it.UnitCostCents, "PO", &id, userID, ""); err != nil {
				return err
			}
			if err := tx.Model(&models.PurchaseOrderItem{}).Where("id = ?", it.ID).Update("qty_received", it.QtyOrdered).Error; err != nil {
				return err
			}
			var cur models.ProductPrice
			err := tx.Where("product_id = ? AND branch_id IS NULL", it.ProductID).Order("effective_at DESC, id DESC").First(&cur).Error
			if err == nil && cur.CostCents != it.UnitCostCents {
				if err := tx.Create(&models.ProductPrice{ProductID: it.ProductID, PriceCents: cur.PriceCents, CostCents: it.UnitCostCents, EffectiveAt: time.Now(), CreatedAt: time.Now()}).Error; err != nil {
					return err
				}
			}
		}
		return tx.Model(&po).Update("status", models.PurchaseOrderReceived).Error
	})
	if err != nil {
		if ae, ok := err.(*utils.AppError); ok {
			fail(c, ae)
			return
		}
		dbFail(c, err)
		return
	}
	a.audit(c, "inventory", "po.receive", "purchase_order", id, nil, gin.H{"code": fmt.Sprintf("PO-%d", id), "status": "RECEIVED"})
	out, _ := a.loadPOs(0, id)
	utils.OK(c, http.StatusOK, out[0])
}
