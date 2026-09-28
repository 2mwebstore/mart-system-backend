package models

import "time"

// ActivityLog is an append-only audit trail row. before_json/after_json
// are stored as raw JSON since the shape varies per entity type.
type ActivityLog struct {
	ID         uint64    `gorm:"primaryKey" json:"id"`
	UserID     uint64    `gorm:"not null;index" json:"user_id"`
	Role       string    `gorm:"size:60" json:"role"`
	BranchID   *uint64   `gorm:"index" json:"branch_id,omitempty"`
	Module     string    `gorm:"size:60;not null;index" json:"module"`
	Action     string    `gorm:"size:60;not null" json:"action"`
	EntityType string    `gorm:"size:60;not null;index" json:"entity_type"`
	EntityID   *uint64   `json:"entity_id,omitempty"`
	BeforeJSON string    `gorm:"type:json" json:"before_json,omitempty"`
	AfterJSON  string    `gorm:"type:json" json:"after_json,omitempty"`
	IP         string    `gorm:"size:64" json:"ip"`
	Device     string    `gorm:"size:120" json:"device"`
	CreatedAt  time.Time `gorm:"not null;index" json:"created_at"`
}

func (ActivityLog) TableName() string { return "activity_logs" }
