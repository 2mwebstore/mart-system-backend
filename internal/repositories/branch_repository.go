package repositories

import (
	"gorm.io/gorm"

	"com-mart/backend/internal/models"
)

type BranchRepository struct {
	db *gorm.DB
}

func NewBranchRepository(db *gorm.DB) *BranchRepository {
	return &BranchRepository{db: db}
}

// AllActiveIDs returns every active branch ID — used to grant Owner-role
// tokens access to all branches without special-casing "is Owner" in
// downstream branch-scope checks (see middleware/branch_scope.go).
func (r *BranchRepository) AllActiveIDs() ([]uint64, error) {
	var ids []uint64
	err := r.db.Model(&models.Branch{}).Where("active = ?", true).Pluck("id", &ids).Error
	return ids, err
}

// AllActive returns every active branch, for populating the Owner's branch
// selector (Owner isn't necessarily rows in user_branches — they see all).
func (r *BranchRepository) AllActive() ([]models.Branch, error) {
	var branches []models.Branch
	err := r.db.Where("active = ?", true).Order("name").Find(&branches).Error
	return branches, err
}
