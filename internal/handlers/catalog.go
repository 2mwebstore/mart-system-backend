package handlers

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
	"com-mart/backend/internal/utils"
)

// ---- Categories --------------------------------------------------------------

type categoryDTO struct {
	ID           uint64 `json:"id"`
	NameEn       string `json:"name_en"`
	NameKm       string `json:"name_km"`
	ProductCount int64  `json:"product_count"`
}

type categoryReq struct {
	NameEn string `json:"name_en" binding:"required,max=100"`
	NameKm string `json:"name_km" binding:"max=100"`
}

func (a *API) ListCategories(c *gin.Context) {
	pg := pagerOf(c)
	base := a.DB.Table("categories c").Where("c.deleted_at IS NULL")
	base = base.Session(&gorm.Session{})
	if search := strings.TrimSpace(c.Query("q")); search != "" {
		base = base.Where("c.name_en LIKE ? OR c.name_km LIKE ?", likeArg(search), likeArg(search))
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		dbFail(c, err)
		return
	}
	var rows []categoryDTO
	err := pg.apply(base.Select("c.id, c.name_en, c.name_km, (SELECT COUNT(*) FROM products p WHERE p.category_id = c.id AND p.deleted_at IS NULL) AS product_count").
		Order("c.sort, c.id")).Scan(&rows).Error
	if err != nil {
		dbFail(c, err)
		return
	}
	pg.respond(c, rows, total, nil)
}

func (a *API) CreateCategory(c *gin.Context) {
	var req categoryReq
	if !bind(c, &req) {
		return
	}
	cat := models.Category{NameEn: req.NameEn, NameKm: req.NameKm}
	if err := a.DB.Create(&cat).Error; err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "inventory", "category.create", "category", cat.ID, nil, req)
	utils.OK(c, http.StatusCreated, categoryDTO{ID: cat.ID, NameEn: cat.NameEn, NameKm: cat.NameKm})
}

func (a *API) UpdateCategory(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req categoryReq
	if !bind(c, &req) {
		return
	}
	var cat models.Category
	if err := a.DB.First(&cat, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	before := categoryReq{cat.NameEn, cat.NameKm}
	cat.NameEn, cat.NameKm = req.NameEn, req.NameKm
	if err := a.DB.Save(&cat).Error; err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "inventory", "category.update", "category", id, before, req)
	utils.OK(c, http.StatusOK, categoryDTO{ID: cat.ID, NameEn: cat.NameEn, NameKm: cat.NameKm})
}

func (a *API) DeleteCategory(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var cat models.Category
	if err := a.DB.First(&cat, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		// Products in a deleted category simply become uncategorised.
		if err := tx.Model(&models.Product{}).Where("category_id = ?", id).Update("category_id", nil).Error; err != nil {
			return err
		}
		return tx.Delete(&cat).Error
	})
	if err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "inventory", "category.delete", "category", id, categoryReq{cat.NameEn, cat.NameKm}, nil)
	utils.OK(c, http.StatusOK, gin.H{"deleted": true})
}

// ---- Suppliers --------------------------------------------------------------

type supplierDTO struct {
	ID           uint64 `json:"id"`
	Name         string `json:"name"`
	Phone        string `json:"phone"`
	Contact      string `json:"contact"`
	PaymentTerms string `json:"payment_terms"`
	// Products supplied (filled by the list endpoint only).
	ProductCount int64 `json:"product_count"`
}

type supplierReq struct {
	Name         string `json:"name" binding:"required,max=150"`
	Phone        string `json:"phone" binding:"max=30"`
	Contact      string `json:"contact" binding:"max=150"`
	PaymentTerms string `json:"payment_terms" binding:"max=150"`
}

func toSupplierDTO(s models.Supplier) supplierDTO {
	return supplierDTO{ID: s.ID, Name: s.Name, Phone: s.Phone, Contact: s.Contact, PaymentTerms: s.PaymentTerms}
}

func (a *API) ListSuppliers(c *gin.Context) {
	pg := pagerOf(c)
	base := a.DB.Table("suppliers s").Where("s.deleted_at IS NULL")
	base = base.Session(&gorm.Session{})
	if search := strings.TrimSpace(c.Query("q")); search != "" {
		base = base.Where("s.name LIKE ? OR s.phone LIKE ? OR s.contact LIKE ?", likeArg(search), likeArg(search), likeArg(search))
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		dbFail(c, err)
		return
	}
	var rows []supplierDTO
	err := pg.apply(base.Select("s.id, s.name, s.phone, s.contact, s.payment_terms, (SELECT COUNT(*) FROM products p WHERE p.supplier_id = s.id AND p.deleted_at IS NULL) AS product_count").
		Order("s.id DESC")).Scan(&rows).Error
	if err != nil {
		dbFail(c, err)
		return
	}
	pg.respond(c, rows, total, nil)
}

func (a *API) CreateSupplier(c *gin.Context) {
	var req supplierReq
	if !bind(c, &req) {
		return
	}
	s := models.Supplier{Name: req.Name, Phone: req.Phone, Contact: req.Contact, PaymentTerms: req.PaymentTerms}
	if err := a.DB.Create(&s).Error; err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "inventory", "supplier.create", "supplier", s.ID, nil, toSupplierDTO(s))
	utils.OK(c, http.StatusCreated, toSupplierDTO(s))
}

func (a *API) UpdateSupplier(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req supplierReq
	if !bind(c, &req) {
		return
	}
	var s models.Supplier
	if err := a.DB.First(&s, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	before := toSupplierDTO(s)
	s.Name, s.Phone, s.Contact, s.PaymentTerms = req.Name, req.Phone, req.Contact, req.PaymentTerms
	if err := a.DB.Save(&s).Error; err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "inventory", "supplier.update", "supplier", id, before, toSupplierDTO(s))
	utils.OK(c, http.StatusOK, toSupplierDTO(s))
}

func (a *API) DeleteSupplier(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var s models.Supplier
	if err := a.DB.First(&s, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	var pos int64
	a.DB.Model(&models.PurchaseOrder{}).Where("supplier_id = ?", id).Count(&pos)
	if pos > 0 {
		fail(c, utils.NewAppError(http.StatusConflict, "SUPPLIER_IN_USE", "This supplier has purchase orders and can't be deleted."))
		return
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Product{}).Where("supplier_id = ?", id).Update("supplier_id", nil).Error; err != nil {
			return err
		}
		return tx.Delete(&s).Error
	})
	if err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "inventory", "supplier.delete", "supplier", id, toSupplierDTO(s), nil)
	utils.OK(c, http.StatusOK, gin.H{"deleted": true})
}

// ---- Products ---------------------------------------------------------------

type productDTO struct {
	ID             uint64  `json:"id"`
	SKU            string  `json:"sku"`
	Barcode        *string `json:"barcode"`
	NameEn         string  `json:"name_en"`
	NameKm         string  `json:"name_km"`
	CategoryID     *uint64 `json:"category_id"`
	CategoryName   *string `json:"category_name"`
	CategoryNameKm *string `json:"category_name_km"`
	SupplierID     *uint64 `json:"supplier_id"`
	SupplierName   *string `json:"supplier_name"`
	Unit           string  `json:"unit"`
	ImageURL       string  `json:"image_url"`
	Active         bool    `json:"active"`
	PriceCents     int64   `json:"price_cents"`
	CostCents      int64   `json:"cost_cents"`
	Qty            int64   `json:"qty"`
	ReorderPoint   int64   `json:"reorder_point"`
}

// productQuery joins a product with its category/supplier names, current
// price + cost (branch-specific price row wins over the all-branches one)
// and its stock at one branch.
func (a *API) productQuery(branchID uint64) *gorm.DB {
	return a.DB.Table("products p").
		Select(`p.id, p.sku, p.barcode, p.name_en, p.name_km, p.category_id, c.name_en AS category_name, c.name_km AS category_name_km,
			p.supplier_id, s.name AS supplier_name, p.unit, p.image_url, p.active,
			COALESCE(pp.price_cents, 0) AS price_cents, COALESCE(pp.cost_cents, 0) AS cost_cents,
			COALESCE(bs.qty, 0) AS qty, COALESCE(bs.reorder_point, 0) AS reorder_point`).
		Joins("LEFT JOIN categories c ON c.id = p.category_id AND c.deleted_at IS NULL").
		Joins("LEFT JOIN suppliers s ON s.id = p.supplier_id AND s.deleted_at IS NULL").
		Joins("LEFT JOIN branch_stock bs ON bs.product_id = p.id AND bs.branch_id = ?", branchID).
		Joins(`LEFT JOIN product_prices pp ON pp.id = (
			SELECT x.id FROM product_prices x
			WHERE x.product_id = p.id AND (x.branch_id IS NULL OR x.branch_id = ?)
			ORDER BY (x.branch_id IS NULL), x.effective_at DESC, x.id DESC LIMIT 1)`, branchID).
		Where("p.deleted_at IS NULL")
}

// productSummary is the whole-branch inventory KPI block (not affected by the
// list's search / category / status filters).
type productSummary struct {
	StockValueCents int64 `json:"stock_value_cents"`
	ActiveProducts  int64 `json:"active_products"`
	BelowReorder    int64 `json:"below_reorder"`
	OutOfStock      int64 `json:"out_of_stock"`
}

// ListProducts filters (all optional): q (name / sku / barcode), category_id,
// status (in_stock | low | out, against this branch's stock), active (true).
func (a *API) ListProducts(c *gin.Context) {
	branchID := branchParam(c)
	pg := pagerOf(c)
	all := func() *gorm.DB { return a.DB.Table("(?) AS t", a.productQuery(branchID)) }
	base := all()
	if search := strings.TrimSpace(c.Query("q")); search != "" {
		base = base.Where("t.name_en LIKE ? OR t.name_km LIKE ? OR t.sku LIKE ? OR t.barcode LIKE ?", likeArg(search), likeArg(search), likeArg(search), likeArg(search))
	}
	if cat := c.Query("category_id"); cat != "" {
		base = base.Where("t.category_id = ?", cat)
	}
	switch c.Query("status") {
	case "out":
		base = base.Where("t.qty <= 0")
	case "low":
		base = base.Where("t.qty > 0 AND t.qty <= t.reorder_point")
	case "in_stock":
		base = base.Where("t.qty > t.reorder_point")
	}
	if c.Query("active") == "true" {
		base = base.Where("t.active = 1")
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		dbFail(c, err)
		return
	}
	var rows []productDTO
	if err := pg.apply(base.Select("t.*").Order("t.id DESC")).Scan(&rows).Error; err != nil {
		dbFail(c, err)
		return
	}
	var summary *productSummary
	if pg.on {
		summary = &productSummary{}
		all().Select(`COALESCE(SUM(t.qty * t.cost_cents), 0) AS stock_value_cents,
			COALESCE(SUM(t.active = 1), 0) AS active_products,
			COALESCE(SUM(t.qty > 0 AND t.qty <= t.reorder_point), 0) AS below_reorder,
			COALESCE(SUM(t.qty <= 0), 0) AS out_of_stock`).Scan(summary)
	}
	pg.respond(c, rows, total, summary)
}

func (a *API) getProduct(branchID, id uint64) (*productDTO, error) {
	var rows []productDTO
	if err := a.productQuery(branchID).Where("p.id = ?", id).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &rows[0], nil
}

type productReq struct {
	NameEn       string  `json:"name_en" binding:"required,max=150"`
	NameKm       string  `json:"name_km" binding:"max=150"`
	Barcode      string  `json:"barcode" binding:"max=60"`
	CategoryID   *uint64 `json:"category_id"`
	SupplierID   *uint64 `json:"supplier_id"`
	Unit         string  `json:"unit" binding:"max=30"`
	ImageURL     string  `json:"image_url"`
	Active       bool    `json:"active"`
	PriceCents   int64   `json:"price_cents" binding:"min=0"`
	CostCents    int64   `json:"cost_cents" binding:"min=0"`
	ReorderPoint int64   `json:"reorder_point" binding:"min=0"`
}

func skuPrefix(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
			if b.Len() == 3 {
				break
			}
		}
	}
	if b.Len() == 0 {
		return "GEN"
	}
	return b.String()
}

func nullableString(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

func (a *API) CreateProduct(c *gin.Context) {
	var req productReq
	if !bind(c, &req) {
		return
	}
	unit := req.Unit
	if unit == "" {
		unit = "pcs"
	}
	var created models.Product
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		prefix := "GEN"
		if req.CategoryID != nil {
			var cat models.Category
			if err := tx.First(&cat, *req.CategoryID).Error; err == nil {
				prefix = skuPrefix(cat.NameEn)
			}
		}
		created = models.Product{
			SKU: fmt.Sprintf("TMP-%d", time.Now().UnixNano()), Barcode: nullableString(req.Barcode), NameEn: req.NameEn, NameKm: req.NameKm,
			CategoryID: req.CategoryID, SupplierID: req.SupplierID, Unit: unit, ImageURL: req.ImageURL, Active: req.Active,
		}
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		created.SKU = fmt.Sprintf("%s-%03d", prefix, created.ID)
		if err := tx.Model(&created).Update("sku", created.SKU).Error; err != nil {
			return err
		}
		if err := tx.Create(&models.ProductPrice{ProductID: created.ID, PriceCents: req.PriceCents, CostCents: req.CostCents, EffectiveAt: time.Now(), CreatedAt: time.Now()}).Error; err != nil {
			return err
		}
		var branches []models.Branch
		if err := tx.Find(&branches).Error; err != nil {
			return err
		}
		for _, b := range branches {
			if err := tx.Create(&models.BranchStock{BranchID: b.ID, ProductID: created.ID, Qty: 0, ReorderPoint: req.ReorderPoint}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "inventory", "product.create", "product", created.ID, nil, req)
	dto, err := a.getProduct(branchParam(c), created.ID)
	if err != nil {
		dbFail(c, err)
		return
	}
	utils.OK(c, http.StatusCreated, dto)
}

func (a *API) UpdateProduct(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req productReq
	if !bind(c, &req) {
		return
	}
	branchID := branchParam(c)
	before, err := a.getProduct(branchID, id)
	if err != nil {
		dbFail(c, err)
		return
	}
	unit := req.Unit
	if unit == "" {
		unit = "pcs"
	}
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Product{}).Where("id = ?", id).Updates(map[string]interface{}{
			"barcode": nullableString(req.Barcode), "name_en": req.NameEn, "name_km": req.NameKm, "category_id": req.CategoryID,
			"supplier_id": req.SupplierID, "unit": unit, "image_url": req.ImageURL, "active": req.Active,
		}).Error; err != nil {
			return err
		}
		if before.PriceCents != req.PriceCents || before.CostCents != req.CostCents {
			if err := tx.Create(&models.ProductPrice{ProductID: id, PriceCents: req.PriceCents, CostCents: req.CostCents, EffectiveAt: time.Now(), CreatedAt: time.Now()}).Error; err != nil {
				return err
			}
		}
		// The form has a single reorder point per product; apply it to every
		// branch's stock row (creating any that are missing).
		var branches []models.Branch
		if err := tx.Find(&branches).Error; err != nil {
			return err
		}
		for _, b := range branches {
			row := models.BranchStock{BranchID: b.ID, ProductID: id, ReorderPoint: req.ReorderPoint}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "branch_id"}, {Name: "product_id"}}, DoUpdates: clause.AssignmentColumns([]string{"reorder_point"})}).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		dbFail(c, err)
		return
	}
	after, _ := a.getProduct(branchID, id)
	a.audit(c, "inventory", "product.update", "product", id, before, after)
	utils.OK(c, http.StatusOK, after)
}

func (a *API) DeleteProduct(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	before, err := a.getProduct(branchParam(c), id)
	if err != nil {
		dbFail(c, err)
		return
	}
	// Soft delete: sales history keeps pointing at the product row.
	if err := a.DB.Where("id = ?", id).Delete(&models.Product{}).Error; err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "inventory", "product.delete", "product", id, before, nil)
	utils.OK(c, http.StatusOK, gin.H{"deleted": true})
}

// ---- Stock movements -----------------------------------------------------------

type stockMovementDTO struct {
	ID           uint64    `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	SKU          string    `json:"sku"`
	Product      string    `json:"product"`
	ProductKm    string    `json:"product_km"`
	Type         string    `json:"type"`
	QtyChange    int64     `json:"qty_change"`
	BalanceAfter int64     `json:"balance_after"`
	User         string    `json:"user"`
	Reference    string    `json:"reference"`
}

// stockMovementBase is the joined, date-scoped movement query without its
// column list, so it can be counted as well as selected from.
func (a *API) stockMovementBase(branchID uint64, from, to time.Time) *gorm.DB {
	return a.DB.Table("stock_movements sm").
		Joins("JOIN products p ON p.id = sm.product_id").
		Joins("LEFT JOIN users u ON u.id = sm.user_id").
		Joins("LEFT JOIN sales s ON sm.reference_type = 'SALE' AND s.id = sm.reference_id").
		Where("sm.branch_id = ? AND sm.created_at >= ? AND sm.created_at < ?", branchID, from, to)
}

func (a *API) stockMovementQuery(branchID uint64, from, to time.Time) *gorm.DB {
	return a.stockMovementBase(branchID, from, to).
		Select(`sm.id, sm.created_at, p.sku, p.name_en AS product, p.name_km AS product_km, sm.type, sm.qty_change, sm.balance_after,
			COALESCE(u.full_name, '') AS user,
			CASE sm.reference_type
				WHEN 'PO' THEN CONCAT('PO-', sm.reference_id)
				WHEN 'SALE' THEN COALESCE(s.receipt_no, CONCAT('SALE-', sm.reference_id))
				ELSE COALESCE(NULLIF(sm.note, ''), sm.reference_type) END AS reference`).
		Order("sm.created_at DESC, sm.id DESC")
}

// stockMovementFilters applies the optional q (product / SKU), type and product_id filters.
func stockMovementFilters(c *gin.Context, q *gorm.DB) *gorm.DB {
	if search := strings.TrimSpace(c.Query("q")); search != "" {
		q = q.Where("p.name_en LIKE ? OR p.name_km LIKE ? OR p.sku LIKE ?", likeArg(search), likeArg(search), likeArg(search))
	}
	if typ := c.Query("type"); typ != "" {
		q = q.Where("sm.type = ?", strings.ToUpper(typ))
	}
	if pid := c.Query("product_id"); pid != "" {
		q = q.Where("sm.product_id = ?", pid)
	}
	return q
}

func (a *API) ListStockMovements(c *gin.Context) {
	branchID := branchParam(c)
	pg := pagerOf(c)
	// No dates = all history; either side narrows it.
	from, to := time.Date(2000, 1, 1, 0, 0, 0, 0, time.Local), today().AddDate(0, 0, 1)
	if f, t := rangeBounds(c); f != nil || t != nil {
		if f != nil {
			from = *f
		}
		if t != nil {
			to = *t
		}
	}
	var total int64
	if err := stockMovementFilters(c, a.stockMovementBase(branchID, from, to)).Count(&total).Error; err != nil {
		dbFail(c, err)
		return
	}
	var rows []stockMovementDTO
	if err := pg.apply(stockMovementFilters(c, a.stockMovementQuery(branchID, from, to))).Scan(&rows).Error; err != nil {
		dbFail(c, err)
		return
	}
	pg.respond(c, rows, total, nil)
}

// recordMovement applies a stock change for one product at one branch and
// writes the matching stock_movements row in the same transaction — branch
// stock is never changed without a movement to explain it. It returns the
// new on-hand quantity.
func recordMovement(tx *gorm.DB, branchID, productID uint64, typ models.StockMovementType, qtyChange, unitCostCents int64, refType string, refID *uint64, userID uint64, note string) (int64, error) {
	var stock models.BranchStock
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("branch_id = ? AND product_id = ?", branchID, productID).First(&stock).Error
	if err == gorm.ErrRecordNotFound {
		stock = models.BranchStock{BranchID: branchID, ProductID: productID}
		if err := tx.Create(&stock).Error; err != nil {
			return 0, err
		}
	} else if err != nil {
		return 0, err
	}
	newQty := stock.Qty + qtyChange
	if err := tx.Model(&models.BranchStock{}).Where("branch_id = ? AND product_id = ?", branchID, productID).Update("qty", newQty).Error; err != nil {
		return 0, err
	}
	mv := models.StockMovement{
		BranchID: branchID, ProductID: productID, Type: typ, QtyChange: qtyChange, BalanceAfter: newQty,
		UnitCostCents: unitCostCents, ReferenceType: refType, ReferenceID: refID, UserID: userID, Note: note, CreatedAt: time.Now(),
	}
	return newQty, tx.Create(&mv).Error
}

var _ = middleware.UserIDFrom
