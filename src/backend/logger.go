package main

import (
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Falls back to warn on an unparseable level so a typo cannot accidentally
// turn on full debug output in production.
func SetupLogger(level string) {
	zerolog.TimeFieldFormat = time.RFC3339

	lvl, err := zerolog.ParseLevel(level)
	if err != nil || lvl == zerolog.NoLevel {
		lvl = zerolog.WarnLevel
	}
	zerolog.SetGlobalLevel(lvl)

	log.Logger = zerolog.New(os.Stdout).With().
		Timestamp().
		Str("service", "syrinx-api").
		Logger()

	if err != nil {
		log.Warn().Str("value", level).Msg("[WARN] invalid LOG_LEVEL, using warn")
	}
}
