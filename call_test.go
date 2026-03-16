package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCmdCall_ParamEqualsValue(t *testing.T) {
	// We can't easily test the full cmdCall (needs server), so test the flag
	// parsing logic by replicating the loop from cmdCall.
	args := []string{"server", "tool", "--msg=hello world", "--count=42", "--flag"}
	dynamicFlags := make(map[string]string)

	for i := 2; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			t.Fatalf("unexpected positional arg %q", arg)
		}
		key := strings.TrimPrefix(arg, "--")
		if eqIdx := strings.IndexByte(key, '='); eqIdx >= 0 {
			dynamicFlags[key[:eqIdx]] = key[eqIdx+1:]
		} else if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
			dynamicFlags[key] = "true"
		} else {
			i++
			dynamicFlags[key] = args[i]
		}
	}

	if dynamicFlags["msg"] != "hello world" {
		t.Errorf("expected msg='hello world', got %q", dynamicFlags["msg"])
	}
	if dynamicFlags["count"] != "42" {
		t.Errorf("expected count='42', got %q", dynamicFlags["count"])
	}
	if dynamicFlags["flag"] != "true" {
		t.Errorf("expected flag='true', got %q", dynamicFlags["flag"])
	}
}

func TestCmdCall_UnexpectedPositionalArg(t *testing.T) {
	err := cmdCall([]string{"server", "tool", "badarg"})
	if err == nil {
		t.Fatal("expected error for positional arg")
	}
	if !strings.Contains(err.Error(), "unexpected argument") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRenderContent_TextBlock(t *testing.T) {
	result := toolCallResult{
		Content: []contentBlock{{Type: "text", Text: "hello world"}},
	}
	got := renderToolCallResult(result)
	if got.Content != "hello world" {
		t.Errorf("expected 'hello world', got %q", got.Content)
	}
	if got.IsError {
		t.Error("expected isError false")
	}
}

func TestRenderContent_ImageBlock(t *testing.T) {
	result := toolCallResult{
		Content: []contentBlock{{Type: "image", MimeType: "image/png", Data: "base64data"}},
	}
	got := renderToolCallResult(result)
	if got.Content != "[image: image/png]" {
		t.Errorf("expected '[image: image/png]', got %q", got.Content)
	}
}

func TestRenderContent_MultipleBlocks(t *testing.T) {
	result := toolCallResult{
		Content: []contentBlock{
			{Type: "text", Text: "line1"},
			{Type: "text", Text: "line2"},
			{Type: "image", MimeType: "image/jpeg"},
		},
	}
	got := renderToolCallResult(result)
	parts := strings.Split(got.Content, "\n")
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d: %q", len(parts), got.Content)
	}
	if parts[0] != "line1" || parts[1] != "line2" || parts[2] != "[image: image/jpeg]" {
		t.Errorf("unexpected content: %q", got.Content)
	}
}

func TestRenderContent_IsError(t *testing.T) {
	result := toolCallResult{
		Content: []contentBlock{{Type: "text", Text: "something failed"}},
		IsError: true,
	}
	got := renderToolCallResult(result)
	if !got.IsError {
		t.Error("expected isError true")
	}
}

func TestRenderContent_UnknownType(t *testing.T) {
	result := toolCallResult{
		Content: []contentBlock{{Type: "resource", Text: "data"}},
	}
	got := renderToolCallResult(result)
	// Unknown types get JSON-serialized
	if !strings.Contains(got.Content, "resource") {
		t.Errorf("expected content to contain 'resource', got %q", got.Content)
	}
}

func TestCallToolFlow(t *testing.T) {
	transport := &mockTransport{
		sendFunc: func(req jsonrpcRequest) (jsonrpcResponse, error) {
			if req.Method != "tools/call" {
				t.Errorf("expected method 'tools/call', got %q", req.Method)
			}

			// Verify params
			data, _ := json.Marshal(req.Params)
			var params toolCallParams
			json.Unmarshal(data, &params)
			if params.Name != "echo" {
				t.Errorf("expected tool 'echo', got %q", params.Name)
			}
			if params.Arguments["message"] != "test" {
				t.Errorf("expected argument message='test', got %v", params.Arguments["message"])
			}

			result := toolCallResult{
				Content: []contentBlock{{Type: "text", Text: "echoed: test"}},
			}
			resultData, _ := json.Marshal(result)
			return jsonrpcResponse{
				JSONRPC: "2.0",
				ID:      json.RawMessage(fmt.Sprintf("%d", req.ID)),
				Result:  resultData,
			}, nil
		},
	}

	output, err := executeToolCall(transport, "echo", map[string]interface{}{"message": "test"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if output.Content != "echoed: test" {
		t.Errorf("expected 'echoed: test', got %q", output.Content)
	}
}

func TestCallToolFlow_JSONRPCError(t *testing.T) {
	transport := &mockTransport{
		sendFunc: func(req jsonrpcRequest) (jsonrpcResponse, error) {
			return jsonrpcResponse{
				JSONRPC: "2.0",
				ID:      json.RawMessage(fmt.Sprintf("%d", req.ID)),
				Error:   &jsonrpcError{Code: -32602, Message: "invalid params"},
			}, nil
		},
	}

	output, err := executeToolCall(transport, "bad-tool", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !output.IsError {
		t.Error("expected isError for JSON-RPC error")
	}
	if output.Content != "invalid params" {
		t.Errorf("expected 'invalid params', got %q", output.Content)
	}
}

func TestCallToolFlow_Stream(t *testing.T) {
	var events []streamEvent
	transport := &mockTransport{
		streamFunc: func(req jsonrpcRequest, onEvent func(streamEvent)) (jsonrpcResponse, error) {
			onEvent(streamEvent{Type: "progress", Data: "working..."})
			events = append(events, streamEvent{Type: "progress", Data: "working..."})

			result := toolCallResult{
				Content: []contentBlock{{Type: "text", Text: "done"}},
			}
			resultData, _ := json.Marshal(result)
			return jsonrpcResponse{
				JSONRPC: "2.0",
				ID:      json.RawMessage(fmt.Sprintf("%d", req.ID)),
				Result:  resultData,
			}, nil
		},
	}

	output, err := executeToolCall(transport, "slow-tool", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if output.Content != "done" {
		t.Errorf("expected 'done', got %q", output.Content)
	}
}
