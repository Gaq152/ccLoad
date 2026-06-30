package app

import (
	"testing"

	"github.com/bytedance/sonic"
)

func TestTransformCodexRequestBodyPreservesParallelToolCalls(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.5",
		"instructions":"test",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],
		"parallel_tool_calls":false,
		"prompt_cache_key":"cache-key",
		"reasoning":{"effort":"high","summary":"auto"},
		"stream":true,
		"store":false
	}`)

	transformed, err := TransformCodexRequestBody(body)
	if err != nil {
		t.Fatalf("TransformCodexRequestBody failed: %v", err)
	}

	var req map[string]any
	if err := sonic.Unmarshal(transformed, &req); err != nil {
		t.Fatalf("unmarshal transformed request: %v", err)
	}

	if got, ok := req["parallel_tool_calls"].(bool); !ok || got {
		t.Fatalf("parallel_tool_calls = %#v, want false", req["parallel_tool_calls"])
	}
}

func TestTransformCodexRequestBodyDoesNotAddMissingParallelToolCalls(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.5",
		"instructions":"test",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],
		"prompt_cache_key":"cache-key",
		"reasoning":{"effort":"high","summary":"auto"},
		"stream":true,
		"store":false
	}`)

	transformed, err := TransformCodexRequestBody(body)
	if err != nil {
		t.Fatalf("TransformCodexRequestBody failed: %v", err)
	}

	var req map[string]any
	if err := sonic.Unmarshal(transformed, &req); err != nil {
		t.Fatalf("unmarshal transformed request: %v", err)
	}

	if _, ok := req["parallel_tool_calls"]; ok {
		t.Fatalf("parallel_tool_calls = %#v, want missing", req["parallel_tool_calls"])
	}
}
