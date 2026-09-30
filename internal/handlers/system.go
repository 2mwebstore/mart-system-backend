package handlers

import (
	"fmt"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/gin-gonic/gin"

	"com-mart/backend/internal/middleware"
	"com-mart/backend/internal/models"
	"com-mart/backend/internal/services"
	"com-mart/backend/internal/utils"
)

// ---- Telegram alert settings (Settings > Alerts & Backup) -------------------
//
// Gated by the `system.manage` permission (Owner only by default, same
// standing as branch.manage) — the bot token is a secret and GET /settings
// is otherwise open to every signed-in user (POS cashiers included).

type notifySettingsDTO struct {
	Enabled    bool     `json:"enabled"`
	BotToken   string   `json:"bot_token"`
	ChatID     string   `json:"chat_id"`
	AlertTypes []string `json:"alert_types"`
}

func toNotifyDTO(s services.NotifySettings) notifySettingsDTO {
	types := make([]string, len(s.AlertTypes))
	for i, t := range s.AlertTypes {
		types[i] = string(t)
	}
	return notifySettingsDTO{Enabled: s.Enabled, BotToken: s.BotToken, ChatID: s.ChatID, AlertTypes: types}
}

func (a *API) GetNotifySettings(c *gin.Context) {
	utils.OK(c, http.StatusOK, toNotifyDTO(a.Notify.LoadSettings()))
}

func validAlertTypes(in []string) ([]services.AlertType, bool) {
	out := make([]services.AlertType, 0, len(in))
	for _, v := range in {
		t := services.AlertType(v)
		if !slices.Contains(services.AllAlertTypes, t) {
			return nil, false
		}
		out = append(out, t)
	}
	return out, true
}

func (a *API) UpdateNotifySettings(c *gin.Context) {
	var req notifySettingsDTO
	if !bind(c, &req) {
		return
	}
	types, ok := validAlertTypes(req.AlertTypes)
	if !ok {
		invalid(c, "alert_types", "Contains an unknown alert type.")
		return
	}
	if req.Enabled && (req.BotToken == "" || req.ChatID == "") {
		invalid(c, "bot_token", "Enter a bot token and chat ID before enabling alerts.")
		return
	}
	before := a.Notify.LoadSettings()
	next := services.NotifySettings{Enabled: req.Enabled, BotToken: req.BotToken, ChatID: req.ChatID, AlertTypes: types}
	if err := a.Notify.SaveSettings(next); err != nil {
		dbFail(c, err)
		return
	}
	// The bot token is a secret; mask it before it's written to the audit
	// log, which a broader audience (report.activity_log) can read than
	// this endpoint's own system.manage gate.
	auditBefore, auditAfter := toNotifyDTO(before), req
	auditBefore.BotToken, auditAfter.BotToken = maskSecret(auditBefore.BotToken), maskSecret(auditAfter.BotToken)
	a.audit(c, "settings", "settings.update", "settings", 0, auditBefore, auditAfter)
	utils.OK(c, http.StatusOK, toNotifyDTO(next))
}

// maskSecret is for audit-log entries, never for what an authorised viewer
// of the real settings endpoint sees.
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••"
	}
	return s[:4] + "…" + s[len(s)-4:]
}

type testAlertReq struct {
	BotToken string `json:"bot_token" binding:"required"`
	ChatID   string `json:"chat_id" binding:"required"`
}

// SendTestAlert lets the person setting this up confirm the bot token/chat ID
// work before saving and turning any real alert on.
func (a *API) SendTestAlert(c *gin.Context) {
	var req testAlertReq
	if !bind(c, &req) {
		return
	}
	if err := a.Notify.SendNow(req.BotToken, req.ChatID, "✅ Com Mart is connected — you'll get alerts here."); err != nil {
		fail(c, utils.NewAppError(http.StatusUnprocessableEntity, "TELEGRAM_TEST_FAILED", "Telegram rejected the message — check the bot token and chat ID."))
		return
	}
	utils.OK(c, http.StatusOK, gin.H{"sent": true})
}

// ---- Backups -----------------------------------------------------------------

type backupDTO struct {
	ID           uint64  `json:"id"`
	Filename     string  `json:"filename"`
	SizeBytes    int64   `json:"size_bytes"`
	Status       string  `json:"status"`
	TriggerType  string  `json:"trigger_type"`
	TriggeredBy  string  `json:"triggered_by,omitempty"`
	Error        string  `json:"error,omitempty"`
	StartedAt    string  `json:"started_at"`
	FinishedAt   *string `json:"finished_at"`
	Downloadable bool    `json:"downloadable"`
}

func (a *API) toBackupDTO(b models.Backup) backupDTO {
	dto := backupDTO{
		ID: b.ID, Filename: b.Filename, SizeBytes: b.SizeBytes, Status: string(b.Status),
		TriggerType: string(b.TriggerType), Error: b.Error, StartedAt: b.StartedAt.Format(time.RFC3339),
	}
	if b.FinishedAt != nil {
		s := b.FinishedAt.Format(time.RFC3339)
		dto.FinishedAt = &s
	}
	if b.TriggeredBy != nil {
		var u struct{ FullName string }
		if a.DB.Table("users").Select("full_name").Where("id = ?", *b.TriggeredBy).Scan(&u); u.FullName != "" {
			dto.TriggeredBy = u.FullName
		}
	}
	if b.Status == models.BackupSuccess {
		if path, err := a.Backup.FilePath(b.Filename); err == nil {
			if _, err := os.Stat(path); err == nil {
				dto.Downloadable = true
			}
		}
	}
	return dto
}

func (a *API) ListBackups(c *gin.Context) {
	rows, err := a.Backup.List(90)
	if err != nil {
		dbFail(c, err)
		return
	}
	out := make([]backupDTO, len(rows))
	for i, r := range rows {
		out[i] = a.toBackupDTO(r)
	}
	s := a.Backup.LoadSettings()
	utils.OK(c, http.StatusOK, gin.H{
		"rows":     out,
		"settings": gin.H{"auto_enabled": s.AutoEnabled, "retention_days": s.RetentionDays},
	})
}

type backupSettingsReq struct {
	AutoEnabled   bool `json:"auto_enabled"`
	RetentionDays int  `json:"retention_days" binding:"required,min=1,max=365"`
}

func (a *API) UpdateBackupSettings(c *gin.Context) {
	var req backupSettingsReq
	if !bind(c, &req) {
		return
	}
	if err := a.Backup.SaveSettings(services.BackupSettings{AutoEnabled: req.AutoEnabled, RetentionDays: req.RetentionDays}); err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "settings", "backup.settings_update", "settings", 0, nil, req)
	s := a.Backup.LoadSettings()
	utils.OK(c, http.StatusOK, gin.H{"auto_enabled": s.AutoEnabled, "retention_days": s.RetentionDays})
}

// RunBackupNow runs mysqldump synchronously and returns the result. The
// store's data is small (a POS database, not a data warehouse), so this
// finishing within an ordinary request timeout is the expected case; a
// backup that was already RUNNING from the scheduler is left alone.
func (a *API) RunBackupNow(c *gin.Context) {
	userID := middleware.UserIDFrom(c)
	row, err := a.Backup.Run(c.Request.Context(), models.BackupManual, &userID)
	if err != nil {
		var id uint64
		if row != nil {
			id = row.ID
		}
		a.audit(c, "settings", "backup.run", "backup", id, nil, gin.H{"status": "failed", "error": err.Error()})
		fail(c, utils.NewAppError(http.StatusUnprocessableEntity, "BACKUP_FAILED", "Backup failed: "+err.Error()))
		return
	}
	a.audit(c, "settings", "backup.run", "backup", row.ID, nil, gin.H{"status": "success", "filename": row.Filename})
	utils.OK(c, http.StatusOK, a.toBackupDTO(*row))
}

func (a *API) DownloadBackup(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var row models.Backup
	if err := a.DB.First(&row, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	if row.Status != models.BackupSuccess {
		fail(c, utils.NewAppError(http.StatusConflict, "BACKUP_NOT_READY", "This backup did not finish successfully."))
		return
	}
	path, err := a.Backup.FilePath(row.Filename)
	if err != nil {
		fail(c, utils.ErrNotFound)
		return
	}
	if _, err := os.Stat(path); err != nil {
		fail(c, utils.NewAppError(http.StatusGone, "BACKUP_FILE_MISSING", "This backup file is no longer on disk (the server may have redeployed since it ran)."))
		return
	}
	a.audit(c, "settings", "backup.download", "backup", row.ID, nil, gin.H{"filename": row.Filename})
	c.FileAttachment(path, row.Filename)
}

// DeleteBackup removes a backup's row and, if still present, its file. A
// RUNNING backup can't be deleted out from under itself.
func (a *API) DeleteBackup(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var row models.Backup
	if err := a.DB.First(&row, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	if row.Status == models.BackupRunning {
		fail(c, utils.NewAppError(http.StatusConflict, "BACKUP_RUNNING", "This backup is still running."))
		return
	}
	if path, err := a.Backup.FilePath(row.Filename); err == nil {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			dbFail(c, err)
			return
		}
	}
	if err := a.DB.Delete(&models.Backup{}, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "settings", "backup.delete", "backup", id, gin.H{"filename": row.Filename}, nil)
	utils.OK(c, http.StatusOK, gin.H{"deleted": true})
}

// ---- Activity log retention (Settings > Alerts & Backup) -------------------
// Same standing as backup settings: system.manage, purged nightly on the
// same scheduler tick (see BackupService.RunScheduler) plus a manual
// "Clear now" button here for the same reason backups got one — no need to
// wait for tonight to see the effect of a setting you just changed.

func (a *API) GetActivityLogRetention(c *gin.Context) {
	utils.OK(c, http.StatusOK, gin.H{"retention_months": a.Backup.LoadActivityLogRetentionMonths()})
}

type activityLogRetentionReq struct {
	RetentionMonths int `json:"retention_months" binding:"required,oneof=1 2 3"`
}

func (a *API) UpdateActivityLogRetention(c *gin.Context) {
	var req activityLogRetentionReq
	if !bind(c, &req) {
		return
	}
	before := a.Backup.LoadActivityLogRetentionMonths()
	if err := a.Backup.SaveActivityLogRetentionMonths(req.RetentionMonths); err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "settings", "settings.update", "settings", 0, gin.H{"retention_months": before}, gin.H{"retention_months": req.RetentionMonths})
	utils.OK(c, http.StatusOK, gin.H{"retention_months": req.RetentionMonths})
}

// ClearActivityLogNow purges everything already older than the configured
// retention window right now, instead of waiting for the nightly tick.
func (a *API) ClearActivityLogNow(c *gin.Context) {
	n, err := a.Backup.PurgeOldActivityLogs()
	if err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "settings", "activity_log.clear", "settings", 0, nil, gin.H{"deleted": n})
	utils.OK(c, http.StatusOK, gin.H{"deleted": n})
}

// ---- Document numbering (Settings > Numbering) -----------------------------
// The "PREFIX-000001" reference number on new sales/expenses/purchase
// orders. Gated by system.manage, same standing as the rest of this file —
// changing where invoice numbers start or what they're prefixed with is a
// system-configuration action, not a day-to-day one.

type numberSequenceDTO struct {
	DocType    string `json:"doc_type"`
	Prefix     string `json:"prefix"`
	NextNumber uint64 `json:"next_number"`
	Preview    string `json:"preview"` // what the next document created will look like
}

func toSequenceDTO(s models.NumberSequence) numberSequenceDTO {
	return numberSequenceDTO{DocType: s.DocType, Prefix: s.Prefix, NextNumber: s.NextNumber, Preview: fmt.Sprintf("%s-%06d", s.Prefix, s.NextNumber)}
}

func (a *API) GetNumberSequences(c *gin.Context) {
	var rows []models.NumberSequence
	if err := a.DB.Order("doc_type").Find(&rows).Error; err != nil {
		dbFail(c, err)
		return
	}
	out := make([]numberSequenceDTO, len(rows))
	for i, r := range rows {
		out[i] = toSequenceDTO(r)
	}
	utils.OK(c, http.StatusOK, out)
}

type numberSequenceReq struct {
	DocType string `json:"doc_type" binding:"required,oneof=SALE EXPENSE PURCHASE_ORDER"`
	// Kept short deliberately: sales.receipt_no is VARCHAR(20), and
	// "<prefix>-000001" needs 7 characters for the "-000001" part, so a
	// longer prefix would risk truncating on that column specifically.
	Prefix     string `json:"prefix" binding:"max=12"`
	NextNumber uint64 `json:"next_number" binding:"required,min=1"`
}

func (a *API) UpdateNumberSequence(c *gin.Context) {
	var req numberSequenceReq
	if !bind(c, &req) {
		return
	}
	var before models.NumberSequence
	if err := a.DB.Where("doc_type = ?", req.DocType).First(&before).Error; err != nil {
		dbFail(c, err)
		return
	}
	if err := a.DB.Model(&models.NumberSequence{}).Where("doc_type = ?", req.DocType).Updates(map[string]interface{}{
		"prefix": req.Prefix, "next_number": req.NextNumber,
	}).Error; err != nil {
		dbFail(c, err)
		return
	}
	var after models.NumberSequence
	a.DB.Where("doc_type = ?", req.DocType).First(&after)
	a.audit(c, "settings", "settings.update", "number_sequence", 0, toSequenceDTO(before), toSequenceDTO(after))
	utils.OK(c, http.StatusOK, toSequenceDTO(after))
}
