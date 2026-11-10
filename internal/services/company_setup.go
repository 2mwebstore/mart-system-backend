package services

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"com-mart/backend/internal/models"
)

// EnsurePermissions adds every permission the app checks that the database
// doesn't have yet. Existing rows are left as they are.
func EnsurePermissions(tx *gorm.DB) error {
	for _, p := range models.PermissionDefs {
		row := p
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

// SetUpCompany gives a company what it needs before anyone can work in it:
// the default roles with their permissions and limits, its own document
// numbering and its payment methods. Running it again changes nothing but the
// default roles' permissions, which are set back to the templates'. It
// returns the company's roles by name.
func SetUpCompany(tx *gorm.DB, companyID uint64) (map[string]models.Role, error) {
	if err := EnsurePermissions(tx); err != nil {
		return nil, err
	}
	var perms []models.Permission
	if err := tx.Order("id").Find(&perms).Error; err != nil {
		return nil, err
	}
	byKey := make(map[string]models.Permission, len(perms))
	for _, p := range perms {
		byKey[p.Key] = p
	}

	roles := make(map[string]models.Role, len(models.DefaultRoles))
	for _, t := range models.DefaultRoles {
		var role models.Role
		err := tx.Where("company_id = ? AND name = ?", companyID, t.Name).Take(&role).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			company := companyID
			role = models.Role{CompanyID: &company, Name: t.Name, Description: t.Description, IsLocked: t.Locked}
			if err := tx.Omit("Permissions", "Limits").Create(&role).Error; err != nil {
				return nil, err
			}
			limit := models.RoleLimit{RoleID: role.ID, MaxDiscountPercent: t.MaxDiscountPercent, RefundWithoutApprovalCents: t.RefundWithoutApprovalCents}
			if err := tx.Create(&limit).Error; err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		}
		granted := perms
		if !t.AllPermissions {
			granted = make([]models.Permission, 0, len(t.Permissions))
			for _, key := range t.Permissions {
				granted = append(granted, byKey[key])
			}
		}
		if err := tx.Where("role_id = ?", role.ID).Delete(&models.RolePermission{}).Error; err != nil {
			return nil, err
		}
		for _, p := range granted {
			if err := tx.Create(&models.RolePermission{RoleID: role.ID, PermissionID: p.ID}).Error; err != nil {
				return nil, err
			}
		}
		roles[t.Name] = role
	}

	for docType, prefix := range models.DefaultNumberSequences {
		seq := models.NumberSequence{CompanyID: companyID, DocType: docType, Prefix: prefix, NextNumber: 1}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seq).Error; err != nil {
			return nil, err
		}
	}

	var methods int64
	if err := tx.Model(&models.PaymentMethodRow{}).Where("company_id = ?", companyID).Count(&methods).Error; err != nil {
		return nil, err
	}
	if methods == 0 {
		for _, m := range models.DefaultPaymentMethods {
			row := m
			row.CompanyID = companyID
			if err := tx.Create(&row).Error; err != nil {
				return nil, err
			}
		}
	}
	return roles, nil
}

// MinSuperAdminPasswordLen is the shortest password a super admin account
// is given from the command line or the production seed.
const MinSuperAdminPasswordLen = 12

// SuperAdminRole is the one role a super admin holds: no company, no
// permissions. Migration 000030 adds it.
func SuperAdminRole(tx *gorm.DB) (models.Role, error) {
	var role models.Role
	err := tx.Where("company_id IS NULL AND name = ?", models.RoleSuperAdmin).Take(&role).Error
	return role, err
}

// ErrNotASuperAdmin is returned when the username belongs to a company's
// account, which is never turned into a super admin.
var ErrNotASuperAdmin = errors.New("that username belongs to a company's account, not a super admin")

// UpsertSuperAdmin makes sure a super admin with this username exists. A new
// one gets the password. An existing one keeps theirs unless reset is set:
// then the password is replaced, the account turned back on and unlocked,
// and every session it had is ended. It reports whether the account is new.
func UpsertSuperAdmin(db *gorm.DB, username, fullName, password string, reset bool) (bool, error) {
	if len(password) < MinSuperAdminPasswordLen {
		return false, fmt.Errorf("the password must be at least %d characters", MinSuperAdminPasswordLen)
	}
	if len(password) > 72 {
		return false, errors.New("the password must be at most 72 characters")
	}
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return false, errors.New("the username is empty")
	}
	created := false
	err := db.Transaction(func(tx *gorm.DB) error {
		role, err := SuperAdminRole(tx)
		if err != nil {
			return fmt.Errorf("the Super admin role is missing (run the migrations): %w", err)
		}
		var user models.User
		err = tx.Unscoped().Where("username = ?", username).Take(&user).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
			if err != nil {
				return err
			}
			user = models.User{FullName: fullName, Username: username, RoleID: role.ID, PasswordHash: string(hash), Active: true}
			created = true
			return tx.Omit("Role", "Branches").Create(&user).Error
		}
		if err != nil {
			return err
		}
		if user.CompanyID != nil || user.RoleID != role.ID {
			return ErrNotASuperAdmin
		}
		if !reset {
			return nil
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		if err := tx.Unscoped().Model(&models.User{}).Where("id = ?", user.ID).Updates(map[string]interface{}{
			"password_hash": string(hash), "active": true, "deleted_at": nil,
			"token_version": gorm.Expr("token_version + 1"),
		}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", user.ID).Delete(&models.LoginAttempt{}).Error; err != nil {
			return err
		}
		return tx.Exec("UPDATE refresh_tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL", time.Now(), user.ID).Error
	})
	return created, err
}
