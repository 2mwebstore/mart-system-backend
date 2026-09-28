// Integration tests that hit a real MySQL instance (per build spec §11:
// "integration tests against a MySQL test container"). They read the same
// DB_*/JWT_* env vars as the API itself and skip (not fail) if no database
// is reachable, so `go test ./...` stays green without Docker running.
//
// To run against a throwaway MySQL 8 container:
//
//	docker run -d --name com_mart_test_mysql \
//	  -e MYSQL_DATABASE=com_mart -e MYSQL_USER=com_mart \
//	  -e MYSQL_PASSWORD=devpass -e MYSQL_ROOT_PASSWORD=rootpass \
//	  -p 3307:3306 mysql:8.0
//	migrate -database "mysql://com_mart:devpass@tcp(127.0.0.1:3307)/com_mart?charset=utf8mb4&collation=utf8mb4_unicode_ci" \
//	  -path migrations up
//	DB_HOST=127.0.0.1 DB_PORT=3307 DB_NAME=com_mart DB_USER=com_mart DB_PASSWORD=devpass \
//	  go run ./seed
//	DB_HOST=127.0.0.1 DB_PORT=3307 DB_NAME=com_mart DB_USER=com_mart DB_PASSWORD=devpass \
//	  JWT_ACCESS_SECRET=x JWT_REFRESH_SECRET=y go test ./tests/... -v
package tests

import (
	"testing"

	"gorm.io/gorm"

	"com-mart/backend/internal/config"
	"com-mart/backend/internal/database"
	"com-mart/backend/internal/repositories"
	"com-mart/backend/internal/services"
	"com-mart/backend/internal/utils"
)

func connectOrSkip(t *testing.T) *gorm.DB {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("skipping integration test, config not loadable: %v", err)
	}
	db, err := database.Connect(cfg)
	if err != nil {
		t.Skipf("skipping integration test, no database reachable: %v", err)
	}
	return db
}

func newAuthService(t *testing.T) (*services.AuthService, *gorm.DB) {
	t.Helper()
	db := connectOrSkip(t)
	cfg, _ := config.Load()

	authService := services.NewAuthService(
		cfg,
		repositories.NewUserRepository(db),
		repositories.NewRefreshTokenRepository(db),
		repositories.NewDeviceRepository(db),
		repositories.NewBranchRepository(db),
		repositories.NewLoginAttemptRepository(db),
	)
	return authService, db
}

func TestLogin_SeededOwner_Succeeds(t *testing.T) {
	authService, _ := newAuthService(t)

	resp, err := authService.Login("sok.dara", "Password123!", "127.0.0.1")
	if err != nil {
		t.Fatalf("expected owner login to succeed, got error: %v", err)
	}
	if resp.User.RoleName != "Owner" {
		t.Errorf("RoleName = %q, want Owner", resp.User.RoleName)
	}
	if len(resp.Branches) < 3 {
		t.Errorf("expected Owner to see all seeded branches, got %d", len(resp.Branches))
	}
	if resp.AccessToken == "" || resp.RefreshToken == "" {
		t.Error("expected non-empty access and refresh tokens")
	}
}

func TestLogin_WrongPassword_Fails(t *testing.T) {
	authService, _ := newAuthService(t)

	_, err := authService.Login("sok.dara", "definitely-wrong", "127.0.0.1")
	if err != utils.ErrUnauthorized {
		t.Fatalf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestLogin_DisabledUser_Fails(t *testing.T) {
	authService, _ := newAuthService(t)

	_, err := authService.Login("ros.chenda", "Password123!", "127.0.0.1")
	if err != utils.ErrAccountDisabled {
		t.Fatalf("expected ErrAccountDisabled, got: %v", err)
	}
}

func TestRefresh_RotatesToken(t *testing.T) {
	authService, _ := newAuthService(t)

	first, err := authService.Login("sok.dara", "Password123!", "127.0.0.1")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	refreshed, err := authService.Refresh(first.RefreshToken)
	if err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	if refreshed.RefreshToken == first.RefreshToken {
		t.Error("expected refresh to rotate to a new refresh token")
	}

	// The original (now-revoked) refresh token must no longer work.
	if _, err := authService.Refresh(first.RefreshToken); err != utils.ErrUnauthorized {
		t.Errorf("expected revoked refresh token to fail with ErrUnauthorized, got: %v", err)
	}
}
