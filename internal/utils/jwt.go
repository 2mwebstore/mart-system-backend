package utils

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AccessClaims are embedded in every access token. BranchIDs and the role
// name/permission keys are carried in the token itself so RBAC/branch-scope
// middleware never needs a DB round trip per request; a disabled user or
// changed role only takes effect on next login/refresh (7-day worst case,
// acceptable for this business's size — see docs/DECISIONS.md if this
// needs tightening later).
type AccessClaims struct {
	UserID      uint64   `json:"user_id"`
	RoleID      uint64   `json:"role_id"`
	RoleName    string   `json:"role_name"`
	Permissions []string `json:"permissions"`
	BranchIDs   []uint64 `json:"branch_ids"`
	jwt.RegisteredClaims
}

func GenerateAccessToken(secret string, ttl time.Duration, claims AccessClaims) (string, error) {
	claims.RegisteredClaims = jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func ParseAccessToken(secret, tokenString string) (*AccessClaims, error) {
	claims := &AccessClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil || !token.Valid {
		return nil, fmt.Errorf("invalid access token: %w", err)
	}
	return claims, nil
}

// GenerateOpaqueToken returns a URL-safe random string used as a refresh
// token. Only its SHA-256 hash is ever persisted (see HashToken).
func GenerateOpaqueToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashToken returns the hex SHA-256 digest of a raw token, for storage/
// lookup of refresh tokens without ever keeping the raw value in the DB.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
