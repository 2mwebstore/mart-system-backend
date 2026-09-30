package models

type PurchaseOrderStatus string

const (
	PurchaseOrderDraft     PurchaseOrderStatus = "DRAFT"
	PurchaseOrderSent      PurchaseOrderStatus = "SENT"
	PurchaseOrderPartial   PurchaseOrderStatus = "PARTIAL"
	PurchaseOrderReceived  PurchaseOrderStatus = "RECEIVED"
	PurchaseOrderCancelled PurchaseOrderStatus = "CANCELLED"
)

type PurchaseOrder struct {
	ID            uint64              `gorm:"primaryKey" json:"id"`
	Code          string              `gorm:"size:30;not null" json:"code"`
	BranchID      uint64              `gorm:"not null;index" json:"branch_id"`
	SupplierID    uint64              `gorm:"not null;index" json:"supplier_id"`
	Status        PurchaseOrderStatus `gorm:"size:20;not null;default:'DRAFT';index" json:"status"`
	Note          string              `gorm:"size:255" json:"note"`
	ShippingCents int64               `gorm:"not null;default:0" json:"shipping_cents"`
	UserID        uint64              `gorm:"not null" json:"user_id"`
	Timestamps

	Supplier *Supplier           `gorm:"foreignKey:SupplierID" json:"supplier,omitempty"`
	Items    []PurchaseOrderItem `gorm:"foreignKey:PurchaseOrderID" json:"items,omitempty"`
}

func (PurchaseOrder) TableName() string { return "purchase_orders" }

type PurchaseOrderItem struct {
	ID              uint64 `gorm:"primaryKey" json:"id"`
	PurchaseOrderID uint64 `gorm:"not null;index" json:"purchase_order_id"`
	ProductID       uint64 `gorm:"not null;index" json:"product_id"`
	QtyOrdered      int64  `gorm:"not null" json:"qty_ordered"`
	QtyReceived     int64  `gorm:"not null;default:0" json:"qty_received"`
	UnitCostCents   int64  `gorm:"not null" json:"unit_cost_cents"`

	Product *Product `gorm:"foreignKey:ProductID" json:"product,omitempty"`
}

func (PurchaseOrderItem) TableName() string { return "purchase_order_items" }

type StockTransferStatus string

const (
	StockTransferDraft     StockTransferStatus = "DRAFT"
	StockTransferSent      StockTransferStatus = "SENT"
	StockTransferReceived  StockTransferStatus = "RECEIVED"
	StockTransferCancelled StockTransferStatus = "CANCELLED"
)

type StockTransfer struct {
	ID           uint64              `gorm:"primaryKey" json:"id"`
	FromBranchID uint64              `gorm:"not null;index" json:"from_branch_id"`
	ToBranchID   uint64              `gorm:"not null;index" json:"to_branch_id"`
	Status       StockTransferStatus `gorm:"size:20;not null;default:'DRAFT';index" json:"status"`
	Note         string              `gorm:"size:255" json:"note"`
	UserID       uint64              `gorm:"not null" json:"user_id"`
	Timestamps

	Items []StockTransferItem `gorm:"foreignKey:StockTransferID" json:"items,omitempty"`
}

func (StockTransfer) TableName() string { return "stock_transfers" }

type StockTransferItem struct {
	ID              uint64 `gorm:"primaryKey" json:"id"`
	StockTransferID uint64 `gorm:"not null;index" json:"stock_transfer_id"`
	ProductID       uint64 `gorm:"not null;index" json:"product_id"`
	Qty             int64  `gorm:"not null" json:"qty"`

	Product *Product `gorm:"foreignKey:ProductID" json:"product,omitempty"`
}

func (StockTransferItem) TableName() string { return "stock_transfer_items" }

type StockCountStatus string

const (
	StockCountOpen      StockCountStatus = "OPEN"
	StockCountCompleted StockCountStatus = "COMPLETED"
)

type StockCount struct {
	ID       uint64           `gorm:"primaryKey" json:"id"`
	BranchID uint64           `gorm:"not null;index" json:"branch_id"`
	Status   StockCountStatus `gorm:"size:20;not null;default:'OPEN';index" json:"status"`
	Note     string           `gorm:"size:255" json:"note"`
	UserID   uint64           `gorm:"not null" json:"user_id"`
	Timestamps

	Items []StockCountItem `gorm:"foreignKey:StockCountID" json:"items,omitempty"`
}

func (StockCount) TableName() string { return "stock_counts" }

type StockCountItem struct {
	ID           uint64 `gorm:"primaryKey" json:"id"`
	StockCountID uint64 `gorm:"not null;index" json:"stock_count_id"`
	ProductID    uint64 `gorm:"not null;index" json:"product_id"`
	ExpectedQty  int64  `gorm:"not null" json:"expected_qty"`
	CountedQty   int64  `gorm:"not null" json:"counted_qty"`

	Product *Product `gorm:"foreignKey:ProductID" json:"product,omitempty"`
}

func (StockCountItem) TableName() string { return "stock_count_items" }
