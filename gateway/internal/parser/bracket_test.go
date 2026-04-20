package parser

import (
	"testing"
)

// --- ParseBracketToolCalls: basic parsing -----------------------------------

func TestParseBracketToolCalls_SingleCall(t *testing.T) {
	text := `[Called get_weather with args: {"city": "London"}]`
	calls := ParseBracketToolCalls(text)

	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "get_weather" {
		t.Errorf("expected name 'get_weather', got %q", calls[0].Name)
	}
	if calls[0].Arguments != `{"city": "London"}` {
		t.Errorf("expected arguments '{\"city\": \"London\"}', got %q", calls[0].Arguments)
	}
}

func TestParseBracketToolCalls_MultipleCallsInText(t *testing.T) {
	text := `Some text before [Called read_file with args: {"path": "main.go"}] ` +
		`and then [Called write_file with args: {"path": "out.txt", "content": "hello"}] done.`
	calls := ParseBracketToolCalls(text)

	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Name != "read_file" {
		t.Errorf("call 0: expected name 'read_file', got %q", calls[0].Name)
	}
	if calls[1].Name != "write_file" {
		t.Errorf("call 1: expected name 'write_file', got %q", calls[1].Name)
	}
}

func TestParseBracketToolCalls_NestedJSONArgs(t *testing.T) {
	text := `[Called create_config with args: {"settings": {"debug": true, "level": 3}}]`
	calls := ParseBracketToolCalls(text)

	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "create_config" {
		t.Errorf("expected name 'create_config', got %q", calls[0].Name)
	}
	expected := `{"settings": {"debug": true, "level": 3}}`
	if calls[0].Arguments != expected {
		t.Errorf("expected arguments %q, got %q", expected, calls[0].Arguments)
	}
}

func TestParseBracketToolCalls_EmptyArgs(t *testing.T) {
	text := `[Called do_something with args: {}]`
	calls := ParseBracketToolCalls(text)

	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Arguments != `{}` {
		t.Errorf("expected empty object '{}', got %q", calls[0].Arguments)
	}
}

func TestParseBracketToolCalls_ArgsWithArrays(t *testing.T) {
	text := `[Called process with args: {"items": ["a", "b", "c"], "count": 3}]`
	calls := ParseBracketToolCalls(text)

	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	expected := `{"items": ["a", "b", "c"], "count": 3}`
	if calls[0].Arguments != expected {
		t.Errorf("expected arguments %q, got %q", expected, calls[0].Arguments)
	}
}

// --- ParseBracketToolCalls: case insensitivity ------------------------------

func TestParseBracketToolCalls_CaseInsensitive(t *testing.T) {
	text := `[called my_func WITH ARGS: {"key": "val"}]`
	calls := ParseBracketToolCalls(text)

	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "my_func" {
		t.Errorf("expected name 'my_func', got %q", calls[0].Name)
	}
}

// --- ParseBracketToolCalls: edge cases and malformed input -------------------

func TestParseBracketToolCalls_EmptyString(t *testing.T) {
	calls := ParseBracketToolCalls("")
	if calls != nil {
		t.Errorf("expected nil for empty string, got %v", calls)
	}
}

func TestParseBracketToolCalls_NoCalledKeyword(t *testing.T) {
	calls := ParseBracketToolCalls("just some regular text without tool calls")
	if calls != nil {
		t.Errorf("expected nil when no [Called keyword, got %v", calls)
	}
}

func TestParseBracketToolCalls_TruncatedJSON(t *testing.T) {
	// JSON is cut off — missing closing brace.
	text := `[Called get_data with args: {"key": "value`
	calls := ParseBracketToolCalls(text)

	if calls != nil {
		t.Errorf("expected nil for truncated JSON, got %v", calls)
	}
}

func TestParseBracketToolCalls_MissingJSONBrace(t *testing.T) {
	// No opening brace after "args:".
	text := `[Called my_func with args: no json here]`
	calls := ParseBracketToolCalls(text)

	if calls != nil {
		t.Errorf("expected nil for missing JSON brace, got %v", calls)
	}
}

func TestParseBracketToolCalls_InvalidJSON(t *testing.T) {
	// Braces match but content is not valid JSON.
	text := `[Called my_func with args: {not valid json}]`
	calls := ParseBracketToolCalls(text)

	if calls != nil {
		t.Errorf("expected nil for invalid JSON, got %v", calls)
	}
}

func TestParseBracketToolCalls_MixedValidAndInvalid(t *testing.T) {
	// First call has truncated JSON, second is valid.
	text := `[Called bad_func with args: {"key": "val` +
		` [Called good_func with args: {"ok": true}]`
	calls := ParseBracketToolCalls(text)

	if len(calls) != 1 {
		t.Fatalf("expected 1 valid call, got %d", len(calls))
	}
	if calls[0].Name != "good_func" {
		t.Errorf("expected name 'good_func', got %q", calls[0].Name)
	}
}

func TestParseBracketToolCalls_NoClosingBracket(t *testing.T) {
	// Missing the outer closing ']' — should still parse since we
	// only require the JSON object to be complete.
	text := `[Called my_func with args: {"key": "value"}`
	calls := ParseBracketToolCalls(text)

	if len(calls) != 1 {
		t.Fatalf("expected 1 call even without closing bracket, got %d", len(calls))
	}
	if calls[0].Name != "my_func" {
		t.Errorf("expected name 'my_func', got %q", calls[0].Name)
	}
}

func TestParseBracketToolCalls_EscapedQuotesInArgs(t *testing.T) {
	text := `[Called format with args: {"template": "say \"hello\""}]`
	calls := ParseBracketToolCalls(text)

	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	expected := `{"template": "say \"hello\""}`
	if calls[0].Arguments != expected {
		t.Errorf("expected arguments %q, got %q", expected, calls[0].Arguments)
	}
}

func TestParseBracketToolCalls_WhitespaceBetweenArgsAndBrace(t *testing.T) {
	text := `[Called my_func with args:   {"key": "val"}]`
	calls := ParseBracketToolCalls(text)

	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "my_func" {
		t.Errorf("expected name 'my_func', got %q", calls[0].Name)
	}
}

func TestParseBracketToolCalls_BracesInsideStringValues(t *testing.T) {
	text := `[Called render with args: {"code": "func main() { fmt.Println(\"hi\") }"}]`
	calls := ParseBracketToolCalls(text)

	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "render" {
		t.Errorf("expected name 'render', got %q", calls[0].Name)
	}
}

func TestParseBracketToolCalls_TextContainsCalledButNoMatch(t *testing.T) {
	// Contains "[Called" but doesn't match the full pattern.
	text := `[Called without proper format`
	calls := ParseBracketToolCalls(text)

	if calls != nil {
		t.Errorf("expected nil for incomplete pattern, got %v", calls)
	}
}

func TestParseBracketToolCalls_UnderscoreAndDigitsInName(t *testing.T) {
	text := `[Called tool_v2_beta3 with args: {"x": 1}]`
	calls := ParseBracketToolCalls(text)

	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "tool_v2_beta3" {
		t.Errorf("expected name 'tool_v2_beta3', got %q", calls[0].Name)
	}
}
