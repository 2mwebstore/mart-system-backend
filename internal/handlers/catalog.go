package handlers

import (
	"encoding/csv"
	"fmt"
	"math"
	"net/http"
	"strconv"
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
	ID                 uint64  `json:"id"`
	ParentProductID    *uint64 `json:"parent_product_id"`
	ParentNameEn       *string `json:"parent_name_en"`
	ParentNameKm       *string `json:"parent_name_km"`
	VariantName        string  `json:"variant_name"`
	VariantCount       int64   `json:"variant_count"`
	SKU                string  `json:"sku"`
	Barcode            *string `json:"barcode"`
	NameEn             string  `json:"name_en"`
	NameKm             string  `json:"name_km"`
	CategoryID         *uint64 `json:"category_id"`
	CategoryName       *string `json:"category_name"`
	CategoryNameKm     *string `json:"category_name_km"`
	SupplierID         *uint64 `json:"supplier_id"`
	SupplierName       *string `json:"supplier_name"`
	Unit               string  `json:"unit"`
	ProductType        string  `json:"product_type"`
	ImageURL           string  `json:"image_url"`
	Active             bool    `json:"active"`
	PriceCents         int64   `json:"price_cents"`
	CostCents          int64   `json:"cost_cents"`
	Qty                int64   `json:"qty"`
	ReorderPoint       int64   `json:"reorder_point"`
	HideWhenOutOfStock bool    `json:"hide_when_out_of_stock"`
}

// productQuery joins a product with its category/supplier names, current
// price + cost (branch-specific price row wins over the all-branches one)
// and its stock at one branch. A SERVICE product never has a branch_stock
// row (see CreateProduct) so its qty/reorder_point are always 0 here — every
// caller that cares about "out of stock" / "below reorder" additionally
// gates on product_type = 'STANDARD', a plain 0 would be misleading on its
// own for something that was never meant to be stocked.
func (a *API) productQuery(branchID uint64) *gorm.DB {
	return a.DB.Table("products p").
		Select(`p.id, p.parent_product_id, pp2.name_en AS parent_name_en, pp2.name_km AS parent_name_km, p.variant_name,
			(SELECT COUNT(*) FROM products v WHERE v.parent_product_id = p.id AND v.deleted_at IS NULL) AS variant_count,
			p.sku, p.barcode, p.name_en, p.name_km, p.category_id, c.name_en AS category_name, c.name_km AS category_name_km,
			p.supplier_id, s.name AS supplier_name, p.unit, p.product_type, p.image_url, p.active,
			p.hide_when_out_of_stock,
			COALESCE(pp.price_cents, 0) AS price_cents, COALESCE(pp.cost_cents, 0) AS cost_cents,
			COALESCE(bs.qty, 0) AS qty, COALESCE(bs.reorder_point, 0) AS reorder_point`).
		Joins("LEFT JOIN categories c ON c.id = p.category_id AND c.deleted_at IS NULL").
		Joins("LEFT JOIN suppliers s ON s.id = p.supplier_id AND s.deleted_at IS NULL").
		Joins("LEFT JOIN products pp2 ON pp2.id = p.parent_product_id").
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
// type (STANDARD | SERVICE), status (in_stock | low | out, against this
// branch's stock — SERVICE products never match any of the three), active
// (true).
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
	if pt := strings.ToUpper(c.Query("type")); pt == string(models.ProductStandard) || pt == string(models.ProductService) {
		base = base.Where("t.product_type = ?", pt)
	}
	switch c.Query("status") {
	case "out":
		base = base.Where("t.product_type = 'STANDARD' AND t.qty <= 0")
	case "low":
		base = base.Where("t.product_type = 'STANDARD' AND t.qty > 0 AND t.qty <= t.reorder_point")
	case "in_stock":
		base = base.Where("t.product_type = 'STANDARD' AND t.qty > t.reorder_point")
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
	// Group each variant directly under its parent (COALESCE(parent_id, id)
	// is the parent's own id either way), newest family first, parent before
	// its variants within a family.
	order := "COALESCE(t.parent_product_id, t.id) DESC, (t.parent_product_id IS NOT NULL), t.id"
	if err := pg.apply(base.Select("t.*").Order(order)).Scan(&rows).Error; err != nil {
		dbFail(c, err)
		return
	}
	var summary *productSummary
	if pg.on {
		summary = &productSummary{}
		all().Select(`COALESCE(SUM(t.qty * t.cost_cents), 0) AS stock_value_cents,
			COALESCE(SUM(t.active = 1), 0) AS active_products,
			COALESCE(SUM(t.product_type = 'STANDARD' AND t.qty > 0 AND t.qty <= t.reorder_point), 0) AS below_reorder,
			COALESCE(SUM(t.product_type = 'STANDARD' AND t.qty <= 0), 0) AS out_of_stock`).Scan(summary)
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
	// NameEn is required for a standalone product; for a variant
	// (ParentProductID set) it's optional — left blank, it's auto-composed
	// from the parent's name + VariantName. See CreateProduct.
	NameEn          string  `json:"name_en" binding:"max=150"`
	NameKm          string  `json:"name_km" binding:"max=150"`
	Barcode         string  `json:"barcode" binding:"max=60"`
	CategoryID      *uint64 `json:"category_id"`
	SupplierID      *uint64 `json:"supplier_id"`
	Unit            string  `json:"unit" binding:"max=30"`
	ProductType     string  `json:"product_type" binding:"omitempty,oneof=STANDARD SERVICE"`
	ImageURL        string  `json:"image_url"`
	Active          bool    `json:"active"`
	PriceCents      int64   `json:"price_cents" binding:"min=0"`
	CostCents       int64   `json:"cost_cents" binding:"min=0"`
	ReorderPoint    int64   `json:"reorder_point" binding:"min=0"`
	ParentProductID *uint64 `json:"parent_product_id"`
	VariantName     string  `json:"variant_name" binding:"max=100"`
	// Ignored for a SERVICE product — never stocked, always sellable.
	HideWhenOutOfStock bool `json:"hide_when_out_of_stock"`
}

func (r productReq) productType() models.ProductType {
	if r.ProductType == "" {
		return models.ProductStandard
	}
	return models.ProductType(r.ProductType)
}

// composeVariantName builds a variant's display name from its parent's and
// its own variant name (e.g. "T-Shirt" + "Red / Large" -> "T-Shirt — Red / Large").
func composeVariantName(parentName, variantName string) string {
	return parentName + " — " + variantName
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

// loadVariantParent validates req.ParentProductID (when set): the parent
// must exist and must not itself be a variant — only one level of nesting.
// Returns (nil, true) when req.ParentProductID is nil (a standalone
// product); on any validation failure it writes the response itself (same
// convention as bind/idParam) and returns (nil, false).
func (a *API) loadVariantParent(c *gin.Context, req productReq) (*models.Product, bool) {
	if req.ParentProductID == nil {
		return nil, true
	}
	if strings.TrimSpace(req.VariantName) == "" {
		invalid(c, "variant_name", "Variant name is required.")
		return nil, false
	}
	var parent models.Product
	if err := a.DB.First(&parent, *req.ParentProductID).Error; err != nil {
		invalid(c, "parent_product_id", "Parent product not found.")
		return nil, false
	}
	if parent.ParentProductID != nil {
		invalid(c, "parent_product_id", "A variant can't itself be the parent of another variant.")
		return nil, false
	}
	return &parent, true
}

func (a *API) CreateProduct(c *gin.Context) {
	var req productReq
	if !bind(c, &req) {
		return
	}
	parent, ok := a.loadVariantParent(c, req)
	if !ok {
		return
	}
	nameEn, nameKm := req.NameEn, req.NameKm
	categoryID, supplierID, imageURL, productType := req.CategoryID, req.SupplierID, req.ImageURL, req.productType()
	if parent != nil {
		// Shared fields always mirror the parent — never independently set
		// on a variant, at creation or afterward (see UpdateProduct).
		categoryID, supplierID, imageURL, productType = parent.CategoryID, parent.SupplierID, parent.ImageURL, parent.Type
		if nameEn == "" {
			nameEn = composeVariantName(parent.NameEn, req.VariantName)
		}
		if nameKm == "" && parent.NameKm != "" {
			nameKm = composeVariantName(parent.NameKm, req.VariantName)
		}
	} else if nameEn == "" {
		invalid(c, "name_en", "Name (English) is required.")
		return
	}
	unit := req.Unit
	if unit == "" {
		unit = "pcs"
	}
	var created models.Product
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		prefix := "GEN"
		if categoryID != nil {
			var cat models.Category
			if err := tx.First(&cat, *categoryID).Error; err == nil {
				prefix = skuPrefix(cat.NameEn)
			}
		}
		created = models.Product{
			ParentProductID: req.ParentProductID, VariantName: req.VariantName,
			SKU: fmt.Sprintf("TMP-%d", time.Now().UnixNano()), Barcode: nullableString(req.Barcode), NameEn: nameEn, NameKm: nameKm,
			CategoryID: categoryID, SupplierID: supplierID, Unit: unit, Type: productType, ImageURL: imageURL, Active: req.Active,
			HideWhenOutOfStock: req.HideWhenOutOfStock,
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
		// A SERVICE product is never stocked — no branch_stock row at all,
		// not even a zeroed one — see the ProductType doc comment.
		if created.Type == models.ProductStandard {
			var branches []models.Branch
			if err := tx.Find(&branches).Error; err != nil {
				return err
			}
			for _, b := range branches {
				if err := tx.Create(&models.BranchStock{BranchID: b.ID, ProductID: created.ID, Qty: 0, ReorderPoint: req.ReorderPoint}).Error; err != nil {
					return err
				}
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
	if req.NameEn == "" {
		invalid(c, "name_en", "Name (English) is required.")
		return
	}
	unit := req.Unit
	if unit == "" {
		unit = "pcs"
	}
	isVariant := before.ParentProductID != nil
	// A variant's category/supplier/image/type are never independently
	// settable — always whatever they already are (kept in sync with the
	// parent by the propagation below, never by this request's own values).
	categoryID, supplierID, imageURL, productType := req.CategoryID, req.SupplierID, req.ImageURL, req.productType()
	variantName := ""
	if isVariant {
		categoryID, supplierID, imageURL = before.CategoryID, before.SupplierID, before.ImageURL
		productType = models.ProductType(before.ProductType)
		variantName = req.VariantName
	}
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Product{}).Where("id = ?", id).Updates(map[string]interface{}{
			"barcode": nullableString(req.Barcode), "name_en": req.NameEn, "name_km": req.NameKm, "category_id": categoryID,
			"supplier_id": supplierID, "unit": unit, "product_type": productType, "image_url": imageURL, "active": req.Active,
			"variant_name": variantName, "hide_when_out_of_stock": req.HideWhenOutOfStock,
		}).Error; err != nil {
			return err
		}
		// This product is a parent with its own variants: keep every
		// variant's shared fields (category/supplier/image/type) in sync —
		// they're never independently editable on the variant itself.
		if !isVariant && before.VariantCount > 0 {
			if err := tx.Model(&models.Product{}).Where("parent_product_id = ?", id).Updates(map[string]interface{}{
				"category_id": categoryID, "supplier_id": supplierID, "image_url": imageURL, "product_type": productType,
			}).Error; err != nil {
				return err
			}
		}
		if before.PriceCents != req.PriceCents || before.CostCents != req.CostCents {
			if err := tx.Create(&models.ProductPrice{ProductID: id, PriceCents: req.PriceCents, CostCents: req.CostCents, EffectiveAt: time.Now(), CreatedAt: time.Now()}).Error; err != nil {
				return err
			}
		}
		// The form has a single reorder point per product; apply it to every
		// branch's stock row (creating any that are missing). Not for
		// SERVICE — switching a product to SERVICE just stops touching
		// whatever branch_stock rows it already has; they're ignored
		// everywhere else once the type says not to look at them. Switching
		// it back to STANDARD later re-enters this branch and creates them.
		if productType == models.ProductStandard {
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
	// Soft delete: sales history keeps pointing at the product row. A
	// parent's variants are meaningless on their own, so they're
	// soft-deleted along with it — same "handle the children explicitly"
	// pattern as category/supplier delete above.
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		if before.VariantCount > 0 {
			if err := tx.Where("parent_product_id = ?", id).Delete(&models.Product{}).Error; err != nil {
				return err
			}
		}
		return tx.Where("id = ?", id).Delete(&models.Product{}).Error
	})
	if err != nil {
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

// ---- CSV import (categories & products) -------------------------------------------------
//
// Both take a `multipart/form-data` upload under field "file", a plain CSV
// with a header row (column order doesn't matter, headers are matched
// case-insensitively). Every row is best-effort: a bad row is skipped with a
// reason rather than failing the whole file, so importing 500 rows with one
// typo still saves the other 499. The matching "download sample" button on
// each page is pure frontend (no endpoint) — it just writes out a CSV with
// these exact headers and one example row.

type importRowError struct {
	Row    int    `json:"row"`
	Reason string `json:"reason"`
}

type importResult struct {
	Created int              `json:"created"`
	Skipped []importRowError `json:"skipped"`
}

func readImportCSV(c *gin.Context) ([][]string, error) {
	fh, err := c.FormFile("file")
	if err != nil {
		return nil, err
	}
	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1 // tolerate short/ragged rows rather than erroring the whole file
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func csvHeaderIndex(header []string) map[string]int {
	idx := make(map[string]int, len(header))
	for i, h := range header {
		// A UTF-8 BOM (Excel/Numbers/Sheets often add one, especially once a
		// file has non-ASCII text like Khmer) lands on the very first cell —
		// TrimSpace doesn't strip it, since it isn't whitespace, so the
		// first column's header would otherwise never match anything and
		// every row would look like that column is missing.
		h = strings.TrimPrefix(h, "\ufeff")
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	return idx
}

func csvCell(row []string, idx map[string]int, keys ...string) string {
	for _, key := range keys {
		if i, ok := idx[key]; ok && i < len(row) {
			if v := strings.TrimSpace(row[i]); v != "" {
				return v
			}
		}
	}
	return ""
}

// parseImportMoney reads a plain dollar amount ("1.50", "$1.50") into cents.
func parseImportMoney(s string) (int64, error) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "$"))
	if s == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	return int64(math.Round(f * 100)), nil
}

func parseImportInt(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid integer %q", s)
	}
	return n, nil
}

func parseImportBool(s string, def bool) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return def
	}
	return s == "1" || s == "true" || s == "yes" || s == "active"
}

// ImportCategories: columns name_en (required), name_km. A name that already
// exists (case-insensitive) is skipped, not duplicated.
func (a *API) ImportCategories(c *gin.Context) {
	rows, err := readImportCSV(c)
	if err != nil {
		fail(c, utils.NewAppError(http.StatusBadRequest, "IMPORT_FILE", "Choose a CSV file to import."))
		return
	}
	result := importResult{Skipped: []importRowError{}}
	if len(rows) < 2 {
		utils.OK(c, http.StatusOK, result)
		return
	}
	idx := csvHeaderIndex(rows[0])
	for i, row := range rows[1:] {
		rowNum := i + 2
		nameEn := csvCell(row, idx, "name_en", "name (english)", "english name")
		nameKm := csvCell(row, idx, "name_km", "name (khmer)", "khmer name")
		if nameEn == "" {
			result.Skipped = append(result.Skipped, importRowError{rowNum, "Name (English) is required."})
			continue
		}
		var existing models.Category
		if err := a.DB.Where("LOWER(name_en) = ?", strings.ToLower(nameEn)).First(&existing).Error; err == nil {
			result.Skipped = append(result.Skipped, importRowError{rowNum, fmt.Sprintf("%q already exists.", nameEn)})
			continue
		}
		if err := a.DB.Create(&models.Category{NameEn: nameEn, NameKm: nameKm}).Error; err != nil {
			result.Skipped = append(result.Skipped, importRowError{rowNum, "Could not save this row."})
			continue
		}
		result.Created++
	}
	a.audit(c, "inventory", "category.import", "category", 0, nil, gin.H{"created": result.Created, "skipped": len(result.Skipped)})
	utils.OK(c, http.StatusOK, result)
}

// ImportProducts: columns name_en (required for a standalone row — optional
// for a variant row, see parent_sku below), name_km, barcode, category_id
// (exact match against an existing category — a row is skipped, not
// silently uncategorised, if it doesn't match one) or category (matched by
// name, case-insensitive, only used when category_id is blank) — one of the
// two is now required for a standalone row, same reasoning as price: a
// catalog row that reaches the products table without either is more likely
// a mistyped column than an intentional choice, unit, type ("standard" or
// "service", case-insensitive, default standard — an unrecognised value
// skips the row rather than guessing), cost, price (plain dollars — now
// required, a sellable product needs a price), reorder_point (ignored for a
// service row — it's never stocked), active (true/false, default true).
// SKU is always auto-generated, same as the "New product" form — importing
// never takes a SKU. No supplier column: a product's supplier isn't a thing
// this app tracks any more (see the product-form decision), only a purchase
// order's is.
//
// parent_sku + variant_name make a row a variant: parent_sku must match an
// existing product's SKU — either already in the database, or created by an
// earlier row in this same file (rows are processed in order, so put a
// product's variants after it) — and that product must not itself be a
// variant (only one level of nesting, same rule as the JSON API). A variant
// row's category/category_id/type columns are ignored — always inherited
// from the parent, matching UpdateProduct's propagation — and name_en is
// optional, auto-composed from the parent's name + variant_name when blank.
func (a *API) ImportProducts(c *gin.Context) {
	rows, err := readImportCSV(c)
	if err != nil {
		fail(c, utils.NewAppError(http.StatusBadRequest, "IMPORT_FILE", "Choose a CSV file to import."))
		return
	}
	result := importResult{Skipped: []importRowError{}}
	if len(rows) < 2 {
		utils.OK(c, http.StatusOK, result)
		return
	}
	idx := csvHeaderIndex(rows[0])

	var categories []models.Category
	a.DB.Find(&categories)
	catByName := make(map[string]uint64, len(categories))
	catByID := make(map[uint64]bool, len(categories))
	for _, cat := range categories {
		catByName[strings.ToLower(cat.NameEn)] = cat.ID
		catByID[cat.ID] = true
	}
	var branches []models.Branch
	if err := a.DB.Find(&branches).Error; err != nil {
		dbFail(c, err)
		return
	}
	// Seeded from the DB, then grown as rows are created, so a row can name
	// a parent that only exists earlier in this same file.
	var existing []models.Product
	a.DB.Find(&existing)
	bySKU := make(map[string]models.Product, len(existing))
	for _, p := range existing {
		bySKU[strings.ToUpper(p.SKU)] = p
	}

	for i, row := range rows[1:] {
		rowNum := i + 2
		nameEn := csvCell(row, idx, "name_en", "name (english)", "english name", "product", "name")
		nameKm := csvCell(row, idx, "name_km", "name (khmer)", "khmer name")
		barcode := csvCell(row, idx, "barcode")
		unit := csvCell(row, idx, "unit")
		if unit == "" {
			unit = "pcs"
		}

		var parent *models.Product
		variantName := csvCell(row, idx, "variant_name", "variant")
		if parentSKU := csvCell(row, idx, "parent_sku", "parent"); parentSKU != "" {
			p, ok := bySKU[strings.ToUpper(parentSKU)]
			if !ok {
				result.Skipped = append(result.Skipped, importRowError{rowNum, fmt.Sprintf("parent_sku %q does not match an existing product — create it in an earlier row or beforehand.", parentSKU)})
				continue
			}
			if p.ParentProductID != nil {
				result.Skipped = append(result.Skipped, importRowError{rowNum, fmt.Sprintf("parent_sku %q is itself a variant; variants can't be nested.", parentSKU)})
				continue
			}
			if variantName == "" {
				result.Skipped = append(result.Skipped, importRowError{rowNum, "variant_name is required when parent_sku is given."})
				continue
			}
			parent = &p
			if nameEn == "" {
				nameEn = composeVariantName(p.NameEn, variantName)
			}
			if nameKm == "" && p.NameKm != "" {
				nameKm = composeVariantName(p.NameKm, variantName)
			}
		} else if nameEn == "" {
			result.Skipped = append(result.Skipped, importRowError{rowNum, "Name (English) is required."})
			continue
		}

		var categoryID *uint64
		productType := models.ProductStandard
		if parent != nil {
			// Shared fields always mirror the parent — the row's own
			// category/category_id/type columns (if any) are ignored.
			categoryID, productType = parent.CategoryID, parent.Type
		} else {
			if idStr := csvCell(row, idx, "category_id", "categoryid"); idStr != "" {
				// category_id wins over category (by name) when both are given —
				// it's unambiguous, where a name can collide or typo silently
				// into "uncategorised".
				n, err := parseImportInt(idStr)
				if err != nil || n == 0 || !catByID[uint64(n)] {
					result.Skipped = append(result.Skipped, importRowError{rowNum, fmt.Sprintf("category_id %q does not match an existing category.", idStr)})
					continue
				}
				id := uint64(n)
				categoryID = &id
			} else if catName := csvCell(row, idx, "category"); catName != "" {
				id, ok := catByName[strings.ToLower(catName)]
				if !ok {
					result.Skipped = append(result.Skipped, importRowError{rowNum, fmt.Sprintf("category %q does not match an existing category.", catName)})
					continue
				}
				categoryID = &id
			} else {
				result.Skipped = append(result.Skipped, importRowError{rowNum, "Category (category_id or category) is required."})
				continue
			}
			if typeStr := csvCell(row, idx, "type", "product_type"); typeStr != "" {
				switch strings.ToUpper(typeStr) {
				case string(models.ProductStandard):
					productType = models.ProductStandard
				case string(models.ProductService):
					productType = models.ProductService
				default:
					result.Skipped = append(result.Skipped, importRowError{rowNum, fmt.Sprintf("type %q must be \"standard\" or \"service\".", typeStr)})
					continue
				}
			}
		}

		if csvCell(row, idx, "price") == "" {
			result.Skipped = append(result.Skipped, importRowError{rowNum, "Price is required."})
			continue
		}
		cost, costErr := parseImportMoney(csvCell(row, idx, "cost"))
		price, priceErr := parseImportMoney(csvCell(row, idx, "price"))
		if costErr != nil || priceErr != nil {
			result.Skipped = append(result.Skipped, importRowError{rowNum, "Cost and price must be plain numbers, e.g. 1.50."})
			continue
		}
		reorder, reorderErr := parseImportInt(csvCell(row, idx, "reorder_point", "reorder"))
		if reorderErr != nil {
			result.Skipped = append(result.Skipped, importRowError{rowNum, "Reorder point must be a whole number."})
			continue
		}
		active := parseImportBool(csvCell(row, idx, "active"), true)

		var parentID *uint64
		imageURL := ""
		if parent != nil {
			parentID, imageURL = &parent.ID, parent.ImageURL
		}
		var created models.Product
		err := a.DB.Transaction(func(tx *gorm.DB) error {
			prefix := "GEN"
			if categoryID != nil {
				var cat models.Category
				if err := tx.First(&cat, *categoryID).Error; err == nil {
					prefix = skuPrefix(cat.NameEn)
				}
			}
			created = models.Product{
				ParentProductID: parentID, VariantName: variantName,
				SKU: fmt.Sprintf("TMP-%d-%d", time.Now().UnixNano(), rowNum), Barcode: nullableString(barcode), NameEn: nameEn, NameKm: nameKm,
				CategoryID: categoryID, Unit: unit, Type: productType, ImageURL: imageURL, Active: active,
			}
			if err := tx.Create(&created).Error; err != nil {
				return err
			}
			created.SKU = fmt.Sprintf("%s-%03d", prefix, created.ID)
			if err := tx.Model(&created).Update("sku", created.SKU).Error; err != nil {
				return err
			}
			if err := tx.Create(&models.ProductPrice{ProductID: created.ID, PriceCents: price, CostCents: cost, EffectiveAt: time.Now(), CreatedAt: time.Now()}).Error; err != nil {
				return err
			}
			if productType == models.ProductStandard {
				for _, b := range branches {
					if err := tx.Create(&models.BranchStock{BranchID: b.ID, ProductID: created.ID, Qty: 0, ReorderPoint: reorder}).Error; err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			result.Skipped = append(result.Skipped, importRowError{rowNum, "Could not save this row."})
			continue
		}
		bySKU[strings.ToUpper(created.SKU)] = created
		result.Created++
	}
	a.audit(c, "inventory", "product.import", "product", 0, nil, gin.H{"created": result.Created, "skipped": len(result.Skipped)})
	utils.OK(c, http.StatusOK, result)
}
