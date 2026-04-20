package truncation

import (
	"strings"
	"sync"
	"testing"
)

// =========================================================================
// State cache tests
// =========================================================================

// ---------------------------------------------------------------------------
// Tool truncation — save and retrieve
// ---------------------------------------------------------------------------

func TestSaveAndGetToolTruncation(t *testing.T) {
	s := NewState()

	s.SaveToolTruncation("call_abc123", "Write", map[string]any{
		"size_bytes": 5000,
		"reason":     "missing 2 closing braces",
	})

	info := s.GetToolTruncation("call_abc123")
	if info == nil {
		t.Fatal("expected tool truncation info, got nil")
	}
	if info.ToolCallID != "call_abc123" {
		t.Errorf("tool_call_id = %q, want %q", info.ToolCallID, "call_abc123")
	}
	if info.ToolName != "Write" {
		t.Errorf("tool_name = %q, want %q", info.ToolName, "Write")
	}
	if info.TruncationInfo["size_bytes"] != 5000 {
		t.Errorf("size_bytes = %v, want 5000", info.TruncationInfo["size_bytes"])
	}
}

// ---------------------------------------------------------------------------
// Tool truncation — one-time retrieval
// ---------------------------------------------------------------------------

func TestToolTruncation_OneTimeRetrieval(t *testing.T) {
	s := NewState()

	s.SaveToolTruncation("call_xyz", "Read", map[string]any{"reason": "test"})

	// First retrieval should succeed.
	info := s.GetToolTruncation("call_xyz")
	if info == nil {
		t.Fatal("first retrieval should return info")
	}

	// Second retrieval should return nil (entry deleted).
	info = s.GetToolTruncation("call_xyz")
	if info != nil {
		t.Error("second retrieval should return nil (one-time retrieval)")
	}
}

// ---------------------------------------------------------------------------
// Tool truncation — missing key
// ---------------------------------------------------------------------------

func TestToolTruncation_MissingKey(t *testing.T) {
	s := NewState()

	info := s.GetToolTruncation("nonexistent")
	if info != nil {
		t.Error("expected nil for missing key")
	}
}

// ---------------------------------------------------------------------------
// Content truncation — save and retrieve
// ---------------------------------------------------------------------------

func TestSaveAndGetContentTruncation(t *testing.T) {
	s := NewState()

	content := "This is some truncated assistant content that was cut off mid-stream"
	hash := s.SaveContentTruncation(content)

	if hash == "" {
		t.Fatal("expected non-empty hash")
	}

	info := s.GetContentTruncation(content)
	if info == nil {
		t.Fatal("expected content truncation info, got nil")
	}
	if info.MessageHash != hash {
		t.Errorf("hash = %q, want %q", info.MessageHash, hash)
	}
	if info.ContentPreview != content {
		t.Errorf("preview = %q, want %q", info.ContentPreview, content)
	}
}

// ---------------------------------------------------------------------------
// Content truncation — one-time retrieval
// ---------------------------------------------------------------------------

func TestContentTruncation_OneTimeRetrieval(t *testing.T) {
	s := NewState()

	content := "truncated content"
	s.SaveContentTruncation(content)

	info := s.GetContentTruncation(content)
	if info == nil {
		t.Fatal("first retrieval should return info")
	}

	info = s.GetContentTruncation(content)
	if info != nil {
		t.Error("second retrieval should return nil (one-time retrieval)")
	}
}

// ---------------------------------------------------------------------------
// Content truncation — preview truncation at 200 chars
// ---------------------------------------------------------------------------

func TestContentTruncation_PreviewTruncation(t *testing.T) {
	s := NewState()

	longContent := strings.Repeat("a", 500)
	s.SaveContentTruncation(longContent)

	info := s.GetContentTruncation(longContent)
	if info == nil {
		t.Fatal("expected info")
	}
	if len(info.ContentPreview) != 200 {
		t.Errorf("preview length = %d, want 200", len(info.ContentPreview))
	}
}

// ---------------------------------------------------------------------------
// Content truncation — hash uses first 500 chars
// ---------------------------------------------------------------------------

func TestContentTruncation_HashStability(t *testing.T) {
	s := NewState()

	// Two strings that share the first 500 chars should produce the same hash.
	base := strings.Repeat("x", 500)
	content1 := base + "AAAA"
	content2 := base + "BBBB"

	hash1 := s.SaveContentTruncation(content1)
	hash2 := s.SaveContentTruncation(content2)

	if hash1 != hash2 {
		t.Errorf("hashes should match for same first 500 chars: %q vs %q", hash1, hash2)
	}
}

// ---------------------------------------------------------------------------
// CacheStats
// ---------------------------------------------------------------------------

func TestCacheStats(t *testing.T) {
	s := NewState()

	stats := s.CacheStats()
	if stats["total"] != 0 {
		t.Errorf("empty cache total = %d, want 0", stats["total"])
	}

	s.SaveToolTruncation("call_1", "Write", nil)
	s.SaveToolTruncation("call_2", "Read", nil)
	s.SaveContentTruncation("some content")

	stats = s.CacheStats()
	if stats["tool_truncations"] != 2 {
		t.Errorf("tool_truncations = %d, want 2", stats["tool_truncations"])
	}
	if stats["content_truncations"] != 1 {
		t.Errorf("content_truncations = %d, want 1", stats["content_truncations"])
	}
	if stats["total"] != 3 {
		t.Errorf("total = %d, want 3", stats["total"])
	}
}

// ---------------------------------------------------------------------------
// Thread safety
// ---------------------------------------------------------------------------

func TestState_ConcurrentAccess(t *testing.T) {
	s := NewState()
	var wg sync.WaitGroup

	// Concurrent writes.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := strings.Repeat("x", i%10+1)
			s.SaveToolTruncation(id, "Tool", nil)
			s.SaveContentTruncation(id)
		}(i)
	}

	// Concurrent reads.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := strings.Repeat("x", i%10+1)
			s.GetToolTruncation(id)
			s.GetContentTruncation(id)
			s.CacheStats()
		}(i)
	}

	wg.Wait()
	// No panic or data race = pass.
}

// =========================================================================
// Truncation detection tests
// =========================================================================

// ---------------------------------------------------------------------------
// Content truncation detection
// ---------------------------------------------------------------------------

func TestDetectContentTruncation(t *testing.T) {
	tests := []struct {
		name                string
		hasCompletionSignal bool
		contentProduced     bool
		want                bool
	}{
		{"no signal, content produced", false, true, true},
		{"has signal, content produced", true, true, false},
		{"no signal, no content", false, false, false},
		{"has signal, no content", true, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectContentTruncation(tt.hasCompletionSignal, tt.contentProduced)
			if got != tt.want {
				t.Errorf("DetectContentTruncation(%v, %v) = %v, want %v",
					tt.hasCompletionSignal, tt.contentProduced, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tool call truncation detection
// ---------------------------------------------------------------------------

func TestDetectToolCallTruncation(t *testing.T) {
	tests := []struct {
		name      string
		arguments string
		want      bool
	}{
		{"empty string", "", false},
		{"empty object", "{}", false},
		{"valid JSON object", `{"key": "value"}`, false},
		{"valid nested", `{"a": {"b": [1, 2]}}`, false},
		{"unbalanced open brace", `{"key": "value"`, true},
		{"unbalanced close brace", `"key": "value"}`, true},
		{"unclosed string", `{"key": "value`, true},
		{"unbalanced bracket", `{"arr": [1, 2`, true},
		{"deeply nested unbalanced", `{"a": {"b": {"c": "d"`, true},
		{"whitespace only", "   ", false},
		{"escaped quote in string", `{"key": "val\"ue"}`, false},
		{"backslash at end of string", `{"key": "value\\"}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectToolCallTruncation(tt.arguments)
			if got != tt.want {
				t.Errorf("DetectToolCallTruncation(%q) = %v, want %v",
					tt.arguments, got, tt.want)
			}
		})
	}
}

// =========================================================================
// Recovery message tests
// =========================================================================

// ---------------------------------------------------------------------------
// Synthetic tool result
// ---------------------------------------------------------------------------

func TestGenerateTruncationToolResult(t *testing.T) {
	result := GenerateTruncationToolResult("Write", "call_123", map[string]any{
		"size_bytes": 5000,
		"reason":     "missing 2 closing braces",
	})

	if result["type"] != "tool_result" {
		t.Errorf("type = %v, want tool_result", result["type"])
	}
	if result["tool_use_id"] != "call_123" {
		t.Errorf("tool_use_id = %v, want call_123", result["tool_use_id"])
	}
	if result["is_error"] != true {
		t.Errorf("is_error = %v, want true", result["is_error"])
	}

	content, ok := result["content"].(string)
	if !ok {
		t.Fatal("content should be a string")
	}
	if !strings.Contains(content, "[API Limitation]") {
		t.Error("content should contain [API Limitation] prefix")
	}
	if !strings.Contains(content, "truncated") {
		t.Error("content should mention truncation")
	}
	if !strings.Contains(content, "adapting your approach") {
		t.Error("content should suggest adapting approach")
	}
}

// ---------------------------------------------------------------------------
// Synthetic user message
// ---------------------------------------------------------------------------

func TestGenerateTruncationUserMessage(t *testing.T) {
	msg := GenerateTruncationUserMessage()

	if !strings.Contains(msg, "[System Notice]") {
		t.Error("message should contain [System Notice] prefix")
	}
	if !strings.Contains(msg, "truncated") {
		t.Error("message should mention truncation")
	}
	if !strings.Contains(msg, "adapt your approach") {
		t.Error("message should suggest adapting approach")
	}
}

// ---------------------------------------------------------------------------
// Prepend tool result notice
// ---------------------------------------------------------------------------

func TestPrependToolResultNotice_WithContent(t *testing.T) {
	result := PrependToolResultNotice("original tool output")

	if !strings.HasPrefix(result, TruncationToolResultNotice) {
		t.Error("result should start with truncation notice")
	}
	if !strings.Contains(result, "original tool output") {
		t.Error("result should contain original content")
	}
	if !strings.Contains(result, "---") {
		t.Error("result should contain separator")
	}
}

func TestPrependToolResultNotice_EmptyContent(t *testing.T) {
	result := PrependToolResultNotice("")

	if result != TruncationToolResultNotice {
		t.Errorf("expected just the notice, got: %s", result)
	}
}

// ---------------------------------------------------------------------------
// System prompt addition
// ---------------------------------------------------------------------------

func TestSystemPromptAddition(t *testing.T) {
	addition := SystemPromptAddition()

	if !strings.Contains(addition, "[System]") {
		t.Error("addition should contain [System] prefix")
	}
	if !strings.Contains(addition, "[API Limitation]") {
		t.Error("addition should reference [API Limitation] messages")
	}
	if !strings.Contains(addition, "[System Notice]") {
		t.Error("addition should reference [System Notice] messages")
	}
}
