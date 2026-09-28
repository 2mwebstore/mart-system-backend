package repositories

import (
	"time"

	"gorm.io/gorm"

	"com-mart/backend/internal/models"
)

type LoginAttemptRepository struct {
	db *gorm.DB
}

func NewLoginAttemptRepository(db *gorm.DB) *LoginAttemptRepository {
	return &LoginAttemptRepository{db: db}
}

func (r *LoginAttemptRepository) Record(userID uint64, deviceID *uint64, success bool, ip string) error {
	return r.db.Create(&models.LoginAttempt{
		UserID:   userID,
		DeviceID: deviceID,
		Success:  success,
		IP:       ip,
	}).Error
}

// RecentFailureCount counts consecutive-window failed attempts for a user
// (+ device, when scoped to a till) within the given lockout window, used
// to enforce the 5-fails/5-minutes lockout rule.
func (r *LoginAttemptRepository) RecentFailureCount(userID uint64, deviceID *uint64, window time.Duration) (int64, error) {
	q := r.db.Model(&models.LoginAttempt{}).
		Where("user_id = ? AND success = ? AND created_at > ?", userID, false, time.Now().Add(-window))
	if deviceID != nil {
		q = q.Where("device_id = ?", *deviceID)
	}
	var count int64
	err := q.Count(&count).Error
	return count, err
}
