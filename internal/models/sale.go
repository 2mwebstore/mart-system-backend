package models

import "time"

type SaleStatus string

const (
	SalePaid          SaleStatus = "PAID"
	SaleVoided        SaleStatus = "VOIDED"
	SaleRefunded      SaleStatus = "REFUNDED"
	SalePartialRefund SaleStatus = "PARTIAL_REFUND"
)

type Sale struct {
	ID             uint64     `gorm:"primaryKey" json:"id"`
	BranchID       uint64     `gorm:"not null;index:idx_sales_branch_sold_at;uniqueIndex:uk_sales_branch_receipt_date" json:"branch_id"`
	ShiftID        uint64     `gorm:"not null;index" json:"shift_id"`
	DeviceID       uint64     `gorm:"not null;index" json:"device_id"`
	CashierID      uint64     `gorm:"not null;index" json:"cashier_id"`
	CustomerID     *uint64    `gorm:"index" json:"customer_id,omitempty"`
	ReceiptNo      string     `gorm:"size:20;not null;uniqueIndex:uk_sales_branch_receipt_date" json:"receipt_no"`
	// BusinessDate is the Asia/Phnom_Penh calendar date the sale belongs to
	// (computed by the service layer, not derived from SoldAt in SQL) —
	// it's what makes ReceiptNo ("#MMDD-NNN") unique per branch per day
	// across years. See build spec §3.
	BusinessDate   time.Time  `gorm:"type:date;not null;uniqueIndex:uk_sales_branch_receipt_date" json:"business_date"`
	Status         SaleStatus `gorm:"size:20;not null;default:'PAID';index" json:"status"`
	SubtotalCents  int64      `gorm:"not null" json:"subtotal_cents"`
	DiscountCents  int64      `gorm:"not null;default:0" json:"discount_cents"`
	TotalCents     int64      `gorm:"not null" json:"total_cents"`
	TotalRiel      int64      `gorm:"not null" json:"total_riel"`
	ExchangeRate   int64      `gorm:"not null" json:"exchange_rate"`
	CostTotalCents int64      `gorm:"not null" json:"cost_total_cents"`
	Note           string     `gorm:"size:255" json:"note"`
	IdempotencyKey string     `gorm:"size:80;not null;uniqueIndex" json:"idempotency_key"`
	SoldAt         time.Time  `gorm:"not null;index:idx_sales_branch_sold_at" json:"sold_at"`

	Branch   *Branch   `gorm:"foreignKey:BranchID" json:"branch,omitempty"`
	Cashier  *User     `gorm:"foreignKey:CashierID" json:"cashier,omitempty"`
	Customer *Customer `gorm:"foreignKey:CustomerID" json:"customer,omitempty"`
	Items    []SaleItem `gorm:"foreignKey:SaleID" json:"items,omitempty"`
	Payments []Payment  `gorm:"foreignKey:SaleID" json:"payments,omitempty"`
}

func (Sale) TableName() string { return "sales" }

// SaleItem snapshots the product name, price and cost at the moment of
// sale so historical reports/receipts never change when the catalog does.
type SaleItem struct {
	ID             uint64 `gorm:"primaryKey" json:"id"`
	SaleID         uint64 `gorm:"not null;index" json:"sale_id"`
	ProductID      uint64 `gorm:"not null;index" json:"product_id"`
	NameSnapshot   string `gorm:"size:150;not null" json:"name_snapshot"`
	Qty            int64  `gorm:"not null" json:"qty"`
	UnitPriceCents int64  `gorm:"not null" json:"unit_price_cents"`
	UnitCostCents  int64  `gorm:"not null" json:"unit_cost_cents"`
	DiscountCents  int64  `gorm:"not null;default:0" json:"discount_cents"`
	Note           string `gorm:"size:255;not null;default:''" json:"note"`
	LineTotalCents int64  `gorm:"not null" json:"line_total_cents"`

	Product *Product `gorm:"foreignKey:ProductID" json:"product,omitempty"`
}

func (SaleItem) TableName() string { return "sale_items" }

type PaymentMethod string

const (
	PaymentCash PaymentMethod = "CASH"
	PaymentKHQR PaymentMethod = "KHQR"
	PaymentCard PaymentMethod = "CARD"
)

type PaymentStatus string

const (
	PaymentPending   PaymentStatus = "PENDING"
	PaymentConfirmed PaymentStatus = "CONFIRMED"
	PaymentFailed    PaymentStatus = "FAILED"
)

type Payment struct {
	ID                 uint64        `gorm:"primaryKey" json:"id"`
	SaleID             uint64        `gorm:"not null;index" json:"sale_id"`
	Method             PaymentMethod `gorm:"size:10;not null" json:"method"`
	Currency           Currency      `gorm:"size:5;not null" json:"currency"`
	AmountCents        int64         `gorm:"not null;default:0" json:"amount_cents"`
	AmountRiel         int64         `gorm:"not null;default:0" json:"amount_riel"`
	ReceivedUSDCents   int64         `gorm:"not null;default:0" json:"received_usd_cents"`
	ReceivedKHRRiel    int64         `gorm:"not null;default:0" json:"received_khr_riel"`
	ChangeUSDCents     int64         `gorm:"not null;default:0" json:"change_usd_cents"`
	ChangeKHRRiel      int64         `gorm:"not null;default:0" json:"change_khr_riel"`
	Reference          string        `gorm:"size:120" json:"reference"`
	Status             PaymentStatus `gorm:"size:12;not null;default:'CONFIRMED'" json:"status"`
	CreatedAt          time.Time     `gorm:"not null" json:"created_at"`
}

func (Payment) TableName() string { return "payments" }

type VoidRefundType string

const (
	VoidType   VoidRefundType = "VOID"
	RefundType VoidRefundType = "REFUND"
)

type VoidRefund struct {
	ID           uint64         `gorm:"primaryKey" json:"id"`
	SaleID       uint64         `gorm:"not null;index" json:"sale_id"`
	SaleItemID   *uint64        `gorm:"index" json:"sale_item_id,omitempty"`
	Type         VoidRefundType `gorm:"size:10;not null;index" json:"type"`
	AmountCents  int64          `gorm:"not null" json:"amount_cents"`
	Reason       string         `gorm:"size:255" json:"reason"`
	CashierID    uint64         `gorm:"not null" json:"cashier_id"`
	ApprovedByID *uint64        `json:"approved_by_id,omitempty"`
	CreatedAt    time.Time      `gorm:"not null;index" json:"created_at"`
}

func (VoidRefund) TableName() string { return "voids_refunds" }
