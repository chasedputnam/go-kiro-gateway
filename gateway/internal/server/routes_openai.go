// Package server — OpenAI API route handlers.
//
// This file implements the OpenAI-compatible endpoints:
//   - GET  /v1/models          — list available models
//   - POST /v1/chat/completions — chat completions (streaming + non-streaming)
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

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/converter"
	gwerrors "github.com/chasedputnam/go-kiro-gateway/gateway/internal/errors"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/models"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/streaming"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/truncation"
)

// ---------------------------------------------------------------------------
// GET /v1/models
// ---------------------------------------------------------------------------

// handleListModels returns the list of available models in OpenAI format.
func (s *Server) handleListModels(w http.ResponseWriter, _ *http.Request) {
	log.Info().Msg("Request to /v1/models")

	modelIDs := s.resolver.GetAvailableModels()

	data := make([]map[string]any, 0, len(modelIDs))
	for _, id := range modelIDs {
		data = append(data, map[string]any{
			"id":       id,
			"object":   "model",
			"created":  time.Now().Unix(),
			"owned_by": "anthropic",
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   data,
	})
}

// ---------------------------------------------------------------------------
// POST /v1/chat/completions
// ---------------------------------------------------------------------------

// handleChatCompletions handles OpenAI-compatible chat completion requests.
func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Parse request body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Error().Err(err).Msg("Failed to read request body")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write(gwerrors.OpenAIErrorResponse("Failed to read request body", "invalid_request_error", "bad_request"))
		return
	}

	var req models.ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write(gwerrors.OpenAIValidationError(fmt.Sprintf("Invalid JSON: %v", err)))
		return
	}

	log.Info().
		Str("model", req.Model).
		Bool("stream", req.Stream).
		Msg("Request to /v1/chat/completions")

	// Validate required fields.
	if req.Model == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write(gwerrors.OpenAIValidationError("model: field required"))
		return
	}
	if len(req.Messages) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write(gwerrors.OpenAIValidationError("messages: field required and must not be empty"))
		return
	}

	// Resolve model name.
	resolution := s.resolver.Resolve(req.Model)
	modelID := resolution.InternalID

	// Truncation recovery: check for truncated tool results and content.
	if s.config.TruncationRecovery {
		req.Messages = s.applyOpenAITruncationRecovery(req.Messages)
	}

	// Generate conversation ID.
	conversationID := uuid.New().String()

	// Determine profile ARN.
	profileARN := ""
	//if s.auth.AuthType() == auth.AuthTypeKiroDesktop {
	profileARN = s.auth.ProfileARN()
	//}

	// Convert to Kiro payload.
	payloadResult, err := converter.BuildOpenAIKiroPayload(req, conversationID, profileARN, modelID, s.config)
	if err != nil {
		log.Error().Err(err).Msg("Conversion error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write(gwerrors.OpenAIErrorResponse(err.Error(), "invalid_request_error", "conversion_error"))
		s.debugLogger.FlushOnError(http.StatusBadRequest, err.Error())
		return
	}

	// Log the Kiro request body for debug.
	if kiroBody, err := json.Marshal(payloadResult.Payload); err == nil {
		s.debugLogger.LogKiroRequestBody(kiroBody)
	}

	// Build Kiro API URL.
	kiroURL := s.auth.APIHost() + "/generateAssistantResponse"

	// Get max input tokens for the model.
	maxInputTokens := s.cache.GetMaxInputTokens(modelID)

	// Stream options.
	streamOpts := streaming.DefaultStreamOptions(s.config)

	if req.Stream {
		s.handleOpenAIStreaming(w, r, payloadResult.Payload, kiroURL, req.Model, maxInputTokens, streamOpts, start)
	} else {
		s.handleOpenAINonStreaming(w, r, payloadResult.Payload, kiroURL, req.Model, maxInputTokens, streamOpts, start)
	}
}

// handleOpenAIStreaming handles streaming chat completion requests.
func (s *Server) handleOpenAIStreaming(
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
		log.Error().Err(err).Dur("duration", duration).Msg("HTTP 502 - POST /v1/chat/completions (streaming)")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		w.Write(gwerrors.OpenAIErrorResponse(err.Error(), "api_error", http.StatusBadGateway))
		s.debugLogger.FlushOnError(http.StatusBadGateway, err.Error())
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
			Msg("POST /v1/chat/completions - Kiro API error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(gwerrors.OpenAIErrorResponse(errMsg, "api_error", resp.StatusCode))
		s.debugLogger.LogRawChunk(errBody)
		s.debugLogger.FlushOnError(resp.StatusCode, errMsg)
		return
	}

	// Parse and stream the response.
	events := streaming.ParseKiroStream(ctx, resp.Body, streamOpts)

	openAIOpts := streaming.OpenAIStreamOptions{
		Model:                model,
		ThinkingHandlingMode: streamOpts.ThinkingHandlingMode,
		MaxInputTokens:       maxInputTokens,
	}

	truncatedCalls := streaming.StreamToOpenAI(w, events, openAIOpts)

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
		Str("path", "/v1/chat/completions").
		Dur("duration", duration).
		Msg("HTTP 200 - POST /v1/chat/completions (streaming) - completed")

	s.debugLogger.DiscardBuffers()
}

// handleOpenAINonStreaming handles non-streaming chat completion requests.
func (s *Server) handleOpenAINonStreaming(
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
		log.Error().Err(err).Dur("duration", duration).Msg("HTTP 502 - POST /v1/chat/completions (non-streaming)")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		w.Write(gwerrors.OpenAIErrorResponse(err.Error(), "api_error", http.StatusBadGateway))
		s.debugLogger.FlushOnError(http.StatusBadGateway, err.Error())
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
			Msg("POST /v1/chat/completions - Kiro API error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(gwerrors.OpenAIErrorResponse(errMsg, "api_error", resp.StatusCode))
		s.debugLogger.LogRawChunk(errBody)
		s.debugLogger.FlushOnError(resp.StatusCode, errMsg)
		return
	}

	// Parse the stream and collect the full response.
	events := streaming.ParseKiroStream(ctx, resp.Body, streamOpts)
	collected := streaming.CollectFullResponse(events)

	openAIResp := streaming.BuildOpenAIResponse(collected, streaming.OpenAINonStreamOptions{
		Model:                model,
		ThinkingHandlingMode: streamOpts.ThinkingHandlingMode,
		MaxInputTokens:       maxInputTokens,
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
		Str("path", "/v1/chat/completions").
		Dur("duration", duration).
		Msg("HTTP 200 - POST /v1/chat/completions (non-streaming) - completed")

	// Log the response for debug and mark request as successful.
	if respBody, err := json.Marshal(openAIResp); err == nil {
		s.debugLogger.LogModifiedChunk(respBody)
	}
	s.debugLogger.DiscardBuffers()

	writeJSON(w, http.StatusOK, openAIResp)
}

// ---------------------------------------------------------------------------
// Truncation recovery for OpenAI messages
// ---------------------------------------------------------------------------

// applyOpenAITruncationRecovery checks messages for truncated tool results
// and content, injecting recovery notices where needed.
func (s *Server) applyOpenAITruncationRecovery(messages []models.ChatMessage) []models.ChatMessage {
	var result []models.ChatMessage
	toolResultsModified := 0
	contentNoticesAdded := 0

	for _, msg := range messages {
		// Check tool messages for truncated tool calls.
		if msg.Role == "tool" && msg.ToolCallID != "" {
			info := s.truncState.GetToolTruncation(msg.ToolCallID)
			if info != nil {
				originalContent := extractStringContent(msg.Content)
				modified := truncation.PrependToolResultNotice(originalContent)
				msg.Content = modified
				result = append(result, msg)
				toolResultsModified++
				log.Debug().Str("tool_call_id", msg.ToolCallID).Msg("Modified tool_result with truncation notice")
				continue
			}
		}

		// Check assistant messages for truncated content.
		if msg.Role == "assistant" {
			textContent := extractStringContent(msg.Content)
			if textContent != "" {
				info := s.truncState.GetContentTruncation(textContent)
				if info != nil {
					result = append(result, msg)
					// Inject synthetic user message about truncation.
					result = append(result, models.ChatMessage{
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
			Msg("Truncation recovery applied")
	}

	return result
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// extractStringContent extracts a string from content that may be a string
// or another type. Returns "" for non-string content.
func extractStringContent(content any) string {
	if s, ok := content.(string); ok {
		return s
	}
	return ""
}

// truncateString truncates a string to maxLen characters, appending "..."
// if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
