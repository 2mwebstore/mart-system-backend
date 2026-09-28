package config

import (
	"fmt"
	"strings"

	"github.com/caarlos0/env/v10"
	"github.com/joho/godotenv"
)

// Config holds every environment-driven setting for the API process.
type Config struct {
	AppEnv      string `env:"APP_ENV" envDefault:"development"`
	AppPort     string `env:"APP_PORT" envDefault:"8080"`
	AppTimezone string `env:"APP_TIMEZONE" envDefault:"Asia/Phnom_Penh"`
	// AutoMigrate applies pending migrations when the API starts. Set to
	// false to manage the schema only via `make migrate-up`.
	AutoMigrate bool `env:"AUTO_MIGRATE" envDefault:"true"`

	DBHost     string `env:"DB_HOST" envDefault:"localhost"`
	DBPort     string `env:"DB_PORT" envDefault:"3306"`
	DBName     string `env:"DB_NAME" envDefault:"com_mart"`
	DBUser     string `env:"DB_USER" envDefault:"com_mart"`
	DBPassword string `env:"DB_PASSWORD"`

	JWTAccessSecret      string `env:"JWT_ACCESS_SECRET,required"`
	JWTRefreshSecret     string `env:"JWT_REFRESH_SECRET,required"`
	JWTAccessTTLMinutes  int    `env:"JWT_ACCESS_TTL_MINUTES" envDefault:"15"`
	JWTRefreshTTLDays    int    `env:"JWT_REFRESH_TTL_DAYS" envDefault:"7"`

	LoginMaxAttempts    int `env:"LOGIN_MAX_ATTEMPTS" envDefault:"5"`
	LoginLockoutMinutes int `env:"LOGIN_LOCKOUT_MINUTES" envDefault:"5"`

	CORSAllowedOrigins []string `env:"CORS_ALLOWED_ORIGINS" envSeparator:","`

	R2AccountID       string `env:"R2_ACCOUNT_ID"`
	R2AccessKeyID     string `env:"R2_ACCESS_KEY_ID"`
	R2SecretAccessKey string `env:"R2_SECRET_ACCESS_KEY"`
	R2Bucket          string `env:"R2_BUCKET" envDefault:"com-mart"`
	R2PublicURL       string `env:"R2_PUBLIC_URL"`

	KHQRProvider   string `env:"KHQR_PROVIDER" envDefault:"mock"`
	KHQRAPIKey     string `env:"KHQR_API_KEY"`
	KHQRMerchantID string `env:"KHQR_MERCHANT_ID"`
}

// Load reads a .env file if present (dev convenience) then parses the
// environment into a Config. In production, real env vars are used and no
// .env file needs to exist.
func Load() (*Config, error) {
	_ = godotenv.Load() // ignore error: fine if .env doesn't exist (e.g. prod)

	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, nil
}

// DSN builds the MySQL data source name for GORM/database-sql.
func (c *Config) DSN() string {
	return fmt.Sprintf(
		"%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&collation=utf8mb4_unicode_ci&parseTime=true&loc=%s",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName, strings.ReplaceAll(c.AppTimezone, "/", "%2F"),
	)
}

func (c *Config) IsProduction() bool {
	return strings.EqualFold(c.AppEnv, "production")
}
