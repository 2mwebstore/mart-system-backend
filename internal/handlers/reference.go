package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"com-mart/backend/internal/middleware"
	"com-mart/backend/internal/models"
	"com-mart/backend/internal/utils"
)

// ---- Branches ------------------------------------------------------------

type branchDTO struct {
	ID            uint64 `json:"id"`
	Code          string `json:"code"`
	Name          string `json:"name"`
	Address       string `json:"address"`
	Phone         string `json:"phone"`
	ReceiptFooter string `json:"receipt_footer"`
	Active        bool   `json:"active"`
}

func toBranchDTO(b models.Branch) branchDTO {
	return branchDTO{b.ID, b.Code, b.Name, b.Address, b.Phone, b.ReceiptFooter, b.Active}
}

type branchReq struct {
	Code          string `json:"code" binding:"required,max=20"`
	Name          string `json:"name" binding:"required,max=120"`
	Address       string `json:"address"`
	Phone         string `json:"phone"`
	ReceiptFooter string `json:"receipt_footer"`
	Active        bool   `json:"active"`
}

func (a *API) ListBranches(c *gin.Context) {
	var rows []models.Branch
	q := a.DB.Where("company_id = ?", companyOf(c)).Order("id")
	// branch.view sees every branch of the company (it's the branch admin screen);
	// everyone else only sees the branches they're assigned to.
	if !hasPerm(c, "branch.view") {
		q = q.Where("id IN ?", middleware.BranchIDsFrom(c))
	}
	if err := q.Find(&rows).Error; err != nil {
		dbFail(c, err)
		return
	}
	out := make([]branchDTO, len(rows))
	for i, r := range rows {
		out[i] = toBranchDTO(r)
	}
	utils.OK(c, http.StatusOK, out)
}

func (a *API) CreateBranch(c *gin.Context) {
	var req branchReq
	if !bind(c, &req) {
		return
	}
	b := models.Branch{CompanyID: companyOf(c), Code: strings.ToUpper(req.Code), Name: req.Name, Address: req.Address, Phone: req.Phone, ReceiptFooter: req.ReceiptFooter, Active: req.Active}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if b.Active {
			if err := roomForActive(tx, b.CompanyID, branchLimit); err != nil {
				return err
			}
		}
		return tx.Create(&b).Error
	})
	if !branchSaved(c, err) {
		return
	}
	// If the creator currently has no branch access at all — the normal
	// state right after a production reset, which now wipes every branch —
	// grant them this one automatically, so creating a branch is enough to
	// start using it, with no separate trip to Users & Roles to un-stick
	// themselves. A no-op for the common case of an admin who already has
	// access to other branches.
	callerID := middleware.UserIDFrom(c)
	var existingAccess int64
	a.DB.Model(&models.UserBranch{}).Where("user_id = ?", callerID).Count(&existingAccess)
	if existingAccess == 0 {
		a.DB.Exec("INSERT IGNORE INTO user_branches (user_id, branch_id) VALUES (?, ?)", callerID, b.ID)
	}
	a.audit(c, "settings", "branch.create", "branch", b.ID, nil, toBranchDTO(b))
	utils.OK(c, http.StatusCreated, toBranchDTO(b))
}

func (a *API) UpdateBranch(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req branchReq
	if !bind(c, &req) {
		return
	}
	var b models.Branch
	if err := a.DB.Where("company_id = ?", companyOf(c)).First(&b, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	before := toBranchDTO(b)
	b.Code, b.Name, b.Address, b.Phone, b.ReceiptFooter, b.Active = strings.ToUpper(req.Code), req.Name, req.Address, req.Phone, req.ReceiptFooter, req.Active
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if b.Active && !before.Active {
			if err := roomForActive(tx, b.CompanyID, branchLimit); err != nil {
				return err
			}
		}
		return tx.Save(&b).Error
	})
	if !branchSaved(c, err) {
		return
	}
	a.audit(c, "settings", "branch.update", "branch", b.ID, before, toBranchDTO(b))
	utils.OK(c, http.StatusOK, toBranchDTO(b))
}

// branchSaved answers the client when saving a branch failed: the company's
// branch limit, or else the code, unique within the company.
func branchSaved(c *gin.Context, err error) bool {
	var appErr *utils.AppError
	if errors.As(err, &appErr) {
		fail(c, err)
		return false
	}
	if err != nil {
		invalid(c, "code", "That branch code is already in use.")
		return false
	}
	return true
}

func (a *API) DeleteBranch(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var b models.Branch
	if err := a.DB.Where("company_id = ?", companyOf(c)).First(&b, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	var used int64
	a.DB.Model(&models.Sale{}).Where("branch_id = ?", id).Count(&used)
	if used > 0 {
		fail(c, utils.NewAppError(http.StatusConflict, "BRANCH_IN_USE", "This branch has sales and can't be deleted — mark it inactive instead."))
		return
	}
	if err := a.DB.Delete(&b).Error; err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "settings", "branch.delete", "branch", id, toBranchDTO(b), nil)
	utils.OK(c, http.StatusOK, gin.H{"deleted": true})
}

func hasPerm(c *gin.Context, key string) bool {
	for _, p := range middleware.PermissionsFrom(c) {
		if p == key {
			return true
		}
	}
	return false
}

// ---- Tills (devices) -------------------------------------------------------

type deviceDTO struct {
	ID         uint64     `json:"id"`
	BranchID   uint64     `json:"branch_id"`
	Name       string     `json:"name"`
	DeviceKey  string     `json:"device_key"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	Active     bool       `json:"active"`
}

func toDeviceDTO(d models.Device) deviceDTO {
	return deviceDTO{d.ID, d.BranchID, d.Name, d.DeviceKey, d.LastSeenAt, d.Active}
}

type deviceReq struct {
	BranchID  uint64 `json:"branch_id" binding:"required"`
	Name      string `json:"name" binding:"required,max=80"`
	DeviceKey string `json:"device_key" binding:"required,max=64"`
	Active    bool   `json:"active"`
}

// PublicTills backs the POS sign-in screen, which has to list tills before
// anyone is authenticated. Only active tills, and only what's needed to pick
// one. (A real kiosk would be provisioned with its device key instead.)
type publicTillDTO struct {
	DeviceKey  string `json:"device_key"`
	Name       string `json:"name"`
	BranchID   uint64 `json:"branch_id"`
	BranchName string `json:"branch_name"`
}

// PublicTills lists one company's tills: the one whose code is in ?company=. An
// unknown or disabled company is 404 UNKNOWN_COMPANY.
func (a *API) PublicTills(c *gin.Context) {
	company, err := a.activeCompanyByCode(c.Query("company"))
	if err != nil {
		fail(c, err)
		return
	}
	// branch_name lets the till picker say which branch each till is in.
	rows := []publicTillDTO{}
	if err := a.DB.Table("devices d").
		Select("d.device_key, d.name, d.branch_id, b.name AS branch_name").
		Joins("JOIN branches b ON b.id = d.branch_id AND b.deleted_at IS NULL").
		Where("d.company_id = ? AND d.active = 1", company.ID).Order("b.name, d.name, d.id").Scan(&rows).Error; err != nil {
		dbFail(c, err)
		return
	}
	utils.OK(c, http.StatusOK, rows)
}

func (a *API) ListDevices(c *gin.Context) {
	var rows []models.Device
	if err := a.DB.Where("company_id = ?", companyOf(c)).Order("id").Find(&rows).Error; err != nil {
		dbFail(c, err)
		return
	}
	out := make([]deviceDTO, len(rows))
	for i, r := range rows {
		out[i] = toDeviceDTO(r)
	}
	utils.OK(c, http.StatusOK, out)
}

func (a *API) CreateDevice(c *gin.Context) {
	var req deviceReq
	if !bind(c, &req) || !a.ownBranch(c, req.BranchID) {
		return
	}
	d := models.Device{CompanyID: companyOf(c), BranchID: req.BranchID, Name: req.Name, DeviceKey: req.DeviceKey, Active: req.Active}
	if err := a.DB.Create(&d).Error; err != nil {
		invalid(c, "device_key", "That device key is already registered.")
		return
	}
	a.audit(c, "settings", "till.create", "device", d.ID, nil, toDeviceDTO(d))
	utils.OK(c, http.StatusCreated, toDeviceDTO(d))
}

func (a *API) UpdateDevice(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req deviceReq
	if !bind(c, &req) || !a.ownBranch(c, req.BranchID) {
		return
	}
	var d models.Device
	if err := a.DB.Where("company_id = ?", companyOf(c)).First(&d, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	before := toDeviceDTO(d)
	d.BranchID, d.Name, d.DeviceKey, d.Active = req.BranchID, req.Name, req.DeviceKey, req.Active
	if err := a.DB.Omit("Branch").Save(&d).Error; err != nil {
		invalid(c, "device_key", "That device key is already registered.")
		return
	}
	a.audit(c, "settings", "till.update", "device", d.ID, before, toDeviceDTO(d))
	utils.OK(c, http.StatusOK, toDeviceDTO(d))
}

func (a *API) DeleteDevice(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var d models.Device
	if err := a.DB.Where("company_id = ?", companyOf(c)).First(&d, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	var used int64
	a.DB.Model(&models.Shift{}).Where("device_id = ?", id).Count(&used)
	if used > 0 {
		fail(c, utils.NewAppError(http.StatusConflict, "TILL_IN_USE", "This till has shift history and can't be deleted — mark it inactive instead."))
		return
	}
	if err := a.DB.Delete(&d).Error; err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "settings", "till.delete", "device", id, toDeviceDTO(d), nil)
	utils.OK(c, http.StatusOK, gin.H{"deleted": true})
}

// ownBranch refuses a branch that isn't the company's, answering 422 on
// branch_id.
func (a *API) ownBranch(c *gin.Context, branchID uint64) bool {
	if !a.companyOwns(c, &models.Branch{}, branchID) {
		invalid(c, "branch_id", "Branch is not valid.")
		return false
	}
	return true
}

// ---- Payment methods -------------------------------------------------------

type paymentMethodDTO struct {
	ID         uint64  `json:"id"`
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	FeePercent float64 `json:"fee_percent"`
	Enabled    bool    `json:"enabled"`
}

func toPMDTO(p models.PaymentMethodRow) paymentMethodDTO {
	return paymentMethodDTO{p.ID, p.Name, p.Type, p.FeePercent, p.Enabled}
}

type paymentMethodReq struct {
	Name       string  `json:"name" binding:"required,max=80"`
	Type       string  `json:"type" binding:"required,oneof=CASH KHQR CARD OTHER"`
	FeePercent float64 `json:"fee_percent" binding:"min=0,max=100"`
	Enabled    bool    `json:"enabled"`
}

func (a *API) ListPaymentMethods(c *gin.Context) {
	var rows []models.PaymentMethodRow
	if err := a.DB.Where("company_id = ?", companyOf(c)).Order("id").Find(&rows).Error; err != nil {
		dbFail(c, err)
		return
	}
	out := make([]paymentMethodDTO, len(rows))
	for i, r := range rows {
		out[i] = toPMDTO(r)
	}
	utils.OK(c, http.StatusOK, out)
}

func (a *API) CreatePaymentMethod(c *gin.Context) {
	var req paymentMethodReq
	if !bind(c, &req) {
		return
	}
	p := models.PaymentMethodRow{CompanyID: companyOf(c), Name: req.Name, Type: req.Type, FeePercent: req.FeePercent, Enabled: req.Enabled}
	if err := a.DB.Create(&p).Error; err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "settings", "payment_method.create", "payment_method", p.ID, nil, toPMDTO(p))
	utils.OK(c, http.StatusCreated, toPMDTO(p))
}

func (a *API) UpdatePaymentMethod(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req paymentMethodReq
	if !bind(c, &req) {
		return
	}
	var p models.PaymentMethodRow
	if err := a.DB.Where("company_id = ?", companyOf(c)).First(&p, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	before := toPMDTO(p)
	p.Name, p.Type, p.FeePercent, p.Enabled = req.Name, req.Type, req.FeePercent, req.Enabled
	if err := a.DB.Save(&p).Error; err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "settings", "payment_method.update", "payment_method", p.ID, before, toPMDTO(p))
	utils.OK(c, http.StatusOK, toPMDTO(p))
}

func (a *API) DeletePaymentMethod(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var p models.PaymentMethodRow
	if err := a.DB.Where("company_id = ?", companyOf(c)).First(&p, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	if err := a.DB.Delete(&p).Error; err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "settings", "payment_method.delete", "payment_method", id, toPMDTO(p), nil)
	utils.OK(c, http.StatusOK, gin.H{"deleted": true})
}

// ---- Settings (receipt + loyalty) ---------------------------------------------

type settingsDTO struct {
	ReceiptHeader       string  `json:"receipt_header"`
	ReceiptFooter       string  `json:"receipt_footer"`
	LoyaltyPointsPerUSD float64 `json:"loyalty_points_per_usd"`
	// AllowOutOfStockSale is store-wide (see UpdateStockPolicy below) —
	// unlike the rest of this DTO it isn't writable through UpdateSettings,
	// only read here so every authenticated caller (including the POS
	// screen, which needs it to decide what to grey out) can see the
	// current value without needing system.manage.
	AllowOutOfStockSale bool `json:"allow_out_of_stock_sale"`
	// DefaultCustomerID is the customer the POS starts every sale with (null:
	// walk-in). Like AllowOutOfStockSale it's only read here, so the POS can
	// see it; it's written by UpdateDefaultCustomer from the Customers page.
	DefaultCustomerID   *uint64 `json:"default_customer_id"`
	DefaultCustomerName string  `json:"default_customer_name"`
	// TaxDefaults is the tax rate each place starts with. Like the default
	// customer it's only read here; PUT /tax-rates/defaults writes it.
	TaxDefaults taxDefaultsDTO `json:"tax_defaults"`
}

const (
	settingReceiptHeader   = "receipt_header"
	settingReceiptFooter   = "receipt_footer"
	settingLoyalty         = "loyalty_points_per_usd"
	settingAllowOOS        = "allow_out_of_stock_sale"
	settingDefaultCustomer = "pos_default_customer_id"
)

// companySettings reads a company's own settings: every one but the logo
// (served on its own), or just keys when given.
func (a *API) companySettings(companyID uint64, keys ...string) map[string]string {
	var rows []models.CompanySetting
	q := a.DB.Where("company_id = ?", companyID)
	if len(keys) > 0 {
		q = q.Where("`key` IN ?", keys)
	} else {
		q = q.Where("`key` <> ?", settingCompanyLogo)
	}
	q.Find(&rows)
	m := map[string]string{}
	for _, r := range rows {
		m[r.Key] = r.Value
	}
	return m
}

// saveCompanySettings writes some of a company's settings.
func saveCompanySettings(tx *gorm.DB, companyID uint64, pairs map[string]string) error {
	for k, v := range pairs {
		row := models.CompanySetting{CompanyID: companyID, Key: k, Value: v}
		if err := tx.Clauses(clause.OnConflict{DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"})}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

// loadSettings is a company's receipt, loyalty and stock settings.
func (a *API) loadSettings(companyID uint64) settingsDTO {
	m := a.companySettings(companyID)
	loyalty, err := strconv.ParseFloat(m[settingLoyalty], 64)
	if err != nil {
		loyalty = 1
	}
	header := m[settingReceiptHeader]
	if header == "" {
		header = m[settingCompanyName]
	}
	if header == "" {
		header = a.companyName(companyID)
	}
	if header == "" {
		header = defaultStoreName
	}
	out := settingsDTO{
		ReceiptHeader: header, ReceiptFooter: m[settingReceiptFooter], LoyaltyPointsPerUSD: loyalty,
		AllowOutOfStockSale: m[settingAllowOOS] == "true",
		TaxDefaults:         a.taxDefaults(companyID, m),
	}
	// Only while that customer is still the company's: one deleted since (or
	// wiped by a production reset) leaves the POS on walk-in.
	if id, err := strconv.ParseUint(m[settingDefaultCustomer], 10, 64); err == nil && id > 0 {
		if cu, err := a.getCustomer(companyID, id); err == nil {
			out.DefaultCustomerID, out.DefaultCustomerName = &cu.ID, cu.Name
		}
	}
	return out
}

func (a *API) GetSettings(c *gin.Context) {
	utils.OK(c, http.StatusOK, a.loadSettings(companyOf(c)))
}

func (a *API) UpdateSettings(c *gin.Context) {
	var req settingsDTO
	if !bind(c, &req) {
		return
	}
	if req.LoyaltyPointsPerUSD < 0 {
		invalid(c, "loyalty_points_per_usd", "Must be zero or more.")
		return
	}
	before := a.loadSettings(companyOf(c))
	pairs := map[string]string{
		settingReceiptHeader: req.ReceiptHeader,
		settingReceiptFooter: req.ReceiptFooter,
		settingLoyalty:       strconv.FormatFloat(req.LoyaltyPointsPerUSD, 'f', -1, 64),
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		return saveCompanySettings(tx, companyOf(c), pairs)
	})
	if err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "settings", "settings.update", "settings", 0, before, req)
	utils.OK(c, http.StatusOK, a.loadSettings(companyOf(c)))
}

type stockPolicyReq struct {
	AllowOutOfStockSale bool `json:"allow_out_of_stock_sale"`
}

// UpdateStockPolicy is the one store-wide switch for whether a sale can go
// through at zero/negative stock (a backorder) — deliberately store-wide,
// not per-product: see docs/DECISIONS.md for why this replaced the
// per-product toggle from the previous turn. Gated by system.manage, same
// standing as the other Settings > System controls (numbering, retention);
// GetSettings (no permission needed) is how everything else, including the
// POS screen, reads the current value.
func (a *API) UpdateStockPolicy(c *gin.Context) {
	var req stockPolicyReq
	if !bind(c, &req) {
		return
	}
	before := a.loadSettings(companyOf(c)).AllowOutOfStockSale
	if err := saveCompanySettings(a.DB, companyOf(c), map[string]string{settingAllowOOS: strconv.FormatBool(req.AllowOutOfStockSale)}); err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "settings", "settings.update", "settings", 0, gin.H{"allow_out_of_stock_sale": before}, gin.H{"allow_out_of_stock_sale": req.AllowOutOfStockSale})
	utils.OK(c, http.StatusOK, gin.H{"allow_out_of_stock_sale": req.AllowOutOfStockSale})
}

type defaultCustomerReq struct {
	CustomerID *uint64 `json:"customer_id"`
}

// UpdateDefaultCustomer picks the customer the POS starts every sale with for
// the caller's company; customer_id null (or 0) goes back to walk-in. Gated by
// customer.update, like editing a customer on the Customers page it's set from. Answers
// with the settings, as GetSettings does.
func (a *API) UpdateDefaultCustomer(c *gin.Context) {
	var req defaultCustomerReq
	if !bind(c, &req) {
		return
	}
	company := companyOf(c)
	value := ""
	if req.CustomerID != nil && *req.CustomerID > 0 {
		if _, err := a.getCustomer(company, *req.CustomerID); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				invalid(c, "customer_id", "No such customer.")
				return
			}
			dbFail(c, err)
			return
		}
		value = strconv.FormatUint(*req.CustomerID, 10)
	}
	before := a.loadSettings(company).DefaultCustomerID
	if err := saveCompanySettings(a.DB, company, map[string]string{settingDefaultCustomer: value}); err != nil {
		dbFail(c, err)
		return
	}
	after := a.loadSettings(company)
	a.audit(c, "settings", "settings.update", "settings", 0, gin.H{"default_customer_id": before}, gin.H{"default_customer_id": after.DefaultCustomerID})
	utils.OK(c, http.StatusOK, after)
}

// ---- Exchange rate ----------------------------------------------------------

type exchangeRateDTO struct {
	ID          uint64    `json:"id"`
	Rate        int64     `json:"rate"`
	SetBy       string    `json:"set_by"`
	EffectiveAt time.Time `json:"effective_at"`
}

// currentRate is the company's riel per USD now.
func (a *API) currentRate(companyID uint64) int64 {
	var r models.ExchangeRate
	if err := a.DB.Where("company_id = ?", companyID).Order("effective_at DESC, id DESC").First(&r).Error; err != nil {
		return 4100
	}
	return r.RateRielPerUSD
}

func (a *API) GetExchangeRate(c *gin.Context) {
	var rows []exchangeRateDTO
	err := a.DB.Table("exchange_rates er").
		Select("er.id, er.rate_riel_per_usd AS rate, COALESCE(u.full_name,'') AS set_by, er.effective_at").
		Joins("LEFT JOIN users u ON u.id = er.set_by_user_id").
		Where("er.company_id = ?", companyOf(c)).
		Order("er.effective_at DESC, er.id DESC").Limit(50).Scan(&rows).Error
	if err != nil {
		dbFail(c, err)
		return
	}
	current := int64(4100)
	if len(rows) > 0 {
		current = rows[0].Rate
	}
	utils.OK(c, http.StatusOK, gin.H{"rate": current, "history": rows})
}

func (a *API) SetExchangeRate(c *gin.Context) {
	var req struct {
		Rate int64 `json:"rate" binding:"required,min=1000,max=10000"`
	}
	if !bind(c, &req) {
		return
	}
	before := a.currentRate(companyOf(c))
	row := models.ExchangeRate{CompanyID: companyOf(c), RateRielPerUSD: req.Rate, SetByUserID: middleware.UserIDFrom(c), EffectiveAt: time.Now(), CreatedAt: time.Now()}
	if err := a.DB.Create(&row).Error; err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "settings", "exchange_rate.update", "exchange_rate", row.ID, gin.H{"rate": before}, gin.H{"rate": req.Rate})
	a.GetExchangeRate(c)
}
