package handlers

// The platform: what a super admin does above the companies. They create,
// rename and disable companies and look after each company's Owner accounts,
// and they run the install-wide tools (backups, production reset). They see
// none of a company's own data: every company route refuses them (see
// middleware.RequireCompany), and these routes refuse everyone else (see
// middleware.RequireSuperAdmin).

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"com-mart/backend/internal/models"
	"com-mart/backend/internal/services"
	"com-mart/backend/internal/utils"
)

var (
	errCompanyCodeTaken = utils.NewAppError(http.StatusUnprocessableEntity, "COMPANY_CODE_TAKEN", "Another company already has that code.")
	companyCodePattern  = regexp.MustCompile(`^[A-Z0-9]{2,12}$`)
)

// activeCompanyByCode finds an active company by its code, in any case.
func (a *API) activeCompanyByCode(code string) (*models.Company, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return nil, services.ErrUnknownCompany
	}
	var company models.Company
	err := a.DB.Where("code = ? AND active = 1", code).Take(&company).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, services.ErrUnknownCompany
	}
	if err != nil {
		return nil, err
	}
	return &company, nil
}

// companyName is a company's own name, "" for none.
func (a *API) companyName(companyID uint64) string {
	var name string
	a.DB.Raw("SELECT name FROM companies WHERE id = ?", companyID).Scan(&name)
	return name
}

type companyOwnerDTO struct {
	ID       uint64 `json:"id"`
	FullName string `json:"full_name"`
	Username string `json:"username"`
	Active   bool   `json:"active"`
}

type companyDTO struct {
	ID     uint64 `json:"id"`
	Name   string `json:"name"`
	Code   string `json:"code"`
	Active bool   `json:"active"`
	// The counts are of branches and user accounts not deleted; the active
	// ones are what its limits count.
	BranchCount       int64 `json:"branch_count"`
	ActiveBranchCount int64 `json:"active_branch_count"`
	MaxBranches       *int  `json:"max_branches"`
	UserCount         int64 `json:"user_count"`
	ActiveUserCount   int64 `json:"active_user_count"`
	MaxUsers          *int  `json:"max_users"`
	// RetentionMonths is how long the company keeps its activity log.
	RetentionMonths int               `json:"retention_months" gorm:"-"`
	Owners          []companyOwnerDTO `json:"owners" gorm:"-"`
	CreatedAt       time.Time         `json:"created_at"`
}

// loadCompanies is every company, or just onlyID, with how many branches and
// staff it has against its limits, how long it keeps its activity log, and
// who its Owners are.
func (a *API) loadCompanies(onlyID uint64) ([]companyDTO, error) {
	q := a.DB.Table("companies co").
		Select(`co.id, co.name, co.code, co.active, co.max_branches, co.max_users, co.created_at,
			(SELECT COUNT(*) FROM branches b WHERE b.company_id = co.id AND b.deleted_at IS NULL) AS branch_count,
			(SELECT COUNT(*) FROM branches b WHERE b.company_id = co.id AND b.deleted_at IS NULL AND b.active = 1) AS active_branch_count,
			(SELECT COUNT(*) FROM users u WHERE u.company_id = co.id AND u.deleted_at IS NULL) AS user_count,
			(SELECT COUNT(*) FROM users u WHERE u.company_id = co.id AND u.deleted_at IS NULL AND u.active = 1) AS active_user_count`).
		Where("co.deleted_at IS NULL").Order("co.id")
	if onlyID != 0 {
		q = q.Where("co.id = ?", onlyID)
	}
	companies := []companyDTO{}
	if err := q.Scan(&companies).Error; err != nil {
		return nil, err
	}
	// A flat struct: GORM skips the fields of an unexported embedded one.
	var owners []struct {
		CompanyID uint64
		ID        uint64
		FullName  string
		Username  string
		Active    bool
	}
	if err := a.DB.Table("users u").
		Select("u.company_id, u.id, u.full_name, u.username, u.active").
		Joins("JOIN roles r ON r.id = u.role_id").
		Where("r.name = ? AND u.company_id IS NOT NULL AND u.deleted_at IS NULL", models.RoleOwner).
		Order("u.id").Scan(&owners).Error; err != nil {
		return nil, err
	}
	byCompany := map[uint64][]companyOwnerDTO{}
	for _, o := range owners {
		byCompany[o.CompanyID] = append(byCompany[o.CompanyID], companyOwnerDTO{ID: o.ID, FullName: o.FullName, Username: o.Username, Active: o.Active})
	}
	for i := range companies {
		companies[i].RetentionMonths = a.Backup.LoadActivityLogRetentionMonths(companies[i].ID)
		companies[i].Owners = byCompany[companies[i].ID]
		if companies[i].Owners == nil {
			companies[i].Owners = []companyOwnerDTO{}
		}
	}
	return companies, nil
}

// ListCompanies is every company, for the super admin.
func (a *API) ListCompanies(c *gin.Context) {
	companies, err := a.loadCompanies(0)
	if err != nil {
		dbFail(c, err)
		return
	}
	utils.OK(c, http.StatusOK, companies)
}

// companyCode reads a company code as it's stored: upper case. It answers
// the client itself when the code isn't 2-12 letters and digits.
func companyCode(c *gin.Context, raw string) (string, bool) {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if !companyCodePattern.MatchString(code) {
		invalid(c, "code", "Use 2 to 12 letters and digits.")
		return "", false
	}
	return code, true
}

// codeTaken reports whether another company (deleted or not) has the code.
func (a *API) codeTaken(code string, exceptID uint64) (bool, error) {
	var n int64
	err := a.DB.Unscoped().Model(&models.Company{}).Where("code = ? AND id <> ?", code, exceptID).Count(&n).Error
	return n > 0, err
}

// ownerPassword checks a new Owner password the way a user's is checked.
func ownerPassword(c *gin.Context, field, password string) bool {
	if len(password) < minPasswordLen {
		invalid(c, field, fmt.Sprintf("Password must be at least %d characters.", minPasswordLen))
		return false
	}
	if len(password) > maxPasswordBytes {
		invalid(c, field, "Password must be at most 72 characters.")
		return false
	}
	return true
}

type createCompanyReq struct {
	Name          string `json:"name" binding:"required,max=120"`
	Code          string `json:"code" binding:"required"`
	OwnerFullName string `json:"owner_full_name" binding:"required,max=120"`
	OwnerUsername string `json:"owner_username" binding:"required,max=60"`
	OwnerPassword string `json:"owner_password" binding:"required"`
	// No limit when left out.
	MaxBranches *int `json:"max_branches" binding:"omitempty,min=1,max=100000"`
	MaxUsers    *int `json:"max_users" binding:"omitempty,min=1,max=100000"`
}

// CreateCompany makes a company ready to use: its default roles, numbering
// and payment methods, and its first Owner, who signs in and sets up the
// branches, tills, staff and catalog.
func (a *API) CreateCompany(c *gin.Context) {
	var req createCompanyReq
	if !bind(c, &req) {
		return
	}
	code, ok := companyCode(c, req.Code)
	if !ok || !ownerPassword(c, "owner_password", req.OwnerPassword) {
		return
	}
	if taken, err := a.codeTaken(code, 0); err != nil {
		dbFail(c, err)
		return
	} else if taken {
		fail(c, errCompanyCodeTaken)
		return
	}
	pw, err := hash(req.OwnerPassword)
	if err != nil {
		dbFail(c, err)
		return
	}
	company := models.Company{Name: strings.TrimSpace(req.Name), Code: code, Active: true, MaxBranches: req.MaxBranches, MaxUsers: req.MaxUsers}
	usernameTaken := false
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&company).Error; err != nil {
			return err
		}
		roles, err := services.SetUpCompany(tx, company.ID)
		if err != nil {
			return err
		}
		owner := models.User{
			CompanyID: &company.ID, FullName: req.OwnerFullName, Username: strings.ToLower(strings.TrimSpace(req.OwnerUsername)),
			RoleID: roles[models.RoleOwner].ID, PasswordHash: pw, Active: true,
		}
		if err := tx.Omit("Role", "Branches").Create(&owner).Error; err != nil {
			usernameTaken = true
			return err
		}
		return nil
	})
	if usernameTaken {
		invalid(c, "owner_username", "That username is already taken.")
		return
	}
	if err != nil {
		dbFail(c, err)
		return
	}
	a.auditIn(c, company.ID, "platform", "company.create", "company", company.ID, nil, gin.H{
		"name": company.Name, "code": company.Code, "owner": req.OwnerUsername, "max_branches": req.MaxBranches, "max_users": req.MaxUsers,
	})
	out, err := a.loadCompanies(company.ID)
	if err != nil || len(out) == 0 {
		dbFail(c, err)
		return
	}
	utils.OK(c, http.StatusCreated, out[0])
}

type updateCompanyReq struct {
	Name   string `json:"name" binding:"required,max=120"`
	Code   string `json:"code" binding:"required"`
	Active bool   `json:"active"`
	// The most active branches and user accounts; null is no limit.
	MaxBranches *int `json:"max_branches" binding:"omitempty,min=1,max=100000"`
	MaxUsers    *int `json:"max_users" binding:"omitempty,min=1,max=100000"`
	// How long the company keeps its activity log. Only the super admin sets
	// it; the company's Owner sees it.
	RetentionMonths int `json:"retention_months" binding:"required,oneof=1 2 3"`
}

// UpdateCompany renames a company, changes its code, turns it on or off,
// sets its limits and how long it keeps its activity log. Every field is the
// new value. Turning it off signs every one of its users out and keeps them
// out: their next request is refused and they can't sign in.
func (a *API) UpdateCompany(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req updateCompanyReq
	if !bind(c, &req) {
		return
	}
	code, ok := companyCode(c, req.Code)
	if !ok {
		return
	}
	var before models.Company
	if err := a.DB.First(&before, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	if taken, err := a.codeTaken(code, id); err != nil {
		dbFail(c, err)
		return
	} else if taken {
		fail(c, errCompanyCodeTaken)
		return
	}
	retentionBefore := a.Backup.LoadActivityLogRetentionMonths(id)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Company{}).Where("id = ?", id).Updates(map[string]interface{}{
			"name": strings.TrimSpace(req.Name), "code": code, "active": req.Active,
			"max_branches": req.MaxBranches, "max_users": req.MaxUsers,
		}).Error; err != nil {
			return err
		}
		if err := services.SaveActivityLogRetentionMonths(tx, id, req.RetentionMonths); err != nil {
			return err
		}
		if before.Active && !req.Active {
			return endCompanySessions(tx, id)
		}
		return nil
	})
	if err != nil {
		dbFail(c, err)
		return
	}
	out, err := a.loadCompanies(id)
	if err != nil || len(out) == 0 {
		dbFail(c, err)
		return
	}
	a.auditIn(c, id, "platform", "company.update", "company", id, gin.H{
		"name": before.Name, "code": before.Code, "active": before.Active,
		"max_branches": before.MaxBranches, "max_users": before.MaxUsers, "retention_months": retentionBefore,
	}, req)
	utils.OK(c, http.StatusOK, out[0])
}

// endCompanySessions signs out everyone who works for a company.
func endCompanySessions(tx *gorm.DB, companyID uint64) error {
	if err := tx.Exec(`UPDATE refresh_tokens SET revoked_at = ? WHERE revoked_at IS NULL
		AND user_id IN (SELECT id FROM users WHERE company_id = ?)`, time.Now(), companyID).Error; err != nil {
		return err
	}
	return tx.Exec("UPDATE users SET token_version = token_version + 1 WHERE company_id = ?", companyID).Error
}

type companyOwnerReq struct {
	// UserID names an Owner of the company whose password is reset. Without
	// it, a new Owner is made from FullName and Username.
	UserID   uint64 `json:"user_id"`
	FullName string `json:"full_name" binding:"max=120"`
	Username string `json:"username" binding:"max=60"`
	Password string `json:"password" binding:"required"`
}

// SetCompanyOwner gives a company another Owner, or resets an Owner's
// password, for when nobody in the company can sign in to do it. A reset
// Owner is turned back on, unlocked and signed out everywhere.
func (a *API) SetCompanyOwner(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req companyOwnerReq
	if !bind(c, &req) || !ownerPassword(c, "password", req.Password) {
		return
	}
	var company models.Company
	if err := a.DB.First(&company, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	var ownerRole models.Role
	if err := a.DB.Where("company_id = ? AND name = ?", id, models.RoleOwner).Take(&ownerRole).Error; err != nil {
		dbFail(c, err)
		return
	}
	pw, err := hash(req.Password)
	if err != nil {
		dbFail(c, err)
		return
	}

	if req.UserID != 0 {
		var owner models.User
		if err := a.DB.Where("id = ? AND company_id = ? AND role_id = ?", req.UserID, id, ownerRole.ID).Take(&owner).Error; err != nil {
			invalid(c, "user_id", "That isn't one of this company's Owners.")
			return
		}
		err := a.DB.Transaction(func(tx *gorm.DB) error {
			// The reset turns the account back on, which takes a place.
			if !owner.Active {
				if err := roomForActive(tx, id, userLimit); err != nil {
					return err
				}
			}
			if err := tx.Model(&models.User{}).Where("id = ?", req.UserID).Updates(map[string]interface{}{"password_hash": pw, "active": true}).Error; err != nil {
				return err
			}
			if err := tx.Where("user_id = ?", req.UserID).Delete(&models.LoginAttempt{}).Error; err != nil {
				return err
			}
			return endSessions(tx, req.UserID)
		})
		if err != nil {
			dbFail(c, err)
			return
		}
		a.auditIn(c, id, "platform", "company.owner_reset", "user", req.UserID, nil, gin.H{"company_id": id})
	} else {
		if strings.TrimSpace(req.FullName) == "" || strings.TrimSpace(req.Username) == "" {
			fields := map[string]string{}
			if strings.TrimSpace(req.FullName) == "" {
				fields["full_name"] = "Full name is required."
			}
			if strings.TrimSpace(req.Username) == "" {
				fields["username"] = "Username is required."
			}
			fail(c, utils.NewValidationError(fields))
			return
		}
		owner := models.User{
			CompanyID: &id, FullName: req.FullName, Username: strings.ToLower(strings.TrimSpace(req.Username)),
			RoleID: ownerRole.ID, PasswordHash: pw, Active: true,
		}
		err := a.DB.Transaction(func(tx *gorm.DB) error {
			if err := roomForActive(tx, id, userLimit); err != nil {
				return err
			}
			return tx.Omit("Role", "Branches").Create(&owner).Error
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
		a.auditIn(c, id, "platform", "company.owner_create", "user", owner.ID, nil, gin.H{"company_id": id, "username": owner.Username})
	}
	out, err := a.loadCompanies(id)
	if err != nil || len(out) == 0 {
		dbFail(c, err)
		return
	}
	utils.OK(c, http.StatusOK, out[0])
}

// companyParam reads the :id of a company that exists (deleted ones don't).
// It answers 404 itself when there isn't one.
func (a *API) companyParam(c *gin.Context) (uint64, bool) {
	id, ok := idParam(c)
	if !ok {
		return 0, false
	}
	if err := a.DB.Select("id").First(&models.Company{}, id).Error; err != nil {
		dbFail(c, err)
		return 0, false
	}
	return id, true
}

// partParam reads the id of one of the company's branches or users, by its
// route parameter name. Another company's is 404, the same as a missing one.
func partParam(c *gin.Context, name string) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil || id == 0 {
		utils.RespondError(c, utils.ErrNotFound)
		return 0, false
	}
	return id, true
}

// ClearCompanyActivityLog purges a company's activity log entries older than
// its retention right away, instead of at tonight's cleanup.
func (a *API) ClearCompanyActivityLog(c *gin.Context) {
	id, ok := a.companyParam(c)
	if !ok {
		return
	}
	n, err := a.Backup.PurgeCompanyActivityLogs(id)
	if err != nil {
		dbFail(c, err)
		return
	}
	a.auditIn(c, id, "platform", "activity_log.clear", "company", id, nil, gin.H{"deleted": n})
	utils.OK(c, http.StatusOK, gin.H{"deleted": n})
}

// ---- A company's branches and user accounts, on and off ---------------------
// The super admin sees each company's branches and accounts and can switch
// them on and off, and nothing else about them: the company runs its own.

type companyBranchDTO struct {
	ID     uint64 `json:"id"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

type companyUserDTO struct {
	ID       uint64 `json:"id"`
	FullName string `json:"full_name"`
	Username string `json:"username"`
	RoleName string `json:"role_name"`
	Active   bool   `json:"active"`
}

type activeReq struct {
	Active *bool `json:"active" binding:"required"`
}

func (a *API) companyBranches(companyID, onlyID uint64) ([]companyBranchDTO, error) {
	q := a.DB.Model(&models.Branch{}).Select("id, code, name, active").Where("company_id = ?", companyID).Order("id")
	if onlyID != 0 {
		q = q.Where("id = ?", onlyID)
	}
	rows := []companyBranchDTO{}
	return rows, q.Scan(&rows).Error
}

func (a *API) companyUsers(companyID, onlyID uint64) ([]companyUserDTO, error) {
	q := a.DB.Table("users u").Select("u.id, u.full_name, u.username, r.name AS role_name, u.active").
		Joins("JOIN roles r ON r.id = u.role_id").
		Where("u.company_id = ? AND u.deleted_at IS NULL", companyID).Order("u.id")
	if onlyID != 0 {
		q = q.Where("u.id = ?", onlyID)
	}
	rows := []companyUserDTO{}
	return rows, q.Scan(&rows).Error
}

// ListCompanyBranches is a company's branches, deleted ones left out.
func (a *API) ListCompanyBranches(c *gin.Context) {
	id, ok := a.companyParam(c)
	if !ok {
		return
	}
	rows, err := a.companyBranches(id, 0)
	if err != nil {
		dbFail(c, err)
		return
	}
	utils.OK(c, http.StatusOK, rows)
}

// SetCompanyBranchActive switches one of a company's branches on or off. On
// takes a place under the company's branch limit.
func (a *API) SetCompanyBranchActive(c *gin.Context) {
	id, ok := a.companyParam(c)
	if !ok {
		return
	}
	branchID, ok := partParam(c, "branch_id")
	if !ok {
		return
	}
	var req activeReq
	if !bind(c, &req) {
		return
	}
	before, err := a.companyBranches(id, branchID)
	if err != nil || len(before) == 0 {
		dbFail(c, gorm.ErrRecordNotFound)
		return
	}
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		if *req.Active && !before[0].Active {
			if err := roomForActive(tx, id, branchLimit); err != nil {
				return err
			}
		}
		return tx.Model(&models.Branch{}).Where("id = ? AND company_id = ?", branchID, id).Update("active", *req.Active).Error
	})
	if err != nil {
		dbFail(c, err)
		return
	}
	after, err := a.companyBranches(id, branchID)
	if err != nil || len(after) == 0 {
		dbFail(c, err)
		return
	}
	a.auditIn(c, id, "platform", "branch.update", "branch", branchID, gin.H{"active": before[0].Active}, gin.H{"active": after[0].Active})
	utils.OK(c, http.StatusOK, after[0])
}

// ListCompanyUsers is a company's user accounts, deleted ones left out.
func (a *API) ListCompanyUsers(c *gin.Context) {
	id, ok := a.companyParam(c)
	if !ok {
		return
	}
	rows, err := a.companyUsers(id, 0)
	if err != nil {
		dbFail(c, err)
		return
	}
	utils.OK(c, http.StatusOK, rows)
}

// SetCompanyUserActive switches one of a company's accounts on or off. On
// takes a place under the company's user limit; off signs the account out
// everywhere, and is refused for the company's only active Owner (the super
// admin adds another Owner first, or turns the whole company off).
func (a *API) SetCompanyUserActive(c *gin.Context) {
	id, ok := a.companyParam(c)
	if !ok {
		return
	}
	userID, ok := partParam(c, "user_id")
	if !ok {
		return
	}
	var req activeReq
	if !bind(c, &req) {
		return
	}
	before, err := a.companyUsers(id, userID)
	if err != nil || len(before) == 0 {
		dbFail(c, gorm.ErrRecordNotFound)
		return
	}
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		switch {
		case *req.Active && !before[0].Active:
			if err := roomForActive(tx, id, userLimit); err != nil {
				return err
			}
		case !*req.Active:
			if err := guardLastOwner(tx, id, userID); err != nil {
				return err
			}
		}
		if err := tx.Model(&models.User{}).Where("id = ? AND company_id = ?", userID, id).Update("active", *req.Active).Error; err != nil {
			return err
		}
		if !*req.Active {
			return endSessions(tx, userID)
		}
		return nil
	})
	if err != nil {
		dbFail(c, err)
		return
	}
	after, err := a.companyUsers(id, userID)
	if err != nil || len(after) == 0 {
		dbFail(c, err)
		return
	}
	a.auditIn(c, id, "platform", "user.update", "user", userID, gin.H{"active": before[0].Active}, gin.H{"active": after[0].Active})
	utils.OK(c, http.StatusOK, after[0])
}
