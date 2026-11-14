package handlers

import (
	"net/http"

	"gorm.io/gorm"

	"com-mart/backend/internal/models"
	"com-mart/backend/internal/utils"
)

// A company's main currency is the one it prices, sells, invoices, spends
// and reports in: US dollars or riel. Every *_cents amount of the company is
// in its minor unit, cents or whole riel; only the till's physical cash
// (*_usd_cents / *_khr_riel) is kept per currency. The super admin sets it on
// the company, and it can't change once there are amounts to misread.

// errMainCurrencyLocked refuses a change of currency once the company has
// amounts stored in the old one.
var errMainCurrencyLocked = &utils.AppError{
	Status: http.StatusUnprocessableEntity, Code: "MAIN_CURRENCY_LOCKED", Message: mainCurrencyLockedMsg,
	Fields: map[string]string{"main_currency": mainCurrencyLockedMsg},
}

const mainCurrencyLockedMsg = "The main currency can't change once the company has products, sales, invoices, purchases, expenses or shifts."

// mainCurrencyOf is a stored main currency as the API gives it: KHR only
// when set, so every company from before it existed stays in dollars.
func mainCurrencyOf(stored string) models.Currency {
	if stored == string(models.CurrencyKHR) {
		return models.CurrencyKHR
	}
	return models.CurrencyUSD
}

// mainCurrency is a company's main currency.
func (a *API) mainCurrency(companyID uint64) models.Currency {
	return mainCurrencyOf(a.companySettings(companyID, settingMainCurrency)[settingMainCurrency])
}

// companiesWithMoney is which of the companies have amounts stored in their
// main currency: a product with a price or cost, or any sale, invoice,
// purchase order, expense or shift, deleted ones included.
func companiesWithMoney(tx *gorm.DB, ids []uint64) (map[uint64]bool, error) {
	out := map[uint64]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	queries := []string{
		`SELECT DISTINCT p.company_id FROM products p JOIN product_prices pp ON pp.product_id = p.id
			WHERE p.company_id IN ? AND (pp.price_cents <> 0 OR pp.cost_cents <> 0)`,
	}
	for _, table := range []string{"sales", "invoices", "purchase_orders", "expenses", "shifts"} {
		queries = append(queries, `SELECT DISTINCT b.company_id FROM `+table+` t JOIN branches b ON b.id = t.branch_id WHERE b.company_id IN ?`)
	}
	for _, q := range queries {
		var found []uint64
		if err := tx.Raw(q, ids).Scan(&found).Error; err != nil {
			return nil, err
		}
		for _, id := range found {
			out[id] = true
		}
	}
	return out, nil
}

// unlimitedRefund is where a refund limit stops being an amount: the Owner's
// 1<<40 means "no limit" in any currency and is never converted.
const unlimitedRefund int64 = 1 << 40

// convertMinor turns an amount from one main currency's minor unit to the
// other's at a rate in riel per dollar: dollars to riel rounded to ៛100,
// riel to dollars rounded to the cent.
func convertMinor(amount int64, from, to models.Currency, rate int64) int64 {
	if from == to || rate <= 0 || amount >= unlimitedRefund {
		return amount
	}
	if to == models.CurrencyKHR {
		return utils.USDCentsToRiel(amount, rate)
	}
	return utils.RoundToNearest(amount*100, rate) / rate
}

// convertRefundLimits turns every role's refund limit of the company into
// the new main currency, the one amount a company has before it sells.
func convertRefundLimits(tx *gorm.DB, companyID uint64, from, to models.Currency, rate int64) error {
	if from == to {
		return nil
	}
	var limits []models.RoleLimit
	if err := tx.Joins("JOIN roles r ON r.id = role_limits.role_id").Where("r.company_id = ?", companyID).Find(&limits).Error; err != nil {
		return err
	}
	for _, l := range limits {
		next := convertMinor(l.RefundWithoutApprovalCents, from, to, rate)
		if next == l.RefundWithoutApprovalCents {
			continue
		}
		if err := tx.Model(&models.RoleLimit{}).Where("role_id = ?", l.RoleID).Update("refund_without_approval_cents", next).Error; err != nil {
			return err
		}
	}
	return nil
}
