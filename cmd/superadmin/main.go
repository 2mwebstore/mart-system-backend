// Command superadmin creates a super admin, or resets one's password, on an
// install that's already running: the way an upgraded database gets its
// first super admin, and the way back in when the password is lost.
//
//	SUPERADMIN_USERNAME=superadmin SUPERADMIN_PASSWORD='…' go run ./cmd/superadmin
//
// The password must be at least 12 characters. A reset also turns the
// account back on, clears its failed sign-ins and ends every session it had.
// A username that belongs to a company's account is refused: staff are never
// turned into super admins.
package main

import (
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"com-mart/backend/internal/config"
	"com-mart/backend/internal/database"
	"com-mart/backend/internal/services"
)

func main() {
	log.Logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()

	username := os.Getenv("SUPERADMIN_USERNAME")
	password := os.Getenv("SUPERADMIN_PASSWORD")
	if username == "" {
		log.Fatal().Msg("set SUPERADMIN_USERNAME")
	}
	if len(password) < services.MinSuperAdminPasswordLen {
		log.Fatal().Int("min", services.MinSuperAdminPasswordLen).Msg("set SUPERADMIN_PASSWORD to at least this many characters")
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}
	// The Super admin role comes from migration 000030.
	if err := database.Migrate(cfg); err != nil {
		log.Fatal().Err(err).Msg("failed to run database migrations")
	}
	db, err := database.Connect(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}

	created, err := services.UpsertSuperAdmin(db, username, "Super admin", password, true)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to set up the super admin")
	}
	if created {
		log.Info().Str("username", username).Msg("super admin created")
	} else {
		log.Info().Str("username", username).Msg("super admin password reset; their sessions have ended")
	}
}
