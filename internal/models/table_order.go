package models

import "time"

// DiningTable is a table in a branch with a QR code customers scan to order.
// Token is the only thing the QR carries (see migration 000023).
type DiningTable struct {
	ID       uint64 `gorm:"primaryKey" json:"id"`
	BranchID uint64 `gorm:"not null;index" json:"branch_id"`
	Name     string `gorm:"size:40;not null" json:"name"`
	Token    string `gorm:"size:32;not null;uniqueIndex" json:"token"`
	Active   bool   `gorm:"not null" json:"active"` // see note on Branch.Active
	Timestamps
}

func (DiningTable) TableName() string { return "dining_tables" }

type TableOrderStatus string

const (
	TableOrderNew       TableOrderStatus = "NEW"
	TableOrderAccepted  TableOrderStatus = "ACCEPTED"
	TableOrderPaid      TableOrderStatus = "PAID"
	TableOrderCancelled TableOrderStatus = "CANCELLED"
)

// TableOrder is what a customer sent from a table's QR menu. It becomes a
// real sale only when staff take payment for it at the POS (SaleID).
type TableOrder struct {
	ID       uint64  `gorm:"primaryKey" json:"id"`
	BranchID uint64  `gorm:"not null" json:"branch_id"`
	TableID  *uint64 `json:"table_id"`
	// The table's name when ordered (a field named TableName would clash
	// with GORM's TableName method).
	TableLabel   string           `gorm:"column:table_name;size:40;not null" json:"table_name"`
	PublicToken  string           `gorm:"size:32;not null;uniqueIndex" json:"-"`
	Status       TableOrderStatus `gorm:"size:12;not null" json:"status"`
	Note         string           `gorm:"size:255;not null" json:"note"`
	TotalCents   int64            `gorm:"not null" json:"total_cents"`
	SaleID       *uint64          `json:"sale_id"`
	HandledBy    *uint64          `json:"handled_by"`
	CancelReason string           `gorm:"size:255;not null" json:"cancel_reason"`
	AcceptedAt   *time.Time       `json:"accepted_at"`
	ClosedAt     *time.Time       `json:"closed_at"`
	Timestamps
}

func (TableOrder) TableName() string { return "table_orders" }

type TableOrderItem struct {
	ID             uint64 `gorm:"primaryKey" json:"id"`
	TableOrderID   uint64 `gorm:"not null;index" json:"table_order_id"`
	ProductID      uint64 `gorm:"not null" json:"product_id"`
	NameEn         string `gorm:"size:150;not null" json:"name_en"`
	NameKm         string `gorm:"size:150;not null" json:"name_km"`
	Qty            int64  `gorm:"not null" json:"qty"`
	UnitPriceCents int64  `gorm:"not null" json:"unit_price_cents"`
	Note           string `gorm:"size:255;not null" json:"note"`
}

func (TableOrderItem) TableName() string { return "table_order_items" }
