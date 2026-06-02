// Package backend — tests for JSON-RPC 2.0 transport layer.
package backend

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"testing"
)

func TestWriteRequest_ProducesNewlineDelimitedJSON(t *testing.T) {
	var buf bytes.Buffer
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params:  map[string]any{"key": "val"},
	}
	if err := writeRequest(&buf, req); err != nil {
		t.Fatalf("writeRequest error: %v", err)
	}
	data := buf.Bytes()
	if len(data) == 0 {
		t.Fatal("writeRequest produced no output")
	}
	if data[len(data)-1] != '\n' {
		t.Error("writeRequest output must be terminated by newline")
	}
	var got rpcRequest
	if err := json.Unmarshal(data[:len(data)-1], &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if got.Method != "initialize" {
		t.Errorf("Method = %q, want %q", got.Method, "initialize")
	}
	if got.ID != 1 {
		t.Errorf("ID = %d, want 1", got.ID)
	}
}

func TestReadMessage_ParsesResponse(t *testing.T) {
	raw := `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}` + "\n"
	r := bufio.NewReader(bytes.NewBufferString(raw))
	msg, err := readMessage(r)
	if err != nil {
		t.Fatalf("readMessage error: %v", err)
	}
	if msg.ID != 1 {
		t.Errorf("ID = %d, want 1", msg.ID)
	}
	if msg.Method != "" {
		t.Errorf("Method should be empty for a response, got %q", msg.Method)
	}
	if msg.Result == nil {
		t.Error("Result should not be nil")
	}
}

func TestReadMessage_ParsesNotification(t *testing.T) {
	raw := `{"jsonrpc":"2.0","method":"session/notification","params":{"sessionId":"abc"}}` + "\n"
	r := bufio.NewReader(bytes.NewBufferString(raw))
	msg, err := readMessage(r)
	if err != nil {
		t.Fatalf("readMessage error: %v", err)
	}
	if msg.Method != "session/notification" {
		t.Errorf("Method = %q, want %q", msg.Method, "session/notification")
	}
	if msg.ID != 0 {
		t.Errorf("ID should be 0 for a notification, got %d", msg.ID)
	}
}

func TestReadMessage_ParsesErrorResponse(t *testing.T) {
	raw := `{"jsonrpc":"2.0","id":2,"error":{"code":-32600,"message":"Invalid Request"}}` + "\n"
	r := bufio.NewReader(bytes.NewBufferString(raw))
	msg, err := readMessage(r)
	if err != nil {
		t.Fatalf("readMessage error: %v", err)
	}
	if msg.Error == nil {
		t.Fatal("Error field should not be nil")
	}
	if msg.Error.Code != -32600 {
		t.Errorf("Error.Code = %d, want -32600", msg.Error.Code)
	}
}

func TestReadMessage_ReturnsEOFOnEmpty(t *testing.T) {
	r := bufio.NewReader(bytes.NewBufferString(""))
	_, err := readMessage(r)
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestReadMessage_ReturnsErrorOnInvalidJSON(t *testing.T) {
	r := bufio.NewReader(bytes.NewBufferString("{not json}\n"))
	_, err := readMessage(r)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestRpcError_ErrorMethod(t *testing.T) {
	e := &rpcError{Code: -32601, Message: "Method not found"}
	got := e.Error()
	if got == "" {
		t.Error("Error() should not return empty string")
	}
}

func TestRoundTrip_WriteAndRead(t *testing.T) {
	pr, pw := io.Pipe()

	req := rpcRequest{JSONRPC: "2.0", ID: 42, Method: "session/new", Params: nil}
	go func() {
		_ = writeRequest(pw, req)
		pw.Close()
	}()

	r := bufio.NewReader(pr)
	msg, err := readMessage(r)
	if err != nil {
		t.Fatalf("readMessage error: %v", err)
	}
	if msg.Method != "session/new" {
		t.Errorf("Method = %q, want %q", msg.Method, "session/new")
	}
	if msg.ID != 42 {
		t.Errorf("ID = %d, want 42", msg.ID)
	}
}
