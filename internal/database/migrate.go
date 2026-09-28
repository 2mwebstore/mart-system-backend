package database

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/rs/zerolog/log"

	"com-mart/backend/internal/config"
	"com-mart/backend/migrations"
)

// Migrate applies every pending migrations/*.sql (embedded in the binary) —
// the same files and the same schema_migrations table the `migrate` CLI and
// `make migrate-up` use, so the two are interchangeable. It uses its own
// short-lived connection because migration files contain multiple statements
// (multiStatements=true), which the app's GORM pool deliberately doesn't allow.
func Migrate(cfg *config.Config) error {
	db, err := sql.Open("mysql", cfg.DSN()+"&multiStatements=true")
	if err != nil {
		return fmt.Errorf("opening migration connection: %w", err)
	}
	defer db.Close()

	driver, err := migratemysql.WithInstance(db, &migratemysql.Config{})
	if err != nil {
		return fmt.Errorf("creating migrate driver: %w", err)
	}
	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("loading embedded migrations: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", source, cfg.DBName, driver)
	if err != nil {
		return fmt.Errorf("creating migrator: %w", err)
	}

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			log.Info().Msg("database schema is up to date")
			return nil
		}
		return fmt.Errorf("applying migrations: %w", err)
	}

	version, dirty, _ := m.Version()
	log.Info().Uint("version", version).Bool("dirty", dirty).Msg("migrations applied")
	return nil
}
