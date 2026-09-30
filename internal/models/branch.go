package models

import "time"

type Branch struct {
	ID            uint64 `gorm:"primaryKey" json:"id"`
	Name          string `gorm:"size:120;not null" json:"name"`
	Code          string `gorm:"size:20;not null;uniqueIndex" json:"code"`
	Address       string `gorm:"size:255" json:"address"`
	Phone         string `gorm:"size:30" json:"phone"`
	ReceiptFooter string `gorm:"size:500" json:"receipt_footer"`
	// No gorm "default" tag here on purpose: GORM can't tell "explicitly
	// false" from "unset" for a plain bool with a default tag, and would
	// silently coerce false back to true on Create. The DB column still
	// has DEFAULT 1 for any raw insert that omits it.
	Active bool `gorm:"not null" json:"active"`
	Timestamps
	SoftDelete
}

func (Branch) TableName() string { return "branches" }

// Device is a registered POS till (a physical browser/kiosk) that PIN
// login and shift-tracking are scoped to.
type Device struct {
	ID         uint64     `gorm:"primaryKey" json:"id"`
	BranchID   uint64     `gorm:"not null;index" json:"branch_id"`
	Name       string     `gorm:"size:80;not null" json:"name"`
	DeviceKey  string     `gorm:"size:64;not null;uniqueIndex" json:"device_key"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	Active     bool       `gorm:"not null" json:"active"` // see note on Branch.Active above
	Timestamps

	Branch *Branch `gorm:"foreignKey:BranchID" json:"branch,omitempty"`
}

func (Device) TableName() string { return "devices" }

// Setting is a business-wide key/value config row (exchange_rate,
// loyalty_rate, receipt header/footer, currency rounding, etc).
type Setting struct {
	Key   string `gorm:"primaryKey;size:100" json:"key"`
	Value string `gorm:"type:text" json:"value"`
	Timestamps
}

func (Setting) TableName() string { return "settings" }

// NumberSequence backs the "PREFIX-000001" reference numbers on sales,
// expenses and purchase orders — one row per document type. See
// nextDocNumber in internal/handlers/api.go.
type NumberSequence struct {
	DocType    string `gorm:"primaryKey;size:20;column:doc_type" json:"doc_type"`
	Prefix     string `gorm:"size:20" json:"prefix"`
	NextNumber uint64 `gorm:"not null;default:1" json:"next_number"`
	Timestamps
}

func (NumberSequence) TableName() string { return "number_sequences" }

// ExchangeRate is the append-only history of USD->KHR rates. The current
// rate is the most recent row; every sale/shift snapshots the rate it used
// so historical reports never change when the rate changes later.
type ExchangeRate struct {
	ID             uint64    `gorm:"primaryKey" json:"id"`
	RateRielPerUSD int64     `gorm:"not null" json:"rate_riel_per_usd"`
	SetByUserID    uint64    `gorm:"not null;index" json:"set_by_user_id"`
	EffectiveAt    time.Time `gorm:"not null;index" json:"effective_at"`
	CreatedAt      time.Time `gorm:"not null" json:"created_at"`

	SetByUser *User `gorm:"foreignKey:SetByUserID" json:"set_by_user,omitempty"`
}

func (ExchangeRate) TableName() string { return "exchange_rates" }

// PaymentMethodRow is a payment option shown at the till (Cash, KHQR, Card
// Visa, ...). Named *Row because models.PaymentMethod is already the sale
// payment's CASH|KHQR|CARD enum type; Type here uses the same values.
type PaymentMethodRow struct {
	ID         uint64  `gorm:"primaryKey" json:"id"`
	Name       string  `gorm:"size:80;not null" json:"name"`
	Type       string  `gorm:"size:10;not null" json:"type"`
	FeePercent float64 `gorm:"type:decimal(5,2);not null;default:0" json:"fee_percent"`
	Enabled    bool    `gorm:"not null" json:"enabled"`
	Timestamps
	SoftDelete
}

func (PaymentMethodRow) TableName() string { return "payment_methods" }
