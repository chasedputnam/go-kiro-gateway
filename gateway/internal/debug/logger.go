// Package debug provides request/response debug logging for Kiro Gateway.
//
// The DebugLogger captures request bodies, Kiro request bodies, raw response
// streams, and modified response streams. It supports three modes:
//
//   - off:    logging disabled (default, production)
//   - errors: data is buffered in memory and flushed to files only on 4xx/5xx
//   - all:    data is written immediately to files for every request
//
// Application logs captured during request processing are saved to
// app_logs.txt. Error information is saved to error_info.json.
package debug

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/rs/zerolog/log"
)

// DebugLogger defines the interface for debug request logging.
type DebugLogger interface {
	// PrepareNewRequest initialises the logger for a new request.
	// In "all" mode it clears the output directory.
	// In "errors" mode it clears internal buffers.
	// In both modes it sets up application log capture.
	PrepareNewRequest()

	// LogRequestBody records the client request body (OpenAI/Anthropic format).
	LogRequestBody(body []byte)

	// LogKiroRequestBody records the transformed Kiro API request body.
	LogKiroRequestBody(body []byte)

	// LogRawChunk appends a raw response chunk from the upstream provider.
	LogRawChunk(chunk []byte)

	// LogModifiedChunk appends a modified response chunk sent to the client.
	LogModifiedChunk(chunk []byte)

	// LogAppMessage records an application log line during request processing.
	LogAppMessage(msg string)

	// FlushOnError writes all buffered data to files on error responses.
	// In "errors" mode it flushes buffers; in "all" mode it only writes
	// error_info.json and app logs.
	FlushOnError(statusCode int, errorMessage string)

	// DiscardBuffers clears buffers without writing to files.
	// Called when a request completes successfully in "errors" mode.
	// In "all" mode it writes app logs for the successful request.
	DiscardBuffers()
}

// ---------------------------------------------------------------------------
// noopLogger — used when debug mode is "off"
// ---------------------------------------------------------------------------

type noopLogger struct{}

func (noopLogger) PrepareNewRequest()           {}
func (noopLogger) LogRequestBody(_ []byte)      {}
func (noopLogger) LogKiroRequestBody(_ []byte)  {}
func (noopLogger) LogRawChunk(_ []byte)         {}
func (noopLogger) LogModifiedChunk(_ []byte)    {}
func (noopLogger) LogAppMessage(_ string)       {}
func (noopLogger) FlushOnError(_ int, _ string) {}
func (noopLogger) DiscardBuffers()              {}

// ---------------------------------------------------------------------------
// debugLogger — active implementation for "errors" and "all" modes
// ---------------------------------------------------------------------------

type debugLogger struct {
	mu       sync.Mutex
	debugDir string
	mode     string // "errors" or "all"

	// Buffers for "errors" mode.
	requestBodyBuf     []byte
	kiroRequestBodyBuf []byte
	rawChunksBuf       []byte
	modifiedChunksBuf  []byte
	appLogsBuf         bytes.Buffer
}

// NewDebugLogger creates a DebugLogger for the given mode and output directory.
// Returns a no-op logger when mode is "off" or unrecognised.
func NewDebugLogger(mode, debugDir string) DebugLogger {
	switch mode {
	case "errors", "all":
		return &debugLogger{
			debugDir: debugDir,
			mode:     mode,
		}
	default:
		return noopLogger{}
	}
}

// ---------------------------------------------------------------------------
// PrepareNewRequest
// ---------------------------------------------------------------------------

func (d *debugLogger) PrepareNewRequest() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.clearBuffers()

	if d.mode == "all" {
		// Clear and recreate the output directory.
		if err := os.RemoveAll(d.debugDir); err != nil {
			log.Error().Err(err).Str("dir", d.debugDir).Msg("[DebugLogger] error removing directory")
		}
		if err := os.MkdirAll(d.debugDir, 0o755); err != nil {
			log.Error().Err(err).Str("dir", d.debugDir).Msg("[DebugLogger] error creating directory")
		}
		log.Debug().Str("dir", d.debugDir).Msg("[DebugLogger] directory cleared for new request")
	}
}

// ---------------------------------------------------------------------------
// LogRequestBody
// ---------------------------------------------------------------------------

func (d *debugLogger) LogRequestBody(body []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.mode == "all" {
		d.writeJSONFile("request_body.json", body)
	} else {
		d.requestBodyBuf = copyBytes(body)
	}
}

// ---------------------------------------------------------------------------
// LogKiroRequestBody
// ---------------------------------------------------------------------------

func (d *debugLogger) LogKiroRequestBody(body []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.mode == "all" {
		d.writeJSONFile("kiro_request_body.json", body)
	} else {
		d.kiroRequestBodyBuf = copyBytes(body)
	}
}

// ---------------------------------------------------------------------------
// LogRawChunk
// ---------------------------------------------------------------------------

func (d *debugLogger) LogRawChunk(chunk []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.mode == "all" {
		d.appendFile("response_stream_raw.txt", chunk)
	} else {
		d.rawChunksBuf = append(d.rawChunksBuf, chunk...)
	}
}

// ---------------------------------------------------------------------------
// LogModifiedChunk
// ---------------------------------------------------------------------------

func (d *debugLogger) LogModifiedChunk(chunk []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.mode == "all" {
		d.appendFile("response_stream_modified.txt", chunk)
	} else {
		d.modifiedChunksBuf = append(d.modifiedChunksBuf, chunk...)
	}
}

// ---------------------------------------------------------------------------
// LogAppMessage
// ---------------------------------------------------------------------------

func (d *debugLogger) LogAppMessage(msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.appLogsBuf.WriteString(msg)
	d.appLogsBuf.WriteByte('\n')
}

// ---------------------------------------------------------------------------
// FlushOnError
// ---------------------------------------------------------------------------

func (d *debugLogger) FlushOnError(statusCode int, errorMessage string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.mode == "all" {
		// Data already written; just add error info and app logs.
		d.writeErrorInfo(statusCode, errorMessage)
		d.writeAppLogs()
		d.clearAppLogsBuf()
		return
	}

	// "errors" mode — check if there is anything to flush.
	if len(d.requestBodyBuf) == 0 &&
		len(d.kiroRequestBodyBuf) == 0 &&
		len(d.rawChunksBuf) == 0 &&
		len(d.modifiedChunksBuf) == 0 {
		return
	}

	// Recreate directory.
	_ = os.RemoveAll(d.debugDir)
	if err := os.MkdirAll(d.debugDir, 0o755); err != nil {
		log.Error().Err(err).Msg("[DebugLogger] error creating directory for flush")
		return
	}

	if len(d.requestBodyBuf) > 0 {
		d.writeJSONFile("request_body.json", d.requestBodyBuf)
	}
	if len(d.kiroRequestBodyBuf) > 0 {
		d.writeJSONFile("kiro_request_body.json", d.kiroRequestBodyBuf)
	}
	if len(d.rawChunksBuf) > 0 {
		d.writeRawFile("response_stream_raw.txt", d.rawChunksBuf)
	}
	if len(d.modifiedChunksBuf) > 0 {
		d.writeRawFile("response_stream_modified.txt", d.modifiedChunksBuf)
	}

	d.writeErrorInfo(statusCode, errorMessage)
	d.writeAppLogs()

	log.Info().Str("dir", d.debugDir).Int("status", statusCode).Msg("[DebugLogger] error logs flushed")

	d.clearBuffers()
}

// ---------------------------------------------------------------------------
// DiscardBuffers
// ---------------------------------------------------------------------------

func (d *debugLogger) DiscardBuffers() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.mode == "errors" {
		d.clearBuffers()
	} else if d.mode == "all" {
		// In "all" mode, save app logs even for successful requests.
		d.writeAppLogs()
		d.clearAppLogsBuf()
	}
}

// ---------------------------------------------------------------------------
// Internal helpers (must be called with d.mu held)
// ---------------------------------------------------------------------------

func (d *debugLogger) clearBuffers() {
	d.requestBodyBuf = nil
	d.kiroRequestBodyBuf = nil
	d.rawChunksBuf = nil
	d.modifiedChunksBuf = nil
	d.appLogsBuf.Reset()
}

func (d *debugLogger) clearAppLogsBuf() {
	d.appLogsBuf.Reset()
}

// writeJSONFile writes body to a file, pretty-printing if it is valid JSON.
func (d *debugLogger) writeJSONFile(name string, body []byte) {
	path := filepath.Join(d.debugDir, name)
	_ = os.MkdirAll(d.debugDir, 0o755)

	var pretty bytes.Buffer
	if json.Indent(&pretty, body, "", "  ") == nil {
		_ = os.WriteFile(path, pretty.Bytes(), 0o644)
	} else {
		_ = os.WriteFile(path, body, 0o644)
	}
}

// writeRawFile writes raw bytes to a file.
func (d *debugLogger) writeRawFile(name string, data []byte) {
	path := filepath.Join(d.debugDir, name)
	_ = os.MkdirAll(d.debugDir, 0o755)
	_ = os.WriteFile(path, data, 0o644)
}

// appendFile appends data to a file, creating it if necessary.
func (d *debugLogger) appendFile(name string, data []byte) {
	path := filepath.Join(d.debugDir, name)
	_ = os.MkdirAll(d.debugDir, 0o755)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(data)
}

// writeErrorInfo writes error_info.json with status code and message.
func (d *debugLogger) writeErrorInfo(statusCode int, errorMessage string) {
	_ = os.MkdirAll(d.debugDir, 0o755)

	info := map[string]any{
		"status_code":   statusCode,
		"error_message": errorMessage,
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(d.debugDir, "error_info.json")
	_ = os.WriteFile(path, data, 0o644)
}

// writeAppLogs writes the captured application logs to app_logs.txt.
func (d *debugLogger) writeAppLogs() {
	content := d.appLogsBuf.String()
	if len(content) == 0 {
		return
	}
	_ = os.MkdirAll(d.debugDir, 0o755)
	path := filepath.Join(d.debugDir, "app_logs.txt")
	_ = os.WriteFile(path, []byte(content), 0o644)
}

// copyBytes returns a copy of b so the caller can reuse the original slice.
func copyBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	c := make([]byte, len(b))
	copy(c, b)
	return c
}
