package parser

import (
	"encoding/json"
	"regexp"
	"strings"
)

// BracketToolCall represents a tool call extracted from bracket-style
// text format. Some models return tool calls embedded in response text
// as `[Called func_name with args: {...}]` instead of structured JSON
// events. This struct holds the parsed result.
type BracketToolCall struct {
	// Name is the function name extracted from the bracket call.
	Name string
	// Arguments is the raw JSON string of the tool call arguments.
	// It is always valid JSON when successfully parsed, or empty
	// string if parsing failed.
	Arguments string
}

// bracketPattern matches the `[Called func_name with args:` prefix.
// The function name is captured in group 1 as one or more word
// characters (\w+). The match is case-insensitive.
var bracketPattern = regexp.MustCompile(`(?i)\[Called\s+(\w+)\s+with\s+args:\s*`)

// ParseBracketToolCalls scans text for bracket-style tool calls in
// the format `[Called func_name with args: {...}]` and returns a
// slice of parsed results.
//
// Each match must contain a valid JSON object as the arguments. If
// the JSON is malformed, truncated, or missing, that particular
// match is silently skipped. The closing `]` bracket of the outer
// format is not required — only the inner JSON object must be
// complete.
//
// Returns nil when no valid bracket tool calls are found.
func ParseBracketToolCalls(text string) []BracketToolCall {
	if text == "" || !strings.Contains(strings.ToLower(text), "[called") {
		return nil
	}

	matches := bracketPattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return nil
	}

	var calls []BracketToolCall

	for _, loc := range matches {
		// loc[0], loc[1] = full match start/end
		// loc[2], loc[3] = group 1 (func name) start/end
		funcName := text[loc[2]:loc[3]]
		argsStart := loc[1] // position right after "with args: "

		// Find the opening brace of the JSON arguments.
		jsonStart := -1
		for i := argsStart; i < len(text); i++ {
			if text[i] == '{' {
				jsonStart = i
				break
			}
			// Stop searching if we hit a non-whitespace character
			// that isn't '{' — the format is malformed.
			if !isSpace(text[i]) {
				break
			}
		}
		if jsonStart == -1 {
			continue
		}

		// Find the matching closing brace.
		jsonEnd := findMatchingBrace(text, jsonStart)
		if jsonEnd == -1 {
			// Truncated or malformed JSON — skip this match.
			continue
		}

		jsonStr := text[jsonStart : jsonEnd+1]

		// Validate that the extracted string is valid JSON.
		var parsed json.RawMessage
		if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
			continue
		}

		calls = append(calls, BracketToolCall{
			Name:      funcName,
			Arguments: jsonStr,
		})
	}

	return calls
}
