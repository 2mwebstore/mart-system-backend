package services

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"com-mart/backend/internal/config"
	"com-mart/backend/internal/dto"
	"com-mart/backend/internal/models"
	"com-mart/backend/internal/repositories"
	"com-mart/backend/internal/utils"
)

const (
	// A refresh token replayed this soon after it was rotated is two tabs (or
	// a retry) racing each other, not a theft. It's refused, nothing more.
	refreshReuseGrace = 30 * time.Second

	pinFailureWindow = 24 * time.Hour
	pinFailureMax    = 20
)

var (
	errTillNotAllowed = utils.NewAppError(http.StatusForbidden, "TILL_NOT_ALLOWED", "This till belongs to a branch you aren't assigned to.")
	// ErrUnknownCompany answers a company code that isn't an active company's.
	ErrUnknownCompany = utils.NewAppError(http.StatusNotFound, "UNKNOWN_COMPANY", "No company has that code. Check it with your manager.")
	// errCompanyDisabled is told to someone who signs in correctly to a
	// company the super admin has disabled.
	errCompanyDisabled = utils.NewAppError(http.StatusForbidden, "COMPANY_DISABLED", "This company has been disabled.")
)

// dummyPasswordHash is compared against when there's no real hash to check, so
// an unknown username (or a user with no PIN) takes as long as a wrong password.
var dummyPasswordHash = sync.OnceValue(func() []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte("no account has this password"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return hash
})

type AuthService struct {
	cfg              *config.Config
	userRepo         *repositories.UserRepository
	refreshTokenRepo *repositories.RefreshTokenRepository
	deviceRepo       *repositories.DeviceRepository
	branchRepo       *repositories.BranchRepository
	loginAttemptRepo *repositories.LoginAttemptRepository
	// unknownLogins locks out a username that doesn't exist exactly as a real
	// one would be, so the lockout can't be used to find out which ones exist.
	unknownLogins *utils.SlidingWindow
}

func NewAuthService(
	cfg *config.Config,
	userRepo *repositories.UserRepository,
	refreshTokenRepo *repositories.RefreshTokenRepository,
	deviceRepo *repositories.DeviceRepository,
	branchRepo *repositories.BranchRepository,
	loginAttemptRepo *repositories.LoginAttemptRepository,
) *AuthService {
	s := &AuthService{
		cfg:              cfg,
		userRepo:         userRepo,
		refreshTokenRepo: refreshTokenRepo,
		deviceRepo:       deviceRepo,
		branchRepo:       branchRepo,
		loginAttemptRepo: loginAttemptRepo,
	}
	s.unknownLogins = utils.NewSlidingWindow(s.lockoutWindow(), cfg.LoginMaxAttempts)
	return s
}

func (s *AuthService) lockoutWindow() time.Duration {
	return time.Duration(s.cfg.LoginLockoutMinutes) * time.Minute
}

// checkLockout counts failures per user across password and every till. A
// per-till count would give a PIN guesser a fresh allowance for each till.
func (s *AuthService) checkLockout(userID uint64) error {
	count, err := s.loginAttemptRepo.RecentFailureCount(userID, s.lockoutWindow())
	if err != nil {
		return err
	}
	if count >= int64(s.cfg.LoginMaxAttempts) {
		return utils.ErrAccountLocked
	}
	return nil
}

// rejectUnknownUser answers for a username that doesn't exist the way a wrong
// password on a real account is answered: same work, same 401, same lockout.
func (s *AuthService) rejectUnknownUser(username, secret string) error {
	// MySQL compares usernames case-insensitively, so the key has to as well.
	if !s.unknownLogins.Allow(utils.HashToken(strings.ToLower(username)), time.Now()) {
		return utils.ErrAccountLocked
	}
	_ = bcrypt.CompareHashAndPassword(dummyPasswordHash(), []byte(secret))
	return utils.ErrUnauthorized
}

// Login authenticates by username + password (back-office / manager login).
func (s *AuthService) Login(username, password, ip string) (*dto.AuthResponse, error) {
	user, err := s.userRepo.FindByUsername(username)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, s.rejectUnknownUser(username, password)
	}

	if err := s.checkLockout(user.ID); err != nil {
		return nil, err
	}

	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		if err := s.loginAttemptRepo.Record(user.ID, nil, false, ip); err != nil {
			return nil, err
		}
		return nil, utils.ErrUnauthorized
	}

	// Only someone who knows the password learns the account is disabled.
	if !user.Active {
		return nil, utils.ErrAccountDisabled
	}
	if err := s.checkCompany(user); err != nil {
		return nil, err
	}

	_ = s.loginAttemptRepo.Record(user.ID, nil, true, ip)
	_ = s.userRepo.TouchLastActive(user.ID)

	return s.issueTokens(user, nil, uuid.NewString())
}

// PINLogin authenticates a cashier at a registered till via a 4-digit PIN.
// The till is one of the company's whose code is given, and so is the user:
// anyone else is answered as if there were no such user.
func (s *AuthService) PINLogin(companyCode, deviceKey, username, pin, ip string) (*dto.AuthResponse, error) {
	company, err := s.userRepo.ActiveCompanyByCode(companyCode)
	if err != nil {
		return nil, err
	}
	if company == nil {
		return nil, ErrUnknownCompany
	}
	device, err := s.deviceRepo.FindByDeviceKey(company.ID, deviceKey)
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
	if user == nil || user.CompanyID == nil || *user.CompanyID != company.ID {
		return nil, s.rejectUnknownUser(username, pin)
	}

	if err := s.checkLockout(user.ID); err != nil {
		return nil, err
	}
	pinFailures, err := s.loginAttemptRepo.RecentPINFailureCount(user.ID, pinFailureWindow)
	if err != nil {
		return nil, err
	}
	if pinFailures >= pinFailureMax {
		return nil, utils.ErrAccountLocked
	}

	// A user with no PIN goes through the same steps as a wrong PIN, so the
	// response doesn't say whether they have one.
	hash := dummyPasswordHash()
	if user.PINHash != nil {
		hash = []byte(*user.PINHash)
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(pin)) != nil || user.PINHash == nil {
		if err := s.loginAttemptRepo.Record(user.ID, &device.ID, false, ip); err != nil {
			return nil, err
		}
		return nil, utils.ErrUnauthorized
	}

	if !user.Active {
		return nil, utils.ErrAccountDisabled
	}

	resp, err := s.issueTokens(user, &device.ID, uuid.NewString())
	if err != nil {
		return nil, err
	}

	_ = s.loginAttemptRepo.Record(user.ID, &device.ID, true, ip)
	_ = s.userRepo.TouchLastActive(user.ID)
	_ = s.deviceRepo.TouchLastSeen(device.ID)

	return resp, nil
}

// Refresh rotates a refresh token: the presented token is revoked and a new
// access/refresh pair is issued, per build spec §1 ("rotated on use"). A token
// that was already rotated is being replayed, which means two parties have
// held it, so the session it belongs to is ended.
func (s *AuthService) Refresh(rawToken string) (*dto.AuthResponse, error) {
	rt, err := s.refreshTokenRepo.FindByHash(utils.HashToken(rawToken))
	if err != nil {
		return nil, err
	}
	if rt == nil || !time.Now().Before(rt.ExpiresAt) {
		return nil, utils.ErrUnauthorized
	}
	if rt.RevokedAt != nil {
		if time.Since(*rt.RevokedAt) > refreshReuseGrace {
			if err := s.endReplayedSession(rt); err != nil {
				return nil, err
			}
		}
		return nil, utils.ErrUnauthorized
	}

	user, err := s.userRepo.FindByID(rt.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil || !user.Active {
		return nil, utils.ErrUnauthorized
	}

	rotated, err := s.refreshTokenRepo.RevokeIfActive(rt.ID)
	if err != nil {
		return nil, err
	}
	if !rotated {
		return nil, utils.ErrUnauthorized
	}

	return s.issueTokens(user, rt.DeviceID, rt.FamilyID)
}

// checkCompany refuses a user whose company is disabled or gone. A super
// admin has none to check.
func (s *AuthService) checkCompany(user *models.User) error {
	if user.CompanyID == nil {
		return nil
	}
	company, err := s.userRepo.CompanyByID(*user.CompanyID)
	if err != nil {
		return err
	}
	if company == nil || !company.Active || company.DeletedAt.Valid {
		return errCompanyDisabled
	}
	return nil
}

// companyOf is the session's company, nil for a super admin.
func (s *AuthService) companyOf(access *repositories.Access) (*dto.CompanySummary, error) {
	if access.CompanyID == 0 {
		return nil, nil
	}
	company, err := s.userRepo.CompanyByID(access.CompanyID)
	if err != nil || company == nil {
		return nil, err
	}
	return &dto.CompanySummary{ID: company.ID, Name: company.Name, Code: company.Code}, nil
}

// endReplayedSession revokes the rest of the login the token came from. The
// token version is bumped only if something was still live, so replaying a dead
// token over and over does nothing to the user's other sessions.
func (s *AuthService) endReplayedSession(rt *models.RefreshToken) error {
	revoked, err := s.refreshTokenRepo.RevokeFamily(rt.FamilyID)
	if err != nil {
		return err
	}
	if revoked == 0 {
		return nil
	}
	return s.userRepo.BumpTokenVersion(rt.UserID)
}

// Logout ends this session. Bumping the token version also kills its access
// token, which would otherwise keep working until it expires; the user's other
// sessions pick up a new one through refresh without noticing.
func (s *AuthService) Logout(rawToken string) error {
	rt, err := s.refreshTokenRepo.FindByHash(utils.HashToken(rawToken))
	if err != nil {
		return err
	}
	if rt == nil {
		return nil
	}
	revoked, err := s.refreshTokenRepo.RevokeIfActive(rt.ID)
	if err != nil {
		return err
	}
	if !revoked {
		return nil
	}
	return s.userRepo.BumpTokenVersion(rt.UserID)
}

// Me describes the session working at branchID (0 for none). A till session
// (pos) only gets told about till work, at every branch.
func (s *AuthService) Me(userID, branchID uint64, pos bool) (*dto.MeResponse, error) {
	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, utils.ErrNotFound
	}
	access, err := s.userRepo.Access(userID, branchID)
	if err != nil {
		return nil, err
	}
	if access == nil {
		return nil, utils.ErrNotFound
	}
	summary, permissions, branches := sessionOf(user, access, pos)
	company, err := s.companyOf(access)
	if err != nil {
		return nil, err
	}
	return &dto.MeResponse{User: summary, Permissions: permissions, Branches: branches, Company: company, IsSuperAdmin: access.CompanyID == 0}, nil
}

// issueTokens opens or continues a session. familyID ties the new refresh token
// to the login it came from. A session at a till (deviceID set) is POS-scoped.
func (s *AuthService) issueTokens(user *models.User, deviceID *uint64, familyID string) (*dto.AuthResponse, error) {
	// A till session works at the till's branch, with the role the user holds
	// there, for as long as it lasts.
	scope, branchID := "", uint64(0)
	if deviceID != nil {
		device, err := s.deviceRepo.FindByID(*deviceID)
		if err != nil {
			return nil, err
		}
		if device == nil {
			return nil, utils.ErrUnauthorized
		}
		scope, branchID = utils.ScopePOS, device.BranchID
	}
	access, err := s.userRepo.Access(user.ID, branchID)
	if err != nil {
		return nil, err
	}
	if access == nil {
		return nil, utils.ErrUnauthorized
	}
	if access.BranchID != branchID {
		return nil, errTillNotAllowed
	}
	summary, permissions, branches := sessionOf(user, access, scope == utils.ScopePOS)
	company, err := s.companyOf(access)
	if err != nil {
		return nil, err
	}

	accessToken, err := utils.GenerateAccessToken(
		s.cfg.JWTAccessSecret,
		time.Duration(s.cfg.JWTAccessTTLMinutes)*time.Minute,
		utils.AccessClaims{
			UserID:       user.ID,
			TokenVersion: user.TokenVersion,
			Scope:        scope,
			BranchID:     branchID,
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
		FamilyID:  familyID,
		DeviceID:  deviceID,
		ExpiresAt: time.Now().Add(time.Duration(s.cfg.JWTRefreshTTLDays) * 24 * time.Hour),
	}); err != nil {
		return nil, err
	}

	return &dto.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		User:         summary,
		Permissions:  permissions,
		Branches:     branches,
		Company:      company,
		IsSuperAdmin: access.CompanyID == 0,
	}, nil
}

// sessionOf is what the client is told about a session: the acting role and
// its permissions, and the role and permissions at each of the user's
// branches. A till session (pos) only gets till permissions.
func sessionOf(user *models.User, access *repositories.Access, pos bool) (dto.UserSummary, []string, []dto.BranchSummary) {
	cut := func(perms []string) []string {
		if pos {
			return utils.POSScopePermissions(perms)
		}
		return perms
	}
	branches := make([]dto.BranchSummary, 0, len(access.Branches))
	for _, b := range access.Branches {
		branches = append(branches, dto.BranchSummary{
			ID: b.BranchID, Name: b.BranchName, Code: b.BranchCode,
			RoleID: b.RoleID, RoleName: b.RoleName, Permissions: cut(access.RolePermissions[b.RoleID]),
		})
	}
	summary := dto.UserSummary{
		ID:       user.ID,
		FullName: user.FullName,
		Username: user.Username,
		RoleID:   access.RoleID,
		RoleName: access.RoleName,
	}
	return summary, cut(access.Permissions), branches
}
