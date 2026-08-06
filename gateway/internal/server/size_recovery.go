// Package server — payload size recovery.
//
// This file implements two complementary defences against the Kiro API's
// CONTENT_LENGTH_EXCEEDS_THRESHOLD rejection (which stops the calling agent):
//
//  1. Proactive: computeInputBudget derives a per-request input-token budget
//     from the model's context window, and completeWithSizeRecovery trims the
//     oldest history (via converter.FitMessagesToBudget) before sending.
//
//  2. Reactive: when the API still returns a content-length rejection (the
//     token estimate is approximate), completeWithSizeRecovery shrinks the
//     budget and retries up to config.PayloadSizeMaxRetries times before
//     surfacing the error — so a single oversized turn no longer hard-fails
//     the agent.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/rs/zerolog/log"

	backendpkg "github.com/chasedputnam/go-kiro-gateway/gateway/internal/backend"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/converter"
	gwerrors "github.com/chasedputnam/go-kiro-gateway/gateway/internal/errors"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/streaming"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/tokenizer"
)

// contentLengthReason is the Kiro API ValidationException reason code returned
// when the request exceeds the model's context-length threshold.
const contentLengthReason = "CONTENT_LENGTH_EXCEEDS_THRESHOLD"

// historyTrimNotice is appended to the system prompt when older history is
// dropped, reusing the [System Notice] convention the TruncationRecovery
// system-prompt block already legitimises for the model.
const historyTrimNotice = "\n\n[System Notice] Older conversation history was trimmed to fit the model's context window."

// sizeRecoveryParams bundles everything completeWithSizeRecovery needs to
// (re)build and send a Kiro payload across retries.
type sizeRecoveryParams struct {
	// Fields needed to rebuild the payload on each attempt.
	Messages       []converter.UnifiedMessage
	SystemPrompt   string
	Tools          []converter.UnifiedTool
	ModelID        string
	ConversationID string
	ProfileARN     string
	InjectThinking bool
	Thinking       *converter.ThinkingConfig

	// Fields needed to send the request.
	Model          string // public model name for the backend request
	KiroURL        string
	Stream         bool
	StreamOpts     streaming.StreamOptions
	MaxInputTokens int // model context window (tokens)
	ReserveTokens  int // tokens reserved for the completion (client max_tokens)
}

// computeInputBudget derives the input-token budget for a request: a fraction
// (InputTokenBudgetRatio) of the model context window, minus the tokens
// reserved for the completion, floored at MinInputTokenBudget so we never trim
// the conversation into uselessness.
func (s *Server) computeInputBudget(maxInputTokens, reserveTokens int) int {
	if maxInputTokens <= 0 {
		maxInputTokens = s.config.DefaultMaxInputTokens
	}
	budget := int(float64(maxInputTokens)*s.config.InputTokenBudgetRatio) - reserveTokens
	if budget < s.config.MinInputTokenBudget {
		budget = s.config.MinInputTokenBudget
	}
	return budget
}

// estimateInputTokens estimates the input-token cost of a message set, tools,
// and system prompt using the tokenizer package.
func estimateInputTokens(messages []converter.UnifiedMessage, tools []converter.UnifiedTool, systemPrompt string) int {
	return tokenizer.EstimatePromptTokensFromMessages(messages, tools) + tokenizer.CountTokens(systemPrompt)
}

// isContentLengthError reports whether err is a Kiro API rejection caused by
// the request exceeding the content-length threshold.
func isContentLengthError(err error) bool {
	var httpErr *backendpkg.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	var body map[string]any
	if json.Unmarshal([]byte(httpErr.Body), &body) != nil {
		return false
	}
	return gwerrors.EnhanceKiroError(body).Reason == contentLengthReason
}

// completeWithSizeRecovery builds the Kiro payload within an input-token
// budget, sends it, and — on a content-length rejection — shrinks the budget
// and retries. It returns the backend event channel and the estimated input
// tokens for the payload that was ultimately sent.
//
// The returned error preserves the underlying *backend.HTTPError (or transport
// error), so callers can forward the upstream status to the client exactly as
// before when recovery is exhausted.
func (s *Server) completeWithSizeRecovery(
	ctx context.Context,
	p sizeRecoveryParams,
) (<-chan streaming.KiroEvent, int, error) {
	budget := s.computeInputBudget(p.MaxInputTokens, p.ReserveTokens)
	maxAttempts := s.config.PayloadSizeMaxRetries + 1

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		messages := p.Messages
		systemPrompt := p.SystemPrompt

		if trimmed, dropped := converter.FitMessagesToBudget(
			p.Messages, p.Tools, p.SystemPrompt, budget, estimateInputTokens,
		); dropped {
			messages = trimmed
			systemPrompt = appendHistoryTrimNotice(p.SystemPrompt)
			log.Warn().
				Int("attempt", attempt).
				Int("budget_tokens", budget).
				Int("kept_messages", len(messages)).
				Int("original_messages", len(p.Messages)).
				Msg("trimmed conversation history to fit input-token budget")
		}

		payloadResult, err := converter.BuildKiroPayload(converter.BuildKiroPayloadOptions{
			Messages:       messages,
			SystemPrompt:   systemPrompt,
			ModelID:        p.ModelID,
			Tools:          p.Tools,
			ConversationID: p.ConversationID,
			ProfileARN:     p.ProfileARN,
			InjectThinking: p.InjectThinking,
			Thinking:       p.Thinking,
			Cfg:            s.config,
		})
		if err != nil {
			return nil, 0, err
		}

		inputTokens := estimateInputTokens(messages, p.Tools, systemPrompt)

		// Log the payload that is actually being sent (post-trim).
		if kiroBody, mErr := json.Marshal(payloadResult.Payload); mErr == nil {
			s.debugLogger.LogKiroRequestBody(kiroBody)
		}

		events, err := s.backend.Complete(ctx, &backendpkg.Request{
			Payload:        payloadResult.Payload,
			Model:          p.Model,
			Stream:         p.Stream,
			ProfileARN:     p.ProfileARN,
			ConversationID: p.ConversationID,
			KiroURL:        p.KiroURL,
			MaxInputTokens: p.MaxInputTokens,
			StreamOpts:     p.StreamOpts,
		})
		if err == nil {
			return events, inputTokens, nil
		}

		lastErr = err

		// Only retry on content-length rejections, and only if attempts remain.
		if !isContentLengthError(err) || attempt == maxAttempts-1 {
			return nil, 0, err
		}

		// Shrink the budget ~30% so FitMessagesToBudget drops more next time.
		newBudget := budget * 7 / 10
		if newBudget < s.config.MinInputTokenBudget {
			newBudget = s.config.MinInputTokenBudget
		}
		log.Warn().
			Int("attempt", attempt).
			Int("old_budget", budget).
			Int("new_budget", newBudget).
			Msg("content-length rejection from Kiro API; shrinking payload and retrying")
		budget = newBudget
	}

	return nil, 0, lastErr
}

// appendHistoryTrimNotice appends the history-trim [System Notice] to the
// system prompt, handling the empty-prompt case.
func appendHistoryTrimNotice(systemPrompt string) string {
	if systemPrompt == "" {
		return strings.TrimSpace(historyTrimNotice)
	}
	return systemPrompt + historyTrimNotice
}
