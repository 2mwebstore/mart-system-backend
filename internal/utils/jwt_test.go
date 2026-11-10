package utils

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "unit-test-secret-unit-test-secret"

func validClaims() jwt.MapClaims {
	now := time.Now().Unix()
	return jwt.MapClaims{
		"user_id": 7, "token_version": 3, "iss": TokenIssuer, "aud": TokenAudience,
		"iat": now, "nbf": now, "exp": now + 600,
	}
}

func signToken(t *testing.T, method jwt.SigningMethod, key interface{}, claims jwt.MapClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAccessTokenRoundTrip(t *testing.T) {
	token, err := GenerateAccessToken(testSecret, time.Minute, AccessClaims{UserID: 7, TokenVersion: 3, Scope: ScopePOS})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseAccessToken(testSecret, token)
	if err != nil {
		t.Fatalf("a token we issued must parse: %v", err)
	}
	if got.UserID != 7 || got.TokenVersion != 3 || got.Scope != ScopePOS {
		t.Fatalf("claims changed in the round trip: %+v", got)
	}
}

// The token says who is calling and nothing about what they may do.
func TestAccessTokenCarriesNoAuthorizationData(t *testing.T) {
	token, err := GenerateAccessToken(testSecret, time.Minute, AccessClaims{UserID: 7})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"role_id", "role_name", "permissions", "branch_ids"} {
		if _, ok := claims[field]; ok {
			t.Errorf("token must not carry %q", field)
		}
	}
}

func TestParseAccessTokenRejects(t *testing.T) {
	now := time.Now().Unix()
	mutate := func(change func(jwt.MapClaims)) jwt.MapClaims {
		c := validClaims()
		change(c)
		return c
	}
	good := signToken(t, jwt.SigningMethodHS256, []byte(testSecret), validClaims())
	parts := strings.Split(good, ".")

	cases := map[string]string{
		"empty":             "",
		"garbage":           "not.a.jwt",
		"wrong signature":   signToken(t, jwt.SigningMethodHS256, []byte("another-secret-another-secret-123"), validClaims()),
		"alg none":          signToken(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, validClaims()),
		"HS384":             signToken(t, jwt.SigningMethodHS384, []byte(testSecret), validClaims()),
		"HS512":             signToken(t, jwt.SigningMethodHS512, []byte(testSecret), validClaims()),
		"signature removed": parts[0] + "." + parts[1] + ".",
		"payload edited": parts[0] + "." +
			base64.RawURLEncoding.EncodeToString([]byte(`{"user_id":1,"iss":"`+TokenIssuer+`","aud":"`+TokenAudience+`","exp":9999999999}`)) +
			"." + parts[2],
		"no expiry":            signToken(t, jwt.SigningMethodHS256, []byte(testSecret), mutate(func(c jwt.MapClaims) { delete(c, "exp") })),
		"expired":              signToken(t, jwt.SigningMethodHS256, []byte(testSecret), mutate(func(c jwt.MapClaims) { c["exp"] = now - 60 })),
		"not yet valid":        signToken(t, jwt.SigningMethodHS256, []byte(testSecret), mutate(func(c jwt.MapClaims) { c["nbf"] = now + 3600 })),
		"issued in the future": signToken(t, jwt.SigningMethodHS256, []byte(testSecret), mutate(func(c jwt.MapClaims) { c["iat"] = now + 3600 })),
		"wrong issuer":         signToken(t, jwt.SigningMethodHS256, []byte(testSecret), mutate(func(c jwt.MapClaims) { c["iss"] = "https://evil.example" })),
		"no issuer":            signToken(t, jwt.SigningMethodHS256, []byte(testSecret), mutate(func(c jwt.MapClaims) { delete(c, "iss") })),
		"wrong audience":       signToken(t, jwt.SigningMethodHS256, []byte(testSecret), mutate(func(c jwt.MapClaims) { c["aud"] = "evil" })),
		"no audience":          signToken(t, jwt.SigningMethodHS256, []byte(testSecret), mutate(func(c jwt.MapClaims) { delete(c, "aud") })),
		"no user":              signToken(t, jwt.SigningMethodHS256, []byte(testSecret), mutate(func(c jwt.MapClaims) { delete(c, "user_id") })),
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseAccessToken(testSecret, token); err == nil {
				t.Fatal("token was accepted")
			}
		})
	}
}

func TestPOSScopePermissions(t *testing.T) {
	got := POSScopePermissions([]string{
		"pos.sell", "pos.void", "shift.own", "product.view", "category.view", "inventory.view", "customer.view", "customer.create",
		"product.create", "supplier.view", "inventory.adjust", "report.sales", "user.create", "role.update", "system.reset",
		"system.manage", "branch.view", "posing.as.pos",
	})
	want := []string{"pos.sell", "pos.void", "shift.own", "product.view", "category.view", "inventory.view", "customer.view", "customer.create"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
	if out := POSScopePermissions(nil); out == nil {
		t.Fatal("must be an empty list, not nil, so it serializes as []")
	}
}
