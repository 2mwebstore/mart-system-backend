package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"com-mart/backend/internal/config"
	"com-mart/backend/internal/middleware"
	"com-mart/backend/internal/models"
	"com-mart/backend/internal/services"
	"com-mart/backend/internal/utils"
)

// API holds the dependencies shared by every resource handler (catalog,
// purchasing, customers, expenses, users, shifts, sales, reports, ...).
// Handlers use GORM directly — these are thin CRUD/aggregation endpoints and
// a repository layer per table would only re-wrap the same calls; anything
// that spans several tables (sales, shifts, PO receiving) runs in one
// transaction via db.Transaction.
type API struct {
	DB     *gorm.DB
	Cfg    *config.Config
	Notify *services.NotifyService
	Backup *services.BackupService
}

func NewAPI(db *gorm.DB, cfg *config.Config) *API {
	notify := services.NewNotifyService(db)
	return &API{DB: db, Cfg: cfg, Notify: notify, Backup: services.NewBackupService(db, cfg, notify)}
}

func bind(c *gin.Context, req interface{}) bool {
	if err := c.ShouldBindJSON(req); err != nil {
		utils.RespondError(c, utils.NewValidationError(validationFields(err)))
		return false
	}
	return true
}

// validationFields turns a binding error into {field: message} with the
// field named as the client sends it (snake_case) and a sentence a person
// can act on, instead of the validator's internal "Key: 'x.Y' Error:...".
func validationFields(err error) map[string]string {
	var ve validator.ValidationErrors
	if !errors.As(err, &ve) {
		return map[string]string{"_": "The request body is not valid JSON of the expected shape."}
	}
	fields := make(map[string]string, len(ve))
	for _, fe := range ve {
		name := snakeCase(fe.Field())
		label := strings.ToUpper(name[:1]) + strings.ReplaceAll(name[1:], "_", " ")
		switch fe.Tag() {
		case "required":
			fields[name] = label + " is required."
		case "min", "max", "gt", "gte", "lt", "lte":
			fields[name] = label + " is out of range."
		case "oneof":
			fields[name] = label + " must be one of the allowed options."
		default:
			fields[name] = label + " is not valid."
		}
	}
	return fields
}

func snakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

func idParam(c *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		utils.RespondError(c, utils.ErrNotFound)
		return 0, false
	}
	return id, true
}

func invalid(c *gin.Context, field, msg string) {
	utils.RespondError(c, utils.NewValidationError(map[string]string{field: msg}))
}

func fail(c *gin.Context, err error) {
	utils.RespondError(c, err)
}

// dbFail maps a GORM error onto the API error envelope.
func dbFail(c *gin.Context, err error) {
	if err == gorm.ErrRecordNotFound {
		utils.RespondError(c, utils.ErrNotFound)
		return
	}
	utils.RespondError(c, utils.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", err.Error()))
}

// Document types nextDocNumber knows about — matches the seeded rows in
// number_sequences (migration 000015).
const (
	DocSale          = "SALE"
	DocExpense       = "EXPENSE"
	DocPurchaseOrder = "PURCHASE_ORDER"
)

// nextDocNumber reads-and-increments a document type's counter under a row
// lock (so two sales/expenses/POs created at the same instant never get the
// same number) and formats it as "PREFIX-000001". Must be called inside the
// same transaction that creates the row using the number, so a rolled-back
// create doesn't burn a number.
func nextDocNumber(tx *gorm.DB, docType string) (string, error) {
	var seq models.NumberSequence
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("doc_type = ?", docType).First(&seq).Error; err != nil {
		return "", err
	}
	if err := tx.Model(&models.NumberSequence{}).Where("doc_type = ?", docType).Update("next_number", seq.NextNumber+1).Error; err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%06d", seq.Prefix, seq.NextNumber), nil
}

// branchParam resolves the branch a request is about: ?branch_id=, falling
// back to the caller's first branch. Access to it is enforced by the
// BranchScope middleware on the route group.
func branchParam(c *gin.Context) uint64 {
	if raw := c.Query("branch_id"); raw != "" {
		if id, err := strconv.ParseUint(raw, 10, 64); err == nil {
			return id
		}
	}
	if ids := middleware.BranchIDsFrom(c); len(ids) > 0 {
		return ids[0]
	}
	return 0
}

func canAccessBranch(c *gin.Context, branchID uint64) bool {
	return slices.Contains(middleware.BranchIDsFrom(c), branchID)
}

// today is the current business date (server timezone, see APP_TIMEZONE).
func today() time.Time {
	n := time.Now()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.Local)
}

func parseDay(s string) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02", s, time.Local)
	return t, err == nil
}

// dateRange reads ?date_from / ?date_to (inclusive YYYY-MM-DD). Defaults to
// the last 14 days ending today. `end` is exclusive (start of the day after
// date_to) so it can be used directly as `< end` in SQL.
func dateRange(c *gin.Context) (start, end time.Time) {
	to := today()
	if t, ok := parseDay(c.Query("date_to")); ok {
		to = t
	}
	from := to.AddDate(0, 0, -13)
	if t, ok := parseDay(c.Query("date_from")); ok {
		from = t
	}
	return from, to.AddDate(0, 0, 1)
}

// rangeBounds reads the optional ?date_from / ?date_to (inclusive
// YYYY-MM-DD). Unlike dateRange it applies no default: a missing side stays
// nil ("no limit"), so a list can offer an "All dates" state. `end` is
// exclusive (start of the day after date_to).
func rangeBounds(c *gin.Context) (start, end *time.Time) {
	if t, ok := parseDay(c.Query("date_from")); ok {
		start = &t
	}
	if t, ok := parseDay(c.Query("date_to")); ok {
		e := t.AddDate(0, 0, 1)
		end = &e
	}
	return start, end
}

// audit appends an activity_logs row. Failures are swallowed on purpose: a
// missing audit row must never fail the business operation it describes.
func (a *API) audit(c *gin.Context, module, action, entityType string, entityID uint64, before, after interface{}) {
	enc := func(v interface{}) string {
		// The columns are JSON typed, so "no value" must be the JSON literal
		// null — an empty string is rejected by MySQL.
		if v == nil {
			return "null"
		}
		b, err := json.Marshal(v)
		if err != nil {
			return "null"
		}
		return string(b)
	}
	id := entityID
	_ = a.DB.Create(&models.ActivityLog{
		UserID:     middleware.UserIDFrom(c),
		Role:       middleware.RoleNameFrom(c),
		Module:     module,
		Action:     action,
		EntityType: entityType,
		EntityID:   &id,
		BeforeJSON: enc(before),
		AfterJSON:  enc(after),
		IP:         c.ClientIP(),
		Device:     c.GetHeader("User-Agent"),
		CreatedAt:  time.Now(),
	}).Error
}

func pageOf(c *gin.Context, total int64, p utils.PageParams) utils.Meta {
	return utils.Meta{Page: p.Page, PerPage: p.PerPage, Total: total}
}
