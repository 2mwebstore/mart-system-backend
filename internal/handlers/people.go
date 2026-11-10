package handlers

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strconv"
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

// customerQuery is the company's customers.
func (a *API) customerQuery(companyID uint64) *gorm.DB {
	return a.DB.Table("customers c").
		Select(`c.id, c.name, c.phone, c.tier, c.points,
			(SELECT DATE_FORMAT(MAX(s.sold_at), '%Y-%m-%d') FROM sales s WHERE s.customer_id = c.id AND s.status = 'PAID') AS last_visit`).
		Where("c.company_id = ? AND c.deleted_at IS NULL", companyID)
}

// ListCustomers filters (optional): q (name / phone), tier (MEMBER | GOLD).
func (a *API) ListCustomers(c *gin.Context) {
	pg := pagerOf(c)
	base := a.DB.Table("customers c").Where("c.company_id = ? AND c.deleted_at IS NULL", companyOf(c))
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

func (a *API) getCustomer(companyID, id uint64) (*customerDTO, error) {
	var rows []customerDTO
	if err := a.customerQuery(companyID).Where("c.id = ?", id).Scan(&rows).Error; err != nil {
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
	cu := models.Customer{CompanyID: companyOf(c), Name: req.Name, Phone: req.Phone, Tier: tier}
	if err := a.DB.Create(&cu).Error; err != nil {
		invalid(c, "phone", "A customer with that phone number already exists.")
		return
	}
	a.audit(c, "customers", "customer.create", "customer", cu.ID, nil, req)
	dto, _ := a.getCustomer(companyOf(c), cu.ID)
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
	before, err := a.getCustomer(companyOf(c), id)
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
	after, _ := a.getCustomer(companyOf(c), id)
	a.audit(c, "customers", "customer.update", "customer", id, before, after)
	utils.OK(c, http.StatusOK, after)
}

func (a *API) DeleteCustomer(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	before, err := a.getCustomer(companyOf(c), id)
	if err != nil {
		dbFail(c, err)
		return
	}
	// A customer with sales or invoices stays with them.
	if a.refuseIfUsed(c, "CUSTOMER_IN_USE", inUseDetails{Entity: "customer", Name: before.Name},
		countWhere("sales", "sales", "customer_id = ?", id),
		countWhere("invoices", "invoices", "customer_id = ?", id),
	) {
		return
	}
	// Soft delete; the unique phone index would otherwise block reusing the
	// number, so free it up.
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Customer{}).Where("id = ?", id).Update("phone", gorm.Expr("CONCAT(phone, '#deleted-', id)")).Error; err != nil {
			return err
		}
		// No longer the POS's default customer, if it was.
		if err := tx.Where("company_id = ? AND `key` = ? AND value = ?", companyOf(c), settingDefaultCustomer, strconv.FormatUint(id, 10)).
			Delete(&models.CompanySetting{}).Error; err != nil {
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
	AmountCents int64  `json:"amount_cents"` // before tax
	// The tax on top of the amount, and the two together: what was paid.
	TaxRateID   *uint64 `json:"tax_rate_id"`
	TaxName     string  `json:"tax_name"`
	TaxPercent  float64 `json:"tax_percent"`
	TaxCents    int64   `json:"tax_cents"`
	TotalCents  int64   `json:"total_cents"`
	ExpenseDate string  `json:"expense_date"`
	Note        string  `json:"note"` // rich text (sanitised HTML)
	User        string  `json:"user"`
}

type expenseReq struct {
	BranchID    uint64 `json:"branch_id" binding:"required"`
	Category    string `json:"category" binding:"required,oneof=WAGES RENT ELECTRICITY WATER INTERNET PACKAGING PAYMENT_FEES STOCK_LOSS OTHER"`
	AmountCents int64  `json:"amount_cents" binding:"required,min=1"`
	ExpenseDate string `json:"expense_date" binding:"required"`
	// No rate (null or 0) is no tax.
	TaxRateID *uint64 `json:"tax_rate_id"`
	Note      string  `json:"note" binding:"max=20000"`
}

const expenseColumns = "e.id, e.code, e.branch_id, e.category, e.amount_cents, e.tax_rate_id, e.tax_name, e.tax_percent, e.tax_cents, e.amount_cents + e.tax_cents AS total_cents, DATE_FORMAT(e.expense_date,'%Y-%m-%d') AS expense_date, e.note, COALESCE(u.full_name,'') AS user"

func (a *API) expenseQuery() *gorm.DB {
	return a.DB.Table("expenses e").
		Select(expenseColumns).
		Joins("LEFT JOIN users u ON u.id = e.user_id").
		Where("e.deleted_at IS NULL")
}

// ListExpenses filters (optional): q (code, note or who recorded it),
// category, date_from / date_to. Meta summary carries the total of every
// matching expense, not just the current page: total_cents is what was paid
// (tax included) and tax_cents the tax in it.
func (a *API) ListExpenses(c *gin.Context) {
	branchID := branchParam(c)
	pg := pagerOf(c)
	// No dates = all history (the Expenses page has no date filter); either
	// side narrows it. A one-year default here hid every older expense.
	base := a.DB.Table("expenses e").Joins("LEFT JOIN users u ON u.id = e.user_id").
		Where("e.deleted_at IS NULL AND e.branch_id = ?", branchID)
	if from, to := rangeBounds(c); from != nil || to != nil {
		if from != nil {
			base = base.Where("e.expense_date >= ?", *from)
		}
		if to != nil {
			base = base.Where("e.expense_date < ?", *to)
		}
	}
	if cat := c.Query("category"); cat != "" {
		base = base.Where("e.category = ?", cat)
	}
	if search := strings.TrimSpace(c.Query("q")); search != "" {
		base = base.Where("e.code LIKE ? OR e.note LIKE ? OR u.full_name LIKE ?", likeArg(search), likeArg(search), likeArg(search))
	}
	base = base.Session(&gorm.Session{})
	var agg struct {
		Count      int64
		Total      int64
		Tax        int64
		Categories int64
	}
	if err := base.Select("COUNT(*) AS count, COALESCE(SUM(e.amount_cents + e.tax_cents), 0) AS total, COALESCE(SUM(e.tax_cents), 0) AS tax, COUNT(DISTINCT e.category) AS categories").Scan(&agg).Error; err != nil {
		dbFail(c, err)
		return
	}
	var rows []expenseDTO
	err := pg.apply(base.Select(expenseColumns).
		Order("e.expense_date DESC, e.id DESC")).Scan(&rows).Error
	if err != nil {
		dbFail(c, err)
		return
	}
	pg.respond(c, rows, agg.Count, gin.H{"total_cents": agg.Total, "tax_cents": agg.Tax, "categories": agg.Categories})
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
	tax, err := a.taxRateFor(a.DB, companyOf(c), req.TaxRateID, models.TaxSnapshot{})
	if err != nil {
		fail(c, err)
		return
	}
	withTax(&tax, req.AmountCents)
	e := models.Expense{BranchID: req.BranchID, Category: models.ExpenseCategory(req.Category), AmountCents: req.AmountCents, TaxSnapshot: tax, ExpenseDate: day, Note: utils.SanitizeRichText(req.Note), UserID: middleware.UserIDFrom(c)}
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		code, err := nextDocNumber(tx, companyOf(c), DocExpense)
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
		amount := services.FormatUSD(e.AmountCents)
		if e.TaxCents > 0 {
			amount = fmt.Sprintf("%s (%s %s)", services.FormatUSD(e.AmountCents+e.TaxCents), services.HTMLEscape(e.TaxName), services.FormatUSD(e.TaxCents))
		}
		a.Notify.Send(companyOf(c), services.AlertExpenseAdded, fmt.Sprintf("💸 <b>Expense added</b>\n%s · %s\n%s", services.HTMLEscape(string(e.Category)), amount, services.HTMLEscape(utils.RichTextPlain(e.Note))))
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
	// Its own rate stays as it was even if since changed or switched off.
	keep := models.TaxSnapshot{TaxRateID: before.TaxRateID, TaxName: before.TaxName, TaxPercent: before.TaxPercent}
	tax, err := a.taxRateFor(a.DB, companyOf(c), req.TaxRateID, keep)
	if err != nil {
		fail(c, err)
		return
	}
	withTax(&tax, req.AmountCents)
	if err := a.DB.Model(&models.Expense{}).Where("id = ?", id).Updates(map[string]interface{}{
		"branch_id": req.BranchID, "category": req.Category, "amount_cents": req.AmountCents, "expense_date": day, "note": utils.SanitizeRichText(req.Note),
		"tax_rate_id": tax.TaxRateID, "tax_name": tax.TaxName, "tax_percent": tax.TaxPercent, "tax_cents": tax.TaxCents,
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
	ID        uint64   `json:"id"`
	FullName  string   `json:"full_name"`
	Username  string   `json:"username"`
	Phone     string   `json:"phone"`
	RoleID    uint64   `json:"role_id"`
	RoleName  string   `json:"role_name"`
	BranchIDs []uint64 `json:"branch_ids" gorm:"-"`
	// BranchRoles are the branches where they hold a role other than their
	// main one (RoleID). Everywhere else in BranchIDs the main role applies.
	BranchRoles []branchRoleDTO `json:"branch_roles" gorm:"-"`
	// HasOtherBranches says they also work at branches left out of BranchIDs
	// because the caller can't see them.
	HasOtherBranches bool       `json:"has_other_branches" gorm:"-"`
	Active           bool       `json:"active"`
	LastActiveAt     *time.Time `json:"last_active_at"`
}

type branchRoleDTO struct {
	BranchID uint64 `json:"branch_id"`
	RoleID   uint64 `json:"role_id"`
	RoleName string `json:"role_name"`
}

// userFilter narrows the users list (all optional but CompanyID).
type userFilter struct {
	// CompanyID is the company whose staff these are. Always set.
	CompanyID uint64
	Q         string
	RoleID    string
	Branch    string
	Active    string // "true" | "false"
	// RoleBranches are where RoleID has to be held: nil for anywhere.
	RoleBranches []uint64
	// Scope limits the result to who the caller may see. Only the list endpoint
	// sets it: the other callers load one user by id and run their own checks.
	Scope func(*gorm.DB) *gorm.DB
}

func (a *API) userBase(f userFilter) *gorm.DB {
	q := a.DB.Table("users u").
		Joins("JOIN roles r ON r.id = u.role_id").
		Where("u.company_id = ? AND u.deleted_at IS NULL", f.CompanyID)
	if f.Scope != nil {
		q = f.Scope(q)
	}
	if f.Q != "" {
		q = q.Where("u.full_name LIKE ? OR u.username LIKE ? OR u.phone LIKE ?", likeArg(f.Q), likeArg(f.Q), likeArg(f.Q))
	}
	if f.RoleID != "" {
		if f.RoleBranches == nil {
			q = q.Where("(u.role_id = ? OR u.id IN (SELECT ub.user_id FROM user_branches ub WHERE ub.role_id = ?))", f.RoleID, f.RoleID)
		} else {
			// Owners hold their role at every branch.
			q = q.Where(`((r.name = ? AND u.role_id = ?) OR EXISTS (SELECT 1 FROM user_branches ub
				WHERE ub.user_id = u.id AND ub.branch_id IN ? AND COALESCE(ub.role_id, u.role_id) = ?))`,
				models.RoleOwner, f.RoleID, f.RoleBranches, f.RoleID)
		}
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
	userIDs := make([]uint64, len(users))
	for i, u := range users {
		userIDs[i] = u.ID
	}
	var links []struct {
		UserID, BranchID uint64
		RoleID           *uint64
		RoleName         *string
	}
	if err := a.DB.Table("user_branches ub").
		Select("ub.user_id, ub.branch_id, ub.role_id, r.name AS role_name").
		Joins("LEFT JOIN roles r ON r.id = ub.role_id").
		Where("ub.user_id IN ?", userIDs).Order("ub.branch_id").
		Scan(&links).Error; err != nil {
		return nil, 0, err
	}
	byUser := map[uint64][]uint64{}
	rolesByUser := map[uint64][]branchRoleDTO{}
	for _, l := range links {
		byUser[l.UserID] = append(byUser[l.UserID], l.BranchID)
		if l.RoleID != nil && l.RoleName != nil {
			rolesByUser[l.UserID] = append(rolesByUser[l.UserID], branchRoleDTO{BranchID: l.BranchID, RoleID: *l.RoleID, RoleName: *l.RoleName})
		}
	}
	var allBranches []uint64
	a.DB.Model(&models.Branch{}).Where("company_id = ? AND active = 1", f.CompanyID).Order("id").Pluck("id", &allBranches)
	for i := range users {
		ids, roles := byUser[users[i].ID], rolesByUser[users[i].ID]
		if users[i].RoleName == models.RoleOwner { // owners are implicitly on every branch of the company, as Owner
			ids, roles = allBranches, nil
		}
		if ids == nil {
			ids = []uint64{}
		}
		if roles == nil {
			roles = []branchRoleDTO{}
		}
		users[i].BranchIDs, users[i].BranchRoles = ids, roles
	}
	return users, total, nil
}

// loadUser loads one of the company's users with their branch links.
func (a *API) loadUser(companyID, id uint64) ([]userDTO, error) {
	users, _, err := a.loadUsers(id, userFilter{CompanyID: companyID}, pager{})
	return users, err
}

// ListUsers filters (optional): q, role_id, branch_id, active (true | false).
// With branch_id, role_id matches the role held at that branch. Anyone but an
// Owner only gets the users they share a branch with, and only sees those
// users at their own branches.
func (a *API) ListUsers(c *gin.Context) {
	actor, err := a.actorFrom(c)
	if err != nil {
		dbFail(c, err)
		return
	}
	f := userFilter{
		CompanyID: companyOf(c),
		Q:         strings.TrimSpace(c.Query("q")), RoleID: c.Query("role_id"), Branch: c.Query("branch_id"), Active: c.Query("active"),
		Scope: actor.scopeUsers,
	}
	if at, err := strconv.ParseUint(f.Branch, 10, 64); err == nil {
		f.RoleBranches = []uint64{at}
	} else if !actor.IsOwner {
		f.RoleBranches = actor.BranchIDs
	}
	pg := pagerOf(c)
	users, total, err := a.loadUsers(0, f, pg)
	if err != nil {
		dbFail(c, err)
		return
	}
	for i := range users {
		actor.trimUser(&users[i])
	}
	pg.respond(c, users, total, nil)
}

// GetUser is one user as ListUsers shows them, for the user's own edit page.
// Someone outside what the caller may list answers 404, like a missing one.
func (a *API) GetUser(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	actor, err := a.actorFrom(c)
	if err != nil {
		dbFail(c, err)
		return
	}
	f := userFilter{CompanyID: companyOf(c), Scope: actor.scopeUsers}
	if !actor.IsOwner {
		f.RoleBranches = actor.BranchIDs
	}
	users, _, err := a.loadUsers(id, f, pager{})
	if err != nil {
		dbFail(c, err)
		return
	}
	if len(users) == 0 {
		dbFail(c, gorm.ErrRecordNotFound)
		return
	}
	actor.trimUser(&users[0])
	utils.OK(c, http.StatusOK, users[0])
}

type userReq struct {
	FullName  string   `json:"full_name" binding:"required,max=120"`
	Username  string   `json:"username" binding:"required,max=60"`
	Phone     string   `json:"phone" binding:"max=30"`
	RoleID    uint64   `json:"role_id" binding:"required"`
	BranchIDs []uint64 `json:"branch_ids"`
	// BranchRoles sets a role other than the main one at some of BranchIDs.
	BranchRoles []branchRoleReq `json:"branch_roles"`
	Active      bool            `json:"active"`
	Password    string          `json:"password"`
	PIN         string          `json:"pin"`
}

type branchRoleReq struct {
	BranchID uint64 `json:"branch_id"`
	RoleID   uint64 `json:"role_id"`
}

// perBranchRoles reads the request's per-branch roles by branch, leaving out
// any that only repeat the main role.
func perBranchRoles(c *gin.Context, req userReq) (map[uint64]uint64, bool) {
	out := map[uint64]uint64{}
	for _, br := range req.BranchRoles {
		if !slices.Contains(req.BranchIDs, br.BranchID) {
			invalid(c, "branch_roles", "Each per-branch role must be for one of the user's branches.")
			return nil, false
		}
		if _, dup := out[br.BranchID]; dup {
			invalid(c, "branch_roles", "Set one role per branch.")
			return nil, false
		}
		out[br.BranchID] = br.RoleID
	}
	for b, r := range out {
		if r == req.RoleID {
			delete(out, b)
		}
	}
	return out, true
}

// assignedRoles looks up the roles a request hands out: the main role and the
// per-branch ones. It answers the client itself when one doesn't exist, or is
// another company's.
func (a *API) assignedRoles(c *gin.Context, mainID uint64, perBranch map[uint64]uint64) (roleDTO, map[uint64]roleDTO, bool) {
	ids := []uint64{mainID}
	for _, id := range perBranch {
		ids = append(ids, id)
	}
	found, err := a.rolesByID(companyOf(c), ids)
	if err != nil {
		dbFail(c, err)
		return roleDTO{}, nil, false
	}
	main, ok := found[mainID]
	if !ok {
		invalid(c, "role_id", "Role is not valid.")
		return roleDTO{}, nil, false
	}
	byBranch := map[uint64]roleDTO{}
	for b, id := range perBranch {
		role, ok := found[id]
		if !ok {
			invalid(c, "branch_roles", "Role is not valid.")
			return roleDTO{}, nil, false
		}
		byBranch[b] = role
	}
	return main, byBranch, true
}

// heldRoles is every role a user of the company holds: their main role and
// per-branch ones.
func (a *API) heldRoles(companyID uint64, u userDTO) ([]roleDTO, error) {
	ids := []uint64{u.RoleID}
	for _, r := range u.BranchRoles {
		ids = append(ids, r.RoleID)
	}
	found, err := a.rolesByID(companyID, ids)
	if err != nil {
		return nil, err
	}
	if _, ok := found[u.RoleID]; !ok {
		return nil, gorm.ErrRecordNotFound
	}
	out := make([]roleDTO, 0, len(found))
	for _, id := range ids {
		if role, ok := found[id]; ok && !slices.ContainsFunc(out, func(r roleDTO) bool { return r.ID == id }) {
			out = append(out, role)
		}
	}
	return out, nil
}

// assignChecks is what a user admin has to pass to hand out these roles at
// these branches.
func assignChecks(actor userActor, req userReq, main roleDTO, perBranch map[uint64]roleDTO) error {
	checks := []error{actor.checkAssignRole(main)}
	for _, role := range perBranch {
		checks = append(checks, actor.checkAssignRole(role))
	}
	checks = append(checks, actor.checkAssignBranches(req.BranchIDs), checkBranchRoles(main, uniqueIDs(req.BranchIDs), perBranch))
	return errors.Join(checks...)
}

func uniqueIDs(ids []uint64) []uint64 {
	out := []uint64{}
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// minPasswordLen is the shortest password a company user (or a company's
// Owner, see platform.go) may have: six characters, so a 6-digit number works.
// Lockout after repeated failures (auth_service.go) is what guards a short one.
// The super admin's own is longer (services.MinSuperAdminPasswordLen).
const minPasswordLen = 6

// maxPasswordBytes is the most bcrypt will hash.
const maxPasswordBytes = 72

func validCredentials(c *gin.Context, req userReq, creating bool) bool {
	if (creating || req.Password != "") && len(req.Password) < minPasswordLen {
		invalid(c, "password", fmt.Sprintf("Password must be at least %d characters.", minPasswordLen))
		return false
	}
	if len(req.Password) > maxPasswordBytes {
		invalid(c, "password", "Password must be at most 72 characters.")
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

// ownBranches refuses branches that aren't the company's, answering 422 on
// branch_ids. Every user admin passes it, Owners included.
func (a *API) ownBranches(c *gin.Context, ids []uint64) bool {
	ids = uniqueIDs(ids)
	if len(ids) == 0 {
		return true
	}
	var n int64
	if err := a.DB.Model(&models.Branch{}).Where("id IN ? AND company_id = ?", ids, companyOf(c)).Count(&n).Error; err != nil {
		dbFail(c, err)
		return false
	}
	if n != int64(len(ids)) {
		invalid(c, "branch_ids", "Branch is not valid.")
		return false
	}
	return true
}

func (a *API) setUserBranches(tx *gorm.DB, userID uint64, ids []uint64, perBranch map[uint64]uint64) error {
	if err := tx.Where("user_id = ?", userID).Delete(&models.UserBranch{}).Error; err != nil {
		return err
	}
	for _, id := range uniqueIDs(ids) {
		link := models.UserBranch{UserID: userID, BranchID: id}
		if roleID, ok := perBranch[id]; ok {
			link.RoleID = &roleID
		}
		if err := tx.Create(&link).Error; err != nil {
			return err
		}
	}
	return nil
}

func (a *API) CreateUser(c *gin.Context) {
	var req userReq
	if !bind(c, &req) || !validCredentials(c, req, true) || !a.ownBranches(c, req.BranchIDs) {
		return
	}
	perBranch, ok := perBranchRoles(c, req)
	if !ok {
		return
	}
	main, perBranchRole, ok := a.assignedRoles(c, req.RoleID, perBranch)
	if !ok {
		return
	}
	actor, err := a.actorFrom(c)
	if err != nil {
		dbFail(c, err)
		return
	}
	if err := assignChecks(actor, req, main, perBranchRole); err != nil {
		fail(c, err)
		return
	}
	pw, err := hash(req.Password)
	if err != nil {
		dbFail(c, err)
		return
	}
	company := companyOf(c)
	u := models.User{CompanyID: &company, FullName: req.FullName, Username: strings.ToLower(req.Username), Phone: req.Phone, RoleID: req.RoleID, Active: req.Active, PasswordHash: pw}
	if req.PIN != "" {
		ph, _ := hash(req.PIN)
		u.PINHash = &ph
	}
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		if u.Active {
			if err := roomForActive(tx, company, userLimit); err != nil {
				return err
			}
		}
		if err := tx.Omit("Role", "Branches").Create(&u).Error; err != nil {
			return err
		}
		return a.setUserBranches(tx, u.ID, req.BranchIDs, perBranch)
	})
	var appErr *utils.AppError
	if errors.As(err, &appErr) {
		fail(c, err)
		return
	}
	if err != nil {
		invalid(c, "username", "That username is already taken.")
		return
	}
	a.audit(c, "users", "user.create", "user", u.ID, nil, gin.H{"username": u.Username, "role_id": u.RoleID, "branch_roles": perBranch})
	out, _ := a.loadUser(companyOf(c), u.ID)
	utils.OK(c, http.StatusCreated, out[0])
}

func (a *API) UpdateUser(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req userReq
	if !bind(c, &req) || !validCredentials(c, req, false) || !a.ownBranches(c, req.BranchIDs) {
		return
	}
	if id == middleware.UserIDFrom(c) && !req.Active {
		fail(c, utils.NewAppError(http.StatusConflict, "CANNOT_DISABLE_SELF", "You can't disable your own account."))
		return
	}
	beforeList, err := a.loadUser(companyOf(c), id)
	if err != nil || len(beforeList) == 0 {
		dbFail(c, gorm.ErrRecordNotFound)
		return
	}
	before := beforeList[0]
	currentRoles, err := a.heldRoles(companyOf(c), before)
	if err != nil {
		dbFail(c, err)
		return
	}
	perBranch, ok := perBranchRoles(c, req)
	if !ok {
		return
	}
	newRole, perBranchRole, ok := a.assignedRoles(c, req.RoleID, perBranch)
	if !ok {
		return
	}
	actor, err := a.actorFrom(c)
	if err != nil {
		dbFail(c, err)
		return
	}
	if err := errors.Join(
		actor.checkManageUser(before, currentRoles),
		actor.checkSelfEdit(before, req, perBranch),
		assignChecks(actor, req, newRole, perBranchRole),
	); err != nil {
		fail(c, err)
		return
	}
	// New credentials or a disabled account end what's already signed in.
	signsOut := req.Password != "" || req.PIN != "" || !req.Active
	removesOwner := !req.Active || newRole.Name != models.RoleOwner
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
		if req.Active && !before.Active {
			if err := roomForActive(tx, companyOf(c), userLimit); err != nil {
				return err
			}
		}
		if removesOwner {
			if err := guardLastOwner(tx, companyOf(c), id); err != nil {
				return err
			}
		}
		if err := tx.Model(&models.User{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return err
		}
		if err := a.setUserBranches(tx, id, req.BranchIDs, perBranch); err != nil {
			return err
		}
		if signsOut {
			return endSessions(tx, id)
		}
		return nil
	})
	var appErr *utils.AppError
	if errors.As(err, &appErr) {
		fail(c, err)
		return
	}
	if err != nil {
		invalid(c, "username", "That username is already taken.")
		return
	}
	after, _ := a.loadUser(companyOf(c), id)
	a.audit(c, "users", "user.update", "user", id, before, after[0])
	utils.OK(c, http.StatusOK, after[0])
}

// userUses is everything that records who did it. A user is deleted for good
// (the username can then be used again), and every one of these tables points
// at users(id) with no ON DELETE clause, so the database would refuse anyway;
// counting them first says why. Soft-deleted expenses count too: their rows
// still point at the user.
func userUses(id uint64) []usage {
	return []usage{
		countWhere("sales", "sales", "cashier_id = ?", id),
		countWhere("shifts", "shifts", "user_id = ?", id),
		countWhere("invoices", "invoices", "user_id = ?", id),
		countWhere("invoice_payments", "invoice_payments", "user_id = ?", id),
		countWhere("purchase_orders", "purchase_orders", "user_id = ?", id),
		countWhere("expenses", "expenses", "user_id = ?", id),
		countWhere("stock_moves", "stock_movements", "user_id = ?", id),
		countWhere("stock_transfers", "stock_transfers", "user_id = ?", id),
		countWhere("stock_counts", "stock_counts", "user_id = ?", id),
		countWhere("cash_movements", "cash_movements", "user_id = ? OR approved_by_id = ?", id, id),
		countWhere("voids_refunds", "voids_refunds", "cashier_id = ? OR approved_by_id = ?", id, id),
		countWhere("exchange_rates", "exchange_rates", "set_by_user_id = ?", id),
		countWhere("table_orders", "table_orders", "handled_by = ?", id),
		countWhere("activity_log", "activity_logs", "user_id = ?", id),
	}
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
	beforeList, err := a.loadUser(companyOf(c), id)
	if err != nil || len(beforeList) == 0 {
		dbFail(c, gorm.ErrRecordNotFound)
		return
	}
	currentRoles, err := a.heldRoles(companyOf(c), beforeList[0])
	if err != nil {
		dbFail(c, err)
		return
	}
	actor, err := a.actorFrom(c)
	if err != nil {
		dbFail(c, err)
		return
	}
	if err := actor.checkManageUser(beforeList[0], currentRoles); err != nil {
		fail(c, err)
		return
	}
	if a.refuseIfUsed(c, "USER_IN_USE", inUseDetails{Entity: "user", Name: beforeList[0].FullName, Hint: hintSwitchOff}, userUses(id)...) {
		return
	}
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		if err := guardLastOwner(tx, companyOf(c), id); err != nil {
			return err
		}
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
	users, err := a.loadUser(companyOf(c), id)
	if err != nil || len(users) == 0 {
		dbFail(c, gorm.ErrRecordNotFound)
		return
	}
	currentRoles, err := a.heldRoles(companyOf(c), users[0])
	if err != nil {
		dbFail(c, err)
		return
	}
	actor, err := a.actorFrom(c)
	if err != nil {
		dbFail(c, err)
		return
	}
	if err := actor.checkManageUser(users[0], currentRoles); err != nil {
		fail(c, err)
		return
	}
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		dbFail(c, err)
		return
	}
	pin := fmt.Sprintf("%04d", n.Int64())
	ph, err := hash(pin)
	if err != nil {
		dbFail(c, err)
		return
	}
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.User{}).Where("id = ?", id).Update("pin_hash", ph).Error; err != nil {
			return err
		}
		// Clears any lockout, including the daily PIN limit.
		if err := tx.Where("user_id = ?", id).Delete(&models.LoginAttempt{}).Error; err != nil {
			return err
		}
		return endSessions(tx, id)
	})
	if err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "users", "user.reset_pin", "user", id, nil, nil)
	utils.OK(c, http.StatusOK, gin.H{"pin": pin})
}

// ---- Roles & permissions ---------------------------------------------------------------

type roleDTO struct {
	ID          uint64 `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// BranchID is the branch the role belongs to, nil when it's shared.
	BranchID                   *uint64  `json:"branch_id"`
	IsLocked                   bool     `json:"is_locked"`
	MaxDiscountPercent         int      `json:"max_discount_percent"`
	RefundWithoutApprovalCents int64    `json:"refund_without_approval_cents"`
	Permissions                []string `json:"permissions" gorm:"-"`
	UserCount                  int64    `json:"user_count"`
}

// roleQuery picks which of a company's roles to load.
type roleQuery struct {
	CompanyID uint64   // always set
	IDs       []uint64 // only these, nil for all
	// When Scoped, only shared roles and those of Branches, and user_count
	// only counts who holds the role at one of Branches. Otherwise user_count
	// is everyone who holds it anywhere, which is what deleting it checks.
	Branches []uint64
	Scoped   bool
}

func (a *API) queryRoles(rq roleQuery) ([]roleDTO, error) {
	count := `(SELECT COUNT(*) FROM users u WHERE u.deleted_at IS NULL AND (u.role_id = r.id
			OR EXISTS (SELECT 1 FROM user_branches ub WHERE ub.user_id = u.id AND ub.role_id = r.id))) AS user_count`
	var args []interface{}
	if rq.Scoped {
		// Owners hold their role at every branch.
		count = `(SELECT COUNT(*) FROM users u WHERE u.deleted_at IS NULL AND ((r.name = ? AND u.role_id = r.id)
			OR EXISTS (SELECT 1 FROM user_branches ub WHERE ub.user_id = u.id AND ub.branch_id IN ? AND COALESCE(ub.role_id, u.role_id) = r.id))) AS user_count`
		args = []interface{}{models.RoleOwner, rq.Branches}
	}
	q := a.DB.Table("roles r").
		Select(`r.id, r.name, r.description, r.branch_id, r.is_locked, COALESCE(rl.max_discount_percent,0) AS max_discount_percent,
			COALESCE(rl.refund_without_approval_cents,0) AS refund_without_approval_cents, `+count, args...).
		Joins("LEFT JOIN role_limits rl ON rl.role_id = r.id").
		Where("r.company_id = ? AND r.deleted_at IS NULL", rq.CompanyID).Order("r.id")
	if rq.IDs != nil {
		q = q.Where("r.id IN ?", rq.IDs)
	}
	if rq.Scoped {
		q = q.Where("(r.branch_id IS NULL OR r.branch_id IN ?)", rq.Branches)
	}
	var roles []roleDTO
	if err := q.Scan(&roles).Error; err != nil {
		return nil, err
	}
	roleIDs := make([]uint64, len(roles))
	for i, r := range roles {
		roleIDs[i] = r.ID
	}
	type rp struct {
		RoleID uint64
		Key    string
	}
	var links []rp
	if err := a.DB.Table("role_permissions rp").Select("rp.role_id, p.`key` AS `key`").Joins("JOIN permissions p ON p.id = rp.permission_id").
		Where("rp.role_id IN ?", roleIDs).Order("p.id").Scan(&links).Error; err != nil {
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

// loadRoles loads every role of the company, or just onlyID, with everyone
// who holds it.
func (a *API) loadRoles(companyID, onlyID uint64) ([]roleDTO, error) {
	rq := roleQuery{CompanyID: companyID}
	if onlyID != 0 {
		rq.IDs = []uint64{onlyID}
	}
	return a.queryRoles(rq)
}

// rolesByID loads the company's roles with these ids that exist.
func (a *API) rolesByID(companyID uint64, ids []uint64) (map[uint64]roleDTO, error) {
	roles, err := a.queryRoles(roleQuery{CompanyID: companyID, IDs: ids})
	if err != nil {
		return nil, err
	}
	out := make(map[uint64]roleDTO, len(roles))
	for _, r := range roles {
		out[r.ID] = r
	}
	return out, nil
}

// ListRoles: the shared roles and those of the caller's branches, with how
// many people hold each at those branches. With ?branch_id=, that branch's
// roles and people only. An Owner who names no branch gets every role.
func (a *API) ListRoles(c *gin.Context) {
	actor, err := a.actorFrom(c)
	if err != nil {
		dbFail(c, err)
		return
	}
	at, _ := strconv.ParseUint(c.Query("branch_id"), 10, 64)
	branches, scoped := actor.roleBranches(at)
	roles, err := a.queryRoles(roleQuery{CompanyID: companyOf(c), Branches: branches, Scoped: scoped})
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
	Name        string `json:"name" binding:"required,max=60"`
	Description string `json:"description" binding:"max=255"`
	// BranchID is the branch a new role is for; nil makes it shared (Owner
	// only). It can't be changed afterwards, so an update ignores it.
	BranchID                   *uint64  `json:"branch_id"`
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
	actor, err := a.actorFrom(c)
	if err != nil {
		dbFail(c, err)
		return
	}
	if err := errors.Join(
		actor.checkNewRoleBranch(req.BranchID),
		actor.checkRolePermissions(req.Permissions),
		actor.checkRoleLimits(req.MaxDiscountPercent, req.RefundWithoutApprovalCents),
	); err != nil {
		fail(c, err)
		return
	}
	if req.BranchID != nil {
		var n int64
		if err := a.DB.Model(&models.Branch{}).Where("id = ? AND company_id = ?", *req.BranchID, companyOf(c)).Count(&n).Error; err != nil {
			dbFail(c, err)
			return
		}
		if n == 0 {
			invalid(c, "branch_id", "Branch is not valid.")
			return
		}
	}
	var roleID uint64
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		company := companyOf(c)
		r := models.Role{CompanyID: &company, Name: req.Name, Description: req.Description, BranchID: req.BranchID}
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
	out, _ := a.loadRoles(companyOf(c), roleID)
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
	beforeList, _ := a.loadRoles(companyOf(c), id)
	if len(beforeList) == 0 {
		dbFail(c, gorm.ErrRecordNotFound)
		return
	}
	if beforeList[0].IsLocked {
		fail(c, utils.NewAppError(http.StatusConflict, "ROLE_LOCKED", "The Owner role is locked and can't be changed."))
		return
	}
	actor, err := a.actorFrom(c)
	if err != nil {
		dbFail(c, err)
		return
	}
	if err := errors.Join(
		actor.checkRoleScope(beforeList[0]),
		actor.checkEditRole(beforeList[0]),
		actor.checkRolePermissions(req.Permissions),
		actor.checkRoleLimits(req.MaxDiscountPercent, req.RefundWithoutApprovalCents),
	); err != nil {
		fail(c, err)
		return
	}
	err = a.DB.Transaction(func(tx *gorm.DB) error {
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
	after, _ := a.loadRoles(companyOf(c), id)
	a.audit(c, "users", "role.update", "role", id, beforeList[0], after[0])
	utils.OK(c, http.StatusOK, after[0])
}

func (a *API) DeleteRole(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	list, _ := a.loadRoles(companyOf(c), id)
	if len(list) == 0 {
		dbFail(c, gorm.ErrRecordNotFound)
		return
	}
	if list[0].IsLocked {
		fail(c, utils.NewAppError(http.StatusConflict, "ROLE_LOCKED", "The Owner role is locked and can't be deleted."))
		return
	}
	actor, err := a.actorFrom(c)
	if err != nil {
		dbFail(c, err)
		return
	}
	if err := errors.Join(actor.checkRoleScope(list[0]), actor.checkEditRole(list[0])); err != nil {
		fail(c, err)
		return
	}
	// Counts everyone who holds it, at any branch.
	if list[0].UserCount > 0 {
		fail(c, inUseError("ROLE_IN_USE", inUseDetails{
			Entity: "role", Name: list[0].Name, Hint: hintReassignUsers,
			Uses: []use{{"staff", int64(list[0].UserCount)}},
		}))
		return
	}
	err = a.DB.Transaction(func(tx *gorm.DB) error {
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
