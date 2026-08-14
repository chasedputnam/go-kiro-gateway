// Package server — OpenAI Responses API route handler.
//
// This file implements:
//
//	POST /v1/responses — OpenAI Responses API endpoint
//
// The handler is a method on Server so it has access to all injected
// dependencies. Its structure mirrors handleChatCompletions exactly:
// parse → validate → convert → build payload → complete with size recovery →
// stream or collect. The only differences are the input/output data shapes.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
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
// POST /v1/responses
// ---------------------------------------------------------------------------

// handleResponses handles OpenAI Responses API requests.
func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Mode guard — return 501 when gateway is in "chat" mode.
	if s.config.OpenAIAPIMode == "chat" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		w.Write(gwerrors.OpenAIErrorResponse(
			"The /v1/responses endpoint is disabled. Set OPENAI_API_MODE=responses (or leave it unset) to enable it.",
			"invalid_request_error",
			"not_implemented",
		))
		return
	}

	// Parse request body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Error().Err(err).Msg("Failed to read request body")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write(gwerrors.OpenAIErrorResponse("Failed to read request body", "invalid_request_error", "bad_request"))
		return
	}

	var req models.ResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write(gwerrors.OpenAIValidationError(fmt.Sprintf("Invalid JSON: %v", err)))
		return
	}

	log.Info().
		Str("model", req.Model).
		Bool("stream", req.Stream).
		Msg("Request to /v1/responses")

	// Validate required fields.
	if req.Model == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write(gwerrors.OpenAIValidationError("model: field required"))
		return
	}
	if req.Input == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write(gwerrors.OpenAIValidationError("input: field required"))
		return
	}
	// Reject an empty string or empty array input.
	if isEmptyInput(req.Input) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write(gwerrors.OpenAIValidationError("input: field required and must not be empty"))
		return
	}

	// Apply truncation recovery to function_call_output items before conversion.
	if s.config.TruncationRecovery {
		req.Input = s.applyResponsesTruncationRecovery(req.Input)
	}

	// Convert to unified format.
	converted, err := converter.ConvertResponsesRequest(req, s.config)
	if err != nil {
		log.Error().Err(err).Msg("Responses API conversion error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write(gwerrors.OpenAIErrorResponse(err.Error(), "invalid_request_error", "conversion_error"))
		s.debugLogger.FlushOnError(http.StatusBadRequest, err.Error())
		return
	}

	customToolNames := make(map[string]bool)
	for _, tool := range converted.Tools {
		if tool.Kind == "custom" {
			customToolNames[tool.Name] = true
		}
	}

	// Resolve model name.
	resolution := s.resolver.Resolve(req.Model)
	modelID := resolution.InternalID

	// Generate conversation ID.
	conversationID := uuid.New().String()

	// Determine profile ARN.
	profileARN := s.auth.ProfileARN()

	// Pre-validate payload construction (tool-name limits, message shape).
	if _, err := converter.BuildKiroPayload(converter.BuildKiroPayloadOptions{
		Messages:       converted.Messages,
		SystemPrompt:   converted.SystemPrompt,
		ModelID:        modelID,
		Tools:          converted.Tools,
		ConversationID: conversationID,
		ProfileARN:     profileARN,
		InjectThinking: true,
		Cfg:            s.config,
	}); err != nil {
		log.Error().Err(err).Msg("Payload build error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write(gwerrors.OpenAIErrorResponse(err.Error(), "invalid_request_error", "conversion_error"))
		s.debugLogger.FlushOnError(http.StatusBadRequest, err.Error())
		return
	}

	// Build Kiro API URL and stream options.
	kiroURL := s.auth.APIHost() + "/generateAssistantResponse"
	maxInputTokens := s.cache.GetMaxInputTokens(modelID)
	streamOpts := streaming.DefaultStreamOptions(s.config)

	// Tokens reserved for the completion (max_output_tokens is optional).
	reserveTokens := 0
	if req.MaxOutputTokens != nil {
		reserveTokens = *req.MaxOutputTokens
	}

	// Build the payload within an input-token budget and send it.
	events, inputTokens, err := s.completeWithSizeRecovery(r.Context(), sizeRecoveryParams{
		Messages:       converted.Messages,
		SystemPrompt:   converted.SystemPrompt,
		Tools:          converted.Tools,
		ModelID:        modelID,
		ConversationID: conversationID,
		ProfileARN:     profileARN,
		InjectThinking: true,
		Model:          req.Model,
		KiroURL:        kiroURL,
		Stream:         req.Stream,
		StreamOpts:     streamOpts,
		MaxInputTokens: maxInputTokens,
		ReserveTokens:  reserveTokens,
	})
	if err != nil {
		s.writeResponsesUpstreamError(w, err, start)
		return
	}

	if req.Stream {
		s.handleResponsesStreaming(w, events, req.Model, maxInputTokens, inputTokens, streamOpts, customToolNames, start)
	} else {
		s.handleResponsesNonStreaming(w, events, req.Model, maxInputTokens, inputTokens, streamOpts, customToolNames, start)
	}
}

// writeResponsesUpstreamError writes an OpenAI-format error for a failed
// upstream request from the Responses API path.
func (s *Server) writeResponsesUpstreamError(w http.ResponseWriter, err error, start time.Time) {
	duration := time.Since(start)
	var httpErr *backendpkg.HTTPError
	if errors.As(err, &httpErr) {
		log.Warn().Int("status", httpErr.StatusCode).Dur("duration", duration).Str("error", truncateString(httpErr.Body, 100)).Msg("POST /v1/responses - upstream error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpErr.StatusCode)
		w.Write(gwerrors.OpenAIErrorResponse(httpErr.Body, "api_error", httpErr.StatusCode))
		s.debugLogger.FlushOnError(httpErr.StatusCode, httpErr.Body)
		return
	}
	log.Error().Err(err).Dur("duration", duration).Msg("HTTP 502 - POST /v1/responses")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	w.Write(gwerrors.OpenAIErrorResponse(err.Error(), "api_error", http.StatusBadGateway))
	s.debugLogger.FlushOnError(http.StatusBadGateway, err.Error())
}

// handleResponsesStreaming streams Kiro events to the client in Responses API
// SSE format.
func (s *Server) handleResponsesStreaming(
	w http.ResponseWriter,
	events <-chan streaming.KiroEvent,
	model string,
	maxInputTokens int,
	inputTokens int,
	streamOpts streaming.StreamOptions,
	customToolNames map[string]bool,
	start time.Time,
) {
	responsesOpts := streaming.ResponsesStreamOptions{
		Model:                model,
		ThinkingHandlingMode: streamOpts.ThinkingHandlingMode,
		MaxInputTokens:       maxInputTokens,
		InputTokens:          inputTokens,
		CustomToolNames:      customToolNames,
	}

	truncatedCalls := streaming.StreamToResponses(w, events, responsesOpts)

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
		Str("path", "/v1/responses").
		Dur("duration", duration).
		Msg("HTTP 200 - POST /v1/responses (streaming) - completed")

	s.debugLogger.DiscardBuffers()
}

// handleResponsesNonStreaming collects Kiro events into a single Responses API
// response.
func (s *Server) handleResponsesNonStreaming(
	w http.ResponseWriter,
	events <-chan streaming.KiroEvent,
	model string,
	maxInputTokens int,
	inputTokens int,
	streamOpts streaming.StreamOptions,
	customToolNames map[string]bool,
	start time.Time,
) {
	collected := streaming.CollectFullResponse(events)

	responsesResp := streaming.BuildResponsesResponse(collected, streaming.ResponsesNonStreamOptions{
		Model:           model,
		MaxInputTokens:  maxInputTokens,
		InputTokens:     inputTokens,
		CustomToolNames: customToolNames,
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
		Str("path", "/v1/responses").
		Dur("duration", duration).
		Msg("HTTP 200 - POST /v1/responses (non-streaming) - completed")

	if respBody, err := json.Marshal(responsesResp); err == nil {
		s.debugLogger.LogModifiedChunk(respBody)
	}
	s.debugLogger.DiscardBuffers()

	writeJSON(w, http.StatusOK, responsesResp)
}

// ---------------------------------------------------------------------------
// Truncation recovery for Responses API input items
// ---------------------------------------------------------------------------

// applyResponsesTruncationRecovery inspects function and custom tool output
// items and prepends a recovery notice when the call_id matches a saved
// truncation. This mirrors the tool-message path in applyOpenAITruncationRecovery.
func (s *Server) applyResponsesTruncationRecovery(input any) any {
	rawItems, ok := input.([]any)
	if !ok {
		return input
	}

	toolResultsModified := 0
	result := make([]any, 0, len(rawItems))
	for _, raw := range rawItems {
		item, ok := raw.(map[string]any)
		if !ok {
			result = append(result, raw)
			continue
		}

		itemType, _ := item["type"].(string)
		if itemType == "function_call_output" || itemType == "custom_tool_call_output" {
			callID, _ := item["call_id"].(string)
			if callID != "" {
				info := s.truncState.GetToolTruncation(callID)
				if info != nil {
					output := responsesOutputText(item["output"])
					modified := truncation.PrependToolResultNotice(output)
					// Clone the item to avoid mutating the original.
					newItem := make(map[string]any, len(item))
					for k, v := range item {
						newItem[k] = v
					}
					newItem["output"] = modified
					result = append(result, newItem)
					toolResultsModified++
					log.Debug().Str("call_id", callID).Str("type", itemType).Msg("Modified Responses tool output with truncation notice")
					continue
				}
			}
		}
		result = append(result, raw)
	}

	if toolResultsModified > 0 {
		log.Info().Int("tool_results_modified", toolResultsModified).Msg("Responses API truncation recovery applied")
	}
	return result
}

func responsesOutputText(output any) string {
	switch value := output.(type) {
	case nil:
		return ""
	case string:
		return value
	case []any:
		var text strings.Builder
		for _, block := range value {
			if object, ok := block.(map[string]any); ok {
				if part, ok := object["text"].(string); ok {
					text.WriteString(part)
				}
			}
		}
		if text.Len() > 0 {
			return text.String()
		}
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return fmt.Sprint(output)
	}
	return string(encoded)
}

// ---------------------------------------------------------------------------
// Input validation helpers
// ---------------------------------------------------------------------------

// isEmptyInput returns true when input is an empty string or an empty array.
func isEmptyInput(input any) bool {
	switch v := input.(type) {
	case string:
		return v == ""
	case []any:
		return len(v) == 0
	}
	return false
}
