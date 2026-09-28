package repositories

import (
	"errors"

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

func (r *UserRepository) TouchLastActive(userID uint64) error {
	return r.db.Model(&models.User{}).Where("id = ?", userID).
		UpdateColumn("last_active_at", gorm.Expr("NOW()")).Error
}
