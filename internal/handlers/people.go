package handlers

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"com-mart/backend/internal/middleware"
	"com-mart/backend/internal/models"
	"com-mart/backend/internal/services"
	"com-mart/backend/internal/utils"
)

// ---- Customers -----------------------------------------------------------------

type customerDTO struct {
	ID        uint64  `json:"id"`
	Name      string  `json:"name"`
	Phone     string  `json:"phone"`
	Tier      string  `json:"tier"`
	Points    int64   `json:"points"`
	LastVisit *string `json:"last_visit"`
}

type customerReq struct {
	Name  string `json:"name" binding:"required,max=120"`
	Phone string `json:"phone" binding:"required,max=30"`
	Tier  string `json:"tier" binding:"omitempty,oneof=MEMBER GOLD"`
}

func (a *API) customerQuery() *gorm.DB {
	return a.DB.Table("customers c").
		Select(`c.id, c.name, c.phone, c.tier, c.points,
			(SELECT DATE_FORMAT(MAX(s.sold_at), '%Y-%m-%d') FROM sales s WHERE s.customer_id = c.id AND s.status = 'PAID') AS last_visit`).
		Where("c.deleted_at IS NULL")
}

// ListCustomers filters (optional): q (name / phone), tier (MEMBER | GOLD).
func (a *API) ListCustomers(c *gin.Context) {
	pg := pagerOf(c)
	base := a.DB.Table("customers c").Where("c.deleted_at IS NULL")
	base = base.Session(&gorm.Session{})
	if search := strings.TrimSpace(c.Query("q")); search != "" {
		base = base.Where("c.name LIKE ? OR c.phone LIKE ?", likeArg(search), likeArg(search))
	}
	if tier := strings.ToUpper(c.Query("tier")); tier != "" {
		base = base.Where("c.tier = ?", tier)
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		dbFail(c, err)
		return
	}
	var rows []customerDTO
	err := pg.apply(base.Select(`c.id, c.name, c.phone, c.tier, c.points,
			(SELECT DATE_FORMAT(MAX(s.sold_at), '%Y-%m-%d') FROM sales s WHERE s.customer_id = c.id AND s.status = 'PAID') AS last_visit`).
		Order("c.id DESC")).Scan(&rows).Error
	if err != nil {
		dbFail(c, err)
		return
	}
	pg.respond(c, rows, total, nil)
}

func (a *API) getCustomer(id uint64) (*customerDTO, error) {
	var rows []customerDTO
	if err := a.customerQuery().Where("c.id = ?", id).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &rows[0], nil
}

func (a *API) CreateCustomer(c *gin.Context) {
	var req customerReq
	if !bind(c, &req) {
		return
	}
	tier := models.CustomerTier(req.Tier)
	if tier == "" {
		tier = models.CustomerMember
	}
	cu := models.Customer{Name: req.Name, Phone: req.Phone, Tier: tier}
	if err := a.DB.Create(&cu).Error; err != nil {
		invalid(c, "phone", "A customer with that phone number already exists.")
		return
	}
	a.audit(c, "customers", "customer.create", "customer", cu.ID, nil, req)
	dto, _ := a.getCustomer(cu.ID)
	utils.OK(c, http.StatusCreated, dto)
}

func (a *API) UpdateCustomer(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req customerReq
	if !bind(c, &req) {
		return
	}
	before, err := a.getCustomer(id)
	if err != nil {
		dbFail(c, err)
		return
	}
	tier := req.Tier
	if tier == "" {
		tier = before.Tier
	}
	if err := a.DB.Model(&models.Customer{}).Where("id = ?", id).Updates(map[string]interface{}{"name": req.Name, "phone": req.Phone, "tier": tier}).Error; err != nil {
		invalid(c, "phone", "A customer with that phone number already exists.")
		return
	}
	after, _ := a.getCustomer(id)
	a.audit(c, "customers", "customer.update", "customer", id, before, after)
	utils.OK(c, http.StatusOK, after)
}

func (a *API) DeleteCustomer(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	before, err := a.getCustomer(id)
	if err != nil {
		dbFail(c, err)
		return
	}
	// Soft delete; the unique phone index would otherwise block reusing the
	// number, so free it up.
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Customer{}).Where("id = ?", id).Update("phone", gorm.Expr("CONCAT(phone, '#deleted-', id)")).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&models.Customer{}).Error
	})
	if err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "customers", "customer.delete", "customer", id, before, nil)
	utils.OK(c, http.StatusOK, gin.H{"deleted": true})
}

// ---- Expenses --------------------------------------------------------------------

type expenseDTO struct {
	ID          uint64 `json:"id"`
	Code        string `json:"code"`
	BranchID    uint64 `json:"branch_id"`
	Category    string `json:"category"`
	AmountCents int64  `json:"amount_cents"`
	ExpenseDate string `json:"expense_date"`
	Note        string `json:"note"`
	User        string `json:"user"`
}

type expenseReq struct {
	BranchID    uint64 `json:"branch_id" binding:"required"`
	Category    string `json:"category" binding:"required,oneof=WAGES RENT ELECTRICITY WATER INTERNET PACKAGING PAYMENT_FEES STOCK_LOSS OTHER"`
	AmountCents int64  `json:"amount_cents" binding:"required,min=1"`
	ExpenseDate string `json:"expense_date" binding:"required"`
	Note        string `json:"note" binding:"max=255"`
}

func (a *API) expenseQuery() *gorm.DB {
	return a.DB.Table("expenses e").
		Select("e.id, e.code, e.branch_id, e.category, e.amount_cents, DATE_FORMAT(e.expense_date,'%Y-%m-%d') AS expense_date, e.note, COALESCE(u.full_name,'') AS user").
		Joins("LEFT JOIN users u ON u.id = e.user_id").
		Where("e.deleted_at IS NULL")
}

// ListExpenses filters (optional): category, date_from / date_to. Meta summary
// carries the total of every matching expense, not just the current page.
func (a *API) ListExpenses(c *gin.Context) {
	branchID := branchParam(c)
	pg := pagerOf(c)
	from, to := dateRange(c)
	if c.Query("date_from") == "" && c.Query("date_to") == "" {
		from = today().AddDate(-1, 0, 0) // the Expenses page has no date filter
	}
	base := a.DB.Table("expenses e").Joins("LEFT JOIN users u ON u.id = e.user_id").
		Where("e.deleted_at IS NULL AND e.branch_id = ? AND e.expense_date >= ? AND e.expense_date < ?", branchID, from, to)
	if cat := c.Query("category"); cat != "" {
		base = base.Where("e.category = ?", cat)
	}
	base = base.Session(&gorm.Session{})
	var agg struct {
		Count      int64
		Total      int64
		Categories int64
	}
	if err := base.Select("COUNT(*) AS count, COALESCE(SUM(e.amount_cents), 0) AS total, COUNT(DISTINCT e.category) AS categories").Scan(&agg).Error; err != nil {
		dbFail(c, err)
		return
	}
	var rows []expenseDTO
	err := pg.apply(base.Select("e.id, e.branch_id, e.category, e.amount_cents, DATE_FORMAT(e.expense_date,'%Y-%m-%d') AS expense_date, e.note, COALESCE(u.full_name,'') AS user").
		Order("e.expense_date DESC, e.id DESC")).Scan(&rows).Error
	if err != nil {
		dbFail(c, err)
		return
	}
	pg.respond(c, rows, agg.Count, gin.H{"total_cents": agg.Total, "categories": agg.Categories})
}

func (a *API) getExpense(id uint64) (*expenseDTO, error) {
	var rows []expenseDTO
	if err := a.expenseQuery().Where("e.id = ?", id).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &rows[0], nil
}

func (a *API) CreateExpense(c *gin.Context) {
	var req expenseReq
	if !bind(c, &req) {
		return
	}
	day, ok := parseDay(req.ExpenseDate)
	if !ok {
		invalid(c, "expense_date", "Use the format YYYY-MM-DD.")
		return
	}
	if !canAccessBranch(c, req.BranchID) {
		fail(c, utils.ErrForbidden)
		return
	}
	e := models.Expense{BranchID: req.BranchID, Category: models.ExpenseCategory(req.Category), AmountCents: req.AmountCents, ExpenseDate: day, Note: req.Note, UserID: middleware.UserIDFrom(c)}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		code, err := nextDocNumber(tx, DocExpense)
		if err != nil {
			return err
		}
		e.Code = code
		return tx.Create(&e).Error
	})
	if err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "expenses", "expense.create", "expense", e.ID, nil, req)
	dto, _ := a.getExpense(e.ID)
	if dto != nil {
		a.Notify.Send(services.AlertExpenseAdded, fmt.Sprintf("💸 <b>Expense added</b>\n%s · %s\n%s", services.HTMLEscape(string(e.Category)), services.FormatUSD(e.AmountCents), services.HTMLEscape(e.Note)))
	}
	utils.OK(c, http.StatusCreated, dto)
}

func (a *API) UpdateExpense(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req expenseReq
	if !bind(c, &req) {
		return
	}
	day, ok := parseDay(req.ExpenseDate)
	if !ok {
		invalid(c, "expense_date", "Use the format YYYY-MM-DD.")
		return
	}
	before, err := a.getExpense(id)
	if err != nil {
		dbFail(c, err)
		return
	}
	if !canAccessBranch(c, before.BranchID) || !canAccessBranch(c, req.BranchID) {
		fail(c, utils.ErrForbidden)
		return
	}
	if err := a.DB.Model(&models.Expense{}).Where("id = ?", id).Updates(map[string]interface{}{
		"branch_id": req.BranchID, "category": req.Category, "amount_cents": req.AmountCents, "expense_date": day, "note": req.Note,
	}).Error; err != nil {
		dbFail(c, err)
		return
	}
	after, _ := a.getExpense(id)
	a.audit(c, "expenses", "expense.update", "expense", id, before, after)
	utils.OK(c, http.StatusOK, after)
}

func (a *API) DeleteExpense(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	before, err := a.getExpense(id)
	if err != nil {
		dbFail(c, err)
		return
	}
	if !canAccessBranch(c, before.BranchID) {
		fail(c, utils.ErrForbidden)
		return
	}
	if err := a.DB.Where("id = ?", id).Delete(&models.Expense{}).Error; err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "expenses", "expense.delete", "expense", id, before, nil)
	utils.OK(c, http.StatusOK, gin.H{"deleted": true})
}

// ---- Users -------------------------------------------------------------------------

type userDTO struct {
	ID           uint64     `json:"id"`
	FullName     string     `json:"full_name"`
	Username     string     `json:"username"`
	Phone        string     `json:"phone"`
	RoleID       uint64     `json:"role_id"`
	RoleName     string     `json:"role_name"`
	BranchIDs    []uint64   `json:"branch_ids" gorm:"-"`
	Active       bool       `json:"active"`
	LastActiveAt *time.Time `json:"last_active_at"`
}

// userFilter narrows the users list (all optional).
type userFilter struct {
	Q      string
	RoleID string
	Branch string
	Active string // "true" | "false"
}

func (a *API) userBase(f userFilter) *gorm.DB {
	q := a.DB.Table("users u").
		Joins("JOIN roles r ON r.id = u.role_id").
		Where("u.deleted_at IS NULL")
	if f.Q != "" {
		q = q.Where("u.full_name LIKE ? OR u.username LIKE ? OR u.phone LIKE ?", likeArg(f.Q), likeArg(f.Q), likeArg(f.Q))
	}
	if f.RoleID != "" {
		q = q.Where("u.role_id = ?", f.RoleID)
	}
	if f.Branch != "" {
		// Owners are implicitly on every branch.
		q = q.Where("(r.name = 'Owner' OR u.id IN (SELECT ub.user_id FROM user_branches ub WHERE ub.branch_id = ?))", f.Branch)
	}
	switch f.Active {
	case "true":
		q = q.Where("u.active = 1")
	case "false":
		q = q.Where("u.active = 0")
	}
	return q
}

// loadUsers returns users (with their branch links) matching f; onlyID != 0
// loads a single user. pg limits the rows; total is the unpaged match count.
func (a *API) loadUsers(onlyID uint64, f userFilter, pg pager) ([]userDTO, int64, error) {
	base := a.userBase(f).Session(&gorm.Session{})
	if onlyID != 0 {
		base = base.Where("u.id = ?", onlyID)
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var users []userDTO
	if err := pg.apply(base.Select("u.id, u.full_name, u.username, u.phone, u.role_id, r.name AS role_name, u.active, u.last_active_at").Order("u.id")).Scan(&users).Error; err != nil {
		return nil, 0, err
	}
	var links []models.UserBranch
	if err := a.DB.Find(&links).Error; err != nil {
		return nil, 0, err
	}
	byUser := map[uint64][]uint64{}
	for _, l := range links {
		byUser[l.UserID] = append(byUser[l.UserID], l.BranchID)
	}
	var allBranches []uint64
	a.DB.Model(&models.Branch{}).Where("active = 1").Order("id").Pluck("id", &allBranches)
	for i := range users {
		ids := byUser[users[i].ID]
		if users[i].RoleName == "Owner" { // owners are implicitly on every branch
			ids = allBranches
		}
		if ids == nil {
			ids = []uint64{}
		}
		users[i].BranchIDs = ids
	}
	return users, total, nil
}

// loadUser loads one user with their branch links.
func (a *API) loadUser(id uint64) ([]userDTO, error) {
	users, _, err := a.loadUsers(id, userFilter{}, pager{})
	return users, err
}

// ListUsers filters (optional): q, role_id, branch_id, active (true | false).
func (a *API) ListUsers(c *gin.Context) {
	pg := pagerOf(c)
	users, total, err := a.loadUsers(0, userFilter{
		Q: strings.TrimSpace(c.Query("q")), RoleID: c.Query("role_id"), Branch: c.Query("branch_id"), Active: c.Query("active"),
	}, pg)
	if err != nil {
		dbFail(c, err)
		return
	}
	pg.respond(c, users, total, nil)
}

type userReq struct {
	FullName  string   `json:"full_name" binding:"required,max=120"`
	Username  string   `json:"username" binding:"required,max=60"`
	Phone     string   `json:"phone" binding:"max=30"`
	RoleID    uint64   `json:"role_id" binding:"required"`
	BranchIDs []uint64 `json:"branch_ids"`
	Active    bool     `json:"active"`
	Password  string   `json:"password"`
	PIN       string   `json:"pin"`
}

func validCredentials(c *gin.Context, req userReq, creating bool) bool {
	if (creating || req.Password != "") && len(req.Password) < 8 {
		invalid(c, "password", "Password must be at least 8 characters.")
		return false
	}
	if req.PIN != "" && (len(req.PIN) != 4 || strings.Trim(req.PIN, "0123456789") != "") {
		invalid(c, "pin", "PIN must be exactly 4 digits.")
		return false
	}
	return true
}

func hash(s string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(s), bcrypt.DefaultCost)
	return string(b), err
}

func (a *API) setUserBranches(tx *gorm.DB, userID uint64, ids []uint64) error {
	if err := tx.Where("user_id = ?", userID).Delete(&models.UserBranch{}).Error; err != nil {
		return err
	}
	seen := map[uint64]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if err := tx.Create(&models.UserBranch{UserID: userID, BranchID: id}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (a *API) CreateUser(c *gin.Context) {
	var req userReq
	if !bind(c, &req) || !validCredentials(c, req, true) {
		return
	}
	pw, err := hash(req.Password)
	if err != nil {
		dbFail(c, err)
		return
	}
	u := models.User{FullName: req.FullName, Username: strings.ToLower(req.Username), Phone: req.Phone, RoleID: req.RoleID, Active: req.Active, PasswordHash: pw}
	if req.PIN != "" {
		ph, _ := hash(req.PIN)
		u.PINHash = &ph
	}
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("Role", "Branches").Create(&u).Error; err != nil {
			return err
		}
		return a.setUserBranches(tx, u.ID, req.BranchIDs)
	})
	if err != nil {
		invalid(c, "username", "That username is already taken.")
		return
	}
	a.audit(c, "users", "user.create", "user", u.ID, nil, gin.H{"username": u.Username, "role_id": u.RoleID})
	out, _ := a.loadUser(u.ID)
	utils.OK(c, http.StatusCreated, out[0])
}

func (a *API) UpdateUser(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req userReq
	if !bind(c, &req) || !validCredentials(c, req, false) {
		return
	}
	if id == middleware.UserIDFrom(c) && !req.Active {
		fail(c, utils.NewAppError(http.StatusConflict, "CANNOT_DISABLE_SELF", "You can't disable your own account."))
		return
	}
	beforeList, err := a.loadUser(id)
	if err != nil || len(beforeList) == 0 {
		dbFail(c, gorm.ErrRecordNotFound)
		return
	}
	updates := map[string]interface{}{"full_name": req.FullName, "username": strings.ToLower(req.Username), "phone": req.Phone, "role_id": req.RoleID, "active": req.Active}
	if req.Password != "" {
		pw, _ := hash(req.Password)
		updates["password_hash"] = pw
	}
	if req.PIN != "" {
		ph, _ := hash(req.PIN)
		updates["pin_hash"] = ph
	}
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.User{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return err
		}
		return a.setUserBranches(tx, id, req.BranchIDs)
	})
	if err != nil {
		invalid(c, "username", "That username is already taken.")
		return
	}
	after, _ := a.loadUser(id)
	a.audit(c, "users", "user.update", "user", id, beforeList[0], after[0])
	utils.OK(c, http.StatusOK, after[0])
}

// userHasActivity reports whether the user has any transactional history —
// sales, shifts, expenses, purchase orders, stock movements, cash movements,
// void/refund approvals or a set exchange rate. Every one of these tables has
// a FOREIGN KEY ... REFERENCES users(id) with no ON DELETE clause (i.e.
// RESTRICT), so a hard delete would fail at the database anyway; this check
// exists to give a clear, translatable reason instead of a raw MySQL error.
func (a *API) userHasActivity(id uint64) (bool, error) {
	checks := []struct{ table, column string }{
		{"sales", "cashier_id"},
		{"shifts", "user_id"},
		{"expenses", "user_id"},
		{"purchase_orders", "user_id"},
		{"stock_movements", "user_id"},
		{"cash_movements", "user_id"},
		{"cash_movements", "approved_by_id"},
		{"voids_refunds", "cashier_id"},
		{"voids_refunds", "approved_by_id"},
		{"exchange_rates", "set_by_user_id"},
	}
	for _, ch := range checks {
		var count int64
		if err := a.DB.Table(ch.table).Where(ch.column+" = ?", id).Count(&count).Error; err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (a *API) DeleteUser(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if id == middleware.UserIDFrom(c) {
		fail(c, utils.NewAppError(http.StatusConflict, "CANNOT_DELETE_SELF", "You can't delete your own account."))
		return
	}
	beforeList, err := a.loadUser(id)
	if err != nil || len(beforeList) == 0 {
		dbFail(c, gorm.ErrRecordNotFound)
		return
	}
	inUse, err := a.userHasActivity(id)
	if err != nil {
		dbFail(c, err)
		return
	}
	if inUse {
		fail(c, utils.NewAppError(http.StatusConflict, "USER_IN_USE", "This user has sales, shifts or other activity and can't be deleted. Deactivate the account instead."))
		return
	}
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", id).Delete(&models.UserBranch{}).Error; err != nil {
			return err
		}
		// Hard delete: the unique index on username would otherwise stop the
		// name being reused after a soft delete.
		return tx.Unscoped().Delete(&models.User{}, id).Error
	})
	if err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "users", "user.delete", "user", id, beforeList[0], nil)
	utils.OK(c, http.StatusOK, gin.H{"deleted": true})
}

// ResetUserPIN issues a fresh random 4-digit PIN and returns it exactly once
// (only its hash is stored).
func (a *API) ResetUserPIN(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		dbFail(c, err)
		return
	}
	pin := fmt.Sprintf("%04d", n.Int64())
	ph, _ := hash(pin)
	res := a.DB.Model(&models.User{}).Where("id = ?", id).Update("pin_hash", ph)
	if res.Error != nil || res.RowsAffected == 0 {
		dbFail(c, gorm.ErrRecordNotFound)
		return
	}
	a.DB.Where("user_id = ?", id).Delete(&models.LoginAttempt{}) // clear any lockout
	a.audit(c, "users", "user.reset_pin", "user", id, nil, nil)
	utils.OK(c, http.StatusOK, gin.H{"pin": pin})
}

// ---- Roles & permissions ---------------------------------------------------------------

type roleDTO struct {
	ID                         uint64   `json:"id"`
	Name                       string   `json:"name"`
	Description                string   `json:"description"`
	IsLocked                   bool     `json:"is_locked"`
	MaxDiscountPercent         int      `json:"max_discount_percent"`
	RefundWithoutApprovalCents int64    `json:"refund_without_approval_cents"`
	Permissions                []string `json:"permissions" gorm:"-"`
	UserCount                  int64    `json:"user_count"`
}

func (a *API) loadRoles(onlyID uint64) ([]roleDTO, error) {
	q := a.DB.Table("roles r").
		Select(`r.id, r.name, r.description, r.is_locked, COALESCE(rl.max_discount_percent,0) AS max_discount_percent,
			COALESCE(rl.refund_without_approval_cents,0) AS refund_without_approval_cents,
			(SELECT COUNT(*) FROM users u WHERE u.role_id = r.id AND u.deleted_at IS NULL) AS user_count`).
		Joins("LEFT JOIN role_limits rl ON rl.role_id = r.id").
		Where("r.deleted_at IS NULL").Order("r.id")
	if onlyID != 0 {
		q = q.Where("r.id = ?", onlyID)
	}
	var roles []roleDTO
	if err := q.Scan(&roles).Error; err != nil {
		return nil, err
	}
	type rp struct {
		RoleID uint64
		Key    string
	}
	var links []rp
	if err := a.DB.Table("role_permissions rp").Select("rp.role_id, p.`key` AS `key`").Joins("JOIN permissions p ON p.id = rp.permission_id").Order("p.id").Scan(&links).Error; err != nil {
		return nil, err
	}
	perms := map[uint64][]string{}
	for _, l := range links {
		perms[l.RoleID] = append(perms[l.RoleID], l.Key)
	}
	for i := range roles {
		roles[i].Permissions = perms[roles[i].ID]
		if roles[i].Permissions == nil {
			roles[i].Permissions = []string{}
		}
	}
	return roles, nil
}

func (a *API) ListRoles(c *gin.Context) {
	roles, err := a.loadRoles(0)
	if err != nil {
		dbFail(c, err)
		return
	}
	utils.OK(c, http.StatusOK, roles)
}

func (a *API) ListPermissions(c *gin.Context) {
	var perms []models.Permission
	if err := a.DB.Order("id").Find(&perms).Error; err != nil {
		dbFail(c, err)
		return
	}
	utils.OK(c, http.StatusOK, perms)
}

type roleReq struct {
	Name                       string   `json:"name" binding:"required,max=60"`
	Description                string   `json:"description" binding:"max=255"`
	Permissions                []string `json:"permissions"`
	MaxDiscountPercent         int      `json:"max_discount_percent" binding:"min=0,max=100"`
	RefundWithoutApprovalCents int64    `json:"refund_without_approval_cents" binding:"min=0"`
}

func (a *API) saveRolePermissions(tx *gorm.DB, roleID uint64, keys []string) error {
	var perms []models.Permission
	if len(keys) > 0 {
		if err := tx.Where("`key` IN ?", keys).Find(&perms).Error; err != nil {
			return err
		}
	}
	if err := tx.Where("role_id = ?", roleID).Delete(&models.RolePermission{}).Error; err != nil {
		return err
	}
	for _, p := range perms {
		if err := tx.Create(&models.RolePermission{RoleID: roleID, PermissionID: p.ID}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (a *API) CreateRole(c *gin.Context) {
	var req roleReq
	if !bind(c, &req) {
		return
	}
	var roleID uint64
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		r := models.Role{Name: req.Name, Description: req.Description}
		if err := tx.Omit("Permissions", "Limits").Create(&r).Error; err != nil {
			return err
		}
		roleID = r.ID
		if err := tx.Create(&models.RoleLimit{RoleID: r.ID, MaxDiscountPercent: req.MaxDiscountPercent, RefundWithoutApprovalCents: req.RefundWithoutApprovalCents}).Error; err != nil {
			return err
		}
		return a.saveRolePermissions(tx, r.ID, req.Permissions)
	})
	if err != nil {
		invalid(c, "name", "A role with that name already exists.")
		return
	}
	a.audit(c, "users", "role.create", "role", roleID, nil, req)
	out, _ := a.loadRoles(roleID)
	utils.OK(c, http.StatusCreated, out[0])
}

func (a *API) UpdateRole(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req roleReq
	if !bind(c, &req) {
		return
	}
	beforeList, _ := a.loadRoles(id)
	if len(beforeList) == 0 {
		dbFail(c, gorm.ErrRecordNotFound)
		return
	}
	if beforeList[0].IsLocked {
		fail(c, utils.NewAppError(http.StatusConflict, "ROLE_LOCKED", "The Owner role is locked and can't be changed."))
		return
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Role{}).Where("id = ?", id).Updates(map[string]interface{}{"name": req.Name, "description": req.Description}).Error; err != nil {
			return err
		}
		limit := models.RoleLimit{RoleID: id, MaxDiscountPercent: req.MaxDiscountPercent, RefundWithoutApprovalCents: req.RefundWithoutApprovalCents}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "role_id"}}, DoUpdates: clause.AssignmentColumns([]string{"max_discount_percent", "refund_without_approval_cents", "updated_at"})}).Create(&limit).Error; err != nil {
			return err
		}
		return a.saveRolePermissions(tx, id, req.Permissions)
	})
	if err != nil {
		invalid(c, "name", "A role with that name already exists.")
		return
	}
	after, _ := a.loadRoles(id)
	a.audit(c, "users", "role.update", "role", id, beforeList[0], after[0])
	utils.OK(c, http.StatusOK, after[0])
}

func (a *API) DeleteRole(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	list, _ := a.loadRoles(id)
	if len(list) == 0 {
		dbFail(c, gorm.ErrRecordNotFound)
		return
	}
	if list[0].IsLocked {
		fail(c, utils.NewAppError(http.StatusConflict, "ROLE_LOCKED", "The Owner role is locked and can't be deleted."))
		return
	}
	if list[0].UserCount > 0 {
		fail(c, utils.NewAppError(http.StatusConflict, "ROLE_IN_USE", "Reassign this role's users before deleting it."))
		return
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		tx.Where("role_id = ?", id).Delete(&models.RolePermission{})
		tx.Where("role_id = ?", id).Delete(&models.RoleLimit{})
		// Hard delete: the unique index on name would otherwise stop the name
		// being reused after a soft delete.
		return tx.Unscoped().Delete(&models.Role{}, id).Error
	})
	if err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "users", "role.delete", "role", id, list[0], nil)
	utils.OK(c, http.StatusOK, gin.H{"deleted": true})
}
