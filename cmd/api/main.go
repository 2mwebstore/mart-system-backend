package main

import (
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"com-mart/backend/internal/config"
	"com-mart/backend/internal/database"
	"com-mart/backend/internal/handlers"
	"com-mart/backend/internal/repositories"
	"com-mart/backend/internal/routes"
	"com-mart/backend/internal/services"
)

func main() {
	log.Logger = zerolog.New(os.Stdout).With().Timestamp().Logger()

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	loc, err := time.LoadLocation(cfg.AppTimezone)
	if err != nil {
		log.Fatal().Err(err).Str("timezone", cfg.AppTimezone).Msg("invalid APP_TIMEZONE")
	}
	time.Local = loc

	if cfg.AutoMigrate {
		if err := database.Migrate(cfg); err != nil {
			log.Fatal().Err(err).Msg("failed to run database migrations")
		}
	} else {
		log.Warn().Msg("AUTO_MIGRATE=false: skipping migrations — run `make migrate-up` if the schema is out of date")
	}

	db, err := database.Connect(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}

	userRepo := repositories.NewUserRepository(db)
	refreshTokenRepo := repositories.NewRefreshTokenRepository(db)
	deviceRepo := repositories.NewDeviceRepository(db)
	branchRepo := repositories.NewBranchRepository(db)
	loginAttemptRepo := repositories.NewLoginAttemptRepository(db)

	authService := services.NewAuthService(cfg, userRepo, refreshTokenRepo, deviceRepo, branchRepo, loginAttemptRepo)
	authHandler := handlers.NewAuthHandler(authService)

	router := routes.Setup(routes.Dependencies{
		Config:      cfg,
		AuthHandler: authHandler,
		API:         handlers.NewAPI(db, cfg),
	})

	log.Info().Str("port", cfg.AppPort).Str("env", cfg.AppEnv).Msg("starting com-mart api")
	if err := router.Run(":" + cfg.AppPort); err != nil {
		log.Fatal().Err(err).Msg("server stopped")
	}
}
