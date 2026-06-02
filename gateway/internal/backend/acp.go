// Package backend — ACP backend implementation.
//
// ACPBackend spawns kiro-cli as a subprocess and communicates with it over
// JSON-RPC 2.0 via stdin/stdout. Each call to Complete creates a new ACP
// session, sends the prompt, streams back KiroEvents, and discards the
// session when done (stateless per request).
package backend

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/config"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/streaming"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// ---------------------------------------------------------------------------
// ACPBackend
// ---------------------------------------------------------------------------

// ACPBackend fulfills requests by communicating with a kiro-cli subprocess
// over JSON-RPC 2.0 (newline-delimited JSON over stdin/stdout).
type ACPBackend struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	cfg    *config.Config
	logger zerolog.Logger

	mu      sync.Mutex              // serialises JSON-RPC writes
	nextID  atomic.Int64            // monotonically increasing request IDs
	pending sync.Map                // int64 → chan *rpcMessage (in-flight requests)
	notifs  sync.Map                // string (sessionID) → chan *SessionNotification

	done chan struct{} // closed when subprocess exits
}

// NewACPBackend locates kiro-cli, spawns it with the acp subcommand,
// performs the initialize handshake, and returns a ready-to-use backend.
func NewACPBackend(cfg *config.Config) (*ACPBackend, error) {
	cliPath, err := resolveKiroCLI(cfg.KiroCLIPath)
	if err != nil {
		return nil, err
	}

	args := []string{"acp"}
	if cfg.ACPAgent != "" {
		args = append(args, "--agent", cfg.ACPAgent)
	}

	cmd := exec.Command(cliPath, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: create stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: create stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("acp: start kiro-cli (%s): %w", cliPath, err)
	}

	b := &ACPBackend{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdoutPipe),
		cfg:    cfg,
		logger: log.With().Str("component", "acp").Logger(),
		done:   make(chan struct{}),
	}

	// Capture stderr and log at WARN level.
	go b.drainStderr(stderrPipe)

	// Dispatch loop: reads messages from stdout and routes them.
	go b.dispatchLoop()

	// Watch for subprocess exit.
	go func() {
		_ = cmd.Wait()
		close(b.done)
	}()

	// Perform the initialize handshake with a 10s timeout.
	if err := b.initialize(); err != nil {
		_ = b.Close()
		return nil, fmt.Errorf("acp: initialize handshake failed: %w", err)
	}

	log.Info().Str("cli_path", cliPath).Msg("ACP backend ready")
	return b, nil
}

// Close terminates the kiro-cli subprocess gracefully.
func (b *ACPBackend) Close() error {
	if b.cmd == nil || b.cmd.Process == nil {
		return nil
	}
	// Send SIGTERM, then wait for the subprocess-exit watcher to signal b.done.
	// We reuse the existing watcher goroutine (which calls cmd.Wait()) rather than
	// calling Wait() a second time, which would return an error on most platforms.
	_ = b.cmd.Process.Signal(os.Interrupt)
	select {
	case <-b.done:
	case <-time.After(5 * time.Second):
		_ = b.cmd.Process.Kill()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Complete — per-request session flow
// ---------------------------------------------------------------------------

// Complete creates a new ACP session, sends the prompt, and returns a channel
// of KiroEvents. The channel is closed on TurnEnd or context cancellation.
func (b *ACPBackend) Complete(ctx context.Context, req *Request) (<-chan streaming.KiroEvent, error) {
	// Check subprocess is alive.
	select {
	case <-b.done:
		return nil, fmt.Errorf("acp: kiro-cli subprocess has exited")
	default:
	}

	// 1. Create a new session.
	sessionID, err := b.sessionNew(ctx)
	if err != nil {
		return nil, fmt.Errorf("acp: session/new failed: %w", err)
	}

	// 2. Set model (best-effort, warn on failure).
	if req.Model != "" {
		if err := b.sessionSetModel(ctx, sessionID, req.Model); err != nil {
			b.logger.Warn().Err(err).Str("model", req.Model).Msg("session/set_model failed, using default")
		}
	}

	// 3. Subscribe to notifications for this session.
	notifCh := make(chan *SessionNotification, 64)
	b.notifs.Store(sessionID, notifCh)

	// 4. Send the prompt.
	promptText := extractPromptText(req.Payload)
	if promptText == "" {
		b.logger.Warn().Str("session_id", sessionID).Msg("ACP: extractPromptText returned empty string; sending empty prompt to kiro-cli")
	}
	if err := b.sessionPrompt(ctx, sessionID, promptText); err != nil {
		b.notifs.Delete(sessionID)
		close(notifCh)
		return nil, fmt.Errorf("acp: session/prompt failed: %w", err)
	}

	// 5. Translate notifications to KiroEvents on a goroutine.
	events := make(chan streaming.KiroEvent, 64)
	start := time.Now()
	go func() {
		defer close(events)
		defer b.notifs.Delete(sessionID)
		defer close(notifCh)

		for {
			select {
			case <-ctx.Done():
				_ = b.sessionCancel(context.Background(), sessionID)
				return

			case <-b.done:
				events <- streaming.KiroEvent{
					Type:  streaming.EventTypeError,
					Error: fmt.Errorf("acp: kiro-cli subprocess exited unexpectedly"),
				}
				return

			case notif, ok := <-notifCh:
				if !ok {
					return
				}
				update, err := ParseUpdate(notif.Update)
				if err != nil {
					b.logger.Warn().Err(err).Msg("skipping unknown ACP notification")
					continue
				}

				switch u := update.(type) {
				case *AgentMessageChunk:
					events <- streaming.KiroEvent{
						Type:    streaming.EventTypeContent,
						Content: u.Content,
					}

				case *ToolCallNotification:
					if u.Status == "running" {
						events <- streaming.KiroEvent{
							Type: streaming.EventTypeToolCall,
							ToolCall: &streaming.ToolCallInfo{
								Name:      u.Name,
								Arguments: string(u.Params),
							},
						}
					}

				case *ToolCallUpdate:
					// Progress update — no KiroEvent equivalent, skip.

				case *TurnEndNotification:
					duration := time.Since(start)
					b.logger.Info().
						Str("session_id", sessionID).
						Str("model", req.Model).
						Dur("duration", duration).
						Msg("ACP session complete")
					return
				}
			}
		}
	}()

	return events, nil
}

// ---------------------------------------------------------------------------
// ACP method calls
// ---------------------------------------------------------------------------

func (b *ACPBackend) initialize() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	params := map[string]any{
		"clientInfo": map[string]any{
			"name":    "go-kiro-gateway",
			"version": "1.0",
		},
	}
	_, err := b.call(ctx, "initialize", params)
	return err
}

func (b *ACPBackend) sessionNew(ctx context.Context) (string, error) {
	result, err := b.call(ctx, "session/new", map[string]any{})
	if err != nil {
		return "", err
	}
	var r struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		return "", fmt.Errorf("acp: parse session/new result: %w", err)
	}
	return r.SessionID, nil
}

func (b *ACPBackend) sessionSetModel(ctx context.Context, sessionID, model string) error {
	_, err := b.call(ctx, "session/set_model", map[string]any{
		"sessionId": sessionID,
		"model":     model,
	})
	return err
}

// sessionPrompt sends a session/prompt request. kiro-cli responds with an
// immediate JSON-RPC ack, then streams session/notification messages
// asynchronously. If kiro-cli does not ack (e.g. protocol mismatch), this
// call will block until ctx is cancelled.
func (b *ACPBackend) sessionPrompt(ctx context.Context, sessionID, text string) error {
	_, err := b.call(ctx, "session/prompt", map[string]any{
		"sessionId": sessionID,
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
	})
	return err
}

func (b *ACPBackend) sessionCancel(ctx context.Context, sessionID string) error {
	_, err := b.call(ctx, "session/cancel", map[string]any{
		"sessionId": sessionID,
	})
	return err
}

// ---------------------------------------------------------------------------
// JSON-RPC call helper
// ---------------------------------------------------------------------------

// call sends a JSON-RPC request and waits for the corresponding response.
func (b *ACPBackend) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := b.nextID.Add(1)
	respCh := make(chan *rpcMessage, 1)
	b.pending.Store(id, respCh)
	defer b.pending.Delete(id)

	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	b.mu.Lock()
	err := writeRequest(b.stdin, req)
	b.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("acp: write %s: %w", method, err)
	}

	b.logger.Debug().Str("method", method).Int64("id", id).Msg("ACP → sent")

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.done:
		return nil, fmt.Errorf("acp: subprocess exited while waiting for %s response", method)
	case msg := <-respCh:
		if msg.Error != nil {
			return nil, msg.Error
		}
		b.logger.Debug().Str("method", method).Int64("id", id).Msg("ACP ← received")
		return msg.Result, nil
	}
}

// ---------------------------------------------------------------------------
// Dispatch loop
// ---------------------------------------------------------------------------

// dispatchLoop reads messages from stdout and routes them to the appropriate
// pending response channel or active session notification channel.
func (b *ACPBackend) dispatchLoop() {
	for {
		msg, err := readMessage(b.stdout)
		if err != nil {
			if err != io.EOF {
				b.logger.Warn().Err(err).Msg("ACP read error")
			}
			return
		}

		if msg.Method == "" {
			// Response to an in-flight request.
			if ch, ok := b.pending.Load(msg.ID); ok {
				ch.(chan *rpcMessage) <- msg
			}
		} else if msg.Method == "session/notification" {
			// Notification — route to the matching session channel.
			var notif SessionNotification
			if err := json.Unmarshal(msg.Params, &notif); err != nil {
				b.logger.Warn().Err(err).Msg("ACP: failed to parse session/notification params")
				continue
			}
			if ch, ok := b.notifs.Load(notif.SessionID); ok {
				select {
				case ch.(chan *SessionNotification) <- &notif:
				default:
					b.logger.Warn().Str("session_id", notif.SessionID).Msg("ACP: notification channel full, dropping")
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// resolveKiroCLI returns the absolute path to kiro-cli, checking the explicit
// path first, then falling back to PATH lookup.
func resolveKiroCLI(explicitPath string) (string, error) {
	if explicitPath != "" {
		if _, err := os.Stat(explicitPath); err != nil {
			return "", fmt.Errorf("acp: kiro-cli not found at KIRO_CLI_PATH=%q: %w", explicitPath, err)
		}
		return explicitPath, nil
	}
	p, err := exec.LookPath("kiro-cli")
	if err != nil {
		return "", fmt.Errorf(
			"acp: kiro-cli not found on PATH and KIRO_CLI_PATH is not set.\n" +
				"Install kiro-cli from https://kiro.dev/downloads/ or set KIRO_CLI_PATH to its location.",
		)
	}
	return p, nil
}

// drainStderr reads stderr from the subprocess and logs each line at WARN.
func (b *ACPBackend) drainStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		b.logger.Warn().Str("stderr", scanner.Text()).Msg("kiro-cli stderr")
	}
}

// extractPromptText pulls the last user message text from a Kiro-format payload.
// Falls back to a serialised JSON representation if structure is unexpected.
func extractPromptText(payload map[string]any) string {
	msgs, ok := payload["messages"].([]any)
	if !ok || len(msgs) == 0 {
		return ""
	}
	// Walk backwards to find the last user message.
	for i := len(msgs) - 1; i >= 0; i-- {
		msg, ok := msgs[i].(map[string]any)
		if !ok {
			continue
		}
		if msg["role"] != "user" {
			continue
		}
		switch c := msg["content"].(type) {
		case string:
			return c
		case []any:
			for _, block := range c {
				b, ok := block.(map[string]any)
				if !ok {
					continue
				}
				if b["type"] == "text" {
					if t, ok := b["text"].(string); ok {
						return t
					}
				}
			}
		}
	}
	return ""
}
