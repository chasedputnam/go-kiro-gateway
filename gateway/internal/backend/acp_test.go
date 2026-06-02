// Package backend — tests for ACPBackend using a fake kiro-cli subprocess.
package backend

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/config"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/streaming"
)

// ---------------------------------------------------------------------------
// Fake kiro-cli helper process
// ---------------------------------------------------------------------------
//
// Tests that need a subprocess call os/exec to re-invoke the test binary with
// the FAKE_KIRO_CLI env var set. The TestFakeKiroCLI function acts as the
// fake binary, reading JSON-RPC requests and writing scripted responses.

// TestFakeKiroCLI is the entrypoint for the fake kiro-cli subprocess.
// It is not a real test — it exits when FAKE_KIRO_CLI is not set.
func TestFakeKiroCLI(t *testing.T) {
	if os.Getenv("FAKE_KIRO_CLI") != "1" {
		t.Skip("not a fake kiro-cli invocation")
	}
	fakeKiroCLIMain()
}

// fakeKiroCLIMain simulates kiro-cli acp over stdin/stdout.
func fakeKiroCLIMain() {
	r := bufio.NewReader(os.Stdin)
	for {
		msg, err := readMessage(r)
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "fake kiro-cli read error: %v\n", err)
			}
			return
		}

		switch msg.Method {
		case "initialize":
			respond(os.Stdout, msg.ID, map[string]any{
				"capabilities": map[string]any{
					"loadSession":       true,
					"promptCapabilities": map[string]any{"image": true},
				},
				"serverInfo": map[string]any{"name": "fake-kiro-cli"},
			})

		case "session/new":
			respond(os.Stdout, msg.ID, map[string]any{"sessionId": "test-session-1"})

		case "session/set_model":
			respond(os.Stdout, msg.ID, map[string]any{"ok": true})

		case "session/prompt":
			// Parse sessionId from params.
			var params struct {
				SessionID string `json:"sessionId"`
			}
			_ = json.Unmarshal(msg.Params, &params)

			// Acknowledge the prompt request.
			respond(os.Stdout, msg.ID, map[string]any{})

			// Send streaming chunks.
			sendNotification(os.Stdout, params.SessionID, AgentMessageChunk{
				Type: "AgentMessageChunk", Content: "Hello",
			})
			sendNotification(os.Stdout, params.SessionID, AgentMessageChunk{
				Type: "AgentMessageChunk", Content: " world",
			})
			sendNotification(os.Stdout, params.SessionID, TurnEndNotification{
				Type: "TurnEnd",
			})

		case "session/cancel":
			respond(os.Stdout, msg.ID, map[string]any{})
		}
	}
}

func respond(w io.Writer, id int64, result any) {
	data, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
	_, _ = w.Write(append(data, '\n'))
}

func sendNotification(w io.Writer, sessionID string, update any) {
	params := map[string]any{
		"sessionId": sessionID,
		"update":    update,
	}
	data, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "session/notification",
		"params":  params,
	})
	_, _ = w.Write(append(data, '\n'))
}

// ---------------------------------------------------------------------------
// Helper: newTestACPBackend
// ---------------------------------------------------------------------------

// newTestACPBackend creates an ACPBackend using the test binary as the fake
// kiro-cli subprocess.
func newTestACPBackend(t *testing.T) *ACPBackend {
	t.Helper()

	testBin, err := os.Executable()
	if err != nil {
		t.Fatalf("failed to get test binary path: %v", err)
	}

	cfg := &config.Config{KiroCLIPath: testBin, ACPAgent: ""}

	// Build the backend manually to inject the fake subprocess.
	args := []string{"-test.run=TestFakeKiroCLI", "-test.v=false"}
	cmd := exec.Command(testBin, args...)
	cmd.Env = append(os.Environ(), "FAKE_KIRO_CLI=1")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start: %v", err)
	}

	b := &ACPBackend{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdoutPipe),
		cfg:    cfg,
		done:   make(chan struct{}),
	}
	import_zerolog(b, t)

	go b.drainStderr(stderrPipe)
	go b.dispatchLoop()
	go func() {
		_ = cmd.Wait()
		close(b.done)
	}()

	if err := b.initialize(); err != nil {
		t.Fatalf("initialize handshake failed: %v", err)
	}
	return b
}

// import_zerolog sets up the logger field (zerolog is not importable as an
// expression, so we use the package-level log via a small helper).
func import_zerolog(b *ACPBackend, t *testing.T) {
	// The zerolog.Logger zero value is a no-op logger — fine for tests.
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestACPBackend_Complete_StreamsChunks(t *testing.T) {
	b := newTestACPBackend(t)
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := &Request{
		Payload: map[string]any{
			"messages": []any{
				map[string]any{"role": "user", "content": "hello"},
			},
		},
		Model: "claude-sonnet-4-6",
	}

	ch, err := b.Complete(ctx, req)
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	var contents []string
	for event := range ch {
		if event.Type == streaming.EventTypeContent {
			contents = append(contents, event.Content)
		}
		if event.Type == streaming.EventTypeError {
			t.Fatalf("received error event: %v", event.Error)
		}
	}

	full := strings.Join(contents, "")
	if full != "Hello world" {
		t.Errorf("accumulated content = %q, want %q", full, "Hello world")
	}
}

func TestACPBackend_Complete_ChannelClosedAfterTurnEnd(t *testing.T) {
	b := newTestACPBackend(t)
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := b.Complete(ctx, &Request{
		Payload: map[string]any{"messages": []any{
			map[string]any{"role": "user", "content": "ping"},
		}},
	})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	// Drain until closed.
	for range ch {
	}
	// If we reach here the channel was closed cleanly.
}

func TestACPBackend_Close_TerminatesProcess(t *testing.T) {
	b := newTestACPBackend(t)
	if err := b.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}
}

func TestACPBackend_Complete_AfterClose_ReturnsError(t *testing.T) {
	b := newTestACPBackend(t)
	_ = b.Close()

	// Wait for done channel to be closed.
	select {
	case <-b.done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for subprocess to exit")
	}

	_, err := b.Complete(context.Background(), &Request{})
	if err == nil {
		t.Error("expected error after Close(), got nil")
	}
}

// ---------------------------------------------------------------------------
// resolveKiroCLI tests (no subprocess needed)
// ---------------------------------------------------------------------------

func TestResolveKiroCLI_ExplicitPathNotFound(t *testing.T) {
	_, err := resolveKiroCLI("/nonexistent/path/kiro-cli")
	if err == nil {
		t.Error("expected error for nonexistent path, got nil")
	}
}

func TestResolveKiroCLI_ExplicitPathFound(t *testing.T) {
	// Use the test binary itself as a stand-in for an existing file.
	self, _ := os.Executable()
	path, err := resolveKiroCLI(self)
	if err != nil {
		t.Errorf("resolveKiroCLI(%q) error: %v", self, err)
	}
	if path != self {
		t.Errorf("path = %q, want %q", path, self)
	}
}

// ---------------------------------------------------------------------------
// extractPromptText tests
// ---------------------------------------------------------------------------

func TestExtractPromptText_LastUserMessage(t *testing.T) {
	payload := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "You are helpful."},
			map[string]any{"role": "user", "content": "Hello"},
			map[string]any{"role": "assistant", "content": "Hi"},
			map[string]any{"role": "user", "content": "How are you?"},
		},
	}
	got := extractPromptText(payload)
	if got != "How are you?" {
		t.Errorf("extractPromptText = %q, want %q", got, "How are you?")
	}
}

func TestExtractPromptText_ContentBlock(t *testing.T) {
	payload := map[string]any{
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "block text"},
				},
			},
		},
	}
	got := extractPromptText(payload)
	if got != "block text" {
		t.Errorf("extractPromptText = %q, want %q", got, "block text")
	}
}

func TestExtractPromptText_EmptyMessages(t *testing.T) {
	payload := map[string]any{"messages": []any{}}
	got := extractPromptText(payload)
	if got != "" {
		t.Errorf("extractPromptText = %q, want empty", got)
	}
}
