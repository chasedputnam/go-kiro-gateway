package streaming

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jwadow/kiro-gateway/gateway/internal/config"
	"github.com/jwadow/kiro-gateway/gateway/internal/thinking"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// collectEvents drains a KiroEvent channel into a slice.
func collectEvents(ch <-chan KiroEvent) []KiroEvent {
	var events []KiroEvent
	for evt := range ch {
		events = append(events, evt)
	}
	return events
}

// makeKiroChunk builds a raw byte payload that the event stream parser can
// extract. The parser scans for known JSON prefixes, so we just embed the
// JSON directly.
func makeContentChunk(content string) []byte {
	j, _ := json.Marshal(map[string]string{"content": content})
	return j
}

func makeToolStartChunk(name, toolUseID string) []byte {
	j, _ := json.Marshal(map[string]string{"name": name, "toolUseId": toolUseID})
	return j
}

func makeToolInputChunk(input string) []byte {
	j, _ := json.Marshal(map[string]string{"input": input})
	return j
}

func makeToolStopChunk() []byte {
	j, _ := json.Marshal(map[string]bool{"stop": true})
	return j
}

func makeUsageChunk(credits float64) []byte {
	j, _ := json.Marshal(map[string]any{"usage": map[string]float64{"credits": credits}})
	return j
}

func makeContextUsageChunk(pct float64) []byte {
	j, _ := json.Marshal(map[string]float64{"contextUsagePercentage": pct})
	return j
}

// combineChunks concatenates multiple byte slices with a separator that
// won't interfere with JSON parsing.
func combineChunks(chunks ...[]byte) []byte {
	var buf bytes.Buffer
	for _, c := range chunks {
		buf.Write(c)
	}
	return buf.Bytes()
}

// defaultOpts returns StreamOptions with thinking disabled and no timeout.
func defaultOpts() StreamOptions {
	return StreamOptions{
		FirstTokenTimeout:    0,
		EnableThinkingParser: false,
	}
}

// thinkingOpts returns StreamOptions with thinking enabled.
func thinkingOpts(mode thinking.HandlingMode) StreamOptions {
	return StreamOptions{
		FirstTokenTimeout:    0,
		EnableThinkingParser: true,
		ThinkingHandlingMode: mode,
	}
}

// ---------------------------------------------------------------------------
// Tests: ParseKiroStream — content events
// ---------------------------------------------------------------------------

func TestParseKiroStream_ContentEvents(t *testing.T) {
	data := combineChunks(
		makeContentChunk("Hello"),
		makeContentChunk(" world"),
	)
	r := bytes.NewReader(data)

	ch := ParseKiroStream(context.Background(), r, defaultOpts())
	events := collectEvents(ch)

	// Expect 2 content events + 1 done event.
	contentEvents := filterByType(events, EventTypeContent)
	if len(contentEvents) != 2 {
		t.Fatalf("expected 2 content events, got %d: %+v", len(contentEvents), events)
	}
	if contentEvents[0].Content != "Hello" {
		t.Errorf("first content = %q, want %q", contentEvents[0].Content, "Hello")
	}
	if contentEvents[1].Content != " world" {
		t.Errorf("second content = %q, want %q", contentEvents[1].Content, " world")
	}

	doneEvents := filterByType(events, EventTypeDone)
	if len(doneEvents) != 1 {
		t.Errorf("expected 1 done event, got %d", len(doneEvents))
	}
}

// ---------------------------------------------------------------------------
// Tests: ParseKiroStream — tool call accumulation
// ---------------------------------------------------------------------------

func TestParseKiroStream_ToolCallAccumulation(t *testing.T) {
	data := combineChunks(
		makeToolStartChunk("get_weather", "call_123"),
		makeToolInputChunk(`{"cit`),
		makeToolInputChunk(`y": "London"}`),
		makeToolStopChunk(),
	)
	r := bytes.NewReader(data)

	ch := ParseKiroStream(context.Background(), r, defaultOpts())
	events := collectEvents(ch)

	toolEvents := filterByType(events, EventTypeToolCall)
	if len(toolEvents) != 1 {
		t.Fatalf("expected 1 tool call event, got %d", len(toolEvents))
	}

	tc := toolEvents[0].ToolCall
	if tc.Name != "get_weather" {
		t.Errorf("tool name = %q, want %q", tc.Name, "get_weather")
	}
	if tc.ID != "call_123" {
		t.Errorf("tool ID = %q, want %q", tc.ID, "call_123")
	}

	// Arguments should be valid JSON.
	var args map[string]string
	if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
		t.Fatalf("tool arguments not valid JSON: %v", err)
	}
	if args["city"] != "London" {
		t.Errorf("args[city] = %q, want %q", args["city"], "London")
	}
	if tc.IsTruncated {
		t.Error("tool call should not be marked as truncated")
	}
}

// ---------------------------------------------------------------------------
// Tests: ParseKiroStream — truncated tool call arguments
// ---------------------------------------------------------------------------

func TestParseKiroStream_TruncatedToolCallArgs(t *testing.T) {
	data := combineChunks(
		makeToolStartChunk("read_file", "call_456"),
		makeToolInputChunk(`{"path": "/some/very/long`), // truncated — no closing brace
		makeToolStopChunk(),
	)
	r := bytes.NewReader(data)

	ch := ParseKiroStream(context.Background(), r, defaultOpts())
	events := collectEvents(ch)

	toolEvents := filterByType(events, EventTypeToolCall)
	if len(toolEvents) != 1 {
		t.Fatalf("expected 1 tool call event, got %d", len(toolEvents))
	}

	tc := toolEvents[0].ToolCall
	if tc.Arguments != "{}" {
		t.Errorf("truncated args should be {}, got %q", tc.Arguments)
	}
	if !tc.IsTruncated {
		t.Error("tool call should be marked as truncated")
	}
}

// ---------------------------------------------------------------------------
// Tests: ParseKiroStream — usage and context usage events
// ---------------------------------------------------------------------------

func TestParseKiroStream_UsageEvents(t *testing.T) {
	data := combineChunks(
		makeContentChunk("answer"),
		makeUsageChunk(0.5),
		makeContextUsageChunk(42.5),
	)
	r := bytes.NewReader(data)

	ch := ParseKiroStream(context.Background(), r, defaultOpts())
	events := collectEvents(ch)

	usageEvents := filterByType(events, EventTypeUsage)
	if len(usageEvents) != 1 {
		t.Fatalf("expected 1 usage event, got %d", len(usageEvents))
	}
	if usageEvents[0].Usage.Credits != 0.5 {
		t.Errorf("credits = %f, want 0.5", usageEvents[0].Usage.Credits)
	}

	// Context usage comes as a content event with ContextUsagePercentage set.
	var ctxPct float64
	for _, evt := range events {
		if evt.ContextUsagePercentage > 0 {
			ctxPct = evt.ContextUsagePercentage
		}
	}
	if ctxPct != 42.5 {
		t.Errorf("context usage percentage = %f, want 42.5", ctxPct)
	}
}

// ---------------------------------------------------------------------------
// Tests: ParseKiroStream — thinking parser integration
// ---------------------------------------------------------------------------

func TestParseKiroStream_ThinkingParser(t *testing.T) {
	data := combineChunks(
		makeContentChunk("<thinking>reasoning here</thinking>The answer is 42"),
	)
	r := bytes.NewReader(data)

	opts := thinkingOpts(thinking.AsReasoningContent)
	ch := ParseKiroStream(context.Background(), r, opts)
	events := collectEvents(ch)

	thinkingEvents := filterByType(events, EventTypeThinking)
	contentEvents := filterByType(events, EventTypeContent)

	if len(thinkingEvents) == 0 {
		t.Fatal("expected at least 1 thinking event")
	}

	// Collect all thinking content.
	var thinkingContent string
	for _, evt := range thinkingEvents {
		thinkingContent += evt.ThinkingContent
	}
	if !strings.Contains(thinkingContent, "reasoning here") {
		t.Errorf("thinking content = %q, want to contain %q", thinkingContent, "reasoning here")
	}

	// Collect all regular content.
	var regularContent string
	for _, evt := range contentEvents {
		regularContent += evt.Content
	}
	if !strings.Contains(regularContent, "The answer is 42") {
		t.Errorf("regular content = %q, want to contain %q", regularContent, "The answer is 42")
	}
}

// ---------------------------------------------------------------------------
// Tests: ParseKiroStream — first token timeout
// ---------------------------------------------------------------------------

func TestParseKiroStream_FirstTokenTimeout(t *testing.T) {
	// Create a reader that blocks forever.
	pr, _ := io.Pipe()

	opts := StreamOptions{
		FirstTokenTimeout:    100 * time.Millisecond,
		EnableThinkingParser: false,
	}

	ch := ParseKiroStream(context.Background(), pr, opts)
	events := collectEvents(ch)

	// Should get an error event with FirstTokenTimeoutError.
	errorEvents := filterByType(events, EventTypeError)
	if len(errorEvents) == 0 {
		t.Fatal("expected an error event for first token timeout")
	}

	if !isFirstTokenTimeout(errorEvents[0].Error) {
		t.Errorf("expected FirstTokenTimeoutError, got %T: %v", errorEvents[0].Error, errorEvents[0].Error)
	}
}

// ---------------------------------------------------------------------------
// Tests: Tool call deduplication
// ---------------------------------------------------------------------------

func TestDeduplicateToolCalls_ByID(t *testing.T) {
	calls := []ToolCallInfo{
		{ID: "call_1", Name: "func_a", Arguments: "{}"},
		{ID: "call_1", Name: "func_a", Arguments: `{"key":"value"}`},
	}

	result := deduplicateToolCalls(calls)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result))
	}
	if result[0].Arguments != `{"key":"value"}` {
		t.Errorf("should keep the more complete arguments, got %q", result[0].Arguments)
	}
}

func TestDeduplicateToolCalls_ByNameArgs(t *testing.T) {
	calls := []ToolCallInfo{
		{ID: "call_1", Name: "func_a", Arguments: `{"x":1}`},
		{ID: "call_2", Name: "func_a", Arguments: `{"x":1}`},
	}

	result := deduplicateToolCalls(calls)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool call after name+args dedup, got %d", len(result))
	}
}

func TestDeduplicateToolCalls_DifferentCalls(t *testing.T) {
	calls := []ToolCallInfo{
		{ID: "call_1", Name: "func_a", Arguments: `{"x":1}`},
		{ID: "call_2", Name: "func_b", Arguments: `{"y":2}`},
	}

	result := deduplicateToolCalls(calls)
	if len(result) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(result))
	}
}

func TestDeduplicateToolCalls_Empty(t *testing.T) {
	result := deduplicateToolCalls(nil)
	if result != nil {
		t.Errorf("expected nil for empty input, got %v", result)
	}
}

// ---------------------------------------------------------------------------
// Tests: StreamWithFirstTokenRetry
// ---------------------------------------------------------------------------

func TestStreamWithFirstTokenRetry_Success(t *testing.T) {
	data := combineChunks(
		makeContentChunk("Hello"),
	)

	makeReq := func(ctx context.Context) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewReader(data)),
		}, nil
	}

	opts := defaultOpts()
	ch := StreamWithFirstTokenRetry(context.Background(), makeReq, opts, 3)
	events := collectEvents(ch)

	contentEvents := filterByType(events, EventTypeContent)
	if len(contentEvents) == 0 {
		t.Fatal("expected at least 1 content event")
	}
	if contentEvents[0].Content != "Hello" {
		t.Errorf("content = %q, want %q", contentEvents[0].Content, "Hello")
	}
}

func TestStreamWithFirstTokenRetry_HTTPError(t *testing.T) {
	makeReq := func(ctx context.Context) (*http.Response, error) {
		return &http.Response{
			StatusCode: 500,
			Body:       io.NopCloser(strings.NewReader("internal error")),
		}, nil
	}

	opts := defaultOpts()
	ch := StreamWithFirstTokenRetry(context.Background(), makeReq, opts, 3)
	events := collectEvents(ch)

	errorEvents := filterByType(events, EventTypeError)
	if len(errorEvents) == 0 {
		t.Fatal("expected an error event for HTTP 500")
	}
	if !strings.Contains(errorEvents[0].Error.Error(), "500") {
		t.Errorf("error should mention status code 500, got: %v", errorEvents[0].Error)
	}
}

func TestStreamWithFirstTokenRetry_RequestError(t *testing.T) {
	makeReq := func(ctx context.Context) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	}

	opts := defaultOpts()
	ch := StreamWithFirstTokenRetry(context.Background(), makeReq, opts, 3)
	events := collectEvents(ch)

	errorEvents := filterByType(events, EventTypeError)
	if len(errorEvents) == 0 {
		t.Fatal("expected an error event for request failure")
	}
}

func TestStreamWithFirstTokenRetry_TimeoutRetry(t *testing.T) {
	attempt := 0
	data := combineChunks(makeContentChunk("success"))

	makeReq := func(ctx context.Context) (*http.Response, error) {
		attempt++
		if attempt == 1 {
			// First attempt: return a reader that blocks (triggers timeout).
			pr, _ := io.Pipe()
			return &http.Response{
				StatusCode: 200,
				Body:       pr,
			}, nil
		}
		// Second attempt: return data immediately.
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewReader(data)),
		}, nil
	}

	opts := StreamOptions{
		FirstTokenTimeout:    100 * time.Millisecond,
		EnableThinkingParser: false,
	}

	ch := StreamWithFirstTokenRetry(context.Background(), makeReq, opts, 3)
	events := collectEvents(ch)

	contentEvents := filterByType(events, EventTypeContent)
	if len(contentEvents) == 0 {
		t.Fatal("expected content events after retry")
	}
	if contentEvents[0].Content != "success" {
		t.Errorf("content = %q, want %q", contentEvents[0].Content, "success")
	}
	if attempt != 2 {
		t.Errorf("expected 2 attempts, got %d", attempt)
	}
}

func TestStreamWithFirstTokenRetry_AllRetriesExhausted(t *testing.T) {
	makeReq := func(ctx context.Context) (*http.Response, error) {
		// Always return a blocking reader.
		pr, _ := io.Pipe()
		return &http.Response{
			StatusCode: 200,
			Body:       pr,
		}, nil
	}

	opts := StreamOptions{
		FirstTokenTimeout:    50 * time.Millisecond,
		EnableThinkingParser: false,
	}

	ch := StreamWithFirstTokenRetry(context.Background(), makeReq, opts, 2)
	events := collectEvents(ch)

	errorEvents := filterByType(events, EventTypeError)
	if len(errorEvents) == 0 {
		t.Fatal("expected an error event when all retries exhausted")
	}
	errMsg := errorEvents[0].Error.Error()
	if !strings.Contains(errMsg, "504") {
		t.Errorf("error should mention 504, got: %v", errMsg)
	}
}

// ---------------------------------------------------------------------------
// Tests: DefaultStreamOptions
// ---------------------------------------------------------------------------

func TestDefaultStreamOptions(t *testing.T) {
	cfg := &config.Config{
		FirstTokenTimeout:          15 * time.Second,
		FakeReasoningEnabled:       true,
		FakeReasoningHandling:      "as_reasoning_content",
		FakeReasoningOpenTags:      []string{"<thinking>", "<think>"},
		FakeReasoningInitialBuffer: 30,
	}

	opts := DefaultStreamOptions(cfg)

	if opts.FirstTokenTimeout != 15*time.Second {
		t.Errorf("FirstTokenTimeout = %v, want 15s", opts.FirstTokenTimeout)
	}
	if !opts.EnableThinkingParser {
		t.Error("EnableThinkingParser should be true")
	}
	if opts.ThinkingHandlingMode != thinking.AsReasoningContent {
		t.Errorf("ThinkingHandlingMode = %q, want %q", opts.ThinkingHandlingMode, thinking.AsReasoningContent)
	}
	if len(opts.ThinkingOpenTags) != 2 {
		t.Errorf("ThinkingOpenTags length = %d, want 2", len(opts.ThinkingOpenTags))
	}
	if opts.ThinkingInitialBuffer != 30 {
		t.Errorf("ThinkingInitialBuffer = %d, want 30", opts.ThinkingInitialBuffer)
	}
}

// ---------------------------------------------------------------------------
// Tests: FirstTokenTimeoutError
// ---------------------------------------------------------------------------

func TestFirstTokenTimeoutError(t *testing.T) {
	err := &FirstTokenTimeoutError{Timeout: 15 * time.Second}
	if !strings.Contains(err.Error(), "15s") {
		t.Errorf("error message = %q, want to contain '15s'", err.Error())
	}
	if !isFirstTokenTimeout(err) {
		t.Error("isFirstTokenTimeout should return true")
	}
	if isFirstTokenTimeout(fmt.Errorf("other error")) {
		t.Error("isFirstTokenTimeout should return false for non-timeout errors")
	}
	if isFirstTokenTimeout(nil) {
		t.Error("isFirstTokenTimeout should return false for nil")
	}
}

// ---------------------------------------------------------------------------
// Tests: ParseKiroStream — empty reader
// ---------------------------------------------------------------------------

func TestParseKiroStream_EmptyReader(t *testing.T) {
	r := bytes.NewReader(nil)
	ch := ParseKiroStream(context.Background(), r, defaultOpts())
	events := collectEvents(ch)

	doneEvents := filterByType(events, EventTypeDone)
	if len(doneEvents) != 1 {
		t.Errorf("expected 1 done event for empty reader, got %d", len(doneEvents))
	}
}

// ---------------------------------------------------------------------------
// Tests: ParseKiroStream — multiple tool calls
// ---------------------------------------------------------------------------

func TestParseKiroStream_MultipleToolCalls(t *testing.T) {
	data := combineChunks(
		makeToolStartChunk("func_a", "call_1"),
		makeToolInputChunk(`{"x": 1}`),
		makeToolStopChunk(),
		makeToolStartChunk("func_b", "call_2"),
		makeToolInputChunk(`{"y": 2}`),
		makeToolStopChunk(),
	)
	r := bytes.NewReader(data)

	ch := ParseKiroStream(context.Background(), r, defaultOpts())
	events := collectEvents(ch)

	toolEvents := filterByType(events, EventTypeToolCall)
	if len(toolEvents) != 2 {
		t.Fatalf("expected 2 tool call events, got %d", len(toolEvents))
	}
	// Tool call order may vary due to map iteration in deduplication.
	names := map[string]bool{}
	for _, evt := range toolEvents {
		names[evt.ToolCall.Name] = true
	}
	if !names["func_a"] {
		t.Error("expected func_a in tool calls")
	}
	if !names["func_b"] {
		t.Error("expected func_b in tool calls")
	}
}

// ---------------------------------------------------------------------------
// Tests: ParseKiroStream — context cancellation
// ---------------------------------------------------------------------------

func TestParseKiroStream_ContextCancellation(t *testing.T) {
	// Create a reader that blocks.
	pr, pw := io.Pipe()
	defer pw.Close()

	ctx, cancel := context.WithCancel(context.Background())

	ch := ParseKiroStream(ctx, pr, defaultOpts())

	// Cancel context immediately.
	cancel()

	events := collectEvents(ch)
	// Should exit cleanly without hanging.
	_ = events
}

// ---------------------------------------------------------------------------
// Tests: ParseKiroStream — tool call with empty arguments
// ---------------------------------------------------------------------------

func TestParseKiroStream_ToolCallEmptyArgs(t *testing.T) {
	data := combineChunks(
		makeToolStartChunk("no_args_func", "call_789"),
		makeToolStopChunk(),
	)
	r := bytes.NewReader(data)

	ch := ParseKiroStream(context.Background(), r, defaultOpts())
	events := collectEvents(ch)

	toolEvents := filterByType(events, EventTypeToolCall)
	if len(toolEvents) != 1 {
		t.Fatalf("expected 1 tool call event, got %d", len(toolEvents))
	}
	if toolEvents[0].ToolCall.Arguments != "{}" {
		t.Errorf("empty args should be {}, got %q", toolEvents[0].ToolCall.Arguments)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func filterByType(events []KiroEvent, typ string) []KiroEvent {
	var filtered []KiroEvent
	for _, evt := range events {
		if evt.Type == typ {
			filtered = append(filtered, evt)
		}
	}
	return filtered
}
