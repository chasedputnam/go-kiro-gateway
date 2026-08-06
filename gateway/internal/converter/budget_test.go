package converter

import "testing"

// charEstimate is a deterministic TokenEstimator for tests: it counts the
// total number of content characters across all messages plus the system
// prompt. Tools are ignored.
func charEstimate(messages []UnifiedMessage, _ []UnifiedTool, systemPrompt string) int {
	total := len(systemPrompt)
	for _, m := range messages {
		total += len(m.Content)
	}
	return total
}

func msgs(contents ...string) []UnifiedMessage {
	out := make([]UnifiedMessage, 0, len(contents))
	for i, c := range contents {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		out = append(out, UnifiedMessage{Role: role, Content: c})
	}
	return out
}

func TestFitMessagesToBudget_DropsOldestWhenOverBudget(t *testing.T) {
	in := msgs("aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc") // 10 + 10 + 10 = 30
	out, dropped := FitMessagesToBudget(in, nil, "", 15, charEstimate)

	if !dropped {
		t.Fatalf("expected dropped=true")
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 message kept, got %d", len(out))
	}
	// The most recent message must always be preserved.
	if out[len(out)-1].Content != "cccccccccc" {
		t.Errorf("expected last message preserved, got %q", out[len(out)-1].Content)
	}
}

func TestFitMessagesToBudget_KeepsAllWhenUnderBudget(t *testing.T) {
	in := msgs("aa", "bb", "cc")
	out, dropped := FitMessagesToBudget(in, nil, "", 1000, charEstimate)

	if dropped {
		t.Fatalf("expected dropped=false when under budget")
	}
	if len(out) != 3 {
		t.Fatalf("expected all 3 messages kept, got %d", len(out))
	}
}

func TestFitMessagesToBudget_AlwaysKeepsFinalMessage(t *testing.T) {
	// The final message alone exceeds the budget; it must still be kept.
	in := msgs("hugehistorymessage", "x") // 18 + 1
	out, dropped := FitMessagesToBudget(in, nil, "", 0+1, charEstimate)

	if !dropped {
		t.Fatalf("expected dropped=true")
	}
	if len(out) != 1 || out[0].Content != "x" {
		t.Fatalf("expected only final message kept, got %+v", out)
	}
}

func TestFitMessagesToBudget_NoOpForZeroBudget(t *testing.T) {
	in := msgs("aa", "bb", "cc")
	out, dropped := FitMessagesToBudget(in, nil, "", 0, charEstimate)

	if dropped || len(out) != 3 {
		t.Fatalf("expected no-op for budget<=0, got dropped=%v len=%d", dropped, len(out))
	}
}

func TestFitMessagesToBudget_NoOpForSingleMessage(t *testing.T) {
	in := msgs("this single message is very large")
	out, dropped := FitMessagesToBudget(in, nil, "", 1, charEstimate)

	if dropped || len(out) != 1 {
		t.Fatalf("expected no-op for single message, got dropped=%v len=%d", dropped, len(out))
	}
}

func TestFitMessagesToBudget_NilEstimatorIsNoOp(t *testing.T) {
	in := msgs("aa", "bb", "cc")
	out, dropped := FitMessagesToBudget(in, nil, "", 1, nil)

	if dropped || len(out) != 3 {
		t.Fatalf("expected no-op for nil estimator, got dropped=%v len=%d", dropped, len(out))
	}
}

func TestFitMessagesToBudget_SystemPromptCountsTowardBudget(t *testing.T) {
	// System prompt is fixed overhead: a large prompt forces more trimming.
	in := msgs("aaaaa", "bbbbb", "ccccc") // 5 + 5 + 5 = 15
	// Budget 12 with a 10-char system prompt: total starts 25, must drop to
	// fit prompt(10)+messages <=12 → keep only final message (10+5=15>12 →
	// keep final only, 10+5=15 still >12 but len==1 stops).
	out, dropped := FitMessagesToBudget(in, nil, "1234567890", 12, charEstimate)

	if !dropped {
		t.Fatalf("expected dropped=true")
	}
	if len(out) != 1 || out[0].Content != "ccccc" {
		t.Fatalf("expected only final message kept, got %+v", out)
	}
}
