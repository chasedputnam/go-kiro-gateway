package errors

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"
)

// =========================================================================
// Network error classification tests
// =========================================================================

// ---------------------------------------------------------------------------
// DNS resolution errors
// ---------------------------------------------------------------------------

func TestClassifyNetworkError_DNSResolution(t *testing.T) {
	dnsErr := &net.DNSError{
		Err:  "no such host",
		Name: "api.example.com",
	}
	info := ClassifyNetworkError(dnsErr)

	if info.Category != CategoryDNSResolution {
		t.Errorf("category = %q, want %q", info.Category, CategoryDNSResolution)
	}
	if info.SuggestedHTTPCode != 502 {
		t.Errorf("http code = %d, want 502", info.SuggestedHTTPCode)
	}
	if !info.IsRetryable {
		t.Error("DNS errors should be retryable")
	}
	if len(info.TroubleshootingSteps) == 0 {
		t.Error("expected troubleshooting steps")
	}
}

func TestClassifyNetworkError_DNSWrappedInOpError(t *testing.T) {
	dnsErr := &net.DNSError{Err: "no such host", Name: "api.example.com"}
	opErr := &net.OpError{Op: "dial", Net: "tcp", Err: dnsErr}

	info := ClassifyNetworkError(opErr)
	if info.Category != CategoryDNSResolution {
		t.Errorf("category = %q, want %q", info.Category, CategoryDNSResolution)
	}
}

// ---------------------------------------------------------------------------
// Connection refused
// ---------------------------------------------------------------------------

func TestClassifyNetworkError_ConnectionRefused(t *testing.T) {
	opErr := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: errors.New("connection refused"),
	}
	info := ClassifyNetworkError(opErr)

	if info.Category != CategoryConnectionRefused {
		t.Errorf("category = %q, want %q", info.Category, CategoryConnectionRefused)
	}
	if info.SuggestedHTTPCode != 502 {
		t.Errorf("http code = %d, want 502", info.SuggestedHTTPCode)
	}
	if !info.IsRetryable {
		t.Error("connection refused should be retryable")
	}
}

func TestClassifyNetworkError_ConnectionRefusedByString(t *testing.T) {
	err := fmt.Errorf("dial tcp 127.0.0.1:443: connection refused")
	info := ClassifyNetworkError(err)

	if info.Category != CategoryConnectionRefused {
		t.Errorf("category = %q, want %q", info.Category, CategoryConnectionRefused)
	}
}

// ---------------------------------------------------------------------------
// Connection reset
// ---------------------------------------------------------------------------

func TestClassifyNetworkError_ConnectionReset(t *testing.T) {
	opErr := &net.OpError{
		Op:  "read",
		Net: "tcp",
		Err: errors.New("connection reset by peer"),
	}
	info := ClassifyNetworkError(opErr)

	if info.Category != CategoryConnectionReset {
		t.Errorf("category = %q, want %q", info.Category, CategoryConnectionReset)
	}
	if !info.IsRetryable {
		t.Error("connection reset should be retryable")
	}
}

func TestClassifyNetworkError_ConnectionResetByString(t *testing.T) {
	err := fmt.Errorf("read tcp: connection reset by peer")
	info := ClassifyNetworkError(err)

	if info.Category != CategoryConnectionReset {
		t.Errorf("category = %q, want %q", info.Category, CategoryConnectionReset)
	}
}

// ---------------------------------------------------------------------------
// Network unreachable
// ---------------------------------------------------------------------------

func TestClassifyNetworkError_NetworkUnreachable(t *testing.T) {
	opErr := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: errors.New("network is unreachable"),
	}
	info := ClassifyNetworkError(opErr)

	if info.Category != CategoryNetworkUnreachable {
		t.Errorf("category = %q, want %q", info.Category, CategoryNetworkUnreachable)
	}
	if info.SuggestedHTTPCode != 502 {
		t.Errorf("http code = %d, want 502", info.SuggestedHTTPCode)
	}
}

func TestClassifyNetworkError_NoRouteToHost(t *testing.T) {
	err := fmt.Errorf("dial tcp: no route to host")
	info := ClassifyNetworkError(err)

	if info.Category != CategoryNetworkUnreachable {
		t.Errorf("category = %q, want %q", info.Category, CategoryNetworkUnreachable)
	}
}

// ---------------------------------------------------------------------------
// Connect timeout
// ---------------------------------------------------------------------------

func TestClassifyNetworkError_ConnectTimeout(t *testing.T) {
	// net.OpError with Timeout() = true and "dial" in the message.
	opErr := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: &timeoutError{msg: "i/o timeout"},
	}
	info := ClassifyNetworkError(opErr)

	if info.Category != CategoryTimeoutConnect {
		t.Errorf("category = %q, want %q", info.Category, CategoryTimeoutConnect)
	}
	if info.SuggestedHTTPCode != 504 {
		t.Errorf("http code = %d, want 504", info.SuggestedHTTPCode)
	}
	if !info.IsRetryable {
		t.Error("connect timeout should be retryable")
	}
}

// ---------------------------------------------------------------------------
// Read timeout
// ---------------------------------------------------------------------------

func TestClassifyNetworkError_ReadTimeout(t *testing.T) {
	opErr := &net.OpError{
		Op:  "read",
		Net: "tcp",
		Err: &timeoutError{msg: "i/o timeout"},
	}
	info := ClassifyNetworkError(opErr)

	if info.Category != CategoryTimeoutRead {
		t.Errorf("category = %q, want %q", info.Category, CategoryTimeoutRead)
	}
	if info.SuggestedHTTPCode != 504 {
		t.Errorf("http code = %d, want 504", info.SuggestedHTTPCode)
	}
}

// ---------------------------------------------------------------------------
// SSL/TLS errors
// ---------------------------------------------------------------------------

func TestClassifyNetworkError_TLSRecordHeaderError(t *testing.T) {
	tlsErr := &tls.RecordHeaderError{Msg: "bad record"}
	info := ClassifyNetworkError(tlsErr)

	if info.Category != CategorySSLError {
		t.Errorf("category = %q, want %q", info.Category, CategorySSLError)
	}
	if info.IsRetryable {
		t.Error("SSL errors should not be retryable")
	}
	if info.SuggestedHTTPCode != 502 {
		t.Errorf("http code = %d, want 502", info.SuggestedHTTPCode)
	}
}

func TestClassifyNetworkError_CertificateError(t *testing.T) {
	err := fmt.Errorf("x509: certificate signed by unknown authority")
	info := ClassifyNetworkError(err)

	if info.Category != CategorySSLError {
		t.Errorf("category = %q, want %q", info.Category, CategorySSLError)
	}
}

func TestClassifyNetworkError_TLSHandshakeError(t *testing.T) {
	err := fmt.Errorf("tls: handshake failure")
	info := ClassifyNetworkError(err)

	if info.Category != CategorySSLError {
		t.Errorf("category = %q, want %q", info.Category, CategorySSLError)
	}
}

// ---------------------------------------------------------------------------
// Proxy errors
// ---------------------------------------------------------------------------

func TestClassifyNetworkError_ProxyError(t *testing.T) {
	inner := errors.New("proxyconnect tcp: dial tcp proxy.example.com:8080: connection refused")
	urlErr := &url.Error{Op: "proxyconnect", URL: "https://api.example.com", Err: inner}

	info := ClassifyNetworkError(urlErr)

	if info.Category != CategoryProxyError {
		t.Errorf("category = %q, want %q", info.Category, CategoryProxyError)
	}
	if !info.IsRetryable {
		t.Error("proxy errors should be retryable")
	}
}

func TestClassifyNetworkError_ProxyByString(t *testing.T) {
	err := fmt.Errorf("proxy connection failed: timeout")
	info := ClassifyNetworkError(err)

	if info.Category != CategoryProxyError {
		t.Errorf("category = %q, want %q", info.Category, CategoryProxyError)
	}
}

// ---------------------------------------------------------------------------
// Unknown / fallback
// ---------------------------------------------------------------------------

func TestClassifyNetworkError_UnknownError(t *testing.T) {
	err := fmt.Errorf("something completely unexpected happened")
	info := ClassifyNetworkError(err)

	if info.Category != CategoryUnknown {
		t.Errorf("category = %q, want %q", info.Category, CategoryUnknown)
	}
	if !info.IsRetryable {
		t.Error("unknown errors should be retryable")
	}
}

func TestClassifyNetworkError_NilError(t *testing.T) {
	info := ClassifyNetworkError(nil)

	if info.Category != CategoryUnknown {
		t.Errorf("category = %q, want %q", info.Category, CategoryUnknown)
	}
	if info.IsRetryable {
		t.Error("nil error should not be retryable")
	}
}

// ---------------------------------------------------------------------------
// FormatUserMessage
// ---------------------------------------------------------------------------

func TestNetworkErrorInfo_FormatUserMessage(t *testing.T) {
	info := &NetworkErrorInfo{
		UserMessage: "Connection refused.",
		TroubleshootingSteps: []string{
			"Check the service",
			"Try again later",
		},
	}

	msg := info.FormatUserMessage()
	if msg == "" {
		t.Fatal("expected non-empty message")
	}
	if !containsAll(msg, "Connection refused", "1. Check the service", "2. Try again later") {
		t.Errorf("formatted message missing expected content: %s", msg)
	}
}

func TestNetworkErrorInfo_FormatUserMessage_NoSteps(t *testing.T) {
	info := &NetworkErrorInfo{
		UserMessage:          "Something failed.",
		TroubleshootingSteps: nil,
	}

	msg := info.FormatUserMessage()
	if msg != "Something failed." {
		t.Errorf("expected plain message, got: %s", msg)
	}
}

// =========================================================================
// Kiro API error enhancement tests
// =========================================================================

func TestEnhanceKiroError_ContentLengthExceedsThreshold(t *testing.T) {
	errorJSON := map[string]any{
		"message": "Input is too long.",
		"reason":  "CONTENT_LENGTH_EXCEEDS_THRESHOLD",
	}

	info := EnhanceKiroError(errorJSON)

	if info.Reason != "CONTENT_LENGTH_EXCEEDS_THRESHOLD" {
		t.Errorf("reason = %q, want CONTENT_LENGTH_EXCEEDS_THRESHOLD", info.Reason)
	}
	if info.UserMessage != "Model context limit reached. Conversation size exceeds model capacity." {
		t.Errorf("user message = %q", info.UserMessage)
	}
	if info.OriginalMessage != "Input is too long." {
		t.Errorf("original message = %q", info.OriginalMessage)
	}
}

func TestEnhanceKiroError_MonthlyRequestCount(t *testing.T) {
	errorJSON := map[string]any{
		"message": "Limit exceeded.",
		"reason":  "MONTHLY_REQUEST_COUNT",
	}

	info := EnhanceKiroError(errorJSON)

	if info.Reason != "MONTHLY_REQUEST_COUNT" {
		t.Errorf("reason = %q, want MONTHLY_REQUEST_COUNT", info.Reason)
	}
	if info.UserMessage != "Monthly request limit exceeded. Account has reached its monthly quota." {
		t.Errorf("user message = %q", info.UserMessage)
	}
}

func TestEnhanceKiroError_UnknownReasonWithCode(t *testing.T) {
	errorJSON := map[string]any{
		"message": "Something went wrong.",
		"reason":  "SOME_NEW_REASON",
	}

	info := EnhanceKiroError(errorJSON)

	expected := "Something went wrong. (reason: SOME_NEW_REASON)"
	if info.UserMessage != expected {
		t.Errorf("user message = %q, want %q", info.UserMessage, expected)
	}
}

func TestEnhanceKiroError_NoReason(t *testing.T) {
	errorJSON := map[string]any{
		"message": "Something went wrong.",
	}

	info := EnhanceKiroError(errorJSON)

	if info.Reason != "UNKNOWN" {
		t.Errorf("reason = %q, want UNKNOWN", info.Reason)
	}
	if info.UserMessage != "Something went wrong." {
		t.Errorf("user message = %q, want original message", info.UserMessage)
	}
}

func TestEnhanceKiroError_NoMessage(t *testing.T) {
	errorJSON := map[string]any{
		"reason": "CONTENT_LENGTH_EXCEEDS_THRESHOLD",
	}

	info := EnhanceKiroError(errorJSON)

	if info.OriginalMessage != "Unknown error" {
		t.Errorf("original message = %q, want 'Unknown error'", info.OriginalMessage)
	}
	// Known reason should still produce the enhanced message.
	if info.UserMessage != "Model context limit reached. Conversation size exceeds model capacity." {
		t.Errorf("user message = %q", info.UserMessage)
	}
}

func TestEnhanceKiroError_EmptyMap(t *testing.T) {
	errorJSON := map[string]any{}

	info := EnhanceKiroError(errorJSON)

	if info.Reason != "UNKNOWN" {
		t.Errorf("reason = %q, want UNKNOWN", info.Reason)
	}
	if info.OriginalMessage != "Unknown error" {
		t.Errorf("original message = %q", info.OriginalMessage)
	}
	if info.UserMessage != "Unknown error" {
		t.Errorf("user message = %q", info.UserMessage)
	}
}

func TestEnhanceKiroError_NonStringValues(t *testing.T) {
	// message and reason are non-string types — should fall back to defaults.
	errorJSON := map[string]any{
		"message": 42,
		"reason":  true,
	}

	info := EnhanceKiroError(errorJSON)

	if info.OriginalMessage != "Unknown error" {
		t.Errorf("original message = %q, want 'Unknown error'", info.OriginalMessage)
	}
	if info.Reason != "UNKNOWN" {
		t.Errorf("reason = %q, want UNKNOWN", info.Reason)
	}
}

// =========================================================================
// Validation error formatting tests
// =========================================================================

// ---------------------------------------------------------------------------
// OpenAI format
// ---------------------------------------------------------------------------

func TestOpenAIErrorResponse_Structure(t *testing.T) {
	body := OpenAIErrorResponse("test message", "test_type", "test_code")

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	errObj, ok := parsed["error"].(map[string]any)
	if !ok {
		t.Fatal("expected 'error' object in response")
	}

	if errObj["message"] != "test message" {
		t.Errorf("message = %v", errObj["message"])
	}
	if errObj["type"] != "test_type" {
		t.Errorf("type = %v", errObj["type"])
	}
	if errObj["code"] != "test_code" {
		t.Errorf("code = %v", errObj["code"])
	}
	if errObj["param"] != nil {
		t.Errorf("param = %v, want nil", errObj["param"])
	}
}

func TestOpenAIValidationError(t *testing.T) {
	body := OpenAIValidationError("model field is required")

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	errObj := parsed["error"].(map[string]any)
	if errObj["type"] != "invalid_request_error" {
		t.Errorf("type = %v, want invalid_request_error", errObj["type"])
	}
	if errObj["code"] != "validation_error" {
		t.Errorf("code = %v, want validation_error", errObj["code"])
	}
}

func TestOpenAINetworkError(t *testing.T) {
	info := &NetworkErrorInfo{
		Category:             CategoryDNSResolution,
		UserMessage:          "DNS failed.",
		TroubleshootingSteps: []string{"Check DNS"},
	}

	body := OpenAINetworkError(info)

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	errObj := parsed["error"].(map[string]any)
	if errObj["type"] != "connectivity_error" {
		t.Errorf("type = %v, want connectivity_error", errObj["type"])
	}
	if errObj["code"] != "dns_resolution" {
		t.Errorf("code = %v, want dns_resolution", errObj["code"])
	}
}

func TestOpenAIKiroError(t *testing.T) {
	info := &KiroErrorInfo{
		Reason:      "CONTENT_LENGTH_EXCEEDS_THRESHOLD",
		UserMessage: "Model context limit reached.",
	}

	body := OpenAIKiroError(info, 400)

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	errObj := parsed["error"].(map[string]any)
	if errObj["type"] != "api_error" {
		t.Errorf("type = %v, want api_error", errObj["type"])
	}
	// JSON numbers are float64.
	if code, ok := errObj["code"].(float64); !ok || int(code) != 400 {
		t.Errorf("code = %v, want 400", errObj["code"])
	}
}

// ---------------------------------------------------------------------------
// Anthropic format
// ---------------------------------------------------------------------------

func TestAnthropicErrorResponse_Structure(t *testing.T) {
	body := AnthropicErrorResponse("test message", "test_type")

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if parsed["type"] != "error" {
		t.Errorf("top-level type = %v, want 'error'", parsed["type"])
	}

	errObj, ok := parsed["error"].(map[string]any)
	if !ok {
		t.Fatal("expected 'error' object in response")
	}

	if errObj["message"] != "test message" {
		t.Errorf("message = %v", errObj["message"])
	}
	if errObj["type"] != "test_type" {
		t.Errorf("type = %v", errObj["type"])
	}
}

func TestAnthropicValidationError(t *testing.T) {
	body := AnthropicValidationError("max_tokens is required")

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	errObj := parsed["error"].(map[string]any)
	if errObj["type"] != "invalid_request_error" {
		t.Errorf("type = %v, want invalid_request_error", errObj["type"])
	}
}

func TestAnthropicNetworkError(t *testing.T) {
	info := &NetworkErrorInfo{
		Category:             CategoryConnectionRefused,
		UserMessage:          "Connection refused.",
		TroubleshootingSteps: []string{"Check service"},
	}

	body := AnthropicNetworkError(info)

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if parsed["type"] != "error" {
		t.Errorf("top-level type = %v, want 'error'", parsed["type"])
	}
	errObj := parsed["error"].(map[string]any)
	if errObj["type"] != "connectivity_error" {
		t.Errorf("type = %v, want connectivity_error", errObj["type"])
	}
}

func TestAnthropicKiroError(t *testing.T) {
	info := &KiroErrorInfo{
		Reason:      "MONTHLY_REQUEST_COUNT",
		UserMessage: "Monthly limit exceeded.",
	}

	body := AnthropicKiroError(info)

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	errObj := parsed["error"].(map[string]any)
	if errObj["type"] != "api_error" {
		t.Errorf("type = %v, want api_error", errObj["type"])
	}
	if errObj["message"] != "Monthly limit exceeded." {
		t.Errorf("message = %v", errObj["message"])
	}
}

// =========================================================================
// Error sanitization tests
// =========================================================================

func TestSanitizeErrorMessage_RemovesBytesLiteral(t *testing.T) {
	msg := "invalid input: b'\\x00\\x01\\x02' in field"
	sanitized := SanitizeErrorMessage(msg)

	if containsAny(sanitized, "b'", "\\x00") {
		t.Errorf("bytes literal not removed: %s", sanitized)
	}
	if !containsAll(sanitized, "[binary data]") {
		t.Errorf("expected [binary data] placeholder, got: %s", sanitized)
	}
}

func TestSanitizeErrorMessage_RemovesGoByteSlice(t *testing.T) {
	msg := "parse error: []byte{0x48, 0x65} unexpected"
	sanitized := SanitizeErrorMessage(msg)

	if containsAny(sanitized, "[]byte{") {
		t.Errorf("Go byte slice not removed: %s", sanitized)
	}
}

func TestSanitizeErrorMessage_RemovesInternalPaths(t *testing.T) {
	msg := "error at /home/user/project/internal/server/handler.go:42: bad request"
	sanitized := SanitizeErrorMessage(msg)

	if containsAny(sanitized, "/home/user", "handler.go") {
		t.Errorf("internal path not removed: %s", sanitized)
	}
}

func TestSanitizeErrorMessage_PreservesNormalMessage(t *testing.T) {
	msg := "model field is required"
	sanitized := SanitizeErrorMessage(msg)

	if sanitized != msg {
		t.Errorf("normal message was modified: %q → %q", msg, sanitized)
	}
}

func TestSanitizeErrorMessage_TrimsWhitespace(t *testing.T) {
	msg := "  some error  "
	sanitized := SanitizeErrorMessage(msg)

	if sanitized != "some error" {
		t.Errorf("whitespace not trimmed: %q", sanitized)
	}
}

// =========================================================================
// FormatValidationErrors tests
// =========================================================================

func TestFormatValidationErrors_MultipleIssues(t *testing.T) {
	issues := []map[string]string{
		{"field": "model", "message": "is required"},
		{"field": "messages", "message": "must not be empty"},
	}

	result := FormatValidationErrors(issues)

	if !containsAll(result, "model: is required", "messages: must not be empty") {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestFormatValidationErrors_EmptyList(t *testing.T) {
	result := FormatValidationErrors(nil)
	if result != "Request validation failed" {
		t.Errorf("expected fallback message, got: %s", result)
	}
}

func TestFormatValidationErrors_MessageOnly(t *testing.T) {
	issues := []map[string]string{
		{"message": "invalid JSON body"},
	}

	result := FormatValidationErrors(issues)
	if result != "invalid JSON body" {
		t.Errorf("expected message only, got: %s", result)
	}
}

// =========================================================================
// Test helpers
// =========================================================================

// timeoutError is a test helper that implements net.Error with Timeout() = true.
type timeoutError struct {
	msg string
}

func (e *timeoutError) Error() string   { return e.msg }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }

// containsAll returns true if s contains all of the given substrings.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

// containsAny returns true if s contains any of the given substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if contains(s, sub) {
			return true
		}
	}
	return false
}

// contains is a simple string-contains check (avoids importing strings in tests).
func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
