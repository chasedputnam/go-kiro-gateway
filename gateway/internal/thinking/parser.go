// Package thinking implements a finite state machine parser for thinking blocks
// in streaming responses.
//
// The parser detects thinking tags (<thinking>, <think>, <reasoning>, <thought>)
// ONLY at the start of the response. Once a thinking block is found and closed,
// all subsequent content is treated as regular content (even if it contains
// thinking tags).
//
// It implements "cautious" buffering to handle tags split across chunks:
//   - In PreContent: buffer until tag found or buffer exceeds limit
//   - In InThinking: buffer last maxTagLength chars to avoid splitting closing tag
package thinking

import "strings"

// State represents the current state of the thinking parser FSM.
type State int

const (
	// PreContent is the initial state. The parser buffers incoming content
	// looking for an opening thinking tag at the start of the response.
	PreContent State = iota
	// InThinking means the parser is inside a thinking block, accumulating
	// thinking content until the matching closing tag is found.
	InThinking
	// Streaming means the thinking block (if any) has been fully consumed.
	// All subsequent content is passed through as regular content.
	Streaming
)

// HandlingMode controls how thinking content is processed.
type HandlingMode string

const (
	// AsReasoningContent emits thinking content as separate reasoning events.
	AsReasoningContent HandlingMode = "as_reasoning_content"
	// Remove silently discards thinking content.
	Remove HandlingMode = "remove"
	// Pass passes thinking tags and content through as regular text.
	Pass HandlingMode = "pass"
	// StripTags removes tags but keeps thinking content as regular text.
	StripTags HandlingMode = "strip_tags"
)

// ThinkingResult holds the output of processing a single content chunk.
type ThinkingResult struct {
	// RegularContent is content to be sent as normal delta content.
	RegularContent string
	// ThinkingContent is content extracted from inside thinking tags.
	ThinkingContent string
	// IsThinking is true when the parser is currently inside a thinking block.
	IsThinking bool
}

// Parser is a finite state machine that detects and extracts thinking blocks
// from streaming text chunks.
//
// Usage:
//
//	p := thinking.NewParser(thinking.AsReasoningContent, nil, 20)
//	result := p.ProcessChunk("<thinking>reasoning</thinking>answer")
//	// result.ThinkingContent == "reasoning"
//	// result.RegularContent == "answer"
type Parser struct {
	handlingMode   HandlingMode
	openTags       []string
	initialBufSize int
	maxTagLength   int

	// FSM state
	state                State
	initialBuffer        string
	thinkingBuffer       string
	openTag              string
	closeTag             string
	isFirstThinkingChunk bool
	thinkingBlockFound   bool
}

// NewParser creates a new thinking parser.
//
// Parameters:
//   - mode: how to handle thinking content (AsReasoningContent, Remove, Pass, StripTags)
//   - openTags: list of opening tags to detect; nil uses the default set
//   - initialBufSize: max chars to buffer while looking for an opening tag
func NewParser(mode HandlingMode, openTags []string, initialBufSize int) *Parser {
	if len(openTags) == 0 {
		openTags = []string{"<thinking>", "<think>", "<reasoning>", "<thought>"}
	}
	if initialBufSize <= 0 {
		initialBufSize = 20
	}

	maxLen := 0
	for _, tag := range openTags {
		if len(tag) > maxLen {
			maxLen = len(tag)
		}
	}

	return &Parser{
		handlingMode:         mode,
		openTags:             openTags,
		initialBufSize:       initialBufSize,
		maxTagLength:         maxLen * 2,
		state:                PreContent,
		isFirstThinkingChunk: true,
	}
}

// State returns the current FSM state.
func (p *Parser) State() State { return p.state }

// FoundThinkingBlock returns true if a thinking block was detected.
func (p *Parser) FoundThinkingBlock() bool { return p.thinkingBlockFound }

// HandlingModeValue returns the configured handling mode.
func (p *Parser) HandlingModeValue() HandlingMode { return p.handlingMode }

// OpenTag returns the detected opening tag, or empty string if none found.
func (p *Parser) OpenTag() string { return p.openTag }

// CloseTag returns the matching closing tag, or empty string if none found.
func (p *Parser) CloseTag() string { return p.closeTag }

// ProcessChunk feeds a chunk of streaming text through the parser and returns
// the result. This is the main entry point for processing streaming content.
func (p *Parser) ProcessChunk(chunk string) ThinkingResult {
	if chunk == "" {
		return ThinkingResult{IsThinking: p.state == InThinking}
	}

	switch p.state {
	case PreContent:
		return p.handlePreContent(chunk)
	case InThinking:
		return p.handleInThinking(chunk)
	case Streaming:
		return p.handleStreaming(chunk)
	default:
		return ThinkingResult{}
	}
}

// Finalize flushes any remaining buffered content when the stream ends.
func (p *Parser) Finalize() ThinkingResult {
	var result ThinkingResult

	if p.thinkingBuffer != "" {
		if p.state == InThinking {
			result.ThinkingContent = p.thinkingBuffer
			result.IsThinking = true
		} else {
			result.RegularContent = p.thinkingBuffer
		}
		p.thinkingBuffer = ""
	}

	if p.initialBuffer != "" {
		result.RegularContent += p.initialBuffer
		p.initialBuffer = ""
	}

	return result
}

// Reset returns the parser to its initial state so it can be reused.
func (p *Parser) Reset() {
	p.state = PreContent
	p.initialBuffer = ""
	p.thinkingBuffer = ""
	p.openTag = ""
	p.closeTag = ""
	p.isFirstThinkingChunk = true
	p.thinkingBlockFound = false
}

// ProcessForOutput transforms thinking content according to the handling mode.
//
// Parameters:
//   - content: raw thinking content
//   - isFirst: true if this is the first thinking chunk
//   - isLast: true if this is the last thinking chunk
//
// Returns the processed string, or empty string for Remove mode.
func (p *Parser) ProcessForOutput(content string, isFirst, isLast bool) string {
	if content == "" {
		return ""
	}

	switch p.handlingMode {
	case Remove:
		return ""
	case Pass:
		var b strings.Builder
		if isFirst && p.openTag != "" {
			b.WriteString(p.openTag)
		}
		b.WriteString(content)
		if isLast && p.closeTag != "" {
			b.WriteString(p.closeTag)
		}
		return b.String()
	case StripTags:
		return content
	default:
		// AsReasoningContent — return as-is; caller puts it in reasoning_content.
		return content
	}
}

// ---------------------------------------------------------------------------
// Internal state handlers
// ---------------------------------------------------------------------------

// handlePreContent buffers content and looks for an opening tag at the start.
func (p *Parser) handlePreContent(chunk string) ThinkingResult {
	p.initialBuffer += chunk
	stripped := strings.TrimLeft(p.initialBuffer, " \t\n\r")

	// Check if buffer starts with any opening tag.
	for _, tag := range p.openTags {
		if strings.HasPrefix(stripped, tag) {
			// Tag found — transition to InThinking.
			p.state = InThinking
			p.openTag = tag
			p.closeTag = "</" + tag[1:]
			p.thinkingBlockFound = true

			afterTag := stripped[len(tag):]
			p.thinkingBuffer = afterTag
			p.initialBuffer = ""

			// Process the thinking buffer for a potential closing tag.
			return p.processThinkingBuffer()
		}
	}

	// Check if we might still be receiving the tag (partial prefix match).
	if p.couldBeTagPrefix(stripped) && len(p.initialBuffer) <= p.initialBufSize {
		return ThinkingResult{}
	}

	// No tag found — transition to Streaming.
	p.state = Streaming
	content := p.initialBuffer
	p.initialBuffer = ""
	return ThinkingResult{RegularContent: content}
}

// couldBeTagPrefix returns true if text could be the beginning of any opening tag.
func (p *Parser) couldBeTagPrefix(text string) bool {
	if text == "" {
		return true
	}
	for _, tag := range p.openTags {
		if strings.HasPrefix(tag, text) {
			return true
		}
	}
	return false
}

// handleInThinking appends the chunk to the thinking buffer and checks for
// the closing tag.
func (p *Parser) handleInThinking(chunk string) ThinkingResult {
	p.thinkingBuffer += chunk
	return p.processThinkingBuffer()
}

// processThinkingBuffer scans the thinking buffer for the closing tag.
// It uses cautious buffering — keeping the last maxTagLength characters in
// the buffer to avoid splitting the closing tag across chunks.
func (p *Parser) processThinkingBuffer() ThinkingResult {
	if p.closeTag == "" {
		return ThinkingResult{IsThinking: true}
	}

	// Look for the closing tag.
	idx := strings.Index(p.thinkingBuffer, p.closeTag)
	if idx >= 0 {
		// Found closing tag — extract thinking content and transition.
		thinkingContent := p.thinkingBuffer[:idx]
		afterTag := p.thinkingBuffer[idx+len(p.closeTag):]

		p.state = Streaming
		p.thinkingBuffer = ""

		var result ThinkingResult
		result.ThinkingContent = p.applyThinkingMode(thinkingContent)
		result.IsThinking = false

		// Content after closing tag is regular content.
		// Strip leading whitespace/newlines that often follow the closing tag.
		if trimmed := strings.TrimLeft(afterTag, " \t\n\r"); trimmed != "" {
			result.RegularContent = trimmed
		}

		return result
	}

	// No closing tag yet — use cautious buffering.
	if len(p.thinkingBuffer) > p.maxTagLength {
		sendPart := p.thinkingBuffer[:len(p.thinkingBuffer)-p.maxTagLength]
		p.thinkingBuffer = p.thinkingBuffer[len(p.thinkingBuffer)-p.maxTagLength:]

		return ThinkingResult{
			ThinkingContent: p.applyThinkingMode(sendPart),
			IsThinking:      true,
		}
	}

	return ThinkingResult{IsThinking: true}
}

// handleStreaming passes content through as regular content.
func (p *Parser) handleStreaming(chunk string) ThinkingResult {
	return ThinkingResult{RegularContent: chunk}
}

// applyThinkingMode transforms thinking content based on the handling mode
// for the ThinkingContent field of ThinkingResult.
func (p *Parser) applyThinkingMode(content string) string {
	if content == "" {
		return ""
	}
	switch p.handlingMode {
	case Remove:
		return ""
	case Pass:
		// In pass mode, thinking content is emitted as regular content
		// (tags are re-added by ProcessForOutput when needed).
		return content
	case StripTags:
		return content
	default:
		// AsReasoningContent
		return content
	}
}
