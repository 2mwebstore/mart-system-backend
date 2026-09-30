package models

import "time"

type BackupStatus string

const (
	BackupRunning BackupStatus = "RUNNING"
	BackupSuccess BackupStatus = "SUCCESS"
	BackupFailed  BackupStatus = "FAILED"
)

type BackupTrigger string

const (
	BackupScheduled BackupTrigger = "SCHEDULED"
	BackupManual    BackupTrigger = "MANUAL"
)

// Backup is one mysqldump run, recorded so the Settings page can show backup
// history (and let it be downloaded) even after the scheduler has moved on.
// The dump file itself lives on local disk (config.BackupDir) — see
// internal/services/backup_service.go for why, and its limits on a host with
// no persistent volume.
type Backup struct {
	ID          uint64        `gorm:"primaryKey" json:"id"`
	Filename    string        `gorm:"size:255;not null" json:"filename"`
	SizeBytes   int64         `gorm:"not null;default:0" json:"size_bytes"`
	Status      BackupStatus  `gorm:"size:20;not null" json:"status"`
	TriggerType BackupTrigger `gorm:"column:trigger_type;size:20;not null" json:"trigger_type"`
	TriggeredBy *uint64       `json:"triggered_by,omitempty"`
	Error       string        `gorm:"type:text" json:"error,omitempty"`
	StartedAt   time.Time     `gorm:"not null" json:"started_at"`
	FinishedAt  *time.Time    `json:"finished_at,omitempty"`
}

func (Backup) TableName() string { return "backups" }
