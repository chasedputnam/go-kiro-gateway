package logging

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func TestInit_SetsGlobalLevel(t *testing.T) {
	tests := []struct {
		input string
		want  zerolog.Level
	}{
		{"TRACE", zerolog.TraceLevel},
		{"trace", zerolog.TraceLevel},
		{"DEBUG", zerolog.DebugLevel},
		{"debug", zerolog.DebugLevel},
		{"INFO", zerolog.InfoLevel},
		{"info", zerolog.InfoLevel},
		{"WARN", zerolog.WarnLevel},
		{"WARNING", zerolog.WarnLevel},
		{"ERROR", zerolog.ErrorLevel},
		{"FATAL", zerolog.FatalLevel},
		{"PANIC", zerolog.PanicLevel},
		{"", zerolog.InfoLevel},
		{"invalid", zerolog.InfoLevel},
		{"  DEBUG  ", zerolog.DebugLevel},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			var buf bytes.Buffer
			Init(tt.input, &buf)

			if zerolog.GlobalLevel() != tt.want {
				t.Errorf("Init(%q): global level = %v, want %v",
					tt.input, zerolog.GlobalLevel(), tt.want)
			}
		})
	}
}

func TestInit_WritesToProvidedWriter(t *testing.T) {
	var buf bytes.Buffer
	Init("DEBUG", &buf)

	log.Info().Msg("hello from test")

	output := buf.String()
	if !strings.Contains(output, "hello from test") {
		t.Errorf("expected log output to contain message, got: %s", output)
	}
}

func TestInit_IncludesTimestamp(t *testing.T) {
	var buf bytes.Buffer
	Init("DEBUG", &buf)

	log.Info().Msg("timestamp check")

	output := buf.String()
	// zerolog JSON output includes "time" field.
	if !strings.Contains(output, "time") {
		t.Errorf("expected log output to contain timestamp field, got: %s", output)
	}
}

func TestInit_IncludesCaller(t *testing.T) {
	var buf bytes.Buffer
	Init("DEBUG", &buf)

	log.Info().Msg("caller check")

	output := buf.String()
	// zerolog JSON output includes "caller" field with file:line.
	if !strings.Contains(output, "caller") {
		t.Errorf("expected log output to contain caller field, got: %s", output)
	}
}

func TestInit_RespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	Init("ERROR", &buf)

	log.Debug().Msg("should not appear")
	log.Info().Msg("should not appear either")
	log.Error().Msg("should appear")

	output := buf.String()
	if strings.Contains(output, "should not appear") {
		t.Errorf("debug/info messages should be filtered at ERROR level, got: %s", output)
	}
	if !strings.Contains(output, "should appear") {
		t.Errorf("error message should appear at ERROR level, got: %s", output)
	}
}

func TestInit_DefaultsToStderrWhenNilWriter(t *testing.T) {
	// Just verify it doesn't panic with nil writer.
	Init("INFO", nil)
	log.Info().Msg("nil writer test")
}
