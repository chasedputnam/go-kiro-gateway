package thinking

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// State enum
// ---------------------------------------------------------------------------

func TestStateValues(t *testing.T) {
	if PreContent != 0 {
		t.Errorf("PreContent = %d, want 0", PreContent)
	}
	if InThinking != 1 {
		t.Errorf("InThinking = %d, want 1", InThinking)
	}
	if Streaming != 2 {
		t.Errorf("Streaming = %d, want 2", Streaming)
	}
}

// ---------------------------------------------------------------------------
// NewParser defaults
// ---------------------------------------------------------------------------

func TestNewParserDefaults(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 0)

	if p.State() != PreContent {
		t.Errorf("initial state = %d, want PreContent", p.State())
	}
	if p.FoundThinkingBlock() {
		t.Error("FoundThinkingBlock should be false initially")
	}
	if p.OpenTag() != "" {
		t.Errorf("OpenTag = %q, want empty", p.OpenTag())
	}
	if p.CloseTag() != "" {
		t.Errorf("CloseTag = %q, want empty", p.CloseTag())
	}
	if len(p.openTags) != 4 {
		t.Errorf("default openTags length = %d, want 4", len(p.openTags))
	}
	if p.initialBufSize != 20 {
		t.Errorf("default initialBufSize = %d, want 20", p.initialBufSize)
	}
}

func TestNewParserCustom(t *testing.T) {
	tags := []string{"<custom>", "<test>"}
	p := NewParser(Remove, tags, 50)

	if p.HandlingModeValue() != Remove {
		t.Errorf("handlingMode = %q, want %q", p.HandlingModeValue(), Remove)
	}
	if len(p.openTags) != 2 {
		t.Errorf("openTags length = %d, want 2", len(p.openTags))
	}
	if p.initialBufSize != 50 {
		t.Errorf("initialBufSize = %d, want 50", p.initialBufSize)
	}
	// maxTagLength = max(len("<custom>"), len("<test>")) * 2 = 8 * 2 = 16
	if p.maxTagLength != 16 {
		t.Errorf("maxTagLength = %d, want 16", p.maxTagLength)
	}
}

// ---------------------------------------------------------------------------
// PreContent state — tag detection
// ---------------------------------------------------------------------------

func TestDetectsThinkingTag(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	p.ProcessChunk("<thinking>Hello")

	if p.State() != InThinking {
		t.Errorf("state = %d, want InThinking", p.State())
	}
	if p.OpenTag() != "<thinking>" {
		t.Errorf("OpenTag = %q, want <thinking>", p.OpenTag())
	}
	if p.CloseTag() != "</thinking>" {
		t.Errorf("CloseTag = %q, want </thinking>", p.CloseTag())
	}
	if !p.FoundThinkingBlock() {
		t.Error("FoundThinkingBlock should be true")
	}
}

func TestDetectsThinkTag(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	p.ProcessChunk("<think>Hello")

	if p.State() != InThinking {
		t.Errorf("state = %d, want InThinking", p.State())
	}
	if p.OpenTag() != "<think>" {
		t.Errorf("OpenTag = %q, want <think>", p.OpenTag())
	}
	if p.CloseTag() != "</think>" {
		t.Errorf("CloseTag = %q, want </think>", p.CloseTag())
	}
}

func TestDetectsReasoningTag(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	p.ProcessChunk("<reasoning>Hello")

	if p.State() != InThinking {
		t.Errorf("state = %d, want InThinking", p.State())
	}
	if p.OpenTag() != "<reasoning>" {
		t.Errorf("OpenTag = %q, want <reasoning>", p.OpenTag())
	}
}

func TestDetectsThoughtTag(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	p.ProcessChunk("<thought>Hello")

	if p.State() != InThinking {
		t.Errorf("state = %d, want InThinking", p.State())
	}
	if p.OpenTag() != "<thought>" {
		t.Errorf("OpenTag = %q, want <thought>", p.OpenTag())
	}
}

func TestStripsLeadingWhitespaceForTagDetection(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 40)
	p.ProcessChunk("  \n\n<thinking>Hello")

	if p.State() != InThinking {
		t.Errorf("state = %d, want InThinking", p.State())
	}
	if p.OpenTag() != "<thinking>" {
		t.Errorf("OpenTag = %q, want <thinking>", p.OpenTag())
	}
}

// ---------------------------------------------------------------------------
// PreContent state — partial tag buffering
// ---------------------------------------------------------------------------

func TestBuffersPartialTag(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	result := p.ProcessChunk("<think")

	if p.State() != PreContent {
		t.Errorf("state = %d, want PreContent", p.State())
	}
	if result.RegularContent != "" || result.ThinkingContent != "" {
		t.Error("partial tag should produce no output")
	}
}

func TestCompletesPartialTagAcrossChunks(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	p.ProcessChunk("<think")

	if p.State() != PreContent {
		t.Errorf("after first chunk: state = %d, want PreContent", p.State())
	}

	p.ProcessChunk("ing>Hello")

	if p.State() != InThinking {
		t.Errorf("after second chunk: state = %d, want InThinking", p.State())
	}
	if p.OpenTag() != "<thinking>" {
		t.Errorf("OpenTag = %q, want <thinking>", p.OpenTag())
	}
}

// ---------------------------------------------------------------------------
// PreContent state — no tag → Streaming
// ---------------------------------------------------------------------------

func TestNoTagTransitionsToStreaming(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	result := p.ProcessChunk("Hello, this is regular content without any thinking tags.")

	if p.State() != Streaming {
		t.Errorf("state = %d, want Streaming", p.State())
	}
	if result.RegularContent != "Hello, this is regular content without any thinking tags." {
		t.Errorf("RegularContent = %q, want original content", result.RegularContent)
	}
	if p.FoundThinkingBlock() {
		t.Error("FoundThinkingBlock should be false")
	}
}

func TestBufferExceedsLimitTransitionsToStreaming(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 10)
	result := p.ProcessChunk("This is a long content that exceeds the buffer limit")

	if p.State() != Streaming {
		t.Errorf("state = %d, want Streaming", p.State())
	}
	if result.RegularContent == "" {
		t.Error("RegularContent should not be empty")
	}
}

func TestEmptyChunkStaysInPreContent(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	result := p.ProcessChunk("")

	if p.State() != PreContent {
		t.Errorf("state = %d, want PreContent", p.State())
	}
	if result.RegularContent != "" || result.ThinkingContent != "" {
		t.Error("empty chunk should produce no output")
	}
}

// ---------------------------------------------------------------------------
// InThinking state
// ---------------------------------------------------------------------------

func TestAccumulatesThinkingContent(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	p.ProcessChunk("<thinking>")
	p.ProcessChunk("This is thinking content")

	// Content should be in the buffer or emitted via cautious sending.
	if p.State() != InThinking {
		t.Errorf("state = %d, want InThinking", p.State())
	}
}

func TestDetectsClosingTag(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	p.ProcessChunk("<thinking>Hello")
	p.ProcessChunk("</thinking>World")

	if p.State() != Streaming {
		t.Errorf("state = %d, want Streaming", p.State())
	}
}

func TestRegularContentAfterClosingTag(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	p.ProcessChunk("<thinking>Thinking")
	result := p.ProcessChunk("</thinking>Regular content")

	if result.RegularContent != "Regular content" {
		t.Errorf("RegularContent = %q, want %q", result.RegularContent, "Regular content")
	}
}

func TestStripsWhitespaceAfterClosingTag(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	p.ProcessChunk("<thinking>Thinking")
	result := p.ProcessChunk("</thinking>\n\nRegular content")

	if result.RegularContent != "Regular content" {
		t.Errorf("RegularContent = %q, want %q", result.RegularContent, "Regular content")
	}
}

func TestCautiousBufferingKeepsMaxTagLength(t *testing.T) {
	p := NewParser(AsReasoningContent, []string{"<t>"}, 20)
	p.ProcessChunk("<t>")

	// Feed content longer than maxTagLength.
	longContent := strings.Repeat("A", 50)
	p.ProcessChunk(longContent)

	// The internal thinking buffer should be at most maxTagLength chars.
	if len(p.thinkingBuffer) > p.maxTagLength {
		t.Errorf("thinkingBuffer length = %d, want <= %d", len(p.thinkingBuffer), p.maxTagLength)
	}
}

func TestSplitClosingTagAcrossChunks(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	p.ProcessChunk("<thinking>Hello")
	p.ProcessChunk("</think")
	p.ProcessChunk("ing>World")

	if p.State() != Streaming {
		t.Errorf("state = %d, want Streaming", p.State())
	}
}

// ---------------------------------------------------------------------------
// Streaming state
// ---------------------------------------------------------------------------

func TestStreamingPassesContentThrough(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	p.ProcessChunk("Regular content") // transitions to Streaming

	result := p.ProcessChunk("More content")

	if result.RegularContent != "More content" {
		t.Errorf("RegularContent = %q, want %q", result.RegularContent, "More content")
	}
	if result.ThinkingContent != "" {
		t.Errorf("ThinkingContent = %q, want empty", result.ThinkingContent)
	}
}

func TestStreamingIgnoresThinkingTags(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	p.ProcessChunk("Regular content") // transitions to Streaming

	result := p.ProcessChunk("<thinking>This should be regular</thinking>")

	if result.RegularContent != "<thinking>This should be regular</thinking>" {
		t.Errorf("RegularContent = %q, want tags passed through", result.RegularContent)
	}
	if result.ThinkingContent != "" {
		t.Errorf("ThinkingContent = %q, want empty", result.ThinkingContent)
	}
}

// ---------------------------------------------------------------------------
// Finalize
// ---------------------------------------------------------------------------

func TestFinalizeFlushesThinkingBuffer(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	p.ProcessChunk("<thinking>Incomplete thinking")

	result := p.Finalize()

	if result.ThinkingContent == "" {
		t.Error("ThinkingContent should not be empty after finalize")
	}
	if !result.IsThinking {
		t.Error("IsThinking should be true")
	}
}

func TestFinalizeFlushesInitialBuffer(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	p.ProcessChunk("<thi") // partial tag, stays in initial buffer

	result := p.Finalize()

	if result.RegularContent != "<thi" {
		t.Errorf("RegularContent = %q, want %q", result.RegularContent, "<thi")
	}
}

func TestFinalizeClearsBuffers(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	p.ProcessChunk("<thinking>Content")
	p.Finalize()

	if p.thinkingBuffer != "" {
		t.Errorf("thinkingBuffer = %q, want empty", p.thinkingBuffer)
	}
	if p.initialBuffer != "" {
		t.Errorf("initialBuffer = %q, want empty", p.initialBuffer)
	}
}

// ---------------------------------------------------------------------------
// Reset
// ---------------------------------------------------------------------------

func TestResetReturnsToInitialState(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	p.ProcessChunk("<thinking>Content</thinking>Regular")

	p.Reset()

	if p.State() != PreContent {
		t.Errorf("state = %d, want PreContent", p.State())
	}
	if p.FoundThinkingBlock() {
		t.Error("FoundThinkingBlock should be false after reset")
	}
	if p.OpenTag() != "" {
		t.Errorf("OpenTag = %q, want empty", p.OpenTag())
	}
	if p.CloseTag() != "" {
		t.Errorf("CloseTag = %q, want empty", p.CloseTag())
	}
	if p.thinkingBuffer != "" {
		t.Errorf("thinkingBuffer = %q, want empty", p.thinkingBuffer)
	}
	if p.initialBuffer != "" {
		t.Errorf("initialBuffer = %q, want empty", p.initialBuffer)
	}
	if !p.isFirstThinkingChunk {
		t.Error("isFirstThinkingChunk should be true after reset")
	}
}

// ---------------------------------------------------------------------------
// ProcessForOutput — handling modes
// ---------------------------------------------------------------------------

func TestProcessForOutputAsReasoningContent(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	p.openTag = "<thinking>"
	p.closeTag = "</thinking>"

	got := p.ProcessForOutput("Thinking content", true, true)
	if got != "Thinking content" {
		t.Errorf("got %q, want %q", got, "Thinking content")
	}
}

func TestProcessForOutputRemove(t *testing.T) {
	p := NewParser(Remove, nil, 20)

	got := p.ProcessForOutput("Thinking content", true, true)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestProcessForOutputPassFirstChunk(t *testing.T) {
	p := NewParser(Pass, nil, 20)
	p.openTag = "<thinking>"
	p.closeTag = "</thinking>"

	got := p.ProcessForOutput("Content", true, false)
	if got != "<thinking>Content" {
		t.Errorf("got %q, want %q", got, "<thinking>Content")
	}
}

func TestProcessForOutputPassLastChunk(t *testing.T) {
	p := NewParser(Pass, nil, 20)
	p.openTag = "<thinking>"
	p.closeTag = "</thinking>"

	got := p.ProcessForOutput("Content", false, true)
	if got != "Content</thinking>" {
		t.Errorf("got %q, want %q", got, "Content</thinking>")
	}
}

func TestProcessForOutputPassFirstAndLast(t *testing.T) {
	p := NewParser(Pass, nil, 20)
	p.openTag = "<thinking>"
	p.closeTag = "</thinking>"

	got := p.ProcessForOutput("Content", true, true)
	if got != "<thinking>Content</thinking>" {
		t.Errorf("got %q, want %q", got, "<thinking>Content</thinking>")
	}
}

func TestProcessForOutputPassMiddleChunk(t *testing.T) {
	p := NewParser(Pass, nil, 20)
	p.openTag = "<thinking>"
	p.closeTag = "</thinking>"

	got := p.ProcessForOutput("Content", false, false)
	if got != "Content" {
		t.Errorf("got %q, want %q", got, "Content")
	}
}

func TestProcessForOutputStripTags(t *testing.T) {
	p := NewParser(StripTags, nil, 20)

	got := p.ProcessForOutput("Thinking content", true, true)
	if got != "Thinking content" {
		t.Errorf("got %q, want %q", got, "Thinking content")
	}
}

func TestProcessForOutputEmptyContent(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)

	got := p.ProcessForOutput("", true, true)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// Full flow integration tests
// ---------------------------------------------------------------------------

func TestCompleteThinkingBlock(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	result := p.ProcessChunk("<thinking>This is my reasoning process.</thinking>Here is the answer.")

	if p.State() != Streaming {
		t.Errorf("state = %d, want Streaming", p.State())
	}
	if !p.FoundThinkingBlock() {
		t.Error("FoundThinkingBlock should be true")
	}
	if result.RegularContent != "Here is the answer." {
		t.Errorf("RegularContent = %q, want %q", result.RegularContent, "Here is the answer.")
	}
	if result.ThinkingContent != "This is my reasoning process." {
		t.Errorf("ThinkingContent = %q, want %q", result.ThinkingContent, "This is my reasoning process.")
	}
}

func TestMultiChunkThinkingBlock(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)

	p.ProcessChunk("<think")
	if p.State() != PreContent {
		t.Errorf("after chunk 1: state = %d, want PreContent", p.State())
	}

	p.ProcessChunk("ing>Let me think")
	if p.State() != InThinking {
		t.Errorf("after chunk 2: state = %d, want InThinking", p.State())
	}

	p.ProcessChunk(" about this...</think")
	if p.State() != InThinking {
		t.Errorf("after chunk 3: state = %d, want InThinking", p.State())
	}

	result := p.ProcessChunk("ing>The answer is 42.")
	if p.State() != Streaming {
		t.Errorf("after chunk 4: state = %d, want Streaming", p.State())
	}
	if result.RegularContent != "The answer is 42." {
		t.Errorf("RegularContent = %q, want %q", result.RegularContent, "The answer is 42.")
	}
}

func TestNoThinkingBlock(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	result := p.ProcessChunk("This is just regular content without any thinking tags.")

	if p.State() != Streaming {
		t.Errorf("state = %d, want Streaming", p.State())
	}
	if p.FoundThinkingBlock() {
		t.Error("FoundThinkingBlock should be false")
	}
	if result.RegularContent != "This is just regular content without any thinking tags." {
		t.Errorf("RegularContent = %q, want original content", result.RegularContent)
	}
}

func TestThinkingBlockWithNewlines(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	result := p.ProcessChunk("<thinking>Reasoning</thinking>\n\n\nAnswer here")

	if result.RegularContent != "Answer here" {
		t.Errorf("RegularContent = %q, want %q", result.RegularContent, "Answer here")
	}
}

func TestEmptyThinkingBlock(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)
	result := p.ProcessChunk("<thinking></thinking>Answer")

	if p.State() != Streaming {
		t.Errorf("state = %d, want Streaming", p.State())
	}
	if result.RegularContent != "Answer" {
		t.Errorf("RegularContent = %q, want %q", result.RegularContent, "Answer")
	}
}

func TestThinkingBlockRemoveMode(t *testing.T) {
	p := NewParser(Remove, nil, 20)
	result := p.ProcessChunk("<thinking>Secret reasoning</thinking>Visible answer")

	if p.State() != Streaming {
		t.Errorf("state = %d, want Streaming", p.State())
	}
	// In remove mode, thinking content should be empty.
	if result.ThinkingContent != "" {
		t.Errorf("ThinkingContent = %q, want empty (remove mode)", result.ThinkingContent)
	}
	if result.RegularContent != "Visible answer" {
		t.Errorf("RegularContent = %q, want %q", result.RegularContent, "Visible answer")
	}
}

func TestThinkingBlockPassMode(t *testing.T) {
	p := NewParser(Pass, nil, 20)
	result := p.ProcessChunk("<thinking>Reasoning</thinking>Answer")

	if p.State() != Streaming {
		t.Errorf("state = %d, want Streaming", p.State())
	}
	// In pass mode, thinking content is still extracted (tags re-added by ProcessForOutput).
	if result.ThinkingContent != "Reasoning" {
		t.Errorf("ThinkingContent = %q, want %q", result.ThinkingContent, "Reasoning")
	}
}

func TestThinkingBlockStripTagsMode(t *testing.T) {
	p := NewParser(StripTags, nil, 20)
	result := p.ProcessChunk("<thinking>Reasoning</thinking>Answer")

	if p.State() != Streaming {
		t.Errorf("state = %d, want Streaming", p.State())
	}
	if result.ThinkingContent != "Reasoning" {
		t.Errorf("ThinkingContent = %q, want %q", result.ThinkingContent, "Reasoning")
	}
	if result.RegularContent != "Answer" {
		t.Errorf("RegularContent = %q, want %q", result.RegularContent, "Answer")
	}
}

// TestIsThinkingField verifies the IsThinking field is set correctly.
func TestIsThinkingField(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)

	// In PreContent, not thinking yet.
	r1 := p.ProcessChunk("<thinking>")
	// After detecting tag, we're in InThinking.
	if p.State() != InThinking {
		t.Errorf("state = %d, want InThinking", p.State())
	}

	// Feed more thinking content.
	r2 := p.ProcessChunk("reasoning here")
	if !r2.IsThinking {
		t.Error("IsThinking should be true while in InThinking state")
	}
	_ = r1

	// Close the thinking block.
	r3 := p.ProcessChunk("</thinking>answer")
	if r3.IsThinking {
		t.Error("IsThinking should be false after closing tag")
	}

	// In Streaming state.
	r4 := p.ProcessChunk("more")
	if r4.IsThinking {
		t.Error("IsThinking should be false in Streaming state")
	}
}

// TestResetAndReuse verifies the parser can be reset and reused.
func TestResetAndReuse(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)

	// First use.
	p.ProcessChunk("<thinking>First</thinking>Answer1")
	if p.State() != Streaming {
		t.Fatalf("first use: state = %d, want Streaming", p.State())
	}

	// Reset and reuse.
	p.Reset()
	result := p.ProcessChunk("<think>Second</think>Answer2")

	if p.State() != Streaming {
		t.Errorf("second use: state = %d, want Streaming", p.State())
	}
	if result.ThinkingContent != "Second" {
		t.Errorf("ThinkingContent = %q, want %q", result.ThinkingContent, "Second")
	}
	if result.RegularContent != "Answer2" {
		t.Errorf("RegularContent = %q, want %q", result.RegularContent, "Answer2")
	}
}

// TestLargeThinkingContentCautiousSending verifies that large thinking content
// is emitted incrementally via cautious buffering.
func TestLargeThinkingContentCautiousSending(t *testing.T) {
	p := NewParser(AsReasoningContent, []string{"<t>"}, 20)
	p.ProcessChunk("<t>")

	// Feed a large chunk that exceeds maxTagLength.
	bigContent := strings.Repeat("X", 100)
	result := p.ProcessChunk(bigContent)

	// Some content should have been emitted, and the buffer should be trimmed.
	if result.ThinkingContent == "" {
		t.Error("expected some ThinkingContent to be emitted via cautious sending")
	}
	if len(p.thinkingBuffer) > p.maxTagLength {
		t.Errorf("thinkingBuffer length = %d, want <= %d", len(p.thinkingBuffer), p.maxTagLength)
	}
}

// TestOnlyDetectsTagsAtStart verifies that thinking tags are only detected
// at the very beginning of the response.
func TestOnlyDetectsTagsAtStart(t *testing.T) {
	p := NewParser(AsReasoningContent, nil, 20)

	// Content that starts with non-tag text should go to Streaming.
	result := p.ProcessChunk("Hello <thinking>not detected</thinking>")

	if p.State() != Streaming {
		t.Errorf("state = %d, want Streaming", p.State())
	}
	if p.FoundThinkingBlock() {
		t.Error("FoundThinkingBlock should be false — tag not at start")
	}
	if !strings.Contains(result.RegularContent, "<thinking>") {
		t.Errorf("RegularContent should contain the tag as regular text, got %q", result.RegularContent)
	}
}
