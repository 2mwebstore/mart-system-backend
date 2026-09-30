package models

import "time"

type ExpenseCategory string

const (
	ExpenseWages       ExpenseCategory = "WAGES"
	ExpenseRent        ExpenseCategory = "RENT"
	ExpenseElectricity ExpenseCategory = "ELECTRICITY"
	ExpenseWater       ExpenseCategory = "WATER"
	ExpenseInternet    ExpenseCategory = "INTERNET"
	ExpensePackaging   ExpenseCategory = "PACKAGING"
	ExpensePaymentFees ExpenseCategory = "PAYMENT_FEES"
	ExpenseStockLoss   ExpenseCategory = "STOCK_LOSS"
	ExpenseOther       ExpenseCategory = "OTHER"
)

type Expense struct {
	ID            uint64          `gorm:"primaryKey" json:"id"`
	Code          string          `gorm:"size:30;not null" json:"code"`
	BranchID      uint64          `gorm:"not null;index" json:"branch_id"`
	Category      ExpenseCategory `gorm:"size:20;not null;index" json:"category"`
	AmountCents   int64           `gorm:"not null" json:"amount_cents"`
	ExpenseDate   time.Time       `gorm:"type:date;not null;index" json:"expense_date"`
	Note          string          `gorm:"size:255" json:"note"`
	UserID        uint64          `gorm:"not null" json:"user_id"`
	AttachmentURL string          `gorm:"size:500" json:"attachment_url"`
	Timestamps
	SoftDelete
}

func (Expense) TableName() string { return "expenses" }
