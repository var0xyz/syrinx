package main

import (
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func SetupLogger() {
	// Configure zerolog
	zerolog.TimeFieldFormat = time.RFC3339
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	log.Logger = zerolog.New(os.Stdout).With().
		Timestamp().
		Str("service", "syrinx-api").
		Logger()
}
