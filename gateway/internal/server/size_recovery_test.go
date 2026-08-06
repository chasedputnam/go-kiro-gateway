package server

import (
	"context"
	"errors"
	"testing"

	backendpkg "github.com/chasedputnam/go-kiro-gateway/gateway/internal/backend"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/config"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/converter"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/debug"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/streaming"
)

// fakeBackend returns a content-length (or custom) error for the first failN
// calls, then a successful (closed) event channel.
type fakeBackend struct {
	calls int
	failN int
	errFn func() error
}

func (f *fakeBackend) Complete(_ context.Context, _ *backendpkg.Request) (<-chan streaming.KiroEvent, error) {
	f.calls++
	if f.calls <= f.failN {
		return nil, f.errFn()
	}
	ch := make(chan streaming.KiroEvent)
	close(ch)
	return ch, nil
}
func (f *fakeBackend) Close() error { return nil }

func contentLengthErr() error {
	return &backendpkg.HTTPError{
		StatusCode: 400,
		Body:       `{"reason":"CONTENT_LENGTH_EXCEEDS_THRESHOLD","message":"Input is too long."}`,
	}
}

func newBudgetTestServer(fb backendpkg.Backend, retries int) *Server {
	return &Server{
		config: &config.Config{
			InputTokenBudgetRatio: 0.85,
			MinInputTokenBudget:   8000,
			PayloadSizeMaxRetries: retries,
			DefaultMaxInputTokens: 200000,
		},
		backend:     fb,
		debugLogger: debug.NewDebugLogger("off", ""),
	}
}

func baseParams() sizeRecoveryParams {
	return sizeRecoveryParams{
		Messages:       []converter.UnifiedMessage{{Role: "user", Content: "hello world"}},
		SystemPrompt:   "",
		ModelID:        "claude-sonnet-4",
		ConversationID: "conv-1",
		Model:          "claude-sonnet-4",
		KiroURL:        "https://example.invalid/generateAssistantResponse",
		Stream:         true,
		MaxInputTokens: 200000,
		ReserveTokens:  0,
	}
}

// ---------------------------------------------------------------------------
// computeInputBudget
// ---------------------------------------------------------------------------

func TestComputeInputBudget_RatioAndReserve(t *testing.T) {
	s := newBudgetTestServer(&fakeBackend{}, 2)
	got := s.computeInputBudget(200000, 4000) // 0.85*200000 - 4000
	if want := 166000; got != want {
		t.Errorf("computeInputBudget = %d, want %d", got, want)
	}
}

func TestComputeInputBudget_FloorAtMinimum(t *testing.T) {
	s := newBudgetTestServer(&fakeBackend{}, 2)
	got := s.computeInputBudget(1000, 0) // 850 < min(8000)
	if want := 8000; got != want {
		t.Errorf("computeInputBudget = %d, want floor %d", got, want)
	}
}

func TestComputeInputBudget_FallsBackToDefaultWindow(t *testing.T) {
	s := newBudgetTestServer(&fakeBackend{}, 2)
	got := s.computeInputBudget(0, 0) // uses DefaultMaxInputTokens=200000
	if want := 170000; got != want {
		t.Errorf("computeInputBudget = %d, want %d", got, want)
	}
}

// ---------------------------------------------------------------------------
// isContentLengthError
// ---------------------------------------------------------------------------

func TestIsContentLengthError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"content-length reason", contentLengthErr(), true},
		{"other reason", &backendpkg.HTTPError{StatusCode: 429, Body: `{"reason":"MONTHLY_REQUEST_COUNT"}`}, false},
		{"non-json body", &backendpkg.HTTPError{StatusCode: 500, Body: "internal error"}, false},
		{"plain error", errors.New("boom"), false},
		{"nil error", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isContentLengthError(tc.err); got != tc.want {
				t.Errorf("isContentLengthError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// completeWithSizeRecovery
// ---------------------------------------------------------------------------

func TestCompleteWithSizeRecovery_RetriesThenSucceeds(t *testing.T) {
	fb := &fakeBackend{failN: 2, errFn: contentLengthErr} // fail twice, succeed on 3rd
	s := newBudgetTestServer(fb, 2)                       // 2 retries → 3 attempts

	events, _, err := s.completeWithSizeRecovery(context.Background(), baseParams())
	if err != nil {
		t.Fatalf("expected success after retries, got error: %v", err)
	}
	if events == nil {
		t.Fatal("expected non-nil events channel")
	}
	if fb.calls != 3 {
		t.Errorf("expected 3 backend calls, got %d", fb.calls)
	}
}

func TestCompleteWithSizeRecovery_ExhaustsRetries(t *testing.T) {
	fb := &fakeBackend{failN: 99, errFn: contentLengthErr} // always fail
	s := newBudgetTestServer(fb, 2)                        // 3 attempts total

	_, _, err := s.completeWithSizeRecovery(context.Background(), baseParams())
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if !isContentLengthError(err) {
		t.Errorf("expected content-length error to be surfaced, got %v", err)
	}
	if fb.calls != 3 {
		t.Errorf("expected 3 backend calls (1 + 2 retries), got %d", fb.calls)
	}
}

func TestCompleteWithSizeRecovery_NonContentLengthErrorNotRetried(t *testing.T) {
	otherErr := func() error {
		return &backendpkg.HTTPError{StatusCode: 429, Body: `{"reason":"MONTHLY_REQUEST_COUNT"}`}
	}
	fb := &fakeBackend{failN: 99, errFn: otherErr}
	s := newBudgetTestServer(fb, 2)

	_, _, err := s.completeWithSizeRecovery(context.Background(), baseParams())
	if err == nil {
		t.Fatal("expected error to be surfaced")
	}
	if fb.calls != 1 {
		t.Errorf("expected exactly 1 backend call (no retry), got %d", fb.calls)
	}
}
