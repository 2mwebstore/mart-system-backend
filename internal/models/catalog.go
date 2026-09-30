package models

import "time"

type Category struct {
	ID     uint64 `gorm:"primaryKey" json:"id"`
	NameEn string `gorm:"size:100;not null" json:"name_en"`
	NameKm string `gorm:"size:100" json:"name_km"`
	Sort   int    `gorm:"not null;default:0" json:"sort"`
	Timestamps
	SoftDelete
}

func (Category) TableName() string { return "categories" }

type Supplier struct {
	ID           uint64 `gorm:"primaryKey" json:"id"`
	Name         string `gorm:"size:150;not null" json:"name"`
	Phone        string `gorm:"size:30" json:"phone"`
	Contact      string `gorm:"size:150" json:"contact"`
	PaymentTerms string `gorm:"size:150" json:"payment_terms"`
	Timestamps
	SoftDelete
}

func (Supplier) TableName() string { return "suppliers" }

// ProductType: STANDARD is a physical item — stock-tracked, can run out.
// SERVICE is not stocked at all — a fee, a delivery charge, an install job —
// always sellable and never appears in a stock report or reorder count. See
// docs/DECISIONS.md for where each is skipped in the sale/stock code path.
type ProductType string

const (
	ProductStandard ProductType = "STANDARD"
	ProductService  ProductType = "SERVICE"
)

// A variant (ParentProductID set) is a full Product row in its own right —
// its own id, SKU, barcode, price history and branch_stock — so every sale,
// purchase order, stock movement and report that already works on a
// product_id keeps working on a variant with no changes at all. Only
// CategoryID/SupplierID/Type/ImageURL are forced to always match the
// parent's (enforced in CreateProduct/UpdateProduct, not at the DB level);
// SKU/barcode/name/price/cost/stock are genuinely independent per variant.
// A variant's own ParentProductID must be nil — one level of nesting only.
// See docs/DECISIONS.md for why this shape was chosen over a separate
// variants table.
type Product struct {
	ID              uint64      `gorm:"primaryKey" json:"id"`
	ParentProductID *uint64     `gorm:"index" json:"parent_product_id,omitempty"`
	SKU             string      `gorm:"size:60;not null;uniqueIndex" json:"sku"`
	Barcode         *string     `gorm:"size:60;index" json:"barcode,omitempty"`
	NameEn          string      `gorm:"size:150;not null" json:"name_en"`
	NameKm          string      `gorm:"size:150" json:"name_km"`
	VariantName     string      `gorm:"size:100;not null;default:''" json:"variant_name"`
	CategoryID      *uint64     `gorm:"index" json:"category_id,omitempty"`
	SupplierID      *uint64     `gorm:"index" json:"supplier_id,omitempty"`
	Unit            string      `gorm:"size:30;not null;default:'pcs'" json:"unit"`
	Type            ProductType `gorm:"column:product_type;size:20;not null;default:'STANDARD';index" json:"product_type"`
	ImageURL        string      `gorm:"type:mediumtext" json:"image_url"`
	Active          bool        `gorm:"not null" json:"active"` // see note on models.Branch.Active
	// STANDARD-only (meaningless for SERVICE, which is never stocked and
	// always sellable regardless): removes the product from the POS grid
	// once stock hits zero, rather than just greying its tile out.
	// Independent per variant, like ReorderPoint — never inherited from a
	// parent. Whether a sale can still go through at zero stock is a
	// store-wide setting instead (settingsDTO.AllowOutOfStockSale in
	// reference.go), not a per-product column — see docs/DECISIONS.md.
	HideWhenOutOfStock bool `gorm:"not null;default:false" json:"hide_when_out_of_stock"`
	Timestamps
	SoftDelete

	Category      *Category `gorm:"foreignKey:CategoryID" json:"category,omitempty"`
	Supplier      *Supplier `gorm:"foreignKey:SupplierID" json:"supplier,omitempty"`
	ParentProduct *Product  `gorm:"foreignKey:ParentProductID" json:"-"`
}

func (Product) TableName() string { return "products" }

// ProductPrice is a price/cost history row. BranchID nullable = applies to
// all branches unless a branch-specific row exists for the same product.
type ProductPrice struct {
	ID          uint64    `gorm:"primaryKey" json:"id"`
	ProductID   uint64    `gorm:"not null;index" json:"product_id"`
	BranchID    *uint64   `gorm:"index" json:"branch_id,omitempty"`
	PriceCents  int64     `gorm:"not null" json:"price_cents"`
	CostCents   int64     `gorm:"not null" json:"cost_cents"`
	EffectiveAt time.Time `gorm:"not null;index" json:"effective_at"`
	CreatedAt   time.Time `gorm:"not null" json:"created_at"`
}

func (ProductPrice) TableName() string { return "product_prices" }

// BranchStock is the current on-hand quantity per branch+product. Never
// written directly — always inside the same transaction as a StockMovement
// row (see docs/DECISIONS.md).
type BranchStock struct {
	BranchID     uint64 `gorm:"primaryKey" json:"branch_id"`
	ProductID    uint64 `gorm:"primaryKey" json:"product_id"`
	Qty          int64  `gorm:"not null;default:0" json:"qty"`
	ReorderPoint int64  `gorm:"not null;default:0" json:"reorder_point"`
	Timestamps
}

func (BranchStock) TableName() string { return "branch_stock" }

type StockMovementType string

const (
	StockMovementSale         StockMovementType = "SALE"
	StockMovementReceive      StockMovementType = "RECEIVE"
	StockMovementWriteOff     StockMovementType = "WRITE_OFF"
	StockMovementDamage       StockMovementType = "DAMAGE"
	StockMovementTransferOut  StockMovementType = "TRANSFER_OUT"
	StockMovementTransferIn   StockMovementType = "TRANSFER_IN"
	StockMovementCountAdjust  StockMovementType = "COUNT_ADJUST"
	StockMovementRefundReturn StockMovementType = "REFUND_RETURN"
)

type StockMovement struct {
	ID            uint64            `gorm:"primaryKey" json:"id"`
	BranchID      uint64            `gorm:"not null;index:idx_stock_movements_branch_product" json:"branch_id"`
	ProductID     uint64            `gorm:"not null;index:idx_stock_movements_branch_product" json:"product_id"`
	Type          StockMovementType `gorm:"size:20;not null;index" json:"type"`
	QtyChange     int64             `gorm:"not null" json:"qty_change"`
	BalanceAfter  int64             `gorm:"not null" json:"balance_after"`
	UnitCostCents int64             `gorm:"not null" json:"unit_cost_cents"`
	ReferenceType string            `gorm:"size:40" json:"reference_type"`
	ReferenceID   *uint64           `json:"reference_id,omitempty"`
	UserID        uint64            `gorm:"not null;index" json:"user_id"`
	Note          string            `gorm:"size:255" json:"note"`
	CreatedAt     time.Time         `gorm:"not null;index" json:"created_at"`

	Product *Product `gorm:"foreignKey:ProductID" json:"product,omitempty"`
}

func (StockMovement) TableName() string { return "stock_movements" }
