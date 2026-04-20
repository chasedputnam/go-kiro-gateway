package tokenizer

import (
	"math"
	"testing"

	"github.com/jwadow/kiro-gateway/gateway/internal/converter"
)

// ---------------------------------------------------------------------------
// CountTokens
// ---------------------------------------------------------------------------

func TestCountTokens_EmptyString(t *testing.T) {
	if got := CountTokens(""); got != 0 {
		t.Errorf("CountTokens(\"\") = %d, want 0", got)
	}
}

func TestCountTokens_SimpleText(t *testing.T) {
	got := CountTokens("hello world")
	if got <= 0 {
		t.Errorf("CountTokens(\"hello world\") = %d, want > 0", got)
	}
}

func TestCountTokens_LongerTextProducesMoreTokens(t *testing.T) {
	short := CountTokens("hello")
	long := CountTokens("hello world, this is a much longer sentence with many more tokens")
	if long <= short {
		t.Errorf("longer text (%d tokens) should produce more tokens than shorter text (%d tokens)", long, short)
	}
}

func TestCountTokens_ReturnsConsistentResults(t *testing.T) {
	text := "The quick brown fox jumps over the lazy dog."
	a := CountTokens(text)
	b := CountTokens(text)
	if a != b {
		t.Errorf("CountTokens should be deterministic: got %d and %d", a, b)
	}
}

// ---------------------------------------------------------------------------
// ApplyClaudeCorrectionFactor
// ---------------------------------------------------------------------------

func TestApplyClaudeCorrectionFactor_Zero(t *testing.T) {
	if got := ApplyClaudeCorrectionFactor(0); got != 0 {
		t.Errorf("ApplyClaudeCorrectionFactor(0) = %d, want 0", got)
	}
}

func TestApplyClaudeCorrectionFactor_PositiveValue(t *testing.T) {
	raw := 100
	got := ApplyClaudeCorrectionFactor(raw)
	want := int(float64(raw) * ClaudeCorrectionFactor) // 115
	if got != want {
		t.Errorf("ApplyClaudeCorrectionFactor(%d) = %d, want %d", raw, got, want)
	}
}

func TestApplyClaudeCorrectionFactor_SmallValue(t *testing.T) {
	raw := 1
	got := ApplyClaudeCorrectionFactor(raw)
	want := int(float64(raw) * ClaudeCorrectionFactor) // 1
	if got != want {
		t.Errorf("ApplyClaudeCorrectionFactor(%d) = %d, want %d", raw, got, want)
	}
}

func TestApplyClaudeCorrectionFactor_LargeValue(t *testing.T) {
	raw := 10000
	got := ApplyClaudeCorrectionFactor(raw)
	want := int(float64(raw) * ClaudeCorrectionFactor) // 11500
	if got != want {
		t.Errorf("ApplyClaudeCorrectionFactor(%d) = %d, want %d", raw, got, want)
	}
}

func TestApplyClaudeCorrectionFactor_AlwaysGreaterOrEqual(t *testing.T) {
	for _, raw := range []int{0, 1, 10, 100, 1000, 50000} {
		got := ApplyClaudeCorrectionFactor(raw)
		if got < raw {
			t.Errorf("ApplyClaudeCorrectionFactor(%d) = %d, should be >= input", raw, got)
		}
	}
}

// ---------------------------------------------------------------------------
// CalculatePromptTokens
// ---------------------------------------------------------------------------

func TestCalculatePromptTokens_BasicCalculation(t *testing.T) {
	// totalTokens = 200000 * 0.5 = 100000
	// promptTokens = 100000 - 500 = 99500
	got := CalculatePromptTokens(500, 0.5, 200000)
	if got != 99500 {
		t.Errorf("CalculatePromptTokens(500, 0.5, 200000) = %d, want 99500", got)
	}
}

func TestCalculatePromptTokens_SmallPercentage(t *testing.T) {
	// totalTokens = 200000 * 0.01 = 2000
	// promptTokens = 2000 - 100 = 1900
	got := CalculatePromptTokens(100, 0.01, 200000)
	if got != 1900 {
		t.Errorf("CalculatePromptTokens(100, 0.01, 200000) = %d, want 1900", got)
	}
}

func TestCalculatePromptTokens_FullContext(t *testing.T) {
	// totalTokens = 200000 * 1.0 = 200000
	// promptTokens = 200000 - 1000 = 199000
	got := CalculatePromptTokens(1000, 1.0, 200000)
	if got != 199000 {
		t.Errorf("CalculatePromptTokens(1000, 1.0, 200000) = %d, want 199000", got)
	}
}

func TestCalculatePromptTokens_ZeroPercentage(t *testing.T) {
	got := CalculatePromptTokens(100, 0.0, 200000)
	if got != 0 {
		t.Errorf("CalculatePromptTokens with zero percentage = %d, want 0", got)
	}
}

func TestCalculatePromptTokens_NegativePercentage(t *testing.T) {
	got := CalculatePromptTokens(100, -0.5, 200000)
	if got != 0 {
		t.Errorf("CalculatePromptTokens with negative percentage = %d, want 0", got)
	}
}

func TestCalculatePromptTokens_ZeroMaxInputTokens(t *testing.T) {
	got := CalculatePromptTokens(100, 0.5, 0)
	if got != 0 {
		t.Errorf("CalculatePromptTokens with zero maxInputTokens = %d, want 0", got)
	}
}

func TestCalculatePromptTokens_NegativeMaxInputTokens(t *testing.T) {
	got := CalculatePromptTokens(100, 0.5, -1)
	if got != 0 {
		t.Errorf("CalculatePromptTokens with negative maxInputTokens = %d, want 0", got)
	}
}

func TestCalculatePromptTokens_CompletionExceedsTotal(t *testing.T) {
	// totalTokens = 200000 * 0.01 = 2000
	// promptTokens = 2000 - 5000 = -3000 → clamped to 0
	got := CalculatePromptTokens(5000, 0.01, 200000)
	if got != 0 {
		t.Errorf("CalculatePromptTokens where completion > total = %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// CountMessageTokens
// ---------------------------------------------------------------------------

func TestCountMessageTokens_EmptySlice(t *testing.T) {
	if got := CountMessageTokens(nil); got != 0 {
		t.Errorf("CountMessageTokens(nil) = %d, want 0", got)
	}
	if got := CountMessageTokens([]converter.UnifiedMessage{}); got != 0 {
		t.Errorf("CountMessageTokens([]) = %d, want 0", got)
	}
}

func TestCountMessageTokens_SingleUserMessage(t *testing.T) {
	msgs := []converter.UnifiedMessage{
		{Role: "user", Content: "Hello, how are you?"},
	}
	got := CountMessageTokens(msgs)
	if got <= 0 {
		t.Errorf("CountMessageTokens with single message = %d, want > 0", got)
	}
}

func TestCountMessageTokens_MultipleMessages(t *testing.T) {
	single := []converter.UnifiedMessage{
		{Role: "user", Content: "Hello"},
	}
	multi := []converter.UnifiedMessage{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there! How can I help you today?"},
		{Role: "user", Content: "Tell me about Go programming."},
	}
	singleCount := CountMessageTokens(single)
	multiCount := CountMessageTokens(multi)
	if multiCount <= singleCount {
		t.Errorf("multiple messages (%d) should produce more tokens than single (%d)", multiCount, singleCount)
	}
}

func TestCountMessageTokens_WithToolCalls(t *testing.T) {
	withoutTools := []converter.UnifiedMessage{
		{Role: "assistant", Content: "Let me check that."},
	}
	withTools := []converter.UnifiedMessage{
		{
			Role:    "assistant",
			Content: "Let me check that.",
			ToolCalls: []map[string]any{
				{
					"id": "call_123",
					"function": map[string]any{
						"name":      "get_weather",
						"arguments": `{"location": "Seattle"}`,
					},
				},
			},
		},
	}
	without := CountMessageTokens(withoutTools)
	with := CountMessageTokens(withTools)
	if with <= without {
		t.Errorf("messages with tool calls (%d) should have more tokens than without (%d)", with, without)
	}
}

func TestCountMessageTokens_WithToolResults(t *testing.T) {
	withoutResults := []converter.UnifiedMessage{
		{Role: "user", Content: "Here is the result."},
	}
	withResults := []converter.UnifiedMessage{
		{
			Role:    "user",
			Content: "Here is the result.",
			ToolResults: []map[string]any{
				{
					"tool_use_id": "call_123",
					"content":     "The weather in Seattle is 72°F and sunny.",
				},
			},
		},
	}
	without := CountMessageTokens(withoutResults)
	with := CountMessageTokens(withResults)
	if with <= without {
		t.Errorf("messages with tool results (%d) should have more tokens than without (%d)", with, without)
	}
}

func TestCountMessageTokens_WithImages(t *testing.T) {
	withoutImages := []converter.UnifiedMessage{
		{Role: "user", Content: "What is this?"},
	}
	withImages := []converter.UnifiedMessage{
		{
			Role:    "user",
			Content: "What is this?",
			Images: []converter.UnifiedImage{
				{MediaType: "image/jpeg", Data: "base64data"},
				{MediaType: "image/png", Data: "base64data2"},
			},
		},
	}
	without := CountMessageTokens(withoutImages)
	with := CountMessageTokens(withImages)
	// Each image adds ~100 tokens.
	if with <= without {
		t.Errorf("messages with images (%d) should have more tokens than without (%d)", with, without)
	}
	// Two images should add roughly 200 raw tokens (before correction).
	diff := with - without
	// After correction: 200 * 1.15 = 230. Allow some tolerance.
	if diff < 200 || diff > 260 {
		t.Errorf("two images should add ~230 tokens (corrected), got diff of %d", diff)
	}
}

func TestCountMessageTokens_IncludesCorrectionFactor(t *testing.T) {
	msgs := []converter.UnifiedMessage{
		{Role: "user", Content: "Hello world, this is a test message for token counting."},
	}
	got := CountMessageTokens(msgs)

	// The result should be > raw count because of the 1.15x correction.
	// We can verify by checking it's roughly 1.15x of what we'd expect.
	// At minimum, the correction should make it larger than a naive count.
	rawEstimate := CountTokens("user") + CountTokens("Hello world, this is a test message for token counting.") + 4 + 3
	corrected := ApplyClaudeCorrectionFactor(rawEstimate)

	// Allow 5% tolerance for rounding.
	tolerance := float64(corrected) * 0.05
	if math.Abs(float64(got)-float64(corrected)) > tolerance+1 {
		t.Errorf("CountMessageTokens = %d, expected ~%d (with correction factor)", got, corrected)
	}
}

// ---------------------------------------------------------------------------
// CountToolsTokens
// ---------------------------------------------------------------------------

func TestCountToolsTokens_EmptySlice(t *testing.T) {
	if got := CountToolsTokens(nil); got != 0 {
		t.Errorf("CountToolsTokens(nil) = %d, want 0", got)
	}
	if got := CountToolsTokens([]converter.UnifiedTool{}); got != 0 {
		t.Errorf("CountToolsTokens([]) = %d, want 0", got)
	}
}

func TestCountToolsTokens_SingleTool(t *testing.T) {
	tools := []converter.UnifiedTool{
		{
			Name:        "get_weather",
			Description: "Get the current weather for a location.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"location": map[string]any{
						"type":        "string",
						"description": "The city and state",
					},
				},
				"required": []any{"location"},
			},
		},
	}
	got := CountToolsTokens(tools)
	if got <= 0 {
		t.Errorf("CountToolsTokens with single tool = %d, want > 0", got)
	}
}

func TestCountToolsTokens_MultipleTools(t *testing.T) {
	single := []converter.UnifiedTool{
		{Name: "tool_a", Description: "Does A."},
	}
	multi := []converter.UnifiedTool{
		{Name: "tool_a", Description: "Does A."},
		{Name: "tool_b", Description: "Does B with a longer description for more tokens."},
		{
			Name:        "tool_c",
			Description: "Does C.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"x": map[string]any{"type": "string"},
					"y": map[string]any{"type": "number"},
				},
			},
		},
	}
	singleCount := CountToolsTokens(single)
	multiCount := CountToolsTokens(multi)
	if multiCount <= singleCount {
		t.Errorf("multiple tools (%d) should produce more tokens than single (%d)", multiCount, singleCount)
	}
}

func TestCountToolsTokens_ToolWithNoSchema(t *testing.T) {
	tools := []converter.UnifiedTool{
		{Name: "simple_tool", Description: "A simple tool with no parameters."},
	}
	got := CountToolsTokens(tools)
	if got <= 0 {
		t.Errorf("CountToolsTokens with no schema = %d, want > 0", got)
	}
}

func TestCountToolsTokens_IncludesCorrectionFactor(t *testing.T) {
	tools := []converter.UnifiedTool{
		{Name: "my_tool", Description: "This is a tool description."},
	}
	got := CountToolsTokens(tools)

	// Manually compute expected: 4 overhead + name tokens + desc tokens, then * 1.15.
	rawOverhead := 4
	rawName := CountTokens("my_tool")
	rawDesc := CountTokens("This is a tool description.")
	rawTotal := rawOverhead + rawName + rawDesc
	corrected := ApplyClaudeCorrectionFactor(rawTotal)

	tolerance := float64(corrected) * 0.05
	if math.Abs(float64(got)-float64(corrected)) > tolerance+1 {
		t.Errorf("CountToolsTokens = %d, expected ~%d (with correction factor)", got, corrected)
	}
}

// ---------------------------------------------------------------------------
// EstimatePromptTokensFromMessages
// ---------------------------------------------------------------------------

func TestEstimatePromptTokensFromMessages_EmptyInputs(t *testing.T) {
	got := EstimatePromptTokensFromMessages(nil, nil)
	if got != 0 {
		t.Errorf("EstimatePromptTokensFromMessages(nil, nil) = %d, want 0", got)
	}
}

func TestEstimatePromptTokensFromMessages_MessagesOnly(t *testing.T) {
	msgs := []converter.UnifiedMessage{
		{Role: "user", Content: "Hello"},
	}
	got := EstimatePromptTokensFromMessages(msgs, nil)
	msgTokens := CountMessageTokens(msgs)
	if got != msgTokens {
		t.Errorf("EstimatePromptTokensFromMessages with no tools = %d, want %d", got, msgTokens)
	}
}

func TestEstimatePromptTokensFromMessages_ToolsOnly(t *testing.T) {
	tools := []converter.UnifiedTool{
		{Name: "tool_a", Description: "Does A."},
	}
	got := EstimatePromptTokensFromMessages(nil, tools)
	toolTokens := CountToolsTokens(tools)
	if got != toolTokens {
		t.Errorf("EstimatePromptTokensFromMessages with no messages = %d, want %d", got, toolTokens)
	}
}

func TestEstimatePromptTokensFromMessages_BothMessagesAndTools(t *testing.T) {
	msgs := []converter.UnifiedMessage{
		{Role: "user", Content: "Use the tool to get weather."},
	}
	tools := []converter.UnifiedTool{
		{Name: "get_weather", Description: "Get weather for a location."},
	}
	got := EstimatePromptTokensFromMessages(msgs, tools)
	msgTokens := CountMessageTokens(msgs)
	toolTokens := CountToolsTokens(tools)
	want := msgTokens + toolTokens
	if got != want {
		t.Errorf("EstimatePromptTokensFromMessages = %d, want %d (msgs=%d + tools=%d)", got, want, msgTokens, toolTokens)
	}
}

// ---------------------------------------------------------------------------
// Integration: encoding initialisation
// ---------------------------------------------------------------------------

func TestGetEncoding_Initialises(t *testing.T) {
	e := getEncoding()
	if e == nil {
		t.Fatal("getEncoding() returned nil; tiktoken should initialise successfully")
	}
}

func TestCountTokens_UsesRealEncoding(t *testing.T) {
	// "hello world" should be 2 tokens with cl100k_base.
	got := CountTokens("hello world")
	if got < 1 || got > 10 {
		t.Errorf("CountTokens(\"hello world\") = %d, expected a small positive number", got)
	}
}
