package models

import "time"

type ShiftStatus string

const (
	ShiftOpen   ShiftStatus = "OPEN"
	ShiftClosed ShiftStatus = "CLOSED"
)

type Shift struct {
	ID       uint64      `gorm:"primaryKey" json:"id"`
	BranchID uint64      `gorm:"not null;index" json:"branch_id"`
	DeviceID uint64      `gorm:"not null;index" json:"device_id"`
	UserID   uint64      `gorm:"not null;index" json:"user_id"`
	Status   ShiftStatus `gorm:"size:10;not null;default:'OPEN';index" json:"status"`

	OpenedAt time.Time  `gorm:"not null;index" json:"opened_at"`
	ClosedAt *time.Time `json:"closed_at,omitempty"`

	ExchangeRate int64 `gorm:"not null" json:"exchange_rate"`

	OpeningUSDCents int64 `gorm:"not null;default:0" json:"opening_usd_cents"`
	OpeningKHRRiel  int64 `gorm:"not null;default:0" json:"opening_khr_riel"`

	ExpectedUSDCents int64 `gorm:"not null;default:0" json:"expected_usd_cents"`
	ExpectedKHRRiel  int64 `gorm:"not null;default:0" json:"expected_khr_riel"`
	CountedUSDCents  int64 `gorm:"not null;default:0" json:"counted_usd_cents"`
	CountedKHRRiel   int64 `gorm:"not null;default:0" json:"counted_khr_riel"`
	DiffUSDCents     int64 `gorm:"not null;default:0" json:"diff_usd_cents"`
	DiffKHRRiel      int64 `gorm:"not null;default:0" json:"diff_khr_riel"`

	Note string `gorm:"size:255" json:"note"`

	Device *Device `gorm:"foreignKey:DeviceID" json:"device,omitempty"`
	User   *User   `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

func (Shift) TableName() string { return "shifts" }

type ShiftCountType string

const (
	ShiftCountOpenType  ShiftCountType = "OPEN"
	ShiftCountCloseType ShiftCountType = "CLOSE"
)

type Currency string

const (
	CurrencyUSD Currency = "USD"
	CurrencyKHR Currency = "KHR"
)

// ShiftCount is one denomination row of a shift's opening or closing cash
// count, e.g. { shift 12, OPEN, USD, 20, qty 3 } = three $20 notes.
type ShiftCount struct {
	ID           uint64         `gorm:"primaryKey" json:"id"`
	ShiftID      uint64         `gorm:"not null;index" json:"shift_id"`
	Type         ShiftCountType `gorm:"size:10;not null" json:"type"`
	Currency     Currency       `gorm:"size:5;not null" json:"currency"`
	Denomination int64          `gorm:"not null" json:"denomination"`
	Quantity     int64          `gorm:"not null" json:"quantity"`
}

func (ShiftCount) TableName() string { return "shift_counts" }

type CashMovementType string

const (
	CashMovementPayout CashMovementType = "PAYOUT"
	CashMovementPayin  CashMovementType = "PAYIN"
	CashMovementRefund CashMovementType = "REFUND"
)

type CashMovement struct {
	ID             uint64            `gorm:"primaryKey" json:"id"`
	ShiftID        uint64            `gorm:"not null;index" json:"shift_id"`
	Type           CashMovementType  `gorm:"size:10;not null" json:"type"`
	AmountUSDCents int64             `gorm:"not null;default:0" json:"amount_usd_cents"`
	AmountKHRRiel  int64             `gorm:"not null;default:0" json:"amount_khr_riel"`
	Reason         string            `gorm:"size:255" json:"reason"`
	UserID         uint64            `gorm:"not null" json:"user_id"`
	ApprovedByID   *uint64           `json:"approved_by_id,omitempty"`
	CreatedAt      time.Time         `gorm:"not null" json:"created_at"`
}

func (CashMovement) TableName() string { return "cash_movements" }
