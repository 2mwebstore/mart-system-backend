package services

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"com-mart/backend/internal/config"
	"com-mart/backend/internal/models"
)

const (
	settingBackupAutoEnabled = "backup_auto_enabled"
	settingBackupRetention   = "backup_retention_days"
	defaultRetentionDays     = 14
)

type BackupSettings struct {
	AutoEnabled   bool `json:"auto_enabled"`
	RetentionDays int  `json:"retention_days"`
}

// BackupService runs `mysqldump`, gzips its output onto local disk
// (config.BackupDir) and records the run in the `backups` table.
//
// The dump lives on local disk, not object storage: this app has no S3/R2
// client wired up yet (see docs/DECISIONS.md — product images are still data
// URLs for the same reason), and adding one is more than this feature needs.
// On a host with a persistent volume (a Railway Volume mounted at BackupDir,
// or a normal server) that's a real backup; on a plain container with no
// volume it resets on every deploy, so a backup only survives until then —
// use "Backup now" + Download before redeploying, or attach a volume. See
// docs/DEPLOY.md.
type BackupService struct {
	DB     *gorm.DB
	Cfg    *config.Config
	Notify *NotifyService
}

func NewBackupService(db *gorm.DB, cfg *config.Config, notify *NotifyService) *BackupService {
	return &BackupService{DB: db, Cfg: cfg, Notify: notify}
}

func (b *BackupService) LoadSettings() BackupSettings {
	var rows []models.Setting
	b.DB.Where("`key` IN ?", []string{settingBackupAutoEnabled, settingBackupRetention}).Find(&rows)
	m := map[string]string{}
	for _, r := range rows {
		m[r.Key] = r.Value
	}
	days, err := strconv.Atoi(m[settingBackupRetention])
	if err != nil || days <= 0 {
		days = defaultRetentionDays
	}
	// Auto-backup defaults to on (a missing row means "never configured",
	// not "turned off") — the nightly backup is meant to just work.
	auto := m[settingBackupAutoEnabled] != "0"
	return BackupSettings{AutoEnabled: auto, RetentionDays: days}
}

func (b *BackupService) SaveSettings(s BackupSettings) error {
	auto := "0"
	if s.AutoEnabled {
		auto = "1"
	}
	if s.RetentionDays <= 0 {
		s.RetentionDays = defaultRetentionDays
	}
	pairs := map[string]string{
		settingBackupAutoEnabled: auto,
		settingBackupRetention:   strconv.Itoa(s.RetentionDays),
	}
	return b.DB.Transaction(func(tx *gorm.DB) error {
		for k, v := range pairs {
			row := models.Setting{Key: k, Value: v}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"})}).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ---- Activity log retention -------------------------------------------------
// Lives here (not its own file/scheduler) because it runs on the exact same
// nightly tick as the backup — one midnight-wait loop, two housekeeping jobs.

const (
	settingActivityLogRetentionMonths = "activity_log_retention_months"
	defaultActivityLogRetentionMonths = 3
)

func (b *BackupService) LoadActivityLogRetentionMonths() int {
	var row models.Setting
	if err := b.DB.Where("`key` = ?", settingActivityLogRetentionMonths).First(&row).Error; err != nil {
		return defaultActivityLogRetentionMonths
	}
	months, err := strconv.Atoi(row.Value)
	if err != nil || months <= 0 {
		return defaultActivityLogRetentionMonths
	}
	return months
}

func (b *BackupService) SaveActivityLogRetentionMonths(months int) error {
	if months <= 0 {
		months = defaultActivityLogRetentionMonths
	}
	row := models.Setting{Key: settingActivityLogRetentionMonths, Value: strconv.Itoa(months)}
	return b.DB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"})}).Create(&row).Error
}

// PurgeOldActivityLogs hard-deletes every activity_logs row older than the
// configured retention window (ActivityLog has no SoftDelete — a retention
// purge that only flagged rows instead of removing them would defeat the
// point). Returns how many rows it removed.
func (b *BackupService) PurgeOldActivityLogs() (int64, error) {
	months := b.LoadActivityLogRetentionMonths()
	cutoff := time.Now().AddDate(0, -months, 0)
	res := b.DB.Where("created_at < ?", cutoff).Delete(&models.ActivityLog{})
	return res.RowsAffected, res.Error
}

func (b *BackupService) List(limit int) ([]models.Backup, error) {
	var rows []models.Backup
	err := b.DB.Order("started_at DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

// FilePath resolves a stored filename to its path on disk, rejecting
// anything that isn't a plain filename (defence in depth for the download
// endpoint, which takes the filename from the backups table, not straight
// from the request — but never trust a path join with anything less).
func (b *BackupService) FilePath(filename string) (string, error) {
	if filename == "" || filename != filepath.Base(filename) {
		return "", errors.New("invalid backup filename")
	}
	return filepath.Join(b.Cfg.BackupDir, filename), nil
}

// Run performs one backup: mysqldump -> gzip -> BackupDir, recorded in the
// backups table from RUNNING to SUCCESS/FAILED. It always returns the row
// (even on failure) so the caller can show what happened.
func (b *BackupService) Run(ctx context.Context, trigger models.BackupTrigger, triggeredBy *uint64) (*models.Backup, error) {
	if err := os.MkdirAll(b.Cfg.BackupDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating backup dir: %w", err)
	}

	started := time.Now()
	filename := fmt.Sprintf("%s-%s.sql.gz", b.Cfg.DBName, started.Format("20060102-150405"))
	row := &models.Backup{
		Filename: filename, Status: models.BackupRunning, TriggerType: trigger,
		TriggeredBy: triggeredBy, StartedAt: started,
	}
	if err := b.DB.Create(row).Error; err != nil {
		return nil, fmt.Errorf("recording backup start: %w", err)
	}

	size, runErr := b.dump(ctx, filepath.Join(b.Cfg.BackupDir, filename))
	finished := time.Now()
	row.FinishedAt = &finished
	if runErr != nil {
		row.Status = models.BackupFailed
		row.Error = runErr.Error()
		log.Error().Err(runErr).Str("filename", filename).Msg("backup failed")
	} else {
		row.Status = models.BackupSuccess
		row.SizeBytes = size
	}
	if err := b.DB.Save(row).Error; err != nil {
		log.Error().Err(err).Msg("recording backup result")
	}

	if runErr != nil {
		b.Notify.Send(AlertBackupFailed, fmt.Sprintf("❌ <b>Backup failed</b>\n%s\n%s", filename, HTMLEscape(runErr.Error())))
	} else {
		b.Notify.Send(AlertBackupSuccess, fmt.Sprintf("✅ <b>Backup complete</b>\n%s (%s)", filename, humanSize(size)))
		b.applyRetention()
	}
	return row, runErr
}

// dump shells out to mysqldump and gzips its stdout straight to disk (no
// intermediate plain-text file, no dependency on a `gzip` binary in the
// container). The password never touches argv (visible via `ps` to anyone on
// the box) — it goes through MYSQL_PWD instead.
func (b *BackupService) dump(ctx context.Context, path string) (int64, error) {
	args := []string{
		"--single-transaction", "--quick", "--routines", "--triggers",
		"--no-tablespaces", // avoids needing the PROCESS privilege on managed MySQL hosts
		"--default-character-set=utf8mb4",
		// Without this, the MySQL client treats DBHost "localhost" as "use
		// the local Unix socket" (a client convention, not ours) and ignores
		// -P entirely — which fails against MAMP, whose socket isn't the
		// distro default. Forcing TCP makes -h/-P always mean what they say.
		"--protocol=TCP",
		"-h", b.Cfg.DBHost, "-P", b.Cfg.DBPort, "-u", b.Cfg.DBUser, b.Cfg.DBName,
	}
	cmd := exec.CommandContext(ctx, b.Cfg.MysqldumpPath, args...)
	cmd.Env = append(os.Environ(), "MYSQL_PWD="+b.Cfg.DBPassword)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("starting %s: %w", b.Cfg.MysqldumpPath, err)
	}
	written, copyErr := io.Copy(gz, stdout)
	closeErr := gz.Close()
	waitErr := cmd.Wait()

	if waitErr != nil {
		msg := stderr.String()
		if msg == "" {
			msg = waitErr.Error()
		}
		os.Remove(path)
		return 0, fmt.Errorf("mysqldump: %s", msg)
	}
	if copyErr != nil {
		os.Remove(path)
		return 0, fmt.Errorf("writing backup file: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(path)
		return 0, fmt.Errorf("closing backup file: %w", closeErr)
	}
	return written, nil
}

// applyRetention drops backup rows (and their files, best-effort) started
// before the configured retention window.
func (b *BackupService) applyRetention() {
	settings := b.LoadSettings()
	cutoff := time.Now().AddDate(0, 0, -settings.RetentionDays)
	var old []models.Backup
	if err := b.DB.Where("started_at < ?", cutoff).Find(&old).Error; err != nil {
		log.Error().Err(err).Msg("backup retention: listing old backups")
		return
	}
	for _, row := range old {
		if path, err := b.FilePath(row.Filename); err == nil {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				log.Warn().Err(err).Str("filename", row.Filename).Msg("backup retention: could not remove file")
			}
		}
		if err := b.DB.Delete(&models.Backup{}, row.ID).Error; err != nil {
			log.Error().Err(err).Uint64("id", row.ID).Msg("backup retention: could not delete row")
		}
	}
}

// RunScheduler blocks, running a backup at local midnight every day until
// ctx is cancelled. time.Local is set to APP_TIMEZONE in main.go, so
// "midnight" is the configured business timezone, not the container's.
func (b *BackupService) RunScheduler(ctx context.Context) {
	for {
		wait := time.Until(nextMidnight(time.Now()))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		if b.LoadSettings().AutoEnabled && !b.ranScheduledToday() {
			// ranScheduledToday guards this one specifically — a restart
			// around midnight must not double-run the backup. The log purge
			// below is naturally idempotent (deleting rows already deleted
			// is a no-op), so it doesn't need the same guard.
			if _, err := b.Run(context.Background(), models.BackupScheduled, nil); err != nil {
				log.Error().Err(err).Msg("scheduled backup failed")
			}
		}
		if n, err := b.PurgeOldActivityLogs(); err != nil {
			log.Error().Err(err).Msg("activity log cleanup failed")
		} else if n > 0 {
			log.Info().Int64("deleted", n).Msg("purged old activity log entries")
		}
	}
}

func (b *BackupService) ranScheduledToday() bool {
	y, m, d := time.Now().Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	var count int64
	b.DB.Model(&models.Backup{}).
		Where("trigger_type = ? AND started_at >= ? AND status IN ?", models.BackupScheduled, midnight, []models.BackupStatus{models.BackupRunning, models.BackupSuccess}).
		Count(&count)
	return count > 0
}

func nextMidnight(now time.Time) time.Time {
	y, m, d := now.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	if !midnight.After(now) {
		midnight = midnight.AddDate(0, 0, 1)
	}
	return midnight
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func HTMLEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
