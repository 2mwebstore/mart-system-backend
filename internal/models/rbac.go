package models

import "time"

type Role struct {
	ID          uint64 `gorm:"primaryKey" json:"id"`
	Name        string `gorm:"size:60;not null;uniqueIndex" json:"name"`
	Description string `gorm:"size:255" json:"description"`
	// IsLocked marks the seeded Owner role: cannot be edited or deleted.
	IsLocked bool `gorm:"not null;default:false" json:"is_locked"`
	Timestamps
	SoftDelete

	Permissions []Permission `gorm:"many2many:role_permissions;" json:"permissions,omitempty"`
	Limits      *RoleLimit   `gorm:"foreignKey:RoleID" json:"limits,omitempty"`
}

func (Role) TableName() string { return "roles" }

// Permission is a single grantable key, e.g. "pos.sell", "inventory.receive".
type Permission struct {
	ID          uint64 `gorm:"primaryKey" json:"id"`
	Key         string `gorm:"size:60;not null;uniqueIndex" json:"key"`
	Group       string `gorm:"size:40;not null;index" json:"group"` // pos|shift|inventory|report|customer|user
	Description string `gorm:"size:255" json:"description"`
}

func (Permission) TableName() string { return "permissions" }

// RolePermission is the many2many join row between roles and permissions.
type RolePermission struct {
	RoleID       uint64 `gorm:"primaryKey" json:"role_id"`
	PermissionID uint64 `gorm:"primaryKey" json:"permission_id"`
}

func (RolePermission) TableName() string { return "role_permissions" }

// RoleLimit holds the numeric guardrails for a role that trigger a manager
// PIN approval when exceeded at the till.
type RoleLimit struct {
	RoleID                     uint64 `gorm:"primaryKey" json:"role_id"`
	MaxDiscountPercent         int    `gorm:"not null;default:0" json:"max_discount_percent"`
	RefundWithoutApprovalCents int64  `gorm:"not null;default:0" json:"refund_without_approval_cents"`
	Timestamps
}

func (RoleLimit) TableName() string { return "role_limits" }

type User struct {
	ID          uint64  `gorm:"primaryKey" json:"id"`
	FullName    string  `gorm:"size:120;not null" json:"full_name"`
	Username    string  `gorm:"size:60;not null;uniqueIndex" json:"username"`
	Phone       string  `gorm:"size:30" json:"phone"`
	PasswordHash string `gorm:"size:255;not null" json:"-"`
	PINHash     *string `gorm:"size:255" json:"-"`
	RoleID      uint64  `gorm:"not null;index" json:"role_id"`
	Active      bool    `gorm:"not null" json:"active"` // see note on models.Branch.Active
	LastActiveAt *time.Time `json:"last_active_at,omitempty"`
	Timestamps
	SoftDelete

	Role     *Role     `gorm:"foreignKey:RoleID" json:"role,omitempty"`
	Branches []*Branch `gorm:"many2many:user_branches;" json:"branches,omitempty"`
}

func (User) TableName() string { return "users" }

// UserBranch grants a user access to a branch (a user may have many).
type UserBranch struct {
	UserID   uint64 `gorm:"primaryKey" json:"user_id"`
	BranchID uint64 `gorm:"primaryKey" json:"branch_id"`
}

func (UserBranch) TableName() string { return "user_branches" }

// RefreshToken stores refresh tokens hashed (never the raw token), and is
// rotated (old row invalidated, new row inserted) on every /auth/refresh.
type RefreshToken struct {
	ID        uint64     `gorm:"primaryKey" json:"id"`
	UserID    uint64     `gorm:"not null;index" json:"user_id"`
	TokenHash string     `gorm:"size:64;not null;uniqueIndex" json:"-"`
	DeviceID  *uint64    `gorm:"index" json:"device_id,omitempty"`
	ExpiresAt time.Time  `gorm:"not null;index" json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `gorm:"not null" json:"created_at"`

	User *User `gorm:"foreignKey:UserID" json:"-"`
}

func (RefreshToken) TableName() string { return "refresh_tokens" }

// LoginAttempt backs the login/PIN lockout rule (5 fails -> 5 minute lock
// per user+device). Not in the original table list (build spec §6); added
// because durable lockout tracking needs a table — see docs/DECISIONS.md.
type LoginAttempt struct {
	ID        uint64    `gorm:"primaryKey" json:"id"`
	UserID    uint64    `gorm:"not null;index:idx_login_attempts_user_device" json:"user_id"`
	DeviceID  *uint64   `gorm:"index:idx_login_attempts_user_device" json:"device_id,omitempty"`
	Success   bool      `gorm:"not null" json:"success"`
	IP        string    `gorm:"size:64" json:"ip"`
	CreatedAt time.Time `gorm:"not null;index" json:"created_at"`
}

func (LoginAttempt) TableName() string { return "login_attempts" }
