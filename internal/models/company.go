package models

// Company is one business: its branches, staff, roles, catalog, customers,
// settings, exchange rate and document numbers are all its own.
type Company struct {
	ID   uint64 `gorm:"primaryKey" json:"id"`
	Name string `gorm:"size:120;not null" json:"name"`
	// Code is what a till asks for before listing the company's tills: 2-12
	// upper-case letters and digits, unique.
	Code string `gorm:"size:12;not null" json:"code"`
	// Active false signs out and keeps out every user of the company.
	Active bool `gorm:"not null" json:"active"` // see note on Branch.Active
	// MaxBranches and MaxUsers are the most active branches and active user
	// accounts the company may have, set by the super admin; nil is no limit.
	MaxBranches *int `json:"max_branches"`
	MaxUsers    *int `json:"max_users"`
	Timestamps
	SoftDelete
}

func (Company) TableName() string { return "companies" }

// CompanySetting is one of a company's own settings: branding, receipt text,
// loyalty, stock policy, alerts and activity-log retention.
type CompanySetting struct {
	CompanyID uint64 `gorm:"primaryKey" json:"-"`
	Key       string `gorm:"primaryKey;size:100" json:"key"`
	Value     string `gorm:"type:mediumtext" json:"value"`
	Timestamps
}

func (CompanySetting) TableName() string { return "company_settings" }
