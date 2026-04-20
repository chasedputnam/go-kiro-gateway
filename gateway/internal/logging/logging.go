// Package logging provides centralized zerolog configuration for Kiro Gateway.
//
// Call Init() once at startup (typically from main.go) to configure the global
// logger with timestamps, caller info, and the level specified by LOG_LEVEL.
// After Init(), use zerolog's global logger (zerolog.Log()) or create
// sub-loggers via log.With() as needed.
package logging

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Init configures the global zerolog logger.
//
// Parameters:
//   - level: log level string (TRACE, DEBUG, INFO, WARN, ERROR, FATAL, PANIC).
//     Case-insensitive. Defaults to INFO on unrecognised values.
//   - w: optional writer override. When nil the logger writes to os.Stderr
//     with a human-friendly console format (coloured, timestamped).
//
// After Init the zerolog global logger (log.Logger) is ready to use:
//
//	log.Info().Str("addr", addr).Msg("server started")
func Init(level string, w io.Writer) {
	// Resolve level.
	lvl := parseLevel(level)
	zerolog.SetGlobalLevel(lvl)

	// Timestamp format — millisecond precision, matching the Python loguru
	// format used in the original gateway.
	zerolog.TimeFieldFormat = time.RFC3339Nano

	if w == nil {
		// Console writer for human-readable output on stderr.
		w = zerolog.ConsoleWriter{
			Out:        os.Stderr,
			TimeFormat: "2006-01-02 15:04:05.000",
		}
	}

	log.Logger = zerolog.New(w).
		With().
		Timestamp().
		Caller().
		Logger()
}

// parseLevel converts a string level name to a zerolog.Level.
// Returns zerolog.InfoLevel for unrecognised values.
func parseLevel(s string) zerolog.Level {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "TRACE":
		return zerolog.TraceLevel
	case "DEBUG":
		return zerolog.DebugLevel
	case "INFO":
		return zerolog.InfoLevel
	case "WARN", "WARNING":
		return zerolog.WarnLevel
	case "ERROR":
		return zerolog.ErrorLevel
	case "FATAL":
		return zerolog.FatalLevel
	case "PANIC":
		return zerolog.PanicLevel
	default:
		return zerolog.InfoLevel
	}
}
