package utils

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	TokenIssuer   = "com-mart-api"
	TokenAudience = "com-mart"

	// ScopePOS marks a session opened by PIN at a till. Its permissions are
	// cut down to till work (see POSScopePermissions), so a guessed 4-digit
	// PIN can't reach the back office, even for an Owner.
	ScopePOS = "pos"
)

// AccessClaims say who a token belongs to and nothing else. The JWT identifies
// the user; their role, permissions and branches are read from the database
// on every request (middleware.RequireAuth), so a change takes effect at once
// and editing a token can't grant anything.
type AccessClaims struct {
	UserID       uint64 `json:"user_id"`
	TokenVersion uint64 `json:"token_version"`
	Scope        string `json:"scope,omitempty"`
	// BranchID is a till session's branch. The user's role there decides what
	// the session may do, whatever branch a request names.
	BranchID uint64 `json:"branch_id,omitempty"`
	jwt.RegisteredClaims
}

func GenerateAccessToken(secret string, ttl time.Duration, claims AccessClaims) (string, error) {
	now := time.Now()
	claims.RegisteredClaims = jwt.RegisteredClaims{
		Issuer:    TokenIssuer,
		Audience:  jwt.ClaimStrings{TokenAudience},
		Subject:   strconv.FormatUint(claims.UserID, 10),
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ParseAccessToken accepts only an HS256 token we issued: right signature,
// issuer and audience, an expiry, and no time claim in the future.
func ParseAccessToken(secret, tokenString string) (*AccessClaims, error) {
	claims := &AccessClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims,
		func(*jwt.Token) (interface{}, error) { return []byte(secret), nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(TokenIssuer),
		jwt.WithAudience(TokenAudience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	if err != nil || !token.Valid {
		return nil, fmt.Errorf("invalid access token: %w", err)
	}
	if claims.UserID == 0 {
		return nil, errors.New("invalid access token: no user")
	}
	return claims, nil
}

// POSScopePermissions keeps only what a till session may use.
func POSScopePermissions(perms []string) []string {
	out := make([]string, 0, len(perms))
	for _, p := range perms {
		switch {
		// The till sells from the catalog and looks up or adds customers.
		case strings.HasPrefix(p, "pos."), strings.HasPrefix(p, "shift."), strings.HasPrefix(p, "customer."),
			p == "product.view", p == "category.view", p == "inventory.view":
			out = append(out, p)
		}
	}
	return out
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
