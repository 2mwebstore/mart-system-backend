package models

import "time"

type CustomerTier string

const (
	CustomerMember CustomerTier = "MEMBER"
	CustomerGold   CustomerTier = "GOLD"
)

type Customer struct {
	ID     uint64       `gorm:"primaryKey" json:"id"`
	Name   string       `gorm:"size:120;not null" json:"name"`
	Phone  string       `gorm:"size:30;uniqueIndex" json:"phone"`
	Tier   CustomerTier `gorm:"size:20;not null;default:'MEMBER'" json:"tier"`
	Points int64        `gorm:"not null;default:0" json:"points"`
	Note   string       `gorm:"size:255" json:"note"`
	Timestamps
	SoftDelete
}

func (Customer) TableName() string { return "customers" }

type LoyaltyTransaction struct {
	ID           uint64  `gorm:"primaryKey" json:"id"`
	CustomerID   uint64  `gorm:"not null;index" json:"customer_id"`
	SaleID       *uint64 `gorm:"index" json:"sale_id,omitempty"`
	PointsChange int64   `gorm:"not null" json:"points_change"`
	Reason       string  `gorm:"size:100;not null" json:"reason"`
	CreatedAt    time.Time `gorm:"not null" json:"created_at"`
}

func (LoyaltyTransaction) TableName() string { return "loyalty_transactions" }
