package models

import "time"

type InvoiceStatus string

const (
	InvoiceOpen      InvoiceStatus = "OPEN"
	InvoiceCancelled InvoiceStatus = "CANCELLED"
)

// InvoicePaymentStatus follows paid_cents against total_cents.
type InvoicePaymentStatus string

const (
	InvoiceDue     InvoicePaymentStatus = "DUE"     // nothing paid yet
	InvoicePending InvoicePaymentStatus = "PENDING" // partly paid
	InvoicePaid    InvoicePaymentStatus = "PAID"    // paid in full
)

// Invoice is a back-office sale to a customer, paid over time (see
// migration 000024). It is separate from POS receipts (Sale).
type Invoice struct {
	ID                uint64               `gorm:"primaryKey" json:"id"`
	Code              string               `gorm:"size:30;not null;uniqueIndex" json:"code"`
	BranchID          uint64               `gorm:"not null" json:"branch_id"`
	CustomerID        uint64               `gorm:"not null" json:"customer_id"`
	InvoiceDate       time.Time            `gorm:"type:date;not null" json:"invoice_date"`
	DueDate           *time.Time           `gorm:"type:date" json:"due_date"`
	Status            InvoiceStatus        `gorm:"size:12;not null" json:"status"`
	PaymentStatus     InvoicePaymentStatus `gorm:"size:12;not null" json:"payment_status"`
	SubtotalCents     int64                `gorm:"not null" json:"subtotal_cents"`
	LineDiscountCents int64                `gorm:"not null" json:"line_discount_cents"`
	DiscountCents     int64                `gorm:"not null" json:"discount_cents"`
	ShippingCents     int64                `gorm:"not null" json:"shipping_cents"`
	TotalCents        int64                `gorm:"not null" json:"total_cents"`
	PaidCents         int64                `gorm:"not null" json:"paid_cents"`
	CostTotalCents    int64                `gorm:"not null" json:"cost_total_cents"`
	ExchangeRate      int64                `gorm:"not null" json:"exchange_rate"`
	Note              string               `gorm:"type:text;not null" json:"note"` // rich text (sanitised HTML)
	CancelReason      string               `gorm:"size:255;not null" json:"cancel_reason"`
	CancelledAt       *time.Time           `json:"cancelled_at"`
	UserID            uint64               `gorm:"not null" json:"user_id"`
	Timestamps
}

func (Invoice) TableName() string { return "invoices" }

type InvoiceItem struct {
	ID             uint64 `gorm:"primaryKey" json:"id"`
	InvoiceID      uint64 `gorm:"not null;index" json:"invoice_id"`
	ProductID      uint64 `gorm:"not null" json:"product_id"`
	NameEn         string `gorm:"size:150;not null" json:"name_en"`
	NameKm         string `gorm:"size:150;not null" json:"name_km"`
	SKU            string `gorm:"column:sku;size:60;not null" json:"sku"`
	Qty            int64  `gorm:"not null" json:"qty"`
	UnitPriceCents int64  `gorm:"not null" json:"unit_price_cents"`
	DiscountCents  int64  `gorm:"not null" json:"discount_cents"`
	LineTotalCents int64  `gorm:"not null" json:"line_total_cents"`
	UnitCostCents  int64  `gorm:"not null" json:"unit_cost_cents"`
	Note           string `gorm:"size:255;not null" json:"note"`
}

func (InvoiceItem) TableName() string { return "invoice_items" }

type InvoicePayment struct {
	ID          uint64    `gorm:"primaryKey" json:"id"`
	InvoiceID   uint64    `gorm:"not null;index" json:"invoice_id"`
	PaidOn      time.Time `gorm:"type:date;not null" json:"paid_on"`
	MethodName  string    `gorm:"size:60;not null" json:"method_name"`
	MethodType  string    `gorm:"size:10;not null" json:"method_type"`
	AmountCents int64     `gorm:"not null" json:"amount_cents"`
	Reference   string    `gorm:"size:120;not null" json:"reference"`
	Note        string    `gorm:"size:255;not null" json:"note"`
	Attachment  *string   `gorm:"type:mediumtext" json:"attachment"`
	UserID      uint64    `gorm:"not null" json:"user_id"`
	CreatedAt   time.Time `gorm:"not null" json:"created_at"`
}

func (InvoicePayment) TableName() string { return "invoice_payments" }
