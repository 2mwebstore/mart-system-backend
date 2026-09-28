package repositories

import (
	"errors"
	"time"

	"gorm.io/gorm"

	"com-mart/backend/internal/models"
)

type RefreshTokenRepository struct {
	db *gorm.DB
}

func NewRefreshTokenRepository(db *gorm.DB) *RefreshTokenRepository {
	return &RefreshTokenRepository{db: db}
}

func (r *RefreshTokenRepository) Create(rt *models.RefreshToken) error {
	return r.db.Create(rt).Error
}

// FindValidByHash returns a non-revoked, non-expired refresh token by its
// SHA-256 hash, or nil if none matches.
func (r *RefreshTokenRepository) FindValidByHash(tokenHash string) (*models.RefreshToken, error) {
	var rt models.RefreshToken
	err := r.db.
		Where("token_hash = ? AND revoked_at IS NULL AND expires_at > ?", tokenHash, time.Now()).
		First(&rt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &rt, err
}

func (r *RefreshTokenRepository) Revoke(id uint64) error {
	return r.db.Model(&models.RefreshToken{}).Where("id = ?", id).
		Update("revoked_at", time.Now()).Error
}

func (r *RefreshTokenRepository) RevokeAllForUser(userID uint64) error {
	return r.db.Model(&models.RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", time.Now()).Error
}
