package handlers

import (
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
	q := a.DB.Order("id")
	// branch.manage sees every branch (it's the branch admin screen);
	// everyone else only sees the branches they're assigned to.
	if !hasPerm(c, "branch.manage") {
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
	b := models.Branch{Code: strings.ToUpper(req.Code), Name: req.Name, Address: req.Address, Phone: req.Phone, ReceiptFooter: req.ReceiptFooter, Active: req.Active}
	if err := a.DB.Create(&b).Error; err != nil {
		invalid(c, "code", "That branch code is already in use.")
		return
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
	if err := a.DB.First(&b, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	before := toBranchDTO(b)
	b.Code, b.Name, b.Address, b.Phone, b.ReceiptFooter, b.Active = strings.ToUpper(req.Code), req.Name, req.Address, req.Phone, req.ReceiptFooter, req.Active
	if err := a.DB.Save(&b).Error; err != nil {
		invalid(c, "code", "That branch code is already in use.")
		return
	}
	a.audit(c, "settings", "branch.update", "branch", b.ID, before, toBranchDTO(b))
	utils.OK(c, http.StatusOK, toBranchDTO(b))
}

func (a *API) DeleteBranch(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var b models.Branch
	if err := a.DB.First(&b, id).Error; err != nil {
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
func (a *API) PublicTills(c *gin.Context) {
	var rows []models.Device
	if err := a.DB.Where("active = 1").Order("id").Find(&rows).Error; err != nil {
		dbFail(c, err)
		return
	}
	out := make([]gin.H, len(rows))
	for i, d := range rows {
		out[i] = gin.H{"device_key": d.DeviceKey, "name": d.Name, "branch_id": d.BranchID}
	}
	utils.OK(c, http.StatusOK, out)
}

func (a *API) ListDevices(c *gin.Context) {
	var rows []models.Device
	if err := a.DB.Order("id").Find(&rows).Error; err != nil {
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
	if !bind(c, &req) {
		return
	}
	d := models.Device{BranchID: req.BranchID, Name: req.Name, DeviceKey: req.DeviceKey, Active: req.Active}
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
	if !bind(c, &req) {
		return
	}
	var d models.Device
	if err := a.DB.First(&d, id).Error; err != nil {
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
	if err := a.DB.First(&d, id).Error; err != nil {
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
	if err := a.DB.Order("id").Find(&rows).Error; err != nil {
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
	p := models.PaymentMethodRow{Name: req.Name, Type: req.Type, FeePercent: req.FeePercent, Enabled: req.Enabled}
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
	if err := a.DB.First(&p, id).Error; err != nil {
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
	if err := a.DB.First(&p, id).Error; err != nil {
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
}

const (
	settingReceiptHeader = "receipt_header"
	settingReceiptFooter = "receipt_footer"
	settingLoyalty       = "loyalty_points_per_usd"
)

func (a *API) loadSettings() settingsDTO {
	var rows []models.Setting
	a.DB.Find(&rows)
	m := map[string]string{}
	for _, r := range rows {
		m[r.Key] = r.Value
	}
	loyalty, err := strconv.ParseFloat(m[settingLoyalty], 64)
	if err != nil {
		loyalty = 1
	}
	header := m[settingReceiptHeader]
	if header == "" {
		header = "Com Mart"
	}
	return settingsDTO{ReceiptHeader: header, ReceiptFooter: m[settingReceiptFooter], LoyaltyPointsPerUSD: loyalty}
}

func (a *API) GetSettings(c *gin.Context) {
	utils.OK(c, http.StatusOK, a.loadSettings())
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
	before := a.loadSettings()
	pairs := map[string]string{
		settingReceiptHeader: req.ReceiptHeader,
		settingReceiptFooter: req.ReceiptFooter,
		settingLoyalty:       strconv.FormatFloat(req.LoyaltyPointsPerUSD, 'f', -1, 64),
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		for k, v := range pairs {
			row := models.Setting{Key: k, Value: v}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"})}).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "settings", "settings.update", "settings", 0, before, req)
	utils.OK(c, http.StatusOK, a.loadSettings())
}

// ---- Exchange rate ----------------------------------------------------------

type exchangeRateDTO struct {
	ID          uint64    `json:"id"`
	Rate        int64     `json:"rate"`
	SetBy       string    `json:"set_by"`
	EffectiveAt time.Time `json:"effective_at"`
}

func (a *API) currentRate() int64 {
	var r models.ExchangeRate
	if err := a.DB.Order("effective_at DESC, id DESC").First(&r).Error; err != nil {
		return 4100
	}
	return r.RateRielPerUSD
}

func (a *API) GetExchangeRate(c *gin.Context) {
	var rows []exchangeRateDTO
	err := a.DB.Table("exchange_rates er").
		Select("er.id, er.rate_riel_per_usd AS rate, COALESCE(u.full_name,'') AS set_by, er.effective_at").
		Joins("LEFT JOIN users u ON u.id = er.set_by_user_id").
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
	before := a.currentRate()
	row := models.ExchangeRate{RateRielPerUSD: req.Rate, SetByUserID: middleware.UserIDFrom(c), EffectiveAt: time.Now(), CreatedAt: time.Now()}
	if err := a.DB.Create(&row).Error; err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "settings", "exchange_rate.update", "exchange_rate", row.ID, gin.H{"rate": before}, gin.H{"rate": req.Rate})
	a.GetExchangeRate(c)
}
