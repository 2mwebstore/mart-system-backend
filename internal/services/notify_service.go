package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"com-mart/backend/internal/models"
)

// AlertType is one kind of event the Telegram integration can notify on.
// Keep this list in sync with admin/src/i18n's `alertType.*` keys and the
// checkboxes on the Settings > Alerts & Backup tab.
type AlertType string

const (
	AlertLowStock      AlertType = "low_stock"
	AlertShiftOpened   AlertType = "shift_opened"
	AlertShiftClosed   AlertType = "shift_closed"
	AlertSaleCompleted AlertType = "sale_completed"
	AlertVoidRefund    AlertType = "void_refund"
	AlertExpenseAdded  AlertType = "expense_added"
	AlertPOReceived    AlertType = "po_received"
	AlertBackupSuccess AlertType = "backup_success"
	AlertBackupFailed  AlertType = "backup_failed"
	AlertTableOrder    AlertType = "table_order"
)

// CompanyAlertTypes are the alerts a company can turn on, in display order:
// what happens in its own branches.
var CompanyAlertTypes = []AlertType{
	AlertLowStock, AlertShiftOpened, AlertShiftClosed, AlertSaleCompleted, AlertVoidRefund, AlertExpenseAdded,
	AlertPOReceived, AlertTableOrder,
}

// PlatformAlertTypes are the super admin's alerts, about the whole install.
// They're sent with company 0 (see Send).
var PlatformAlertTypes = []AlertType{AlertBackupSuccess, AlertBackupFailed}

const (
	settingTelegramEnabled = "telegram_enabled"
	settingTelegramToken   = "telegram_bot_token"
	settingTelegramChatID  = "telegram_chat_id"
	settingTelegramAlerts  = "telegram_alert_types" // comma-separated AlertType list
)

// NotifySettings is the Telegram integration's config. Each company has its
// own, in company_settings (same table/pattern as receipt header, loyalty
// rate, etc. — see handlers/reference.go). The install has one more, in
// `settings`, for the alerts that aren't any company's: backups. Company id 0
// means that one.
type NotifySettings struct {
	Enabled    bool        `json:"enabled"`
	BotToken   string      `json:"bot_token"`
	ChatID     string      `json:"chat_id"`
	AlertTypes []AlertType `json:"alert_types"`
}

func (s NotifySettings) wants(t AlertType) bool {
	if !s.Enabled || s.BotToken == "" || s.ChatID == "" {
		return false
	}
	for _, a := range s.AlertTypes {
		if a == t {
			return true
		}
	}
	return false
}

// NotifyService loads the Telegram settings and sends alerts through the Bot
// API's sendMessage call — no SDK, it's one HTTP POST.
type NotifyService struct {
	DB *gorm.DB
}

func NewNotifyService(db *gorm.DB) *NotifyService { return &NotifyService{DB: db} }

// FormatUSD renders integer cents as "$12.34" (or "-$12.34"), for Telegram
// alert text. The admin app formats money client-side; this is the one place
// the server needs to render money as a string for a human.
func FormatUSD(cents int64) string {
	neg := ""
	if cents < 0 {
		neg = "-"
		cents = -cents
	}
	return neg + "$" + strconv.FormatInt(cents/100, 10) + "." + fmt.Sprintf("%02d", cents%100)
}

var telegramKeys = []string{settingTelegramEnabled, settingTelegramToken, settingTelegramChatID, settingTelegramAlerts}

// LoadSettings is a company's Telegram settings, or the install's for 0.
func (n *NotifyService) LoadSettings(companyID uint64) NotifySettings {
	m := map[string]string{}
	if companyID == 0 {
		var rows []models.Setting
		n.DB.Where("`key` IN ?", telegramKeys).Find(&rows)
		for _, r := range rows {
			m[r.Key] = r.Value
		}
	} else {
		var rows []models.CompanySetting
		n.DB.Where("company_id = ? AND `key` IN ?", companyID, telegramKeys).Find(&rows)
		for _, r := range rows {
			m[r.Key] = r.Value
		}
	}
	var types []AlertType
	if m[settingTelegramAlerts] != "" {
		for _, v := range strings.Split(m[settingTelegramAlerts], ",") {
			if v != "" {
				types = append(types, AlertType(v))
			}
		}
	}
	return NotifySettings{
		Enabled:    m[settingTelegramEnabled] == "1",
		BotToken:   m[settingTelegramToken],
		ChatID:     m[settingTelegramChatID],
		AlertTypes: types,
	}
}

// SaveSettings writes a company's Telegram settings, or the install's for 0.
func (n *NotifyService) SaveSettings(companyID uint64, s NotifySettings) error {
	keys := make([]string, 0, len(s.AlertTypes))
	for _, a := range s.AlertTypes {
		keys = append(keys, string(a))
	}
	enabled := "0"
	if s.Enabled {
		enabled = "1"
	}
	pairs := map[string]string{
		settingTelegramEnabled: enabled,
		settingTelegramToken:   s.BotToken,
		settingTelegramChatID:  s.ChatID,
		settingTelegramAlerts:  strings.Join(keys, ","),
	}
	return n.DB.Transaction(func(tx *gorm.DB) error {
		for k, v := range pairs {
			var row interface{} = &models.Setting{Key: k, Value: v}
			if companyID != 0 {
				row = &models.CompanySetting{CompanyID: companyID, Key: k, Value: v}
			}
			if err := tx.Clauses(clause.OnConflict{DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"})}).Create(row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// Send fires a Telegram message for the given alert type, if the integration
// is enabled and that type is selected. It never blocks the caller and never
// returns an error to it — a failed alert must not fail the business action
// that triggered it (a sale, a void, ...); the failure is logged instead.
// It goes to the company's own chat; company 0 is the install's (backups).
func (n *NotifyService) Send(companyID uint64, t AlertType, message string) {
	s := n.LoadSettings(companyID)
	if !s.wants(t) {
		return
	}
	go n.deliver(s.BotToken, s.ChatID, message)
}

// SendNow is the synchronous form, for the "send a test message" action
// where the caller wants to report success/failure to the person who clicked
// it. It ignores the enabled/alert-type gating — a test message always goes
// out if a token and chat id are given.
func (n *NotifyService) SendNow(botToken, chatID, message string) error {
	return n.deliver(botToken, chatID, message)
}

func (n *NotifyService) deliver(botToken, chatID, message string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	body, _ := json.Marshal(map[string]string{"chat_id": chatID, "text": message, "parse_mode": "HTML"})
	url := "https://api.telegram.org/bot" + botToken + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Error().Err(err).Msg("telegram: sendMessage request failed")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Error().Int("status", resp.StatusCode).Msg("telegram: sendMessage rejected")
		return errStatus(resp.StatusCode)
	}
	return nil
}

type errStatus int

func (e errStatus) Error() string { return "telegram API returned status " + strconv.Itoa(int(e)) }

// FormatItemLines renders a bulleted list for a Telegram message (e.g. sale
// items, PO lines), capped at max entries so a large cart/order doesn't blow
// past Telegram's message length limit — the rest are summarised as one line.
func FormatItemLines(lines []string, max int) string {
	if len(lines) == 0 {
		return ""
	}
	shown := lines
	var extra int
	if len(lines) > max {
		shown = lines[:max]
		extra = len(lines) - max
	}
	out := make([]string, len(shown))
	for i, l := range shown {
		out[i] = "• " + l
	}
	if extra > 0 {
		out = append(out, fmt.Sprintf("…and %d more", extra))
	}
	return strings.Join(out, "\n")
}
