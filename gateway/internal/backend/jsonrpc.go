// Package backend — JSON-RPC 2.0 transport over stdio.
//
// Messages are framed as single-line JSON terminated by \n (newline-delimited
// JSON). The protocol distinguishes three message kinds:
//
//   - Requests:       have "id" and "method", sent by the client
//   - Responses:      have "id" and ("result" or "error"), sent by the server
//   - Notifications:  have "method" but no "id", sent by the server
package backend

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// ---------------------------------------------------------------------------
// Wire types
// ---------------------------------------------------------------------------

// rpcRequest is a JSON-RPC 2.0 request sent from the gateway to kiro-cli.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcMessage is the generic incoming message shape. After unmarshalling,
// callers inspect ID and Method to determine the message kind.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcError represents the error object in a JSON-RPC error response.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

// ---------------------------------------------------------------------------
// writeRequest encodes and sends a request over the writer.
// ---------------------------------------------------------------------------

// writeRequest marshals req as a single-line JSON object followed by \n and
// writes it to w. It is safe to call concurrently as long as callers
// serialize writes externally (e.g. via a mutex).
func writeRequest(w io.Writer, req rpcRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("jsonrpc marshal: %w", err)
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

// ---------------------------------------------------------------------------
// readMessage reads one newline-delimited JSON message from r.
// ---------------------------------------------------------------------------

// readMessage reads exactly one newline-terminated JSON line from r and
// unmarshals it into an rpcMessage. Returns io.EOF when the stream closes.
func readMessage(r *bufio.Reader) (*rpcMessage, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var msg rpcMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil, fmt.Errorf("jsonrpc unmarshal: %w", err)
	}
	return &msg, nil
}
