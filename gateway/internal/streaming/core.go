// Package streaming provides the core streaming logic for parsing Kiro API
// responses and formatting them as SSE events for OpenAI and Anthropic clients.
//
// This file contains the core (format-agnostic) layer:
//   - KiroEvent: unified event type emitted by the stream parser
//   - ToolCallInfo / UsageInfo: structured data carried by events
//   - ParseKiroStream: reads an io.Reader, parses through the event stream
//     parser and thinking FSM, accumulates tool calls, deduplicates, and
//     sends events on a channel
//   - StreamWithFirstTokenRetry: wraps a request function with first-token
//     timeout and transparent retry logic
//
// API-specific formatters (openai.go, anthropic.go) consume the channel
// returned by ParseKiroStream and write SSE chunks to the HTTP response.
package streaming

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/config"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/parser"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/thinking"
)

// ---------------------------------------------------------------------------
// Event types
// ---------------------------------------------------------------------------

const (
	EventTypeContent  = "content"
	EventTypeThinking = "thinking"
	EventTypeToolCall = "tool_call"
	EventTypeUsage    = "usage"
	EventTypeDone     = "done"
	EventTypeError    = "error"
)

// ---------------------------------------------------------------------------
// Data structs
// ---------------------------------------------------------------------------

// ToolCallInfo holds a completed, deduplicated tool call ready for SSE
// formatting. Arguments is always a valid JSON string (or "{}").
type ToolCallInfo struct {
	ID        string // tool call ID from Kiro API (or generated)
	Name      string // function name
	Arguments string // JSON string of arguments

	// IsTruncated is true when the original arguments failed JSON parsing
	// due to upstream truncation. Arguments is set to "{}" in that case.
	IsTruncated bool
}

// UsageInfo holds credit/token usage data from a Kiro usage event.
type UsageInfo struct {
	Credits float64
}

// KiroEvent is the unified, API-agnostic event emitted by ParseKiroStream.
// Downstream formatters inspect Type to decide which fields are populated.
type KiroEvent struct {
	// Type is one of the EventType* constants.
	Type string

	// Content holds text content (Type == EventTypeContent).
	Content string

	// ThinkingContent holds reasoning content (Type == EventTypeThinking).
	ThinkingContent string

	// IsFirstThinkingChunk is true for the first thinking event in a stream.
	IsFirstThinkingChunk bool
	// IsLastThinkingChunk is true for the last thinking event in a stream.
	IsLastThinkingChunk bool

	// ToolCall holds a completed tool call (Type == EventTypeToolCall).
	ToolCall *ToolCallInfo

	// Usage holds credit consumption data (Type == EventTypeUsage).
	Usage *UsageInfo

	// ContextUsagePercentage carries the context usage percentage from
	// Kiro API (Type == EventTypeContent with this field > 0, or a
	// dedicated event). Formatters use this for token calculation.
	ContextUsagePercentage float64

	// Error carries the error when Type == EventTypeError.
	Error error
}

// ---------------------------------------------------------------------------
// ParseKiroStream options
// ---------------------------------------------------------------------------

// StreamOptions configures ParseKiroStream behaviour.
type StreamOptions struct {
	// FirstTokenTimeout is how long to wait for the first content/thinking
	// event before raising a timeout. Zero disables the timeout.
	FirstTokenTimeout time.Duration

	// EnableThinkingParser controls whether the thinking FSM is active.
	EnableThinkingParser bool

	// ThinkingHandlingMode is the thinking parser handling mode.
	ThinkingHandlingMode thinking.HandlingMode

	// ThinkingOpenTags overrides the default set of opening tags.
	ThinkingOpenTags []string

	// ThinkingInitialBuffer overrides the initial buffer size.
	ThinkingInitialBuffer int
}

// DefaultStreamOptions returns sensible defaults derived from a Config.
func DefaultStreamOptions(cfg *config.Config) StreamOptions {
	return StreamOptions{
		FirstTokenTimeout:     cfg.FirstTokenTimeout,
		EnableThinkingParser:  cfg.FakeReasoningEnabled,
		ThinkingHandlingMode:  thinking.HandlingMode(cfg.FakeReasoningHandling),
		ThinkingOpenTags:      cfg.FakeReasoningOpenTags,
		ThinkingInitialBuffer: cfg.FakeReasoningInitialBuffer,
	}
}

// ---------------------------------------------------------------------------
// ParseKiroStream
// ---------------------------------------------------------------------------

// ParseKiroStream reads raw bytes from r (typically an HTTP response body),
// parses them through the AWS event stream parser and thinking FSM,
// accumulates tool calls, deduplicates them, and sends KiroEvent values on
// the returned channel. The channel is closed when the stream ends or an
// error occurs.
//
// The caller should range over the returned channel. If ctx is cancelled
// the goroutine drains remaining data and exits.
func ParseKiroStream(ctx context.Context, r io.Reader, opts StreamOptions) <-chan KiroEvent {
	ch := make(chan KiroEvent, 64)

	go func() {
		defer close(ch)
		parseKiroStreamInternal(ctx, r, opts, ch)
	}()

	return ch
}

// send is a helper that sends an event on ch, respecting context cancellation.
func send(ctx context.Context, ch chan<- KiroEvent, evt KiroEvent) bool {
	select {
	case ch <- evt:
		return true
	case <-ctx.Done():
		return false
	}
}

// parseKiroStreamInternal contains the actual parsing logic. It runs inside
// the goroutine spawned by ParseKiroStream.
func parseKiroStreamInternal(ctx context.Context, r io.Reader, opts StreamOptions, ch chan<- KiroEvent) {
	// Initialise thinking parser if enabled.
	var thinkingParser *thinking.Parser
	if opts.EnableThinkingParser {
		thinkingParser = thinking.NewParser(
			opts.ThinkingHandlingMode,
			opts.ThinkingOpenTags,
			opts.ThinkingInitialBuffer,
		)
		log.Printf("Thinking parser initialised with mode: %s", opts.ThinkingHandlingMode)
	}

	// Tool call accumulation state.
	type toolCallState struct {
		id        string
		name      string
		argsAccum strings.Builder
	}
	var currentTC *toolCallState
	var completedToolCalls []ToolCallInfo

	// finalizeToolCall validates and stores the current tool call.
	finalizeToolCall := func() {
		if currentTC == nil {
			return
		}
		args := strings.TrimSpace(currentTC.argsAccum.String())
		tc := ToolCallInfo{
			ID:   currentTC.id,
			Name: currentTC.name,
		}

		if args == "" {
			tc.Arguments = "{}"
		} else {
			// Try to parse as JSON to validate.
			var parsed json.RawMessage
			if err := json.Unmarshal([]byte(args), &parsed); err != nil {
				// Truncated or malformed — check if truncated.
				if parser.IsToolCallTruncated(args) {
					log.Printf("Tool call truncated by Kiro API: tool=%q, id=%s, size=%d bytes",
						currentTC.name, currentTC.id, len(args))
					tc.IsTruncated = true
				} else {
					log.Printf("Failed to parse tool %q arguments: %v. Raw: %.200s",
						currentTC.name, err, args)
				}
				tc.Arguments = "{}"
			} else {
				// Re-serialize to ensure canonical JSON.
				tc.Arguments = string(parsed)
			}
		}

		completedToolCalls = append(completedToolCalls, tc)
		currentTC = nil
	}

	// Read loop — read chunks from the reader.
	buf := make([]byte, 32*1024) // 32 KB read buffer
	firstTokenReceived := false

	// Set up first-token timeout context if configured.
	var firstTokenCtx context.Context
	var firstTokenCancel context.CancelFunc
	if opts.FirstTokenTimeout > 0 {
		firstTokenCtx, firstTokenCancel = context.WithTimeout(ctx, opts.FirstTokenTimeout)
	} else {
		firstTokenCtx = ctx
		firstTokenCancel = func() {}
	}
	defer firstTokenCancel()

	// readChunk reads from r with first-token timeout awareness.
	// Returns the bytes read, or an error. io.EOF signals end of stream.
	type readResult struct {
		n   int
		err error
	}

	for {
		// Check parent context.
		if ctx.Err() != nil {
			return
		}

		// Read with timeout awareness.
		resultCh := make(chan readResult, 1)
		go func() {
			n, err := r.Read(buf)
			resultCh <- readResult{n, err}
		}()

		var rr readResult
		if !firstTokenReceived {
			// Wait with first-token timeout.
			select {
			case rr = <-resultCh:
				// Got data or EOF.
			case <-firstTokenCtx.Done():
				// First token timeout expired.
				send(ctx, ch, KiroEvent{
					Type:  EventTypeError,
					Error: &FirstTokenTimeoutError{Timeout: opts.FirstTokenTimeout},
				})
				return
			case <-ctx.Done():
				return
			}
		} else {
			// After first token, just wait for data or parent context.
			select {
			case rr = <-resultCh:
			case <-ctx.Done():
				return
			}
		}

		if rr.n > 0 {
			chunk := buf[:rr.n]

			// Parse through event stream parser.
			events := parser.ParseEventStream(chunk)

			for _, evt := range events {
				switch evt.Type {
				case parser.EventContent:
					if thinkingParser != nil {
						result := thinkingParser.ProcessChunk(evt.Content)
						if result.ThinkingContent != "" {
							if !send(ctx, ch, KiroEvent{
								Type:            EventTypeThinking,
								ThinkingContent: result.ThinkingContent,
							}) {
								return
							}
							if !firstTokenReceived {
								firstTokenReceived = true
								firstTokenCancel() // Cancel timeout early.
							}
						}
						if result.RegularContent != "" {
							if !send(ctx, ch, KiroEvent{
								Type:    EventTypeContent,
								Content: result.RegularContent,
							}) {
								return
							}
							if !firstTokenReceived {
								firstTokenReceived = true
								firstTokenCancel() // Cancel timeout early.
							}
						}
					} else {
						if !send(ctx, ch, KiroEvent{
							Type:    EventTypeContent,
							Content: evt.Content,
						}) {
							return
						}
						if !firstTokenReceived {
							firstTokenReceived = true
							firstTokenCancel() // Cancel timeout early.
						}
					}

				case parser.EventToolStart:
					// Finalize any in-progress tool call.
					finalizeToolCall()
					currentTC = &toolCallState{
						id:   evt.ToolUseID,
						name: evt.ToolName,
					}

				case parser.EventToolInput:
					if currentTC != nil {
						currentTC.argsAccum.WriteString(evt.ToolInput)
					}

				case parser.EventToolStop:
					finalizeToolCall()

				case parser.EventUsage:
					if evt.Usage != nil {
						if !send(ctx, ch, KiroEvent{
							Type:  EventTypeUsage,
							Usage: &UsageInfo{Credits: evt.Usage.Credits},
						}) {
							return
						}
					}

				case parser.EventContextUsage:
					if !send(ctx, ch, KiroEvent{
						Type:                   EventTypeContent,
						ContextUsagePercentage: evt.ContextUsagePercentage,
					}) {
						return
					}
				}
			}
		}

		if rr.err != nil {
			if rr.err == io.EOF {
				break // Normal end of stream.
			}
			// Read error.
			send(ctx, ch, KiroEvent{
				Type:  EventTypeError,
				Error: fmt.Errorf("stream read error: %w", rr.err),
			})
			return
		}
	}

	// Finalize any in-progress tool call.
	finalizeToolCall()

	// Finalize thinking parser.
	if thinkingParser != nil {
		final := thinkingParser.Finalize()
		if final.ThinkingContent != "" {
			send(ctx, ch, KiroEvent{
				Type:            EventTypeThinking,
				ThinkingContent: final.ThinkingContent,
			})
		}
		if final.RegularContent != "" {
			send(ctx, ch, KiroEvent{
				Type:    EventTypeContent,
				Content: final.RegularContent,
			})
		}
	}

	// Deduplicate and emit tool calls.
	deduplicated := deduplicateToolCalls(completedToolCalls)
	for i := range deduplicated {
		if !send(ctx, ch, KiroEvent{
			Type:     EventTypeToolCall,
			ToolCall: &deduplicated[i],
		}) {
			return
		}
	}

	// Send done event.
	send(ctx, ch, KiroEvent{Type: EventTypeDone})
}

// ---------------------------------------------------------------------------
// Tool call deduplication
// ---------------------------------------------------------------------------

// deduplicateToolCalls removes duplicate tool calls using two criteria:
//  1. By ID — if multiple calls share the same ID, keep the one with the
//     most complete (longest, non-"{}") arguments.
//  2. By name+arguments — remove exact duplicates.
func deduplicateToolCalls(calls []ToolCallInfo) []ToolCallInfo {
	if len(calls) == 0 {
		return nil
	}

	// Phase 1: deduplicate by ID.
	byID := make(map[string]*ToolCallInfo, len(calls))
	var noID []ToolCallInfo

	for i := range calls {
		tc := &calls[i]
		if tc.ID == "" {
			noID = append(noID, *tc)
			continue
		}
		existing, ok := byID[tc.ID]
		if !ok {
			byID[tc.ID] = tc
			continue
		}
		// Keep the one with more complete arguments.
		if tc.Arguments != "{}" && (existing.Arguments == "{}" || len(tc.Arguments) > len(existing.Arguments)) {
			log.Printf("Replacing tool call %s with better arguments: %d -> %d",
				tc.ID, len(existing.Arguments), len(tc.Arguments))
			byID[tc.ID] = tc
		}
	}

	// Collect: ID-based first, then no-ID.
	merged := make([]ToolCallInfo, 0, len(byID)+len(noID))
	for _, tc := range byID {
		merged = append(merged, *tc)
	}
	merged = append(merged, noID...)

	// Phase 2: deduplicate by name+arguments.
	seen := make(map[string]struct{}, len(merged))
	unique := make([]ToolCallInfo, 0, len(merged))

	for _, tc := range merged {
		key := tc.Name + "-" + tc.Arguments
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, tc)
	}

	if len(calls) != len(unique) {
		log.Printf("Deduplicated tool calls: %d -> %d", len(calls), len(unique))
	}

	return unique
}

// ---------------------------------------------------------------------------
// First-token timeout error
// ---------------------------------------------------------------------------

// FirstTokenTimeoutError is returned when the model does not produce a first
// content or thinking token within the configured timeout.
type FirstTokenTimeoutError struct {
	Timeout time.Duration
}

func (e *FirstTokenTimeoutError) Error() string {
	return fmt.Sprintf("no response within %s", e.Timeout)
}

// ---------------------------------------------------------------------------
// StreamWithFirstTokenRetry
// ---------------------------------------------------------------------------

// MakeRequestFunc creates a new HTTP request to the Kiro API and returns
// the response. Each call should produce a fresh request (for retry).
type MakeRequestFunc func(ctx context.Context) (*http.Response, error)

// StreamWithFirstTokenRetry wraps a streaming request with first-token
// timeout and transparent retry logic. If the model does not produce a
// first token within the configured timeout, the request is cancelled and
// retried. Up to maxRetries attempts are made.
//
// The returned channel emits KiroEvent values. If all retries are exhausted
// a single error event with a 504-appropriate message is sent.
//
// Retries are transparent to the client — no partial data is sent before a
// retry because the channel is only returned after the first successful
// attempt begins producing events.
func StreamWithFirstTokenRetry(
	ctx context.Context,
	makeRequest MakeRequestFunc,
	opts StreamOptions,
	maxRetries int,
) <-chan KiroEvent {
	out := make(chan KiroEvent, 64)

	go func() {
		defer close(out)

		for attempt := 0; attempt < maxRetries; attempt++ {
			if ctx.Err() != nil {
				return
			}

			if attempt > 0 {
				log.Printf("Retry attempt %d/%d after first token timeout", attempt+1, maxRetries)
			}

			resp, err := makeRequest(ctx)
			if err != nil {
				send(ctx, out, KiroEvent{
					Type:  EventTypeError,
					Error: fmt.Errorf("request failed: %w", err),
				})
				return
			}

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				resp.Body.Close()
				errText := string(body)
				log.Printf("Error from Kiro API: %d - %s", resp.StatusCode, errText)
				send(ctx, out, KiroEvent{
					Type:  EventTypeError,
					Error: fmt.Errorf("upstream API error (%d): %s", resp.StatusCode, errText),
				})
				return
			}

			// Parse the stream. We buffer events internally to detect
			// first-token timeout before forwarding anything to the client.
			innerCh := ParseKiroStream(ctx, resp.Body, opts)

			timedOut := false
			for evt := range innerCh {
				if evt.Type == EventTypeError {
					if isFirstTokenTimeout(evt.Error) {
						timedOut = true
						resp.Body.Close()
						break
					}
					// Non-timeout error — forward and stop.
					send(ctx, out, evt)
					resp.Body.Close()
					return
				}
				// Forward event to client.
				if !send(ctx, out, evt) {
					resp.Body.Close()
					return
				}
			}

			if timedOut {
				log.Printf("[FirstTokenTimeout] Attempt %d/%d failed — model did not respond within %s",
					attempt+1, maxRetries, opts.FirstTokenTimeout)
				continue
			}

			// Stream completed successfully.
			return
		}

		// All retries exhausted — send 504 error.
		log.Printf("[FirstTokenTimeout] All %d attempts exhausted", maxRetries)
		send(ctx, out, KiroEvent{
			Type: EventTypeError,
			Error: fmt.Errorf(
				"model did not respond within %s after %d attempts — please try again (504 Gateway Timeout)",
				opts.FirstTokenTimeout, maxRetries,
			),
		})
	}()

	return out
}

// isFirstTokenTimeout checks whether an error is a FirstTokenTimeoutError.
func isFirstTokenTimeout(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*FirstTokenTimeoutError)
	return ok
}
