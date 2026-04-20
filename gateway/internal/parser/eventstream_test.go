package parser

import (
	"encoding/json"
	"testing"
)

// --- ParseEventStream: content events ---------------------------------------

func TestParseEventStream_SingleContentEvent(t *testing.T) {
	data := []byte(`{"content":"Hello, world!"}`)
	events := ParseEventStream(data)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventContent {
		t.Errorf("expected type %s, got %s", EventContent, events[0].Type)
	}
	if events[0].Content != "Hello, world!" {
		t.Errorf("expected content 'Hello, world!', got %q", events[0].Content)
	}
}

func TestParseEventStream_MultipleContentEvents(t *testing.T) {
	data := []byte(`{"content":"Hello"}{"content":" world"}`)
	events := ParseEventStream(data)

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Content != "Hello" {
		t.Errorf("expected 'Hello', got %q", events[0].Content)
	}
	if events[1].Content != " world" {
		t.Errorf("expected ' world', got %q", events[1].Content)
	}
}

func TestParseEventStream_DeduplicatesConsecutiveContent(t *testing.T) {
	data := []byte(`{"content":"Hello"}{"content":"Hello"}{"content":"Hello"}`)
	events := ParseEventStream(data)

	if len(events) != 1 {
		t.Fatalf("expected 1 event after dedup, got %d", len(events))
	}
	if events[0].Content != "Hello" {
		t.Errorf("expected 'Hello', got %q", events[0].Content)
	}
}

func TestParseEventStream_DeduplicatesOnlyConsecutive(t *testing.T) {
	// Same content separated by different content should not be deduped.
	data := []byte(`{"content":"A"}{"content":"B"}{"content":"A"}`)
	events := ParseEventStream(data)

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Content != "A" || events[1].Content != "B" || events[2].Content != "A" {
		t.Errorf("unexpected content sequence: %q, %q, %q",
			events[0].Content, events[1].Content, events[2].Content)
	}
}

func TestParseEventStream_SkipsFollowupPrompt(t *testing.T) {
	data := []byte(`{"content":"Hello","followupPrompt":"What next?"}`)
	events := ParseEventStream(data)

	if len(events) != 0 {
		t.Fatalf("expected 0 events for followupPrompt, got %d", len(events))
	}
}

func TestParseEventStream_EmptyContent(t *testing.T) {
	data := []byte(`{"content":""}`)
	events := ParseEventStream(data)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Content != "" {
		t.Errorf("expected empty content, got %q", events[0].Content)
	}
}

func TestParseEventStream_ContentWithSpecialChars(t *testing.T) {
	data := []byte(`{"content":"line1\nline2\ttab \"quoted\""}`)
	events := ParseEventStream(data)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	expected := "line1\nline2\ttab \"quoted\""
	if events[0].Content != expected {
		t.Errorf("expected %q, got %q", expected, events[0].Content)
	}
}

// --- ParseEventStream: tool events ------------------------------------------

func TestParseEventStream_ToolStartEvent(t *testing.T) {
	data := []byte(`{"name":"get_weather","toolUseId":"tool_123"}`)
	events := ParseEventStream(data)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventToolStart {
		t.Errorf("expected type %s, got %s", EventToolStart, events[0].Type)
	}
	if events[0].ToolName != "get_weather" {
		t.Errorf("expected tool name 'get_weather', got %q", events[0].ToolName)
	}
	if events[0].ToolUseID != "tool_123" {
		t.Errorf("expected tool use ID 'tool_123', got %q", events[0].ToolUseID)
	}
}

func TestParseEventStream_ToolInputEvent(t *testing.T) {
	data := []byte(`{"input":"{\"city\":\"London\"}"}`)
	events := ParseEventStream(data)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventToolInput {
		t.Errorf("expected type %s, got %s", EventToolInput, events[0].Type)
	}
	if events[0].ToolInput != `{"city":"London"}` {
		t.Errorf("expected tool input '{\"city\":\"London\"}', got %q", events[0].ToolInput)
	}
}

func TestParseEventStream_ToolInputAsObject(t *testing.T) {
	data := []byte(`{"input":{"city":"London"}}`)
	events := ParseEventStream(data)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventToolInput {
		t.Errorf("expected type %s, got %s", EventToolInput, events[0].Type)
	}
	// When input is an object, it should be serialized as JSON string.
	var parsed map[string]string
	if err := json.Unmarshal([]byte(events[0].ToolInput), &parsed); err != nil {
		t.Fatalf("tool input should be valid JSON: %v", err)
	}
	if parsed["city"] != "London" {
		t.Errorf("expected city 'London', got %q", parsed["city"])
	}
}

func TestParseEventStream_ToolStopEvent(t *testing.T) {
	data := []byte(`{"stop":true}`)
	events := ParseEventStream(data)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventToolStop {
		t.Errorf("expected type %s, got %s", EventToolStop, events[0].Type)
	}
}

func TestParseEventStream_FullToolCallSequence(t *testing.T) {
	data := []byte(
		`{"name":"read_file","toolUseId":"call_abc"}` +
			`{"input":"{\"path\":"}` +
			`{"input":"\"src/main.go\"}"}` +
			`{"stop":true}`,
	)
	events := ParseEventStream(data)

	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}

	if events[0].Type != EventToolStart {
		t.Errorf("event 0: expected %s, got %s", EventToolStart, events[0].Type)
	}
	if events[0].ToolName != "read_file" {
		t.Errorf("event 0: expected tool name 'read_file', got %q", events[0].ToolName)
	}

	if events[1].Type != EventToolInput {
		t.Errorf("event 1: expected %s, got %s", EventToolInput, events[1].Type)
	}
	if events[2].Type != EventToolInput {
		t.Errorf("event 2: expected %s, got %s", EventToolInput, events[2].Type)
	}

	// Accumulate inputs like the streaming layer would.
	accumulated := events[1].ToolInput + events[2].ToolInput
	if accumulated != `{"path":"src/main.go"}` {
		t.Errorf("accumulated input: expected '{\"path\":\"src/main.go\"}', got %q", accumulated)
	}

	if events[3].Type != EventToolStop {
		t.Errorf("event 3: expected %s, got %s", EventToolStop, events[3].Type)
	}
}

// --- ParseEventStream: usage events -----------------------------------------

func TestParseEventStream_UsageEvent(t *testing.T) {
	data := []byte(`{"usage":{"credits":0.5}}`)
	events := ParseEventStream(data)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventUsage {
		t.Errorf("expected type %s, got %s", EventUsage, events[0].Type)
	}
	if events[0].Usage == nil {
		t.Fatal("expected usage data, got nil")
	}
	if events[0].Usage.Credits != 0.5 {
		t.Errorf("expected credits 0.5, got %f", events[0].Usage.Credits)
	}
}

func TestParseEventStream_UsageEventZeroCredits(t *testing.T) {
	data := []byte(`{"usage":{"credits":0}}`)
	events := ParseEventStream(data)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Usage == nil {
		t.Fatal("expected usage data, got nil")
	}
	if events[0].Usage.Credits != 0 {
		t.Errorf("expected credits 0, got %f", events[0].Usage.Credits)
	}
}

// --- ParseEventStream: context usage events ---------------------------------

func TestParseEventStream_ContextUsageEvent(t *testing.T) {
	data := []byte(`{"contextUsagePercentage":45.2}`)
	events := ParseEventStream(data)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventContextUsage {
		t.Errorf("expected type %s, got %s", EventContextUsage, events[0].Type)
	}
	if events[0].ContextUsagePercentage != 45.2 {
		t.Errorf("expected 45.2, got %f", events[0].ContextUsagePercentage)
	}
}

func TestParseEventStream_ContextUsageZero(t *testing.T) {
	data := []byte(`{"contextUsagePercentage":0}`)
	events := ParseEventStream(data)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ContextUsagePercentage != 0 {
		t.Errorf("expected 0, got %f", events[0].ContextUsagePercentage)
	}
}

// --- ParseEventStream: mixed events -----------------------------------------

func TestParseEventStream_MixedEventTypes(t *testing.T) {
	data := []byte(
		`{"content":"Hello"}` +
			`{"name":"tool1","toolUseId":"id1"}` +
			`{"input":"{\"key\":\"val\"}"}` +
			`{"stop":true}` +
			`{"content":" Done."}` +
			`{"usage":{"credits":1.0}}` +
			`{"contextUsagePercentage":30.5}`,
	)
	events := ParseEventStream(data)

	if len(events) != 7 {
		t.Fatalf("expected 7 events, got %d", len(events))
	}

	expected := []string{
		EventContent, EventToolStart, EventToolInput,
		EventToolStop, EventContent, EventUsage, EventContextUsage,
	}
	for i, exp := range expected {
		if events[i].Type != exp {
			t.Errorf("event %d: expected type %s, got %s", i, exp, events[i].Type)
		}
	}
}

func TestParseEventStream_BinaryGarbageBetweenEvents(t *testing.T) {
	// Simulate binary framing bytes between JSON events.
	data := []byte("\x00\x01\x02{\"content\":\"Hello\"}\x00\x03{\"content\":\" world\"}")
	events := ParseEventStream(data)

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Content != "Hello" {
		t.Errorf("expected 'Hello', got %q", events[0].Content)
	}
	if events[1].Content != " world" {
		t.Errorf("expected ' world', got %q", events[1].Content)
	}
}

// --- ParseEventStream: edge cases -------------------------------------------

func TestParseEventStream_EmptyInput(t *testing.T) {
	events := ParseEventStream([]byte{})
	if len(events) != 0 {
		t.Errorf("expected 0 events for empty input, got %d", len(events))
	}
}

func TestParseEventStream_NilInput(t *testing.T) {
	events := ParseEventStream(nil)
	if len(events) != 0 {
		t.Errorf("expected 0 events for nil input, got %d", len(events))
	}
}

func TestParseEventStream_IncompleteJSON(t *testing.T) {
	// Truncated JSON — should be skipped.
	data := []byte(`{"content":"Hello`)
	events := ParseEventStream(data)

	if len(events) != 0 {
		t.Errorf("expected 0 events for incomplete JSON, got %d", len(events))
	}
}

func TestParseEventStream_MalformedJSON(t *testing.T) {
	// Valid braces but invalid JSON content.
	data := []byte(`{"content":invalid}`)
	events := ParseEventStream(data)

	// The brace matcher will find a match, but json.Unmarshal will fail.
	if len(events) != 0 {
		t.Errorf("expected 0 events for malformed JSON, got %d", len(events))
	}
}

func TestParseEventStream_NestedBracesInContent(t *testing.T) {
	data := []byte(`{"content":"code: {\"key\": {\"nested\": true}}"}`)
	events := ParseEventStream(data)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	expected := `code: {"key": {"nested": true}}`
	if events[0].Content != expected {
		t.Errorf("expected %q, got %q", expected, events[0].Content)
	}
}

func TestParseEventStream_UnknownEventType(t *testing.T) {
	// An event with an unknown prefix should be skipped.
	data := []byte(`{"unknown":"value"}{"content":"Hello"}`)
	events := ParseEventStream(data)

	// Only the content event should be parsed.
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventContent {
		t.Errorf("expected type %s, got %s", EventContent, events[0].Type)
	}
}

// --- IsToolCallTruncated ----------------------------------------------------

func TestIsToolCallTruncated_EmptyString(t *testing.T) {
	if IsToolCallTruncated("") {
		t.Error("empty string should not be truncated")
	}
}

func TestIsToolCallTruncated_EmptyObject(t *testing.T) {
	if IsToolCallTruncated("{}") {
		t.Error("empty object should not be truncated")
	}
}

func TestIsToolCallTruncated_ValidJSON(t *testing.T) {
	if IsToolCallTruncated(`{"city":"London","temp":20}`) {
		t.Error("valid JSON should not be truncated")
	}
}

func TestIsToolCallTruncated_UnbalancedBraces(t *testing.T) {
	if !IsToolCallTruncated(`{"city":"London","data":{"nested":true}`) {
		t.Error("unbalanced braces should be detected as truncated")
	}
}

func TestIsToolCallTruncated_UnclosedString(t *testing.T) {
	if !IsToolCallTruncated(`{"content":"this is a long string that was cut`) {
		t.Error("unclosed string should be detected as truncated")
	}
}

func TestIsToolCallTruncated_UnbalancedBrackets(t *testing.T) {
	if !IsToolCallTruncated(`{"items":["a","b","c"`) {
		t.Error("unbalanced brackets should be detected as truncated")
	}
}

func TestIsToolCallTruncated_MissingClosingBrace(t *testing.T) {
	if !IsToolCallTruncated(`{"path":"src/main.go","content":"func main() {`) {
		t.Error("missing closing brace should be detected as truncated")
	}
}

func TestIsToolCallTruncated_WellFormedNestedJSON(t *testing.T) {
	valid := `{"a":{"b":{"c":[1,2,3]}},"d":"hello"}`
	if IsToolCallTruncated(valid) {
		t.Error("well-formed nested JSON should not be truncated")
	}
}

func TestIsToolCallTruncated_EscapedQuotes(t *testing.T) {
	// Escaped quotes inside strings should not confuse the parser.
	valid := `{"msg":"He said \"hello\" to her"}`
	if IsToolCallTruncated(valid) {
		t.Error("JSON with escaped quotes should not be truncated")
	}
}

// --- diagnoseJSONTruncation -------------------------------------------------

func TestDiagnoseJSONTruncation_EmptyString(t *testing.T) {
	info := diagnoseJSONTruncation("")
	if info.IsTruncated {
		t.Error("empty string should not be truncated")
	}
	if info.Reason != "empty string" {
		t.Errorf("expected reason 'empty string', got %q", info.Reason)
	}
}

func TestDiagnoseJSONTruncation_WellFormed(t *testing.T) {
	info := diagnoseJSONTruncation(`{"key":"value"}`)
	if info.IsTruncated {
		t.Error("well-formed JSON should not be truncated")
	}
	if info.Reason != "well-formed" {
		t.Errorf("expected reason 'well-formed', got %q", info.Reason)
	}
}

func TestDiagnoseJSONTruncation_UnbalancedBraces(t *testing.T) {
	info := diagnoseJSONTruncation(`{"key":"value"`)
	if !info.IsTruncated {
		t.Error("unbalanced braces should be detected")
	}
	if info.Reason != "unbalanced braces" {
		t.Errorf("expected reason 'unbalanced braces', got %q", info.Reason)
	}
}

func TestDiagnoseJSONTruncation_UnclosedString(t *testing.T) {
	info := diagnoseJSONTruncation(`{"key":"value`)
	if !info.IsTruncated {
		t.Error("unclosed string should be detected")
	}
	if info.Reason != "unclosed string literal" {
		t.Errorf("expected reason 'unclosed string literal', got %q", info.Reason)
	}
}

func TestDiagnoseJSONTruncation_UnbalancedBrackets(t *testing.T) {
	info := diagnoseJSONTruncation(`{"items":["a","b"`)
	if !info.IsTruncated {
		t.Error("unbalanced brackets should be detected")
	}
}

func TestDiagnoseJSONTruncation_SizeBytes(t *testing.T) {
	input := `{"key":"value"}`
	info := diagnoseJSONTruncation(input)
	if info.SizeBytes != len(input) {
		t.Errorf("expected size %d, got %d", len(input), info.SizeBytes)
	}
}

// --- findMatchingBrace ------------------------------------------------------

func TestFindMatchingBrace_SimpleObject(t *testing.T) {
	text := `{"key":"value"}`
	pos := findMatchingBrace(text, 0)
	if pos != len(text)-1 {
		t.Errorf("expected %d, got %d", len(text)-1, pos)
	}
}

func TestFindMatchingBrace_NestedObjects(t *testing.T) {
	text := `{"a":{"b":{"c":1}}}`
	pos := findMatchingBrace(text, 0)
	if pos != len(text)-1 {
		t.Errorf("expected %d, got %d", len(text)-1, pos)
	}
}

func TestFindMatchingBrace_BracesInStrings(t *testing.T) {
	text := `{"msg":"hello {world}"}`
	pos := findMatchingBrace(text, 0)
	if pos != len(text)-1 {
		t.Errorf("expected %d, got %d", len(text)-1, pos)
	}
}

func TestFindMatchingBrace_EscapedQuotes(t *testing.T) {
	text := `{"msg":"say \"hi\""}`
	pos := findMatchingBrace(text, 0)
	if pos != len(text)-1 {
		t.Errorf("expected %d, got %d", len(text)-1, pos)
	}
}

func TestFindMatchingBrace_Incomplete(t *testing.T) {
	text := `{"key":"value"`
	pos := findMatchingBrace(text, 0)
	if pos != -1 {
		t.Errorf("expected -1 for incomplete JSON, got %d", pos)
	}
}

func TestFindMatchingBrace_NotAtBrace(t *testing.T) {
	text := `"hello"`
	pos := findMatchingBrace(text, 0)
	if pos != -1 {
		t.Errorf("expected -1 when not starting at brace, got %d", pos)
	}
}

func TestFindMatchingBrace_OutOfBounds(t *testing.T) {
	text := `{}`
	pos := findMatchingBrace(text, 10)
	if pos != -1 {
		t.Errorf("expected -1 for out of bounds, got %d", pos)
	}
}

func TestFindMatchingBrace_EmptyObject(t *testing.T) {
	text := `{}`
	pos := findMatchingBrace(text, 0)
	if pos != 1 {
		t.Errorf("expected 1, got %d", pos)
	}
}

func TestFindMatchingBrace_OffsetStart(t *testing.T) {
	text := `xxx{"key":"val"}yyy`
	pos := findMatchingBrace(text, 3)
	if pos != 15 {
		t.Errorf("expected 15, got %d", pos)
	}
}
