package models

// TaxRate is a tax a company can add on top of a POS sale, invoice, purchase
// order or expense (VAT 10%…). Prices are always before tax.
type TaxRate struct {
	ID        uint64  `gorm:"primaryKey" json:"id"`
	CompanyID uint64  `gorm:"not null;index" json:"company_id"`
	Name      string  `gorm:"size:60;not null" json:"name"`
	Percent   float64 `gorm:"type:decimal(5,2);not null" json:"percent"`
	// No default tag: GORM would swap an inactive (false) rate for the
	// column's default on insert. See the note on Branch.Active.
	Active bool `gorm:"not null" json:"active"`
	Timestamps
	SoftDelete
}

func (TaxRate) TableName() string { return "tax_rates" }

// TaxSnapshot is the tax rate a document used and the tax it came to, kept on
// the document itself so a rate changed or deleted later never changes it.
// No rate: TaxRateID nil and the rest zero.
type TaxSnapshot struct {
	TaxRateID  *uint64 `json:"tax_rate_id"`
	TaxName    string  `gorm:"size:60;not null;default:''" json:"tax_name"`
	TaxPercent float64 `gorm:"type:decimal(5,2);not null;default:0" json:"tax_percent"`
	TaxCents   int64   `gorm:"not null;default:0" json:"tax_cents"`
}
