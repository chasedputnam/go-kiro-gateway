// Package backend — ACP session notification types.
//
// These structs represent the update payloads delivered inside
// session/notification messages from kiro-cli. ParseUpdate decodes
// the "type" discriminator and returns the appropriate concrete type.
package backend

import (
	"encoding/json"
	"fmt"
)

// ---------------------------------------------------------------------------
// Notification update types
// ---------------------------------------------------------------------------

// AgentMessageChunk is a streaming text chunk from the agent.
type AgentMessageChunk struct {
	Type    string `json:"type"` // "AgentMessageChunk"
	Content string `json:"content"`
}

// ToolCallNotification signals a tool invocation.
type ToolCallNotification struct {
	Type   string          `json:"type"`   // "ToolCall"
	Name   string          `json:"name"`
	Params json.RawMessage `json:"params"`
	Status string          `json:"status"` // "running" | "complete" | "error"
}

// ToolCallUpdate carries a progress update for a running tool.
type ToolCallUpdate struct {
	Type   string `json:"type"`   // "ToolCallUpdate"
	Name   string `json:"name"`
	Status string `json:"status"`
}

// TurnEndNotification signals the agent turn has completed.
type TurnEndNotification struct {
	Type string `json:"type"` // "TurnEnd"
}

// SessionNotification is the params payload of a session/notification message.
type SessionNotification struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
}

// ---------------------------------------------------------------------------
// ParseUpdate
// ---------------------------------------------------------------------------

// typeDiscriminator is used to peek at the "type" field before full decode.
type typeDiscriminator struct {
	Type string `json:"type"`
}

// ParseUpdate decodes the "type" discriminator in raw and returns the
// concrete notification update type. Unknown types return an error.
func ParseUpdate(raw json.RawMessage) (any, error) {
	var d typeDiscriminator
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("acp: decode update type: %w", err)
	}

	switch d.Type {
	case "AgentMessageChunk":
		var v AgentMessageChunk
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("acp: decode AgentMessageChunk: %w", err)
		}
		return &v, nil

	case "ToolCall":
		var v ToolCallNotification
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("acp: decode ToolCall: %w", err)
		}
		return &v, nil

	case "ToolCallUpdate":
		var v ToolCallUpdate
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("acp: decode ToolCallUpdate: %w", err)
		}
		return &v, nil

	case "TurnEnd":
		var v TurnEndNotification
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("acp: decode TurnEnd: %w", err)
		}
		return &v, nil

	default:
		return nil, fmt.Errorf("acp: unknown update type %q", d.Type)
	}
}
