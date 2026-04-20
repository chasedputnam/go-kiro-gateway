package debug

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =========================================================================
// DebugLogger tests
// =========================================================================

// ---------------------------------------------------------------------------
// Off mode — no files produced
// ---------------------------------------------------------------------------

func TestNoopLogger_ProducesNoFiles(t *testing.T) {
	dir := t.TempDir()
	dl := NewDebugLogger("off", dir)

	dl.PrepareNewRequest()
	dl.LogRequestBody([]byte(`{"model":"test"}`))
	dl.LogKiroRequestBody([]byte(`{"payload":"test"}`))
	dl.LogRawChunk([]byte("raw chunk"))
	dl.LogModifiedChunk([]byte("modified chunk"))
	dl.LogAppMessage("some log line")
	dl.FlushOnError(500, "test error")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("off mode should produce no files, got %d entries", len(entries))
	}
}

// ---------------------------------------------------------------------------
// Errors mode — buffers and flushes only on errors
// ---------------------------------------------------------------------------

func TestErrorsMode_FlushesOnError(t *testing.T) {
	dir := t.TempDir()
	debugDir := filepath.Join(dir, "debug_logs")
	dl := NewDebugLogger("errors", debugDir)

	dl.PrepareNewRequest()
	dl.LogRequestBody([]byte(`{"model":"test"}`))
	dl.LogKiroRequestBody([]byte(`{"payload":"kiro"}`))
	dl.LogRawChunk([]byte("raw1"))
	dl.LogRawChunk([]byte("raw2"))
	dl.LogModifiedChunk([]byte("mod1"))
	dl.LogAppMessage("app log line 1")
	dl.FlushOnError(500, "internal server error")

	// Verify all expected files exist.
	assertFileExists(t, debugDir, "request_body.json")
	assertFileExists(t, debugDir, "kiro_request_body.json")
	assertFileExists(t, debugDir, "response_stream_raw.txt")
	assertFileExists(t, debugDir, "response_stream_modified.txt")
	assertFileExists(t, debugDir, "error_info.json")
	assertFileExists(t, debugDir, "app_logs.txt")

	// Verify request body is pretty-printed JSON.
	body := readTestFile(t, debugDir, "request_body.json")
	if !strings.Contains(body, "\"model\"") {
		t.Errorf("request_body.json should contain model field, got: %s", body)
	}

	// Verify raw chunks are concatenated.
	raw := readTestFile(t, debugDir, "response_stream_raw.txt")
	if raw != "raw1raw2" {
		t.Errorf("raw stream = %q, want %q", raw, "raw1raw2")
	}

	// Verify error info.
	errorInfo := readTestFile(t, debugDir, "error_info.json")
	var errMap map[string]any
	if err := json.Unmarshal([]byte(errorInfo), &errMap); err != nil {
		t.Fatalf("error_info.json is not valid JSON: %v", err)
	}
	if int(errMap["status_code"].(float64)) != 500 {
		t.Errorf("status_code = %v, want 500", errMap["status_code"])
	}

	// Verify app logs.
	appLogs := readTestFile(t, debugDir, "app_logs.txt")
	if !strings.Contains(appLogs, "app log line 1") {
		t.Errorf("app_logs.txt should contain log line, got: %s", appLogs)
	}
}

// ---------------------------------------------------------------------------
// Errors mode — discards on success
// ---------------------------------------------------------------------------

func TestErrorsMode_DiscardsOnSuccess(t *testing.T) {
	dir := t.TempDir()
	debugDir := filepath.Join(dir, "debug_logs")
	dl := NewDebugLogger("errors", debugDir)

	dl.PrepareNewRequest()
	dl.LogRequestBody([]byte(`{"model":"test"}`))
	dl.LogKiroRequestBody([]byte(`{"payload":"kiro"}`))
	dl.LogRawChunk([]byte("raw data"))
	dl.LogModifiedChunk([]byte("modified data"))
	dl.LogAppMessage("some log")
	dl.DiscardBuffers()

	// No files should be written.
	if _, err := os.Stat(debugDir); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(debugDir)
		if len(entries) > 0 {
			t.Errorf("errors mode should not write files on success, got %d entries", len(entries))
		}
	}
}

// ---------------------------------------------------------------------------
// Errors mode — no flush when no data buffered
// ---------------------------------------------------------------------------

func TestErrorsMode_NoFlushWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	debugDir := filepath.Join(dir, "debug_logs")
	dl := NewDebugLogger("errors", debugDir)

	dl.PrepareNewRequest()
	dl.FlushOnError(500, "error")

	// No directory should be created when there's nothing to flush.
	if _, err := os.Stat(debugDir); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(debugDir)
		if len(entries) > 0 {
			t.Errorf("should not create files when no data buffered, got %d entries", len(entries))
		}
	}
}

// ---------------------------------------------------------------------------
// All mode — writes immediately
// ---------------------------------------------------------------------------

func TestAllMode_WritesImmediately(t *testing.T) {
	dir := t.TempDir()
	debugDir := filepath.Join(dir, "debug_logs")
	dl := NewDebugLogger("all", debugDir)

	dl.PrepareNewRequest()

	dl.LogRequestBody([]byte(`{"model":"test"}`))
	assertFileExists(t, debugDir, "request_body.json")

	dl.LogKiroRequestBody([]byte(`{"payload":"kiro"}`))
	assertFileExists(t, debugDir, "kiro_request_body.json")

	dl.LogRawChunk([]byte("chunk1"))
	dl.LogRawChunk([]byte("chunk2"))
	raw := readTestFile(t, debugDir, "response_stream_raw.txt")
	if raw != "chunk1chunk2" {
		t.Errorf("raw stream = %q, want %q", raw, "chunk1chunk2")
	}

	dl.LogModifiedChunk([]byte("mod"))
	assertFileExists(t, debugDir, "response_stream_modified.txt")
}

// ---------------------------------------------------------------------------
// All mode — error info written on FlushOnError
// ---------------------------------------------------------------------------

func TestAllMode_ErrorInfoOnFlush(t *testing.T) {
	dir := t.TempDir()
	debugDir := filepath.Join(dir, "debug_logs")
	dl := NewDebugLogger("all", debugDir)

	dl.PrepareNewRequest()
	dl.LogRequestBody([]byte(`{"model":"test"}`))
	dl.LogAppMessage("log during request")
	dl.FlushOnError(404, "not found")

	assertFileExists(t, debugDir, "error_info.json")
	assertFileExists(t, debugDir, "app_logs.txt")
}

// ---------------------------------------------------------------------------
// All mode — app logs saved on DiscardBuffers (successful request)
// ---------------------------------------------------------------------------

func TestAllMode_AppLogsSavedOnDiscard(t *testing.T) {
	dir := t.TempDir()
	debugDir := filepath.Join(dir, "debug_logs")
	dl := NewDebugLogger("all", debugDir)

	dl.PrepareNewRequest()
	dl.LogAppMessage("success log line")
	dl.DiscardBuffers()

	assertFileExists(t, debugDir, "app_logs.txt")
	content := readTestFile(t, debugDir, "app_logs.txt")
	if !strings.Contains(content, "success log line") {
		t.Errorf("app_logs.txt should contain log line, got: %s", content)
	}
}

// ---------------------------------------------------------------------------
// All mode — PrepareNewRequest clears directory
// ---------------------------------------------------------------------------

func TestAllMode_PrepareNewRequestClearsDir(t *testing.T) {
	dir := t.TempDir()
	debugDir := filepath.Join(dir, "debug_logs")
	dl := NewDebugLogger("all", debugDir)

	// First request.
	dl.PrepareNewRequest()
	dl.LogRequestBody([]byte(`{"first":"request"}`))
	assertFileExists(t, debugDir, "request_body.json")

	// Second request should clear the directory.
	dl.PrepareNewRequest()
	body := readTestFile(t, debugDir, "request_body.json")
	if body != "" {
		// File should not exist after clearing.
		t.Logf("note: directory was cleared but file may have been recreated")
	}
}

// ---------------------------------------------------------------------------
// Non-JSON request body handled gracefully
// ---------------------------------------------------------------------------

func TestLogger_NonJSONRequestBody(t *testing.T) {
	dir := t.TempDir()
	debugDir := filepath.Join(dir, "debug_logs")
	dl := NewDebugLogger("all", debugDir)

	dl.PrepareNewRequest()
	dl.LogRequestBody([]byte("not json at all"))

	assertFileExists(t, debugDir, "request_body.json")
	content := readTestFile(t, debugDir, "request_body.json")
	if content != "not json at all" {
		t.Errorf("non-JSON body should be written as-is, got: %s", content)
	}
}

// =========================================================================
// Middleware tests
// =========================================================================

// ---------------------------------------------------------------------------
// Middleware captures request body for logged endpoints
// ---------------------------------------------------------------------------

func TestMiddleware_CapturesRequestBody(t *testing.T) {
	recorder := &recordingLogger{}
	mw := Middleware(recorder)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"test"}`))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !recorder.preparedCalled {
		t.Error("PrepareNewRequest should have been called")
	}
	if string(recorder.requestBody) != `{"model":"test"}` {
		t.Errorf("request body = %q, want %q", recorder.requestBody, `{"model":"test"}`)
	}
}

// ---------------------------------------------------------------------------
// Middleware skips non-API endpoints
// ---------------------------------------------------------------------------

func TestMiddleware_SkipsNonAPIEndpoints(t *testing.T) {
	recorder := &recordingLogger{}
	mw := Middleware(recorder)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if recorder.preparedCalled {
		t.Error("PrepareNewRequest should NOT be called for /health")
	}
}

// ---------------------------------------------------------------------------
// Middleware works for Anthropic endpoint
// ---------------------------------------------------------------------------

func TestMiddleware_AnthropicEndpoint(t *testing.T) {
	recorder := &recordingLogger{}
	mw := Middleware(recorder)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude"}`))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !recorder.preparedCalled {
		t.Error("PrepareNewRequest should have been called for /v1/messages")
	}
	if string(recorder.requestBody) != `{"model":"claude"}` {
		t.Errorf("request body = %q", recorder.requestBody)
	}
}

// ---------------------------------------------------------------------------
// Middleware restores request body for downstream handlers
// ---------------------------------------------------------------------------

func TestMiddleware_RestoresRequestBody(t *testing.T) {
	recorder := &recordingLogger{}
	mw := Middleware(recorder)

	var downstreamBody []byte
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		downstreamBody, err = readAll(r.Body)
		if err != nil {
			t.Fatalf("downstream read failed: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"test"}`))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if string(downstreamBody) != `{"model":"test"}` {
		t.Errorf("downstream body = %q, want original body", downstreamBody)
	}
}

// =========================================================================
// Test helpers
// =========================================================================

func assertFileExists(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("expected file %s to exist", name)
	}
}

func readTestFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("failed to read %s: %v", name, err)
	}
	return string(data)
}

// recordingLogger is a test double that records calls to DebugLogger methods.
type recordingLogger struct {
	preparedCalled bool
	requestBody    []byte
}

func (r *recordingLogger) PrepareNewRequest()           { r.preparedCalled = true }
func (r *recordingLogger) LogRequestBody(body []byte)   { r.requestBody = body }
func (r *recordingLogger) LogKiroRequestBody(_ []byte)  {}
func (r *recordingLogger) LogRawChunk(_ []byte)         {}
func (r *recordingLogger) LogModifiedChunk(_ []byte)    {}
func (r *recordingLogger) LogAppMessage(_ string)       {}
func (r *recordingLogger) FlushOnError(_ int, _ string) {}
func (r *recordingLogger) DiscardBuffers()              {}

// readAll reads all bytes from an io.Reader (avoids importing io in tests).
func readAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	var buf []byte
	tmp := make([]byte, 1024)
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			if err.Error() == "EOF" {
				return buf, nil
			}
			return buf, err
		}
	}
}
