package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"com-mart/backend/internal/middleware"
	"com-mart/backend/internal/models"
	"com-mart/backend/internal/utils"
)

type cashMovementDTO struct {
	ID             uint64    `json:"id"`
	Type           string    `json:"type"`
	AmountUSDCents int64     `json:"amount_usd_cents"`
	AmountKHRRiel  int64     `json:"amount_khr_riel"`
	Reason         string    `json:"reason"`
	CreatedAt      time.Time `json:"created_at"`
}

type shiftDTO struct {
	ID              uint64     `json:"id"`
	BranchID        uint64     `json:"branch_id"`
	DeviceID        uint64     `json:"device_id"`
	DeviceKey       string     `json:"device_key"`
	DeviceName      string     `json:"device_name"`
	UserID          uint64     `json:"user_id"`
	CashierName     string     `json:"cashier_name"`
	Status          string     `json:"status"`
	OpenedAt        time.Time  `json:"opened_at"`
	ClosedAt        *time.Time `json:"closed_at"`
	ExchangeRate    int64      `json:"exchange_rate"`
	OpeningUSDCents int64      `json:"opening_usd_cents"`
	OpeningKHRRiel  int64      `json:"opening_khr_riel"`

	// Live (or final) activity on this shift. Sales only count while PAID.
	Transactions    int64 `json:"transactions"`
	ItemsSold       int64 `json:"items_sold"`
	GrossSalesCents int64 `json:"gross_sales_cents"`
	CashSalesCount  int64 `json:"cash_sales_count"`
	CashUSDCents    int64 `json:"cash_usd_cents"`
	CashKHRRiel     int64 `json:"cash_khr_riel"`
	KHQRCents       int64 `json:"khqr_cents"`
	CardCents       int64 `json:"card_cents"`
	VoidedCount     int64 `json:"voided_count"`
	VoidedCents     int64 `json:"voided_cents"`
	PayinUSDCents   int64 `json:"payin_usd_cents"`
	PayinKHRRiel    int64 `json:"payin_khr_riel"`
	PayoutUSDCents  int64 `json:"payout_usd_cents"`
	PayoutKHRRiel   int64 `json:"payout_khr_riel"`

	// Expected drawer contents. Withheld (null) while the shift is open for
	// callers without shift.see_expected — that's what makes it a blind count.
	ExpectedUSDCents *int64 `json:"expected_usd_cents"`
	ExpectedKHRRiel  *int64 `json:"expected_khr_riel"`
	CountedUSDCents  int64  `json:"counted_usd_cents"`
	CountedKHRRiel   int64  `json:"counted_khr_riel"`
	DiffUSDCents     int64  `json:"diff_usd_cents"`
	DiffKHRRiel      int64  `json:"diff_khr_riel"`

	CashMovements []cashMovementDTO `json:"cash_movements" gorm:"-"`
}

// expectedDrawer is opening float + net cash taken (received minus change
// handed back) + pay-ins - pay-outs, per currency.
func expectedDrawer(s *shiftDTO) (usd, khr int64) {
	usd = s.OpeningUSDCents + s.CashUSDCents + s.PayinUSDCents - s.PayoutUSDCents
	khr = s.OpeningKHRRiel + s.CashKHRRiel + s.PayinKHRRiel - s.PayoutKHRRiel
	return
}

// buildShift assembles a shiftDTO with its live totals. It reads with the
// given handle so it can run inside the close transaction.
func buildShift(db *gorm.DB, shiftID uint64) (*shiftDTO, error) {
	var rows []shiftDTO
	err := db.Table("shifts sh").
		Select(`sh.id, sh.branch_id, sh.device_id, d.device_key, d.name AS device_name, sh.user_id, u.full_name AS cashier_name,
			sh.status, sh.opened_at, sh.closed_at, sh.exchange_rate, sh.opening_usd_cents, sh.opening_khr_riel,
			sh.counted_usd_cents, sh.counted_khr_riel, sh.diff_usd_cents, sh.diff_khr_riel`).
		Joins("JOIN devices d ON d.id = sh.device_id").
		Joins("JOIN users u ON u.id = sh.user_id").
		Where("sh.id = ?", shiftID).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	s := rows[0]

	var agg struct {
		Transactions    int64
		GrossSalesCents int64
		VoidedCount     int64
		VoidedCents     int64
	}
	if err := db.Table("sales").Select(`
		COALESCE(SUM(status='PAID'),0) AS transactions,
		COALESCE(SUM(CASE WHEN status='PAID' THEN total_cents END),0) AS gross_sales_cents,
		COALESCE(SUM(status='VOIDED'),0) AS voided_count,
		COALESCE(SUM(CASE WHEN status='VOIDED' THEN total_cents END),0) AS voided_cents`).
		Where("shift_id = ?", shiftID).Scan(&agg).Error; err != nil {
		return nil, err
	}
	s.Transactions, s.GrossSalesCents, s.VoidedCount, s.VoidedCents = agg.Transactions, agg.GrossSalesCents, agg.VoidedCount, agg.VoidedCents

	if err := db.Table("sale_items si").Select("COALESCE(SUM(si.qty),0)").
		Joins("JOIN sales s ON s.id = si.sale_id AND s.status = 'PAID'").Where("s.shift_id = ?", shiftID).Scan(&s.ItemsSold).Error; err != nil {
		return nil, err
	}

	var pay struct {
		CashCount int64
		CashUSD   int64
		CashKHR   int64
		KHQR      int64
		Card      int64
	}
	if err := db.Table("payments p").Select(`
		COALESCE(SUM(p.method='CASH'),0) AS cash_count,
		COALESCE(SUM(CASE WHEN p.method='CASH' THEN p.received_usd_cents - p.change_usd_cents END),0) AS cash_usd,
		COALESCE(SUM(CASE WHEN p.method='CASH' THEN p.received_khr_riel - p.change_khr_riel END),0) AS cash_khr,
		COALESCE(SUM(CASE WHEN p.method='KHQR' THEN p.amount_cents END),0) AS khqr,
		COALESCE(SUM(CASE WHEN p.method='CARD' THEN p.amount_cents END),0) AS card`).
		Joins("JOIN sales s ON s.id = p.sale_id AND s.status = 'PAID'").Where("s.shift_id = ?", shiftID).Scan(&pay).Error; err != nil {
		return nil, err
	}
	s.CashSalesCount, s.CashUSDCents, s.CashKHRRiel, s.KHQRCents, s.CardCents = pay.CashCount, pay.CashUSD, pay.CashKHR, pay.KHQR, pay.Card

	var moves []models.CashMovement
	if err := db.Where("shift_id = ?", shiftID).Order("id DESC").Find(&moves).Error; err != nil {
		return nil, err
	}
	s.CashMovements = make([]cashMovementDTO, len(moves))
	for i, m := range moves {
		s.CashMovements[i] = cashMovementDTO{m.ID, string(m.Type), m.AmountUSDCents, m.AmountKHRRiel, m.Reason, m.CreatedAt}
		switch m.Type {
		case models.CashMovementPayin:
			s.PayinUSDCents += m.AmountUSDCents
			s.PayinKHRRiel += m.AmountKHRRiel
		case models.CashMovementPayout:
			s.PayoutUSDCents += m.AmountUSDCents
			s.PayoutKHRRiel += m.AmountKHRRiel
		}
	}
	usd, khr := expectedDrawer(&s)
	s.ExpectedUSDCents, s.ExpectedKHRRiel = &usd, &khr
	return &s, nil
}

// hideExpectedIfBlind withholds the expected drawer amount from callers who
// may not see it while a shift is still open (blind count).
func hideExpectedIfBlind(c *gin.Context, s *shiftDTO) {
	if s.Status == string(models.ShiftOpen) && !hasPerm(c, "shift.see_expected") {
		s.ExpectedUSDCents, s.ExpectedKHRRiel = nil, nil
	}
}

func (a *API) deviceByKey(c *gin.Context, key string) (*models.Device, bool) {
	var d models.Device
	if err := a.DB.Where("device_key = ? AND active = 1", key).First(&d).Error; err != nil {
		fail(c, utils.NewAppError(http.StatusUnprocessableEntity, "UNKNOWN_DEVICE", "That till isn't registered or is inactive."))
		return nil, false
	}
	if !canAccessBranch(c, d.BranchID) {
		fail(c, utils.ErrForbidden)
		return nil, false
	}
	return &d, true
}

func (a *API) openShiftOn(deviceID uint64) (*models.Shift, error) {
	var s models.Shift
	err := a.DB.Where("device_id = ? AND status = ?", deviceID, models.ShiftOpen).First(&s).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &s, err
}

// GetCurrentShift returns the open shift on a till (?device_key=), or null.
func (a *API) GetCurrentShift(c *gin.Context) {
	d, ok := a.deviceByKey(c, c.Query("device_key"))
	if !ok {
		return
	}
	s, err := a.openShiftOn(d.ID)
	if err != nil {
		dbFail(c, err)
		return
	}
	if s == nil {
		utils.OK(c, http.StatusOK, nil)
		return
	}
	dto, err := buildShift(a.DB, s.ID)
	if err != nil {
		dbFail(c, err)
		return
	}
	hideExpectedIfBlind(c, dto)
	utils.OK(c, http.StatusOK, dto)
}

func (a *API) OpenShift(c *gin.Context) {
	var req struct {
		DeviceKey       string `json:"device_key" binding:"required"`
		OpeningUSDCents int64  `json:"opening_usd_cents" binding:"min=0"`
		OpeningKHRRiel  int64  `json:"opening_khr_riel" binding:"min=0"`
	}
	if !bind(c, &req) {
		return
	}
	d, ok := a.deviceByKey(c, req.DeviceKey)
	if !ok {
		return
	}
	var shiftID uint64
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var existing int64
		tx.Model(&models.Shift{}).Where("device_id = ? AND status = ?", d.ID, models.ShiftOpen).Count(&existing)
		if existing > 0 {
			return utils.NewAppError(http.StatusConflict, "SHIFT_ALREADY_OPEN", "This till already has an open shift.")
		}
		s := models.Shift{
			BranchID: d.BranchID, DeviceID: d.ID, UserID: middleware.UserIDFrom(c), Status: models.ShiftOpen, OpenedAt: time.Now(),
			ExchangeRate: a.currentRate(), OpeningUSDCents: req.OpeningUSDCents, OpeningKHRRiel: req.OpeningKHRRiel,
		}
		if err := tx.Omit("Device", "User").Create(&s).Error; err != nil {
			return err
		}
		shiftID = s.ID
		now := time.Now()
		return tx.Model(&models.Device{}).Where("id = ?", d.ID).Update("last_seen_at", now).Error
	})
	if err != nil {
		if ae, ok := err.(*utils.AppError); ok {
			fail(c, ae)
			return
		}
		dbFail(c, err)
		return
	}
	a.audit(c, "shift", "shift.open", "shift", shiftID, nil, gin.H{"device": d.Name, "opening_usd_cents": req.OpeningUSDCents, "opening_khr_riel": req.OpeningKHRRiel})
	dto, _ := buildShift(a.DB, shiftID)
	hideExpectedIfBlind(c, dto)
	utils.OK(c, http.StatusCreated, dto)
}

// loadOwnedOpenShift loads an open shift the caller may act on: their own,
// or anyone's if they hold shift.close_others.
func (a *API) loadActableShift(c *gin.Context, id uint64) (*models.Shift, bool) {
	var s models.Shift
	if err := a.DB.First(&s, id).Error; err != nil {
		dbFail(c, err)
		return nil, false
	}
	if !canAccessBranch(c, s.BranchID) {
		fail(c, utils.ErrForbidden)
		return nil, false
	}
	if s.UserID != middleware.UserIDFrom(c) && !hasPerm(c, "shift.close_others") {
		fail(c, utils.ErrForbidden)
		return nil, false
	}
	if s.Status != models.ShiftOpen {
		fail(c, utils.NewAppError(http.StatusConflict, "SHIFT_CLOSED", "This shift is already closed."))
		return nil, false
	}
	return &s, true
}

func (a *API) AddCashMovement(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		Type           string `json:"type" binding:"required,oneof=PAYOUT PAYIN"`
		AmountUSDCents int64  `json:"amount_usd_cents" binding:"min=0"`
		AmountKHRRiel  int64  `json:"amount_khr_riel" binding:"min=0"`
		Reason         string `json:"reason" binding:"required,max=255"`
	}
	if !bind(c, &req) {
		return
	}
	if req.AmountUSDCents == 0 && req.AmountKHRRiel == 0 {
		invalid(c, "amount_usd_cents", "Enter an amount in USD or KHR.")
		return
	}
	if _, ok := a.loadActableShift(c, id); !ok {
		return
	}
	m := models.CashMovement{ShiftID: id, Type: models.CashMovementType(req.Type), AmountUSDCents: req.AmountUSDCents, AmountKHRRiel: req.AmountKHRRiel, Reason: req.Reason, UserID: middleware.UserIDFrom(c), CreatedAt: time.Now()}
	if err := a.DB.Create(&m).Error; err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "shift", "cash_movement."+req.Type, "shift", id, nil, req)
	dto, _ := buildShift(a.DB, id)
	hideExpectedIfBlind(c, dto)
	utils.OK(c, http.StatusCreated, dto)
}

func (a *API) CloseShift(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		CountedUSDCents int64  `json:"counted_usd_cents" binding:"min=0"`
		CountedKHRRiel  int64  `json:"counted_khr_riel" binding:"min=0"`
		Note            string `json:"note" binding:"max=255"`
	}
	if !bind(c, &req) {
		return
	}
	if _, ok := a.loadActableShift(c, id); !ok {
		return
	}
	var final *shiftDTO
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		live, err := buildShift(tx, id)
		if err != nil {
			return err
		}
		expUSD, expKHR := expectedDrawer(live)
		now := time.Now()
		if err := tx.Model(&models.Shift{}).Where("id = ?", id).Updates(map[string]interface{}{
			"status": models.ShiftClosed, "closed_at": now,
			"expected_usd_cents": expUSD, "expected_khr_riel": expKHR,
			"counted_usd_cents": req.CountedUSDCents, "counted_khr_riel": req.CountedKHRRiel,
			"diff_usd_cents": req.CountedUSDCents - expUSD, "diff_khr_riel": req.CountedKHRRiel - expKHR, "note": req.Note,
		}).Error; err != nil {
			return err
		}
		final, err = buildShift(tx, id)
		return err
	})
	if err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "shift", "shift.close", "shift", id, nil, gin.H{
		"expected_usd_cents": final.ExpectedUSDCents, "expected_khr_riel": final.ExpectedKHRRiel,
		"counted_usd_cents": final.CountedUSDCents, "counted_khr_riel": final.CountedKHRRiel,
		"diff_usd_cents": final.DiffUSDCents, "diff_khr_riel": final.DiffKHRRiel,
	})
	utils.OK(c, http.StatusOK, final)
}

// ListShifts is the shift history for a branch (open shifts included).
// Optional filters: status (OPEN | CLOSED), device_key (till), user_id
// (cashier), date_from / date_to (when the shift was opened; no dates = all
// time). Meta summary lists the branch's cashiers for the filter dropdown.
func (a *API) ListShifts(c *gin.Context) {
	branchID := branchParam(c)
	pg := pagerOf(c)
	base := a.DB.Model(&models.Shift{}).Where("branch_id = ?", branchID)
	if from, to := rangeBounds(c); from != nil || to != nil {
		if from != nil {
			base = base.Where("opened_at >= ?", *from)
		}
		if to != nil {
			base = base.Where("opened_at < ?", *to)
		}
	}
	if st := strings.ToUpper(c.Query("status")); st != "" {
		base = base.Where("status = ?", st)
	}
	if key := c.Query("device_key"); key != "" {
		base = base.Where("device_id IN (SELECT id FROM devices WHERE device_key = ?)", key)
	}
	if uid := c.Query("user_id"); uid != "" {
		base = base.Where("user_id = ?", uid)
	}
	base = base.Session(&gorm.Session{})
	var total int64
	if err := base.Count(&total).Error; err != nil {
		dbFail(c, err)
		return
	}
	var ids []uint64
	if err := pg.apply(base.Order("opened_at DESC, id DESC")).Pluck("id", &ids).Error; err != nil {
		dbFail(c, err)
		return
	}
	out := make([]*shiftDTO, 0, len(ids))
	for _, id := range ids {
		s, err := buildShift(a.DB, id)
		if err != nil {
			dbFail(c, err)
			return
		}
		s.CashMovements = nil // history rows don't need the movement list
		hideExpectedIfBlind(c, s)
		out = append(out, s)
	}
	var summary gin.H
	if pg.on {
		var cashiers []struct {
			ID   uint64 `json:"id"`
			Name string `json:"name"`
		}
		a.DB.Table("users u").Select("u.id, u.full_name AS name").
			Where("u.id IN (SELECT user_id FROM shifts WHERE branch_id = ?)", branchID).Order("u.full_name").Scan(&cashiers)
		summary = gin.H{"cashiers": cashiers}
	}
	pg.respond(c, out, total, summary)
}
