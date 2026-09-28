package repositories

import (
	"errors"

	"gorm.io/gorm"

	"com-mart/backend/internal/models"
)

type DeviceRepository struct {
	db *gorm.DB
}

func NewDeviceRepository(db *gorm.DB) *DeviceRepository {
	return &DeviceRepository{db: db}
}

func (r *DeviceRepository) FindByDeviceKey(deviceKey string) (*models.Device, error) {
	var device models.Device
	err := r.db.Where("device_key = ? AND active = ?", deviceKey, true).First(&device).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &device, err
}

func (r *DeviceRepository) TouchLastSeen(id uint64) error {
	return r.db.Model(&models.Device{}).Where("id = ?", id).
		UpdateColumn("last_seen_at", gorm.Expr("NOW()")).Error
}
