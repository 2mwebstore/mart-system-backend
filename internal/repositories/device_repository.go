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

// FindByDeviceKey finds one of a company's active tills by its key: each
// company names its own tills, so two can have the same key.
func (r *DeviceRepository) FindByDeviceKey(companyID uint64, deviceKey string) (*models.Device, error) {
	var device models.Device
	err := r.db.Where("company_id = ? AND device_key = ? AND active = ?", companyID, deviceKey, true).First(&device).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &device, err
}

func (r *DeviceRepository) TouchLastSeen(id uint64) error {
	return r.db.Model(&models.Device{}).Where("id = ?", id).
		UpdateColumn("last_seen_at", gorm.Expr("NOW()")).Error
}

// FindByID finds a till whether or not it's still active, so a session that
// was opened at it keeps its branch on refresh.
func (r *DeviceRepository) FindByID(id uint64) (*models.Device, error) {
	var device models.Device
	err := r.db.First(&device, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &device, err
}
