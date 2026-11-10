package database

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"com-mart/backend/internal/config"
)

// Connect opens the GORM/MySQL connection pool. Schema is managed entirely
// by migrations/*.sql (golang-migrate) — GORM AutoMigrate is never called.
func Connect(cfg *config.Config) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(cfg.DSN()), &gorm.Config{
		Logger:                                   newLogger(log.New(os.Stdout, "\r\n", log.LstdFlags), cfg.IsProduction()),
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		return nil, fmt.Errorf("connecting to database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("getting sql.DB handle: %w", err)
	}
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return db, nil
}

// newLogger logs every query in development. In production it logs only errors
// and slow queries, and without their values: those include usernames, password
// hashes and token hashes. A row not being found doesn't count as an error, since
// a mistyped username or an old refresh token is normal, and otherwise anyone could
// write what they like into the logs (people do type passwords as usernames).
func newLogger(out logger.Writer, production bool) logger.Interface {
	level := logger.Info
	if production {
		level = logger.Warn
	}
	return logger.New(out, logger.Config{
		SlowThreshold:             200 * time.Millisecond,
		LogLevel:                  level,
		IgnoreRecordNotFoundError: true,
		ParameterizedQueries:      production,
		Colorful:                  !production,
	})
}

// WithTransaction runs fn inside a DB transaction, committing on success
// and rolling back if fn returns an error or panics. Every multi-table
// write in the app (sales, shifts, stock movements, ...) must go through
// this rather than issuing writes on the plain *gorm.DB.
func WithTransaction(db *gorm.DB, fn func(tx *gorm.DB) error) error {
	return db.Transaction(fn)
}
