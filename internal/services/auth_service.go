package services

import (
	"time"

	"golang.org/x/crypto/bcrypt"

	"com-mart/backend/internal/config"
	"com-mart/backend/internal/dto"
	"com-mart/backend/internal/models"
	"com-mart/backend/internal/repositories"
	"com-mart/backend/internal/utils"
)

const ownerRoleName = "Owner"

type AuthService struct {
	cfg              *config.Config
	userRepo         *repositories.UserRepository
	refreshTokenRepo *repositories.RefreshTokenRepository
	deviceRepo       *repositories.DeviceRepository
	branchRepo       *repositories.BranchRepository
	loginAttemptRepo *repositories.LoginAttemptRepository
}

func NewAuthService(
	cfg *config.Config,
	userRepo *repositories.UserRepository,
	refreshTokenRepo *repositories.RefreshTokenRepository,
	deviceRepo *repositories.DeviceRepository,
	branchRepo *repositories.BranchRepository,
	loginAttemptRepo *repositories.LoginAttemptRepository,
) *AuthService {
	return &AuthService{
		cfg:              cfg,
		userRepo:         userRepo,
		refreshTokenRepo: refreshTokenRepo,
		deviceRepo:       deviceRepo,
		branchRepo:       branchRepo,
		loginAttemptRepo: loginAttemptRepo,
	}
}

func (s *AuthService) lockoutWindow() time.Duration {
	return time.Duration(s.cfg.LoginLockoutMinutes) * time.Minute
}

func (s *AuthService) checkLockout(userID uint64, deviceID *uint64) error {
	count, err := s.loginAttemptRepo.RecentFailureCount(userID, deviceID, s.lockoutWindow())
	if err != nil {
		return err
	}
	if count >= int64(s.cfg.LoginMaxAttempts) {
		return utils.ErrAccountLocked
	}
	return nil
}

// Login authenticates by username + password (back-office / manager login).
func (s *AuthService) Login(username, password, ip string) (*dto.AuthResponse, error) {
	user, err := s.userRepo.FindByUsername(username)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, utils.ErrUnauthorized
	}

	if err := s.checkLockout(user.ID, nil); err != nil {
		return nil, err
	}

	if !user.Active {
		return nil, utils.ErrAccountDisabled
	}

	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		_ = s.loginAttemptRepo.Record(user.ID, nil, false, ip)
		return nil, utils.ErrUnauthorized
	}

	_ = s.loginAttemptRepo.Record(user.ID, nil, true, ip)
	_ = s.userRepo.TouchLastActive(user.ID)

	return s.issueTokens(user, nil)
}

// PINLogin authenticates a cashier at a registered till via a 4-digit PIN.
func (s *AuthService) PINLogin(deviceKey, username, pin, ip string) (*dto.AuthResponse, error) {
	device, err := s.deviceRepo.FindByDeviceKey(deviceKey)
	if err != nil {
		return nil, err
	}
	if device == nil {
		return nil, utils.NewAppError(401, "UNKNOWN_DEVICE", "This till is not registered.")
	}

	user, err := s.userRepo.FindByUsername(username)
	if err != nil {
		return nil, err
	}
	if user == nil || user.PINHash == nil {
		return nil, utils.ErrUnauthorized
	}

	if err := s.checkLockout(user.ID, &device.ID); err != nil {
		return nil, err
	}

	if !user.Active {
		return nil, utils.ErrAccountDisabled
	}

	if bcrypt.CompareHashAndPassword([]byte(*user.PINHash), []byte(pin)) != nil {
		_ = s.loginAttemptRepo.Record(user.ID, &device.ID, false, ip)
		return nil, utils.ErrUnauthorized
	}

	_ = s.loginAttemptRepo.Record(user.ID, &device.ID, true, ip)
	_ = s.userRepo.TouchLastActive(user.ID)
	_ = s.deviceRepo.TouchLastSeen(device.ID)

	return s.issueTokens(user, &device.ID)
}

// Refresh rotates a refresh token: the presented token is revoked and a new
// access/refresh pair is issued, per build spec §1 ("rotated on use").
func (s *AuthService) Refresh(rawToken string) (*dto.AuthResponse, error) {
	hash := utils.HashToken(rawToken)
	rt, err := s.refreshTokenRepo.FindValidByHash(hash)
	if err != nil {
		return nil, err
	}
	if rt == nil {
		return nil, utils.ErrUnauthorized
	}

	user, err := s.userRepo.FindByID(rt.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil || !user.Active {
		return nil, utils.ErrUnauthorized
	}

	if err := s.refreshTokenRepo.Revoke(rt.ID); err != nil {
		return nil, err
	}

	return s.issueTokens(user, rt.DeviceID)
}

func (s *AuthService) Logout(rawToken string) error {
	hash := utils.HashToken(rawToken)
	rt, err := s.refreshTokenRepo.FindValidByHash(hash)
	if err != nil {
		return err
	}
	if rt == nil {
		return nil
	}
	return s.refreshTokenRepo.Revoke(rt.ID)
}

func (s *AuthService) Me(userID uint64) (*dto.MeResponse, error) {
	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, utils.ErrNotFound
	}

	permissions, err := s.permissionKeys(user)
	if err != nil {
		return nil, err
	}
	branches, err := s.branchSummaries(user)
	if err != nil {
		return nil, err
	}

	return &dto.MeResponse{
		User:        userSummary(user),
		Permissions: permissions,
		Branches:    branches,
	}, nil
}

func (s *AuthService) issueTokens(user *models.User, deviceID *uint64) (*dto.AuthResponse, error) {
	permissions, err := s.permissionKeys(user)
	if err != nil {
		return nil, err
	}
	branchIDs, err := s.branchIDs(user)
	if err != nil {
		return nil, err
	}
	branches, err := s.branchSummaries(user)
	if err != nil {
		return nil, err
	}

	accessToken, err := utils.GenerateAccessToken(
		s.cfg.JWTAccessSecret,
		time.Duration(s.cfg.JWTAccessTTLMinutes)*time.Minute,
		utils.AccessClaims{
			UserID:      user.ID,
			RoleID:      user.RoleID,
			RoleName:    roleName(user),
			Permissions: permissions,
			BranchIDs:   branchIDs,
		},
	)
	if err != nil {
		return nil, err
	}

	rawRefresh, err := utils.GenerateOpaqueToken()
	if err != nil {
		return nil, err
	}
	if err := s.refreshTokenRepo.Create(&models.RefreshToken{
		UserID:    user.ID,
		TokenHash: utils.HashToken(rawRefresh),
		DeviceID:  deviceID,
		ExpiresAt: time.Now().Add(time.Duration(s.cfg.JWTRefreshTTLDays) * 24 * time.Hour),
	}); err != nil {
		return nil, err
	}

	return &dto.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		User:         userSummary(user),
		Permissions:  permissions,
		Branches:     branches,
	}, nil
}

func (s *AuthService) permissionKeys(user *models.User) ([]string, error) {
	if user.Role == nil {
		return []string{}, nil
	}
	keys := make([]string, 0, len(user.Role.Permissions))
	for _, p := range user.Role.Permissions {
		keys = append(keys, p.Key)
	}
	return keys, nil
}

// branchIDs returns every branch ID the token should carry: all active
// branches for Owner, otherwise the user's assigned branches.
func (s *AuthService) branchIDs(user *models.User) ([]uint64, error) {
	if roleName(user) == ownerRoleName {
		return s.branchRepo.AllActiveIDs()
	}
	ids := make([]uint64, 0, len(user.Branches))
	for _, b := range user.Branches {
		ids = append(ids, b.ID)
	}
	return ids, nil
}

func (s *AuthService) branchSummaries(user *models.User) ([]dto.BranchSummary, error) {
	if roleName(user) == ownerRoleName {
		all, err := s.branchRepo.AllActive()
		if err != nil {
			return nil, err
		}
		out := make([]dto.BranchSummary, 0, len(all))
		for _, b := range all {
			out = append(out, dto.BranchSummary{ID: b.ID, Name: b.Name, Code: b.Code})
		}
		return out, nil
	}
	out := make([]dto.BranchSummary, 0, len(user.Branches))
	for _, b := range user.Branches {
		out = append(out, dto.BranchSummary{ID: b.ID, Name: b.Name, Code: b.Code})
	}
	return out, nil
}

func roleName(user *models.User) string {
	if user.Role == nil {
		return ""
	}
	return user.Role.Name
}

func userSummary(user *models.User) dto.UserSummary {
	return dto.UserSummary{
		ID:       user.ID,
		FullName: user.FullName,
		Username: user.Username,
		RoleID:   user.RoleID,
		RoleName: roleName(user),
	}
}
