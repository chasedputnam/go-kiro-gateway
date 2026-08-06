// Package server — Anthropic API route handlers.
//
// This file implements the Anthropic-compatible endpoint:
//   - POST /v1/messages — messages API (streaming + non-streaming)
//
// Handlers are methods on the Server struct so they have access to all
// injected dependencies (resolver, converter, HTTP client, etc.).
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	backendpkg "github.com/chasedputnam/go-kiro-gateway/gateway/internal/backend"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/converter"
	gwerrors "github.com/chasedputnam/go-kiro-gateway/gateway/internal/errors"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/models"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/streaming"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/truncation"
)

// ---------------------------------------------------------------------------
// POST /v1/messages
// ---------------------------------------------------------------------------

// handleMessages handles Anthropic-compatible messages requests.
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Parse request body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Error().Err(err).Msg("Failed to read request body")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write(gwerrors.AnthropicErrorResponse("Failed to read request body", "invalid_request_error"))
		return
	}

	var req models.AnthropicMessagesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write(gwerrors.AnthropicValidationError(fmt.Sprintf("Invalid JSON: %v", err)))
		return
	}

	log.Info().
		Str("model", req.Model).
		Bool("stream", req.Stream).
		Msg("Request to /v1/messages")

	// Validate required fields.
	if req.Model == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write(gwerrors.AnthropicValidationError("model: field required"))
		return
	}
	if len(req.Messages) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write(gwerrors.AnthropicValidationError("messages: field required and must not be empty"))
		return
	}
	if req.MaxTokens <= 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write(gwerrors.AnthropicValidationError("max_tokens: field required and must be greater than 0"))
		return
	}

	// Resolve model name.
	resolution := s.resolver.Resolve(req.Model)
	modelID := resolution.InternalID

	// Truncation recovery: check for truncated tool results and content.
	if s.config.TruncationRecovery {
		req.Messages = s.applyAnthropicTruncationRecovery(req.Messages)
	}

	// Generate conversation ID.
	conversationID := uuid.New().String()

	// Determine profile ARN.
	profileARN := ""
	//if s.auth.AuthType() == auth.AuthTypeKiroDesktop {
	profileARN = s.auth.ProfileARN()
	//}

	// Convert to unified format, then build Kiro payload.
	// Two-step conversion so we can estimate input tokens from the unified messages.
	converted, err := converter.ConvertAnthropicRequest(req, s.config)
	if err != nil {
		log.Error().Err(err).Msg("Conversion error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write(gwerrors.AnthropicErrorResponse(err.Error(), "invalid_request_error"))
		s.debugLogger.FlushOnError(http.StatusBadRequest, err.Error())
		return
	}

	// Convert thinking config if provided.
	var thinkingConfig *converter.ThinkingConfig
	if req.Thinking != nil {
		thinkingConfig = &converter.ThinkingConfig{
			Type:         req.Thinking.Type,
			BudgetTokens: req.Thinking.BudgetTokens,
		}
	}

	// Pre-validate payload construction (tool-name limits, message shape).
	// The actual — and possibly history-trimmed — payload is rebuilt inside
	// completeWithSizeRecovery. These validation errors do not depend on
	// trimming, so surfacing them here preserves the 400 semantics.
	if _, err := converter.BuildKiroPayload(converter.BuildKiroPayloadOptions{
		Messages:       converted.Messages,
		SystemPrompt:   converted.SystemPrompt,
		ModelID:        modelID,
		Tools:          converted.Tools,
		ConversationID: conversationID,
		ProfileARN:     profileARN,
		InjectThinking: true,
		Thinking:       thinkingConfig,
		Cfg:            s.config,
	}); err != nil {
		log.Error().Err(err).Msg("Payload build error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write(gwerrors.AnthropicErrorResponse(err.Error(), "invalid_request_error"))
		s.debugLogger.FlushOnError(http.StatusBadRequest, err.Error())
		return
	}

	// Build Kiro API URL and stream options.
	kiroURL := s.auth.APIHost() + "/generateAssistantResponse"
	maxInputTokens := s.cache.GetMaxInputTokens(modelID)
	streamOpts := streaming.DefaultStreamOptions(s.config)

	// Build the payload within an input-token budget and send it: trims oldest
	// history proactively and retries on CONTENT_LENGTH_EXCEEDS_THRESHOLD.
	events, inputTokens, err := s.completeWithSizeRecovery(r.Context(), sizeRecoveryParams{
		Messages:       converted.Messages,
		SystemPrompt:   converted.SystemPrompt,
		Tools:          converted.Tools,
		ModelID:        modelID,
		ConversationID: conversationID,
		ProfileARN:     profileARN,
		InjectThinking: true,
		Thinking:       thinkingConfig,
		Model:          req.Model,
		KiroURL:        kiroURL,
		Stream:         req.Stream,
		StreamOpts:     streamOpts,
		MaxInputTokens: maxInputTokens,
		ReserveTokens:  req.MaxTokens,
	})
	if err != nil {
		s.writeAnthropicUpstreamError(w, err, start)
		return
	}

	if req.Stream {
		s.handleAnthropicStreaming(w, events, req.Model, maxInputTokens, inputTokens, streamOpts, start)
	} else {
		s.handleAnthropicNonStreaming(w, events, req.Model, maxInputTokens, inputTokens, streamOpts, start)
	}
}

// writeAnthropicUpstreamError writes an Anthropic-format error response for a
// failed upstream request. It forwards the upstream status for *HTTPError and
// returns 502 for transport-level errors, matching the previous behaviour.
func (s *Server) writeAnthropicUpstreamError(w http.ResponseWriter, err error, start time.Time) {
	duration := time.Since(start)
	var httpErr *backendpkg.HTTPError
	if errors.As(err, &httpErr) {
		log.Warn().Int("status", httpErr.StatusCode).Dur("duration", duration).Str("error", truncateString(httpErr.Body, 100)).Msg("POST /v1/messages - upstream error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpErr.StatusCode)
		w.Write(gwerrors.AnthropicErrorResponse(httpErr.Body, "api_error"))
		s.debugLogger.FlushOnError(httpErr.StatusCode, httpErr.Body)
		return
	}
	log.Error().Err(err).Dur("duration", duration).Msg("HTTP 502 - POST /v1/messages")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	w.Write(gwerrors.AnthropicErrorResponse(err.Error(), "api_error"))
	s.debugLogger.FlushOnError(http.StatusBadGateway, err.Error())
}

// handleAnthropicStreaming streams the given Kiro events to the client in
// Anthropic SSE format. The upstream request has already been sent by
// completeWithSizeRecovery.
func (s *Server) handleAnthropicStreaming(
	w http.ResponseWriter,
	events <-chan streaming.KiroEvent,
	model string,
	maxInputTokens int,
	inputTokens int,
	streamOpts streaming.StreamOptions,
	start time.Time,
) {
	anthropicOpts := streaming.AnthropicStreamOptions{
		Model:                model,
		ThinkingHandlingMode: streamOpts.ThinkingHandlingMode,
		MaxInputTokens:       maxInputTokens,
		InputTokens:          inputTokens,
	}

	truncatedCalls := streaming.StreamToAnthropic(w, events, anthropicOpts)

	if s.config.TruncationRecovery {
		for _, tc := range truncatedCalls {
			s.truncState.SaveToolTruncation(tc.ID, tc.Name, map[string]any{
				"size_bytes": len(tc.Arguments),
				"reason":     "upstream_truncation",
			})
		}
	}

	duration := time.Since(start)
	log.Info().
		Int("status", http.StatusOK).
		Str("method", "POST").
		Str("path", "/v1/messages").
		Dur("duration", duration).
		Msg("HTTP 200 - POST /v1/messages (streaming) - completed")

	s.debugLogger.DiscardBuffers()
}

// handleAnthropicNonStreaming collects the given Kiro events into a single
// Anthropic response. The upstream request has already been sent by
// completeWithSizeRecovery.
func (s *Server) handleAnthropicNonStreaming(
	w http.ResponseWriter,
	events <-chan streaming.KiroEvent,
	model string,
	maxInputTokens int,
	inputTokens int,
	streamOpts streaming.StreamOptions,
	start time.Time,
) {
	collected := streaming.CollectFullResponse(events)

	anthropicResp := streaming.BuildAnthropicResponse(collected, streaming.AnthropicNonStreamOptions{
		Model:                model,
		ThinkingHandlingMode: streamOpts.ThinkingHandlingMode,
		MaxInputTokens:       maxInputTokens,
		InputTokens:          inputTokens,
	})

	if s.config.TruncationRecovery {
		for _, tc := range collected.TruncatedToolCalls {
			s.truncState.SaveToolTruncation(tc.ID, tc.Name, map[string]any{
				"size_bytes": len(tc.Arguments),
				"reason":     "upstream_truncation",
			})
		}
	}

	duration := time.Since(start)
	log.Info().
		Int("status", http.StatusOK).
		Str("method", "POST").
		Str("path", "/v1/messages").
		Dur("duration", duration).
		Msg("HTTP 200 - POST /v1/messages (non-streaming) - completed")

	// Log the response for debug and mark request as successful.
	if respBody, err := json.Marshal(anthropicResp); err == nil {
		s.debugLogger.LogModifiedChunk(respBody)
	}
	s.debugLogger.DiscardBuffers()

	writeJSON(w, http.StatusOK, anthropicResp)
}

// ---------------------------------------------------------------------------
// Truncation recovery for Anthropic messages
// ---------------------------------------------------------------------------

// applyAnthropicTruncationRecovery checks messages for truncated tool
// results and content, injecting recovery notices where needed.
func (s *Server) applyAnthropicTruncationRecovery(messages []models.AnthropicMessage) []models.AnthropicMessage {
	var result []models.AnthropicMessage
	toolResultsModified := 0
	contentNoticesAdded := 0

	for _, msg := range messages {
		// Check user messages for tool_result blocks with truncated tool calls.
		if msg.Role == "user" {
			if blocks, ok := msg.Content.([]any); ok {
				modified, count := s.processAnthropicToolResultBlocks(blocks)
				if count > 0 {
					toolResultsModified += count
					msg.Content = modified
				}
			}
		}

		// Check assistant messages for truncated content.
		if msg.Role == "assistant" {
			textContent := extractAnthropicTextContent(msg.Content)
			if textContent != "" {
				info := s.truncState.GetContentTruncation(textContent)
				if info != nil {
					result = append(result, msg)
					// Inject synthetic user message about truncation.
					result = append(result, models.AnthropicMessage{
						Role:    "user",
						Content: truncation.GenerateTruncationUserMessage(),
					})
					contentNoticesAdded++
					log.Debug().Str("hash", info.MessageHash).Msg("Added truncation notice after assistant message")
					continue
				}
			}
		}

		result = append(result, msg)
	}

	if toolResultsModified > 0 || contentNoticesAdded > 0 {
		log.Info().
			Int("tool_results_modified", toolResultsModified).
			Int("content_notices_added", contentNoticesAdded).
			Msg("Truncation recovery applied (Anthropic)")
	}

	return result
}

// processAnthropicToolResultBlocks scans content blocks for tool_result
// entries that match truncated tool calls, prepending the truncation notice.
func (s *Server) processAnthropicToolResultBlocks(blocks []any) ([]any, int) {
	modified := make([]any, 0, len(blocks))
	count := 0

	for _, item := range blocks {
		block, ok := item.(map[string]any)
		if !ok {
			modified = append(modified, item)
			continue
		}

		blockType, _ := block["type"].(string)
		toolUseID, _ := block["tool_use_id"].(string)

		if blockType == "tool_result" && toolUseID != "" {
			info := s.truncState.GetToolTruncation(toolUseID)
			if info != nil {
				originalContent, _ := block["content"].(string)
				newContent := truncation.PrependToolResultNotice(originalContent)
				newBlock := make(map[string]any, len(block))
				for k, v := range block {
					newBlock[k] = v
				}
				newBlock["content"] = newContent
				modified = append(modified, newBlock)
				count++
				log.Debug().Str("tool_use_id", toolUseID).Msg("Modified tool_result with truncation notice")
				continue
			}
		}

		modified = append(modified, item)
	}

	return modified, count
}

// extractAnthropicTextContent extracts text from Anthropic message content.
func extractAnthropicTextContent(content any) string {
	if s, ok := content.(string); ok {
		return s
	}
	if blocks, ok := content.([]any); ok {
		var parts []string
		for _, item := range blocks {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if blockType, _ := block["type"].(string); blockType == "text" {
				if text, ok := block["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		if len(parts) > 0 {
			return parts[0] // Use first text block for hash matching.
		}
	}
	return ""
}
