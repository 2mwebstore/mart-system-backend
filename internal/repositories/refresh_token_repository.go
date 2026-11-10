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

// FindByHash returns a refresh token by its SHA-256 hash, or nil if none
// matches. Revoked and expired rows are returned too: refreshing has to tell a
// replayed old token from one it has never seen.
func (r *RefreshTokenRepository) FindByHash(tokenHash string) (*models.RefreshToken, error) {
	var rt models.RefreshToken
	err := r.db.Where("token_hash = ?", tokenHash).First(&rt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &rt, err
}

// RevokeIfActive revokes one token and reports whether this call was the one
// that did it, so two simultaneous refreshes can't both rotate the same token.
func (r *RefreshTokenRepository) RevokeIfActive(id uint64) (bool, error) {
	res := r.db.Model(&models.RefreshToken{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Update("revoked_at", time.Now())
	return res.RowsAffected > 0, res.Error
}

// RevokeFamily revokes what's left of one login's tokens and returns how many
// were still live.
func (r *RefreshTokenRepository) RevokeFamily(familyID string) (int64, error) {
	res := r.db.Model(&models.RefreshToken{}).
		Where("family_id = ? AND revoked_at IS NULL", familyID).
		Update("revoked_at", time.Now())
	return res.RowsAffected, res.Error
}
