package repositories

import (
	"errors"
	"strings"

	"gorm.io/gorm"

	"com-mart/backend/internal/models"
)

type UserRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

func (r *UserRepository) FindByUsername(username string) (*models.User, error) {
	var user models.User
	err := r.db.
		Preload("Role.Permissions").
		Preload("Role.Limits").
		Preload("Branches").
		Where("username = ?", username).
		First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &user, err
}

func (r *UserRepository) FindByID(id uint64) (*models.User, error) {
	var user models.User
	err := r.db.
		Preload("Role.Permissions").
		Preload("Role.Limits").
		Preload("Branches").
		First(&user, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &user, err
}

// BumpTokenVersion ends every access token already issued to this user. Their
// sessions recover through refresh unless those were revoked as well.
func (r *UserRepository) BumpTokenVersion(userID uint64) error {
	return r.db.Model(&models.User{}).Where("id = ?", userID).
		UpdateColumn("token_version", gorm.Expr("token_version + 1")).Error
}

func (r *UserRepository) TouchLastActive(userID uint64) error {
	return r.db.Model(&models.User{}).Where("id = ?", userID).
		UpdateColumn("last_active_at", gorm.Expr("NOW()")).Error
}

// CompanyByID finds a company, active or not; nil if there's none.
func (r *UserRepository) CompanyByID(id uint64) (*models.Company, error) {
	var company models.Company
	err := r.db.Where("id = ?", id).Take(&company).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &company, err
}

// ActiveCompanyByCode finds an active company by its code; nil if there's
// none.
func (r *UserRepository) ActiveCompanyByCode(code string) (*models.Company, error) {
	var company models.Company
	err := r.db.Where("code = ? AND active = 1", strings.ToUpper(strings.TrimSpace(code))).Take(&company).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &company, err
}
