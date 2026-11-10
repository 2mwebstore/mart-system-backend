package tests

import (
	"errors"
	"testing"

	"com-mart/backend/internal/utils"
)

func TestLogin_Owner_Succeeds(t *testing.T) {
	e := setup(t)

	resp, err := e.auth.Login(e.owner.Username, testPassword, "127.0.0.1")
	if err != nil {
		t.Fatalf("expected owner login to succeed, got error: %v", err)
	}
	if resp.User.RoleName != "Owner" {
		t.Errorf("RoleName = %q, want Owner", resp.User.RoleName)
	}
	if len(resp.Branches) < 2 {
		t.Errorf("expected Owner to see every branch, got %d", len(resp.Branches))
	}
	if resp.AccessToken == "" || resp.RefreshToken == "" {
		t.Error("expected non-empty access and refresh tokens")
	}
}

func TestLogin_WrongPassword_Fails(t *testing.T) {
	e := setup(t)

	if _, err := e.auth.Login(e.owner.Username, "definitely-wrong", "127.0.0.1"); !errors.Is(err, utils.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestLogin_DisabledUser_Fails(t *testing.T) {
	e := setup(t)

	if _, err := e.auth.Login(e.disabledCashier.Username, testPassword, "127.0.0.1"); !errors.Is(err, utils.ErrAccountDisabled) {
		t.Fatalf("expected ErrAccountDisabled, got: %v", err)
	}
}

func TestRefresh_RotatesToken(t *testing.T) {
	e := setup(t)

	first, err := e.auth.Login(e.owner.Username, testPassword, "127.0.0.1")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	refreshed, err := e.auth.Refresh(first.RefreshToken)
	if err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	if refreshed.RefreshToken == first.RefreshToken {
		t.Error("expected refresh to rotate to a new refresh token")
	}

	// The original (now-revoked) refresh token must no longer work.
	if _, err := e.auth.Refresh(first.RefreshToken); !errors.Is(err, utils.ErrUnauthorized) {
		t.Errorf("expected revoked refresh token to fail with ErrUnauthorized, got: %v", err)
	}
}
