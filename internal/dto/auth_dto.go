package dto

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type PINLoginRequest struct {
	DeviceKey string `json:"device_key" binding:"required"`
	Username  string `json:"username" binding:"required"`
	PIN       string `json:"pin" binding:"required,len=4,numeric"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type LogoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type BranchSummary struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
	Code string `json:"code"`
}

type UserSummary struct {
	ID       uint64 `json:"id"`
	FullName string `json:"full_name"`
	Username string `json:"username"`
	RoleID   uint64 `json:"role_id"`
	RoleName string `json:"role_name"`
}

type AuthResponse struct {
	AccessToken  string          `json:"access_token"`
	RefreshToken string          `json:"refresh_token"`
	User         UserSummary     `json:"user"`
	Permissions  []string        `json:"permissions"`
	Branches     []BranchSummary `json:"branches"`
}

type MeResponse struct {
	User        UserSummary     `json:"user"`
	Permissions []string        `json:"permissions"`
	Branches    []BranchSummary `json:"branches"`
}
