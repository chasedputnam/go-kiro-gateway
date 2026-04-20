// Package truncation provides in-memory truncation state tracking and
// recovery message generation for Kiro Gateway.
//
// When the upstream Kiro API truncates a response (tool call arguments cut
// off, or content stream ends without completion signals), the state cache
// records the truncation so that the next request can inject recovery
// messages informing the model about what happened.
//
// Thread-safe for concurrent requests.
package truncation

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// ToolTruncationInfo holds information about a truncated tool call.
type ToolTruncationInfo struct {
	ToolCallID     string
	ToolName       string
	TruncationInfo map[string]any
	Timestamp      float64
}

// ContentTruncationInfo holds information about truncated content
// (non-tool output).
type ContentTruncationInfo struct {
	MessageHash    string
	ContentPreview string
	Timestamp      float64
}

// State is an in-memory cache for truncation recovery information.
// Entries persist until retrieved (one-time get-and-delete) or until
// the gateway restarts. There is no TTL — if a user takes a break for
// hours, truncation info should still be available.
type State struct {
	mu           sync.Mutex
	toolCache    map[string]ToolTruncationInfo
	contentCache map[string]ContentTruncationInfo
}

// NewState creates an empty truncation state cache.
func NewState() *State {
	return &State{
		toolCache:    make(map[string]ToolTruncationInfo),
		contentCache: make(map[string]ContentTruncationInfo),
	}
}

// ---------------------------------------------------------------------------
// Tool truncation
// ---------------------------------------------------------------------------

// SaveToolTruncation records truncation info for a specific tool call.
func (s *State) SaveToolTruncation(toolCallID, toolName string, truncationInfo map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.toolCache[toolCallID] = ToolTruncationInfo{
		ToolCallID:     toolCallID,
		ToolName:       toolName,
		TruncationInfo: truncationInfo,
		Timestamp:      float64(time.Now().Unix()),
	}
	log.Debug().Str("tool_call_id", toolCallID).Str("tool", toolName).Msg("saved tool truncation")
}

// GetToolTruncation retrieves and removes truncation info for a tool call.
// Returns nil when no entry exists. This is a one-time retrieval — the
// entry is deleted after the first read.
func (s *State) GetToolTruncation(toolCallID string) *ToolTruncationInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	info, ok := s.toolCache[toolCallID]
	if !ok {
		return nil
	}
	delete(s.toolCache, toolCallID)
	log.Debug().Str("tool_call_id", toolCallID).Msg("retrieved tool truncation")
	return &info
}

// ---------------------------------------------------------------------------
// Content truncation
// ---------------------------------------------------------------------------

// SaveContentTruncation records truncation info for content (non-tool output).
// Returns the hash used as the cache key.
func (s *State) SaveContentTruncation(content string) string {
	hash := contentHash(content)

	s.mu.Lock()
	defer s.mu.Unlock()

	preview := content
	if len(preview) > 200 {
		preview = preview[:200]
	}

	s.contentCache[hash] = ContentTruncationInfo{
		MessageHash:    hash,
		ContentPreview: preview,
		Timestamp:      float64(time.Now().Unix()),
	}
	log.Debug().Str("hash", hash).Msg("saved content truncation")
	return hash
}

// GetContentTruncation retrieves and removes truncation info for content.
// The content is hashed and looked up in the cache. Returns nil when no
// entry exists. This is a one-time retrieval.
func (s *State) GetContentTruncation(content string) *ContentTruncationInfo {
	hash := contentHash(content)

	s.mu.Lock()
	defer s.mu.Unlock()

	info, ok := s.contentCache[hash]
	if !ok {
		return nil
	}
	delete(s.contentCache, hash)
	log.Debug().Str("hash", hash).Msg("retrieved content truncation")
	return &info
}

// ---------------------------------------------------------------------------
// Stats
// ---------------------------------------------------------------------------

// CacheStats returns the current number of entries in each cache.
func (s *State) CacheStats() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return map[string]int{
		"tool_truncations":    len(s.toolCache),
		"content_truncations": len(s.contentCache),
		"total":               len(s.toolCache) + len(s.contentCache),
	}
}

// ---------------------------------------------------------------------------
// Truncation detection helpers
// ---------------------------------------------------------------------------

// DetectContentTruncation returns true when a stream ended without
// completion signals (no usage/context_usage events) but content was
// produced. The caller should pass hasCompletionSignal=false when the
// stream ended without a usage or context_usage event.
func DetectContentTruncation(hasCompletionSignal bool, contentProduced bool) bool {
	return !hasCompletionSignal && contentProduced
}

// DetectToolCallTruncation returns true when tool call arguments appear
// to be truncated — unbalanced braces or unclosed strings.
func DetectToolCallTruncation(arguments string) bool {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" || arguments == "{}" {
		return false
	}

	braceDepth := 0
	bracketDepth := 0
	inString := false
	escaped := false

	for _, ch := range arguments {
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch ch {
		case '{':
			braceDepth++
		case '}':
			braceDepth--
		case '[':
			bracketDepth++
		case ']':
			bracketDepth--
		}
	}

	return braceDepth != 0 || bracketDepth != 0 || inString
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// contentHash produces a short hex hash of the first 500 characters of
// content, matching the Python implementation.
func contentHash(content string) string {
	toHash := content
	if len(toHash) > 500 {
		toHash = toHash[:500]
	}
	h := sha256.Sum256([]byte(toHash))
	return fmt.Sprintf("%x", h[:8]) // 16 hex chars
}
