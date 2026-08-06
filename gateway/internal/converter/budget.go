// Package converter — input-token budgeting.
//
// FitMessagesToBudget proactively trims the oldest conversation history so the
// estimated input-token count stays within a caller-supplied budget. This is
// the primary defence against the Kiro API's CONTENT_LENGTH_EXCEEDS_THRESHOLD
// rejection, which is enforced against the entire conversationState (history +
// current message + tools + system prompt), not against any single item.
//
// The trimming strategy mirrors the Kiro CLI's own behaviour of dropping the
// oldest messages first while always preserving the most recent turn.
package converter

// TokenEstimator estimates the input-token cost of a set of messages, tools,
// and system prompt. The tokenizer package provides the concrete
// implementation; it is injected here to avoid an import cycle (tokenizer
// imports converter for its types).
type TokenEstimator func(messages []UnifiedMessage, tools []UnifiedTool, systemPrompt string) int

// FitMessagesToBudget drops the oldest messages until the estimated input
// token count is within budgetTokens. It always preserves at least the final
// (most recent) message, and never drops the tools or system prompt — those
// are considered fixed overhead for the request.
//
// Parameters:
//   - messages:    the full ordered conversation (oldest first, current last)
//   - tools:       tool definitions sent with the request (fixed overhead)
//   - systemPrompt: the assembled system prompt (fixed overhead)
//   - budgetTokens: the maximum allowed estimated input tokens
//   - estimate:    the token-estimation function
//
// Returns the (possibly shortened) message slice and a bool indicating whether
// any messages were dropped. When budgetTokens <= 0, or when there is at most
// one message, the input is returned unchanged. The returned slice is a
// re-slice of the input (no copy) when messages are dropped, so callers should
// treat the input as consumed.
func FitMessagesToBudget(
	messages []UnifiedMessage,
	tools []UnifiedTool,
	systemPrompt string,
	budgetTokens int,
	estimate TokenEstimator,
) ([]UnifiedMessage, bool) {
	if estimate == nil || budgetTokens <= 0 || len(messages) <= 1 {
		return messages, false
	}

	dropped := false
	// Drop the oldest message while over budget and more than the final
	// message remains. The final message (the current turn) is always kept.
	for len(messages) > 1 && estimate(messages, tools, systemPrompt) > budgetTokens {
		messages = messages[1:]
		dropped = true
	}
	return messages, dropped
}
