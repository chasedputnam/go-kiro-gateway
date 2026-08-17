package server

import (
	"strings"
	"testing"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/truncation"
)

func TestApplyResponsesTruncationRecovery_CustomAndArrayOutputs(t *testing.T) {
	state := truncation.NewState()
	state.SaveToolTruncation("call_function", "read", map[string]any{"reason": "truncated"})
	state.SaveToolTruncation("call_custom", "exec", map[string]any{"reason": "truncated"})
	s := &Server{truncState: state}

	input := []any{
		map[string]any{
			"type":    "function_call_output",
			"call_id": "call_function",
			"output": []any{
				map[string]any{"type": "input_text", "text": "first "},
				map[string]any{"type": "input_text", "text": "result"},
			},
		},
		map[string]any{
			"type":    "custom_tool_call_output",
			"call_id": "call_custom",
			"output":  "custom result",
		},
	}

	got := s.applyResponsesTruncationRecovery(input).([]any)
	functionOutput := got[0].(map[string]any)["output"].(string)
	customOutput := got[1].(map[string]any)["output"].(string)

	if !strings.Contains(functionOutput, truncation.TruncationToolResultNotice) || !strings.Contains(functionOutput, "first result") {
		t.Fatalf("function array output was not preserved with notice: %q", functionOutput)
	}
	if !strings.Contains(customOutput, truncation.TruncationToolResultNotice) || !strings.Contains(customOutput, "custom result") {
		t.Fatalf("custom output was not preserved with notice: %q", customOutput)
	}
}
