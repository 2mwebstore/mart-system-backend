package models

import "time"

// Timestamps is embedded by every table that has created_at/updated_at.
type Timestamps struct {
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

// SoftDelete is embedded by tables that support soft deletion.
type SoftDelete struct {
	DeletedAt *time.Time `gorm:"index" json:"deleted_at,omitempty"`
}
