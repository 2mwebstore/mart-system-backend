package repositories

import (
	"time"

	"gorm.io/gorm"

	"com-mart/backend/internal/models"
)

// maxAttemptIPLen is the size of the login_attempts.ip column.
const maxAttemptIPLen = 64

type LoginAttemptRepository struct {
	db *gorm.DB
}

func NewLoginAttemptRepository(db *gorm.DB) *LoginAttemptRepository {
	return &LoginAttemptRepository{db: db}
}

func (r *LoginAttemptRepository) Record(userID uint64, deviceID *uint64, success bool, ip string) error {
	// A value that doesn't fit would fail the insert, and a failure that isn't
	// recorded doesn't count toward the lockout.
	if len(ip) > maxAttemptIPLen {
		ip = ip[:maxAttemptIPLen]
	}
	return r.db.Create(&models.LoginAttempt{
		UserID:   userID,
		DeviceID: deviceID,
		Success:  success,
		IP:       ip,
	}).Error
}

// RecentFailureCount counts every failed attempt for a user, password or PIN
// at any till, within the window. This backs the 5-fails/5-minutes lockout.
func (r *LoginAttemptRepository) RecentFailureCount(userID uint64, window time.Duration) (int64, error) {
	var count int64
	err := r.db.Model(&models.LoginAttempt{}).
		Where("user_id = ? AND success = ? AND created_at > ?", userID, false, time.Now().Add(-window)).
		Count(&count).Error
	return count, err
}

// RecentPINFailureCount counts failed PIN attempts only (PIN attempts are the
// ones made at a till). A 4-digit PIN needs a limit over a longer span than the
// short lockout: that alone would still allow about 1,440 guesses a day.
func (r *LoginAttemptRepository) RecentPINFailureCount(userID uint64, window time.Duration) (int64, error) {
	var count int64
	err := r.db.Model(&models.LoginAttempt{}).
		Where("user_id = ? AND device_id IS NOT NULL AND success = ? AND created_at > ?", userID, false, time.Now().Add(-window)).
		Count(&count).Error
	return count, err
}
