package models

import (
	"time"

	"gorm.io/gorm"
)

// Timestamps is embedded by every table that has created_at/updated_at.
type Timestamps struct {
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

// SoftDelete is embedded by tables that support soft deletion. This must be
// gorm.DeletedAt, not a plain *time.Time: only gorm.DeletedAt makes GORM
// treat the model as soft-deletable, which is what turns a plain
// tx.Delete(&row) into an UPDATE ... SET deleted_at = ... instead of a real
// DELETE, and what makes .First()/.Find() automatically add
// "WHERE deleted_at IS NULL". Every handler that deletes one of these models
// (products, categories, suppliers, customers, branches, tills, expenses,
// users, roles) was already written assuming that behavior — see e.g.
// DeleteProduct's "Soft delete: sales history keeps pointing at the product
// row" comment, or DeleteRole's explicit .Unscoped() for the one case that
// deliberately wants a hard delete. With a plain *time.Time, none of that
// was actually happening: every "soft" delete was silently a hard DELETE,
// which only worked for rows nothing referenced yet — e.g. deleting *any*
// product always failed with a foreign-key error (branch_stock always
// exists), 500ing on a brand-new, never-sold product.
type SoftDelete struct {
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}
