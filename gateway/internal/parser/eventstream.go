// Package parser provides parsers for Kiro API response formats.
//
// This file implements the AWS event stream parser that extracts
// structured events from Kiro's binary response format. The Kiro API
// returns responses as a stream of bytes containing embedded JSON
// objects. Each JSON object represents a different event type: content
// (text response data), tool start (tool name and ID), tool input
// (accumulated JSON arguments), tool stop (end of tool call), usage
// (credit consumption), and context usage percentage (for token
// calculation).
//
// The parser scans a byte buffer for known JSON patterns, extracts
// complete JSON objects using brace-matching, and returns a slice of
// typed KiroStreamEvent values. It deduplicates consecutive identical
// content events and detects truncated tool call arguments.
package parser

import "encoding/json"

// Event type constants identify the kind of each parsed event.
const (
	EventContent      = "content"
	EventToolStart    = "tool_start"
	EventToolInput    = "tool_input"
	EventToolStop     = "tool_stop"
	EventUsage        = "usage"
	EventContextUsage = "context_usage"
)

// KiroStreamEvent represents a single parsed event from the Kiro API
// event stream. The Type field indicates which other fields are
// populated:
//
//   - EventContent: Content is set
//   - EventToolStart: ToolName and ToolUseID are set
//   - EventToolInput: ToolInput is set (partial JSON fragment)
//   - EventToolStop: no additional fields
//   - EventUsage: Usage is set
//   - EventContextUsage: ContextUsagePercentage is set
type KiroStreamEvent struct {
	Type                   string
	Content                string
	ToolName               string
	ToolUseID              string
	ToolInput              string
	Usage                  *UsageData
	ContextUsagePercentage float64
}

// UsageData holds credit consumption information from a usage event.
type UsageData struct {
	Credits float64 `json:"credits"`
}

// eventPattern maps a JSON prefix to its event type for scanning.
type eventPattern struct {
	prefix    string
	eventType string
}

// knownPatterns lists the JSON prefixes the parser scans for, ordered
// by specificity. The parser finds the earliest match in the buffer
// on each iteration.
var knownPatterns = []eventPattern{
	{`{"content":`, EventContent},
	{`{"name":`, EventToolStart},
	{`{"input":`, EventToolInput},
	{`{"stop":`, EventToolStop},
	{`{"usage":`, EventUsage},
	{`{"contextUsagePercentage":`, EventContextUsage},
}

// ParseEventStream parses raw bytes from a Kiro API response into a
// slice of structured events. It scans the data for known JSON
// patterns, extracts complete JSON objects, and converts them into
// KiroStreamEvent values.
//
// Consecutive identical content events are deduplicated (only the
// first occurrence is kept). Incomplete JSON objects at the end of
// the data are silently skipped — the caller should buffer and
// re-submit remaining bytes in the next call.
func ParseEventStream(data []byte) []KiroStreamEvent {
	text := string(data)
	var events []KiroStreamEvent
	var lastContent string
	hasLastContent := false

	for {
		// Find the earliest known pattern in the remaining text.
		earliestPos := -1
		var earliestType string

		for _, p := range knownPatterns {
			pos := indexOf(text, p.prefix)
			if pos != -1 && (earliestPos == -1 || pos < earliestPos) {
				earliestPos = pos
				earliestType = p.eventType
			}
		}

		if earliestPos == -1 {
			break
		}

		// Extract the complete JSON object starting at earliestPos.
		jsonEnd := findMatchingBrace(text, earliestPos)
		if jsonEnd == -1 {
			// Incomplete JSON — stop processing.
			break
		}

		jsonStr := text[earliestPos : jsonEnd+1]
		text = text[jsonEnd+1:]

		// Parse the JSON into a generic map.
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
			continue
		}

		evt := processRawEvent(raw, earliestType)
		if evt == nil {
			continue
		}

		// Deduplicate consecutive identical content events.
		if evt.Type == EventContent {
			if hasLastContent && evt.Content == lastContent {
				continue
			}
			lastContent = evt.Content
			hasLastContent = true
		}

		events = append(events, *evt)
	}

	return events
}

// IsToolCallTruncated checks whether a tool call argument string
// appears to be truncated. It detects unbalanced braces, unbalanced
// brackets, and unclosed string literals.
//
// This is used to diagnose upstream truncation from the Kiro API,
// where large tool call arguments may be cut off mid-stream.
func IsToolCallTruncated(args string) bool {
	if args == "" || args == "{}" {
		return false
	}

	info := diagnoseJSONTruncation(args)
	return info.IsTruncated
}

// TruncationInfo holds diagnostic information about a potentially
// truncated JSON string.
type TruncationInfo struct {
	IsTruncated bool
	Reason      string
	SizeBytes   int
}

// diagnoseJSONTruncation analyzes a malformed JSON string to determine
// if it was truncated by the upstream API. It checks for unbalanced
// braces, unbalanced brackets, and unclosed string literals.
func diagnoseJSONTruncation(s string) TruncationInfo {
	sizeBytes := len(s)
	stripped := trimSpace(s)

	if stripped == "" {
		return TruncationInfo{IsTruncated: false, Reason: "empty string", SizeBytes: sizeBytes}
	}

	// Count braces and brackets outside of strings.
	openBraces, closeBraces := 0, 0
	openBrackets, closeBrackets := 0, 0
	inString := false
	escapeNext := false

	for i := 0; i < len(stripped); i++ {
		ch := stripped[i]

		if escapeNext {
			escapeNext = false
			continue
		}
		if ch == '\\' && inString {
			escapeNext = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if !inString {
			switch ch {
			case '{':
				openBraces++
			case '}':
				closeBraces++
			case '[':
				openBrackets++
			case ']':
				closeBrackets++
			}
		}
	}

	// Check for unclosed string literal.
	if inString {
		return TruncationInfo{
			IsTruncated: true,
			Reason:      "unclosed string literal",
			SizeBytes:   sizeBytes,
		}
	}

	// Check for unbalanced braces.
	if openBraces != closeBraces {
		return TruncationInfo{
			IsTruncated: true,
			Reason:      "unbalanced braces",
			SizeBytes:   sizeBytes,
		}
	}

	// Check for unbalanced brackets.
	if openBrackets != closeBrackets {
		return TruncationInfo{
			IsTruncated: true,
			Reason:      "unbalanced brackets",
			SizeBytes:   sizeBytes,
		}
	}

	return TruncationInfo{IsTruncated: false, Reason: "well-formed", SizeBytes: sizeBytes}
}

// processRawEvent converts a parsed JSON map into a KiroStreamEvent
// based on the detected event type. Returns nil if the event should
// be skipped (e.g., followupPrompt content).
func processRawEvent(raw map[string]json.RawMessage, eventType string) *KiroStreamEvent {
	switch eventType {
	case EventContent:
		return processContentEvent(raw)
	case EventToolStart:
		return processToolStartEvent(raw)
	case EventToolInput:
		return processToolInputEvent(raw)
	case EventToolStop:
		return processToolStopEvent(raw)
	case EventUsage:
		return processUsageEvent(raw)
	case EventContextUsage:
		return processContextUsageEvent(raw)
	default:
		return nil
	}
}

// processContentEvent extracts text content from a content event.
// Events with a followupPrompt field are skipped.
func processContentEvent(raw map[string]json.RawMessage) *KiroStreamEvent {
	// Skip followupPrompt events.
	if _, ok := raw["followupPrompt"]; ok {
		return nil
	}

	var content string
	if v, ok := raw["content"]; ok {
		_ = json.Unmarshal(v, &content)
	}

	return &KiroStreamEvent{
		Type:    EventContent,
		Content: content,
	}
}

// processToolStartEvent extracts the tool name and use ID from a
// tool start event.
func processToolStartEvent(raw map[string]json.RawMessage) *KiroStreamEvent {
	var name, toolUseID string
	if v, ok := raw["name"]; ok {
		_ = json.Unmarshal(v, &name)
	}
	if v, ok := raw["toolUseId"]; ok {
		_ = json.Unmarshal(v, &toolUseID)
	}

	return &KiroStreamEvent{
		Type:      EventToolStart,
		ToolName:  name,
		ToolUseID: toolUseID,
	}
}

// processToolInputEvent extracts the partial input fragment from a
// tool input event. The input may be a string or a JSON object; both
// are normalized to a string representation.
func processToolInputEvent(raw map[string]json.RawMessage) *KiroStreamEvent {
	var input string
	if v, ok := raw["input"]; ok {
		// Try as string first.
		if err := json.Unmarshal(v, &input); err != nil {
			// Not a string — use the raw JSON representation.
			input = string(v)
		}
	}

	return &KiroStreamEvent{
		Type:      EventToolInput,
		ToolInput: input,
	}
}

// processToolStopEvent creates a tool stop event. The stop field
// value is not inspected — its presence is sufficient.
func processToolStopEvent(_ map[string]json.RawMessage) *KiroStreamEvent {
	return &KiroStreamEvent{
		Type: EventToolStop,
	}
}

// processUsageEvent extracts credit consumption data from a usage
// event.
func processUsageEvent(raw map[string]json.RawMessage) *KiroStreamEvent {
	evt := &KiroStreamEvent{Type: EventUsage}

	if v, ok := raw["usage"]; ok {
		var usage UsageData
		if err := json.Unmarshal(v, &usage); err == nil {
			evt.Usage = &usage
		}
	}

	return evt
}

// processContextUsageEvent extracts the context usage percentage
// from a context usage event.
func processContextUsageEvent(raw map[string]json.RawMessage) *KiroStreamEvent {
	evt := &KiroStreamEvent{Type: EventContextUsage}

	if v, ok := raw["contextUsagePercentage"]; ok {
		_ = json.Unmarshal(v, &evt.ContextUsagePercentage)
	}

	return evt
}

// findMatchingBrace finds the position of the closing brace that
// matches the opening brace at startPos. It correctly handles nested
// braces and quoted strings with escape sequences.
//
// Returns -1 if no matching brace is found (incomplete JSON).
func findMatchingBrace(text string, startPos int) int {
	if startPos >= len(text) || text[startPos] != '{' {
		return -1
	}

	braceCount := 0
	inString := false
	escapeNext := false

	for i := startPos; i < len(text); i++ {
		ch := text[i]

		if escapeNext {
			escapeNext = false
			continue
		}

		if ch == '\\' && inString {
			escapeNext = true
			continue
		}

		if ch == '"' {
			inString = !inString
			continue
		}

		if !inString {
			switch ch {
			case '{':
				braceCount++
			case '}':
				braceCount--
				if braceCount == 0 {
					return i
				}
			}
		}
	}

	return -1
}

// indexOf returns the index of the first occurrence of substr in s,
// or -1 if not found. This is a simple wrapper around strings.Index
// without importing the strings package.
func indexOf(s, substr string) int {
	n := len(substr)
	if n == 0 {
		return 0
	}
	if n > len(s) {
		return -1
	}
	for i := 0; i <= len(s)-n; i++ {
		if s[i:i+n] == substr {
			return i
		}
	}
	return -1
}

// trimSpace removes leading and trailing ASCII whitespace from s.
func trimSpace(s string) string {
	start := 0
	for start < len(s) && isSpace(s[start]) {
		start++
	}
	end := len(s)
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

// isSpace returns true for ASCII whitespace characters.
func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
