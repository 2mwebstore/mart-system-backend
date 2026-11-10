package handlers

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"com-mart/backend/internal/models"
	"com-mart/backend/internal/utils"
)

// ---- Tax rates ----------------------------------------------------------------
//
// A company's tax rates (VAT 10%…), added on top of a POS sale, an invoice, a
// purchase order or an expense. Prices are always before tax. Each of those
// documents keeps a snapshot of the rate it used (see models.TaxSnapshot), so
// changing or deleting a rate never changes one already made. Listing them
// needs no permission: the till and every form pick from them.

type taxRateDTO struct {
	ID      uint64  `json:"id"`
	Name    string  `json:"name"`
	Percent float64 `json:"percent"`
	Active  bool    `json:"active"`
}

func toTaxRateDTO(r models.TaxRate) taxRateDTO {
	return taxRateDTO{ID: r.ID, Name: r.Name, Percent: r.Percent, Active: r.Active}
}

type taxRateReq struct {
	Name    string  `json:"name" binding:"required,max=60"`
	Percent float64 `json:"percent" binding:"min=0,max=100"`
	Active  bool    `json:"active"`
}

// clean trims the name and keeps the percent to the column's two decimals.
func (r *taxRateReq) clean(c *gin.Context) bool {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		invalid(c, "name", "Name is required.")
		return false
	}
	r.Percent = math.Round(r.Percent*100) / 100
	return true
}

func (a *API) ListTaxRates(c *gin.Context) {
	var rows []models.TaxRate
	if err := a.DB.Where("company_id = ?", companyOf(c)).Order("name, id").Find(&rows).Error; err != nil {
		dbFail(c, err)
		return
	}
	out := make([]taxRateDTO, len(rows))
	for i, r := range rows {
		out[i] = toTaxRateDTO(r)
	}
	utils.OK(c, http.StatusOK, out)
}

func (a *API) CreateTaxRate(c *gin.Context) {
	var req taxRateReq
	if !bind(c, &req) || !req.clean(c) {
		return
	}
	r := models.TaxRate{CompanyID: companyOf(c), Name: req.Name, Percent: req.Percent, Active: req.Active}
	if err := a.DB.Create(&r).Error; err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "settings", "tax_rate.create", "tax_rate", r.ID, nil, toTaxRateDTO(r))
	utils.OK(c, http.StatusCreated, toTaxRateDTO(r))
}

func (a *API) UpdateTaxRate(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req taxRateReq
	if !bind(c, &req) || !req.clean(c) {
		return
	}
	var r models.TaxRate
	if err := a.DB.Where("company_id = ?", companyOf(c)).First(&r, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	before := toTaxRateDTO(r)
	r.Name, r.Percent, r.Active = req.Name, req.Percent, req.Active
	if err := a.DB.Save(&r).Error; err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "settings", "tax_rate.update", "tax_rate", r.ID, before, toTaxRateDTO(r))
	utils.OK(c, http.StatusOK, toTaxRateDTO(r))
}

// DeleteTaxRate soft-deletes the rate: documents that used it keep their
// snapshot (and their foreign key). A place that started on it starts with no
// tax from then on.
func (a *API) DeleteTaxRate(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	company := companyOf(c)
	var r models.TaxRate
	if err := a.DB.Where("company_id = ?", company).First(&r, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&r).Error; err != nil {
			return err
		}
		return tx.Model(&models.CompanySetting{}).
			Where("company_id = ? AND `key` IN ? AND value = ?", company, taxDefaultKeys(), strconv.FormatUint(id, 10)).
			Update("value", "").Error
	})
	if err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "settings", "tax_rate.delete", "tax_rate", id, toTaxRateDTO(r), nil)
	utils.OK(c, http.StatusOK, gin.H{"deleted": true})
}

// ---- Where each place starts --------------------------------------------------

// The rate a new POS sale, invoice, purchase order or expense starts with
// (company settings; ” for no tax). Staff can change it on each document.
const (
	settingTaxDefaultPOS      = "tax_default_pos"
	settingTaxDefaultInvoice  = "tax_default_invoice"
	settingTaxDefaultPurchase = "tax_default_purchase"
	settingTaxDefaultExpense  = "tax_default_expense"
)

func taxDefaultKeys() []string {
	return []string{settingTaxDefaultPOS, settingTaxDefaultInvoice, settingTaxDefaultPurchase, settingTaxDefaultExpense}
}

// taxDefaultsDTO is each place's starting rate; null is no tax.
type taxDefaultsDTO struct {
	POS      *uint64 `json:"pos"`
	Invoice  *uint64 `json:"invoice"`
	Purchase *uint64 `json:"purchase"`
	Expense  *uint64 `json:"expense"`
}

// taxDefaults reads the starting rates from a company's settings, keeping
// only those that are still one of its active rates.
func (a *API) taxDefaults(companyID uint64, settings map[string]string) taxDefaultsDTO {
	var active []uint64
	a.DB.Model(&models.TaxRate{}).Where("company_id = ? AND active = ?", companyID, true).Pluck("id", &active)
	usable := map[uint64]bool{}
	for _, id := range active {
		usable[id] = true
	}
	pick := func(key string) *uint64 {
		id, err := strconv.ParseUint(settings[key], 10, 64)
		if err != nil || !usable[id] {
			return nil
		}
		return &id
	}
	return taxDefaultsDTO{
		POS:      pick(settingTaxDefaultPOS),
		Invoice:  pick(settingTaxDefaultInvoice),
		Purchase: pick(settingTaxDefaultPurchase),
		Expense:  pick(settingTaxDefaultExpense),
	}
}

// UpdateTaxDefaults sets every place's starting rate at once (null: no tax).
// Each must be one of the company's active rates.
func (a *API) UpdateTaxDefaults(c *gin.Context) {
	var req taxDefaultsDTO
	if !bind(c, &req) {
		return
	}
	company := companyOf(c)
	values := map[string]string{}
	for _, place := range []struct {
		field, key string
		id         *uint64
	}{
		{"pos", settingTaxDefaultPOS, req.POS},
		{"invoice", settingTaxDefaultInvoice, req.Invoice},
		{"purchase", settingTaxDefaultPurchase, req.Purchase},
		{"expense", settingTaxDefaultExpense, req.Expense},
	} {
		values[place.key] = ""
		if place.id == nil || *place.id == 0 {
			continue
		}
		if _, err := a.taxRateFor(a.DB, company, place.id, models.TaxSnapshot{}); err != nil {
			fail(c, utils.NewValidationError(map[string]string{place.field: "No such tax rate."}))
			return
		}
		values[place.key] = strconv.FormatUint(*place.id, 10)
	}
	before := a.taxDefaults(company, a.companySettings(company, taxDefaultKeys()...))
	if err := saveCompanySettings(a.DB, company, values); err != nil {
		dbFail(c, err)
		return
	}
	after := a.taxDefaults(company, a.companySettings(company, taxDefaultKeys()...))
	a.audit(c, "settings", "tax_rate.defaults", "settings", 0, before, after)
	utils.OK(c, http.StatusOK, after)
}

// ---- Working out a document's tax --------------------------------------------

// taxCents is percent of base, rounded half up to the cent, in whole numbers
// so it never drifts: the percent is taken to hundredths (basis points). The
// admin app's utils/tax.ts does the same sum to show it before saving.
func taxCents(base int64, percent float64) int64 {
	if base <= 0 || percent <= 0 {
		return 0
	}
	bp := int64(math.Round(percent * 100))
	return (base*bp + 5000) / 10000
}

// errNoSuchTaxRate answers a tax_rate_id that isn't one of the company's
// active rates, like any other field naming something that isn't there.
var errNoSuchTaxRate = utils.NewValidationError(map[string]string{"tax_rate_id": "No such tax rate."})

// taxRateFor turns the rate a document asks for into the snapshot it keeps.
// No rate (nil or 0) is no tax. The document's own rate (keep) stays exactly
// as it was even if it has since changed or been switched off, the way an
// edited invoice keeps its lines' prices; any other must be one of the
// company's active rates. The caller works out TaxCents (withTax).
func (a *API) taxRateFor(db *gorm.DB, companyID uint64, id *uint64, keep models.TaxSnapshot) (models.TaxSnapshot, error) {
	if id == nil || *id == 0 {
		return models.TaxSnapshot{}, nil
	}
	if keep.TaxRateID != nil && *keep.TaxRateID == *id {
		keep.TaxCents = 0
		return keep, nil
	}
	var r models.TaxRate
	err := db.Where("company_id = ? AND active = ?", companyID, true).First(&r, *id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.TaxSnapshot{}, errNoSuchTaxRate
	}
	if err != nil {
		return models.TaxSnapshot{}, err
	}
	return models.TaxSnapshot{TaxRateID: &r.ID, TaxName: r.Name, TaxPercent: r.Percent}, nil
}

// withTax sets the snapshot's tax on base and returns it.
func withTax(snap *models.TaxSnapshot, base int64) int64 {
	snap.TaxCents = taxCents(base, snap.TaxPercent)
	return snap.TaxCents
}
