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
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/jwadow/kiro-gateway/gateway/internal/auth"
	"github.com/jwadow/kiro-gateway/gateway/internal/converter"
	gwerrors "github.com/jwadow/kiro-gateway/gateway/internal/errors"
	"github.com/jwadow/kiro-gateway/gateway/internal/models"
	"github.com/jwadow/kiro-gateway/gateway/internal/streaming"
	"github.com/jwadow/kiro-gateway/gateway/internal/truncation"
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
	if s.auth.AuthType() == auth.AuthTypeKiroDesktop {
		profileARN = s.auth.ProfileARN()
	}

	// Convert to Kiro payload.
	payloadResult, err := converter.BuildAnthropicKiroPayload(req, conversationID, profileARN, modelID, s.config)
	if err != nil {
		log.Error().Err(err).Msg("Conversion error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write(gwerrors.AnthropicErrorResponse(err.Error(), "invalid_request_error"))
		return
	}

	// Build Kiro API URL.
	kiroURL := s.auth.APIHost() + "/generateAssistantResponse"

	// Get max input tokens for the model.
	maxInputTokens := s.cache.GetMaxInputTokens(modelID)

	// Stream options.
	streamOpts := streaming.DefaultStreamOptions(s.config)

	if req.Stream {
		s.handleAnthropicStreaming(w, r, payloadResult.Payload, kiroURL, req.Model, maxInputTokens, streamOpts, start)
	} else {
		s.handleAnthropicNonStreaming(w, r, payloadResult.Payload, kiroURL, req.Model, maxInputTokens, streamOpts, start)
	}
}

// handleAnthropicStreaming handles streaming messages requests.
func (s *Server) handleAnthropicStreaming(
	w http.ResponseWriter,
	r *http.Request,
	payload map[string]any,
	kiroURL string,
	model string,
	maxInputTokens int,
	streamOpts streaming.StreamOptions,
	start time.Time,
) {
	ctx := r.Context()

	// Send request to Kiro API.
	resp, err := s.httpClient.RequestWithRetry(ctx, "POST", kiroURL, payload, true)
	if err != nil {
		duration := time.Since(start)
		log.Error().Err(err).Dur("duration", duration).Msg("HTTP 502 - POST /v1/messages (streaming)")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		w.Write(gwerrors.AnthropicErrorResponse(err.Error(), "api_error"))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		duration := time.Since(start)
		errMsg := string(errBody)
		log.Warn().
			Int("status", resp.StatusCode).
			Dur("duration", duration).
			Str("error", truncateString(errMsg, 100)).
			Msg("POST /v1/messages - Kiro API error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(gwerrors.AnthropicErrorResponse(errMsg, "api_error"))
		return
	}

	// Parse and stream the response.
	events := streaming.ParseKiroStream(ctx, resp.Body, streamOpts)

	anthropicOpts := streaming.AnthropicStreamOptions{
		Model:                model,
		ThinkingHandlingMode: streamOpts.ThinkingHandlingMode,
		MaxInputTokens:       maxInputTokens,
	}

	streaming.StreamToAnthropic(w, events, anthropicOpts)

	duration := time.Since(start)
	log.Info().
		Int("status", http.StatusOK).
		Str("method", "POST").
		Str("path", "/v1/messages").
		Dur("duration", duration).
		Msg("HTTP 200 - POST /v1/messages (streaming) - completed")
}

// handleAnthropicNonStreaming handles non-streaming messages requests.
func (s *Server) handleAnthropicNonStreaming(
	w http.ResponseWriter,
	r *http.Request,
	payload map[string]any,
	kiroURL string,
	model string,
	maxInputTokens int,
	streamOpts streaming.StreamOptions,
	start time.Time,
) {
	ctx := r.Context()

	// Send request to Kiro API.
	resp, err := s.httpClient.RequestWithRetry(ctx, "POST", kiroURL, payload, true)
	if err != nil {
		duration := time.Since(start)
		log.Error().Err(err).Dur("duration", duration).Msg("HTTP 502 - POST /v1/messages (non-streaming)")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		w.Write(gwerrors.AnthropicErrorResponse(err.Error(), "api_error"))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		duration := time.Since(start)
		errMsg := string(errBody)
		log.Warn().
			Int("status", resp.StatusCode).
			Dur("duration", duration).
			Str("error", truncateString(errMsg, 100)).
			Msg("POST /v1/messages - Kiro API error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(gwerrors.AnthropicErrorResponse(errMsg, "api_error"))
		return
	}

	// Parse the stream and collect the full response.
	events := streaming.ParseKiroStream(ctx, resp.Body, streamOpts)
	collected := streaming.CollectFullResponse(events)

	anthropicResp := streaming.BuildAnthropicResponse(collected, streaming.AnthropicNonStreamOptions{
		Model:                model,
		ThinkingHandlingMode: streamOpts.ThinkingHandlingMode,
		MaxInputTokens:       maxInputTokens,
	})

	duration := time.Since(start)
	log.Info().
		Int("status", http.StatusOK).
		Str("method", "POST").
		Str("path", "/v1/messages").
		Dur("duration", duration).
		Msg("HTTP 200 - POST /v1/messages (non-streaming) - completed")

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
