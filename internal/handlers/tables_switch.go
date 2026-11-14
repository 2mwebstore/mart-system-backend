package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"com-mart/backend/internal/models"
	"com-mart/backend/internal/utils"
)

// Whether a company uses tables (dining tables, their QR codes and the orders
// customers send from them, see table_orders.go). The super admin turns it on
// or off on the company; it's on unless turned off, so every company from
// before the switch keeps its tables. Off, the staff table routes answer
// TABLES_OFF and every QR code of the company answers TABLE_NOT_FOUND. The
// tables themselves are kept, so turning it back on brings them back as
// they were.

const settingTables = "tables_enabled"

// tablesOn is a stored switch as the API gives it: off only when set off.
func tablesOn(stored string) bool {
	return stored != "0"
}

func tablesSetting(on bool) string {
	if on {
		return "1"
	}
	return "0"
}

// usesTables is whether a company uses tables.
func (a *API) usesTables(companyID uint64) bool {
	return tablesOn(a.companySettings(companyID, settingTables)[settingTables])
}

var errTablesOff = utils.NewAppError(http.StatusForbidden, "TABLES_OFF", "This company doesn't use tables. The platform administrator can turn them on.")

// errTableOrdersOpen refuses turning tables off while customers' orders are
// still waiting or not yet paid: nobody could see them to finish them.
var errTableOrdersOpen = &utils.AppError{
	Status: http.StatusUnprocessableEntity, Code: "TABLE_ORDERS_OPEN", Message: tableOrdersOpenMsg,
	Fields: map[string]string{"uses_tables": tableOrdersOpenMsg},
}

const tableOrdersOpenMsg = "Tables can't be turned off while a table has orders waiting or not yet paid. The company can take payment for them or cancel them first."

// RequireTables guards the staff table routes: refused when the caller's
// company has tables off.
func (a *API) RequireTables(c *gin.Context) {
	if !a.usesTables(companyOf(c)) {
		fail(c, errTablesOff)
		c.Abort()
		return
	}
	c.Next()
}

// hasOpenTableOrders is whether any of a company's tables has an order
// waiting (NEW) or accepted and not yet paid.
func hasOpenTableOrders(tx *gorm.DB, companyID uint64) (bool, error) {
	var n int64
	err := tx.Table("table_orders o").
		Joins("JOIN branches b ON b.id = o.branch_id").
		Where("b.company_id = ? AND o.status IN ?", companyID, []models.TableOrderStatus{models.TableOrderNew, models.TableOrderAccepted}).
		Count(&n).Error
	return n > 0, err
}
