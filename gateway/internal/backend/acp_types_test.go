// Package backend — tests for ACP message types and ParseUpdate.
package backend

import (
	"encoding/json"
	"testing"
)

func TestParseUpdate_AgentMessageChunk(t *testing.T) {
	raw := json.RawMessage(`{"type":"AgentMessageChunk","content":"hello world"}`)
	v, err := ParseUpdate(raw)
	if err != nil {
		t.Fatalf("ParseUpdate error: %v", err)
	}
	chunk, ok := v.(*AgentMessageChunk)
	if !ok {
		t.Fatalf("expected *AgentMessageChunk, got %T", v)
	}
	if chunk.Content != "hello world" {
		t.Errorf("Content = %q, want %q", chunk.Content, "hello world")
	}
}

func TestParseUpdate_ToolCall(t *testing.T) {
	raw := json.RawMessage(`{"type":"ToolCall","name":"read_file","params":{},"status":"running"}`)
	v, err := ParseUpdate(raw)
	if err != nil {
		t.Fatalf("ParseUpdate error: %v", err)
	}
	tc, ok := v.(*ToolCallNotification)
	if !ok {
		t.Fatalf("expected *ToolCallNotification, got %T", v)
	}
	if tc.Name != "read_file" {
		t.Errorf("Name = %q, want %q", tc.Name, "read_file")
	}
	if tc.Status != "running" {
		t.Errorf("Status = %q, want %q", tc.Status, "running")
	}
}

func TestParseUpdate_ToolCallUpdate(t *testing.T) {
	raw := json.RawMessage(`{"type":"ToolCallUpdate","name":"read_file","status":"complete"}`)
	v, err := ParseUpdate(raw)
	if err != nil {
		t.Fatalf("ParseUpdate error: %v", err)
	}
	tcu, ok := v.(*ToolCallUpdate)
	if !ok {
		t.Fatalf("expected *ToolCallUpdate, got %T", v)
	}
	if tcu.Status != "complete" {
		t.Errorf("Status = %q, want %q", tcu.Status, "complete")
	}
}

func TestParseUpdate_TurnEnd(t *testing.T) {
	raw := json.RawMessage(`{"type":"TurnEnd"}`)
	v, err := ParseUpdate(raw)
	if err != nil {
		t.Fatalf("ParseUpdate error: %v", err)
	}
	_, ok := v.(*TurnEndNotification)
	if !ok {
		t.Fatalf("expected *TurnEndNotification, got %T", v)
	}
}

func TestParseUpdate_UnknownType(t *testing.T) {
	raw := json.RawMessage(`{"type":"SomeFutureType","data":"x"}`)
	_, err := ParseUpdate(raw)
	if err == nil {
		t.Error("expected error for unknown type, got nil")
	}
}

func TestParseUpdate_InvalidJSON(t *testing.T) {
	raw := json.RawMessage(`{not valid}`)
	_, err := ParseUpdate(raw)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestParseUpdate_MissingTypeField(t *testing.T) {
	raw := json.RawMessage(`{"content":"hello"}`)
	_, err := ParseUpdate(raw)
	if err == nil {
		t.Error("expected error when type field is missing/empty, got nil")
	}
}
