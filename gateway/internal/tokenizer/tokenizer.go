// Package tokenizer provides approximate token counting for usage reporting.
//
// It uses go-tiktoken with the cl100k_base encoding (GPT-4/ChatGPT), which is
// close enough to Claude's tokenisation for estimation purposes. Anthropic does
// not publish their tokeniser, so a correction factor is applied.
//
// The correction factor CLAUDE_CORRECTION_FACTOR = 1.15 is based on empirical
// observations: Claude tokenises text approximately 15 % more than GPT-4
// (cl100k_base). This is due to differences in BPE vocabularies.
package tokenizer

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/jwadow/kiro-gateway/gateway/internal/converter"
	tiktoken "github.com/pkoukk/tiktoken-go"
)

// ClaudeCorrectionFactor is the empirical multiplier applied to raw token
// counts to approximate Claude's tokenisation. Claude tokenises text roughly
// 15 % more than GPT-4 (cl100k_base).
const ClaudeCorrectionFactor = 1.15

// encodingName is the tiktoken encoding used for all token counts.
const encodingName = "cl100k_base"

// ---------------------------------------------------------------------------
// Lazy-initialised singleton encoding
// ---------------------------------------------------------------------------

var (
	encOnce sync.Once
	enc     *tiktoken.Tiktoken
	encErr  error
)

// getEncoding returns the lazily-initialised cl100k_base encoding.
// If initialisation fails the error is logged once and nil is returned.
func getEncoding() *tiktoken.Tiktoken {
	encOnce.Do(func() {
		enc, encErr = tiktoken.GetEncoding(encodingName)
		if encErr != nil {
			log.Printf("[Tokenizer] Failed to initialise tiktoken (%s): %v", encodingName, encErr)
		} else {
			log.Printf("[Tokenizer] Initialised tiktoken with %s encoding", encodingName)
		}
	})
	return enc
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// CountTokens returns the approximate number of tokens in text using the
// cl100k_base encoding. If the encoding is unavailable a rough character-
// based estimate is returned instead (~4 chars per token).
func CountTokens(text string) int {
	if text == "" {
		return 0
	}

	if e := getEncoding(); e != nil {
		return len(e.Encode(text, nil, nil))
	}

	// Fallback: ~4 characters per token for English.
	return len(text)/4 + 1
}

// ApplyClaudeCorrectionFactor multiplies rawTokens by the Claude correction
// factor (1.15) and returns the result as an integer.
func ApplyClaudeCorrectionFactor(rawTokens int) int {
	return int(float64(rawTokens) * ClaudeCorrectionFactor)
}

// CalculatePromptTokens derives prompt tokens from the context usage
// percentage reported by the Kiro API.
//
//	totalTokens = maxInputTokens * contextUsagePercentage
//	promptTokens = totalTokens - completionTokens
//
// If the result would be negative, 0 is returned.
func CalculatePromptTokens(completionTokens int, contextUsagePercentage float64, maxInputTokens int) int {
	if contextUsagePercentage <= 0 || maxInputTokens <= 0 {
		return 0
	}

	totalTokens := int(float64(maxInputTokens) * contextUsagePercentage)
	promptTokens := totalTokens - completionTokens
	if promptTokens < 0 {
		return 0
	}
	return promptTokens
}

// EstimatePromptTokensFromMessages estimates prompt tokens by counting
// tokens in the original request messages and tool definitions. This is the
// fallback used when context usage percentage is not available from the API.
func EstimatePromptTokensFromMessages(messages []converter.UnifiedMessage, tools []converter.UnifiedTool) int {
	return CountMessageTokens(messages) + CountToolsTokens(tools)
}

// CountMessageTokens counts the approximate number of tokens in a slice of
// unified messages. It accounts for per-message overhead (role, delimiters)
// and applies the Claude correction factor to the total.
func CountMessageTokens(messages []converter.UnifiedMessage) int {
	if len(messages) == 0 {
		return 0
	}

	total := 0

	for _, msg := range messages {
		// ~4 tokens per message for role and delimiters.
		total += 4

		// Role token (short string, no correction).
		total += CountTokens(msg.Role)

		// Content tokens.
		total += CountTokens(msg.Content)

		// Tool calls.
		for _, tc := range msg.ToolCalls {
			total += 4 // service tokens per tool call
			if funcMap, ok := tc["function"].(map[string]any); ok {
				if name, ok := funcMap["name"].(string); ok {
					total += CountTokens(name)
				}
				if args, ok := funcMap["arguments"].(string); ok {
					total += CountTokens(args)
				}
			}
		}

		// Tool results.
		for _, tr := range msg.ToolResults {
			if id, ok := tr["tool_use_id"].(string); ok {
				total += CountTokens(id)
			}
			if content, ok := tr["content"].(string); ok {
				total += CountTokens(content)
			}
		}

		// Images: ~100 tokens each (average estimate).
		total += len(msg.Images) * 100
	}

	// Final service tokens.
	total += 3

	return ApplyClaudeCorrectionFactor(total)
}

// CountToolsTokens counts the approximate number of tokens in a slice of
// unified tool definitions. It accounts for per-tool overhead and applies
// the Claude correction factor to the total.
func CountToolsTokens(tools []converter.UnifiedTool) int {
	if len(tools) == 0 {
		return 0
	}

	total := 0

	for _, tool := range tools {
		// ~4 service tokens per tool definition.
		total += 4

		total += CountTokens(tool.Name)
		total += CountTokens(tool.Description)

		// Parameters (JSON schema).
		if len(tool.InputSchema) > 0 {
			schemaBytes, err := json.Marshal(tool.InputSchema)
			if err == nil {
				total += CountTokens(string(schemaBytes))
			}
		}
	}

	return ApplyClaudeCorrectionFactor(total)
}
