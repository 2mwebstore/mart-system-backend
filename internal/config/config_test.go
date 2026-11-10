package config

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	strong := strings.Repeat("a", minProductionSecretLen)
	other := strings.Repeat("b", minProductionSecretLen)
	cases := []struct {
		name           string
		cfg            Config
		wantErrContain string
	}{
		{"dev accepts short secrets", Config{AppEnv: "development", JWTAccessSecret: "x", JWTRefreshSecret: "y"}, ""},
		{"dev still refuses empty", Config{AppEnv: "development", JWTAccessSecret: "", JWTRefreshSecret: "y"}, "must not be empty"},
		{"production accepts strong, different secrets", Config{AppEnv: "production", JWTAccessSecret: strong, JWTRefreshSecret: other}, ""},
		{"production refuses a short access secret", Config{AppEnv: "production", JWTAccessSecret: strong[1:], JWTRefreshSecret: other}, "at least"},
		{"production refuses a short refresh secret", Config{AppEnv: "production", JWTAccessSecret: strong, JWTRefreshSecret: other[1:]}, "at least"},
		{"production refuses the same secret twice", Config{AppEnv: "production", JWTAccessSecret: strong, JWTRefreshSecret: strong}, "different"},
		{"production is matched in any case", Config{AppEnv: "PRODUCTION", JWTAccessSecret: "x", JWTRefreshSecret: "y"}, "at least"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErrContain == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrContain) {
				t.Fatalf("want an error containing %q, got %v", tc.wantErrContain, err)
			}
		})
	}
}
