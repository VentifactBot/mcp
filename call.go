package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// errToolFailed is returned when the tool reported an error.
// The JSON output has already been printed; main should exit 1 silently.
var errToolFailed = errors.New("tool returned error")

// defaultMaxOutput caps tool output to stay within LLM token budgets.
const defaultMaxOutput = 30_000

// cmdCall handles the `mcp call <server> <tool> [--params '{}'] [--stream] [--max-output N]` command.
func cmdCall(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: mcp call <server> <tool> [--params '{...}'] [--stream] [--max-output N]")
	}

	serverName := args[0]
	if err := validateServerName(serverName); err != nil {
		return err
	}
	toolName := args[1]
	var paramsStr string
	stream := false
	maxOutput := defaultMaxOutput

	// Parse remaining args
	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--params", "-p":
			if i+1 >= len(args) {
				return fmt.Errorf("--params requires a value")
			}
			i++
			paramsStr = args[i]
		case "--stream":
			stream = true
		case "--max-output":
			if i+1 >= len(args) {
				return fmt.Errorf("--max-output requires a value")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil {
				return fmt.Errorf("invalid --max-output value: %s", args[i])
			}
			maxOutput = n
		}
	}

	// If no params from flag, try stdin
	if paramsStr == "" {
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			const maxStdinSize = 10 << 20 // 10 MB — generous for piped JSON params
			limited := io.LimitReader(os.Stdin, maxStdinSize+1)
			data, err := io.ReadAll(limited)
			if err != nil {
				return fmt.Errorf("read stdin: %w", err)
			}
			if len(data) > maxStdinSize {
				return fmt.Errorf("stdin input exceeds %d bytes", maxStdinSize)
			}
			paramsStr = strings.TrimSpace(string(data))
		}
	}

	// Parse params
	params := make(map[string]interface{})
	if paramsStr != "" {
		if err := json.Unmarshal([]byte(paramsStr), &params); err != nil {
			return fmt.Errorf("invalid params JSON: %w", err)
		}
	}

	// Load server config
	server, err := getServerConfig(serverName)
	if err != nil {
		return err
	}

	if !server.IsEnabled() {
		return fmt.Errorf("server %q is disabled", serverName)
	}

	// Get auth token
	authToken, err := getAuthToken(serverName)
	if err != nil {
		logStderr("warning: auth token load failed: %v", err)
	}

	// Connect
	transport, err := mcpConnect(server, authToken)
	if err != nil {
		return err
	}
	defer transport.Close()

	// Call tool
	output, err := executeToolCall(transport, toolName, params, stream)
	if err != nil {
		return err
	}

	// Truncate output to stay within token budgets.
	if maxOutput > 0 && len(output.Content) > maxOutput {
		savedPath := saveFullOutput(serverName, toolName, output.Content)
		output.Content = output.Content[:maxOutput] + fmt.Sprintf("\n[output truncated at %d chars]", maxOutput)
		if savedPath != "" {
			output.Content += fmt.Sprintf("\n[full output saved to %s]", savedPath)
		}
		output.Truncated = true
	}

	if err := outputJSON(output); err != nil {
		return err
	}

	// Signal tool error so main() can exit 1 after defers have run
	if output.IsError {
		return errToolFailed
	}

	return nil
}

// sanitizePathComponent replaces characters unsafe for filenames.
var unsafePathChars = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

func sanitizePathComponent(s string) string {
	return unsafePathChars.ReplaceAllString(s, "_")
}

// saveFullOutput writes the full output to a temp file and returns its path.
func saveFullOutput(serverName, toolName, content string) string {
	dir := filepath.Join(os.TempDir(), "mcp-results")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return ""
	}
	name := fmt.Sprintf("%d-%s-%s.txt", time.Now().Unix(), sanitizePathComponent(serverName), sanitizePathComponent(toolName))
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return ""
	}
	return path
}

// handleToolResponse converts a JSON-RPC response into a callOutput.
func handleToolResponse(resp jsonrpcResponse) (callOutput, error) {
	if resp.Error != nil {
		return callOutput{Content: resp.Error.Message, IsError: true}, nil
	}

	var result toolCallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return callOutput{}, fmt.Errorf("unmarshal tool result: %w", err)
	}

	return renderToolCallResult(result), nil
}

// renderToolCallResult converts a toolCallResult into a callOutput.
func renderToolCallResult(result toolCallResult) callOutput {
	var parts []string
	for _, block := range result.Content {
		switch block.Type {
		case "text":
			parts = append(parts, block.Text)
		case "image":
			parts = append(parts, fmt.Sprintf("[image: %s]", block.MimeType))
		default:
			data, _ := json.Marshal(block)
			parts = append(parts, string(data))
		}
	}
	return callOutput{
		Content: strings.Join(parts, "\n"),
		IsError: result.IsError,
	}
}

// executeToolCall sends a tools/call request and returns the output.
func executeToolCall(transport Transport, toolName string, params map[string]interface{}, stream bool) (callOutput, error) {
	req := jsonrpcRequest{
		JSONRPC: jsonrpcVersion,
		ID:      nextID(),
		Method:  "tools/call",
		Params: toolCallParams{
			Name:      toolName,
			Arguments: params,
		},
	}

	var resp jsonrpcResponse
	var err error

	if stream {
		resp, err = transport.SendStreaming(req, func(evt streamEvent) {
			data, _ := json.Marshal(evt)
			fmt.Fprintln(os.Stdout, string(data))
		})
	} else {
		resp, err = transport.Send(req)
	}

	if err != nil {
		return callOutput{}, fmt.Errorf("call tool: %w", err)
	}

	return handleToolResponse(resp)
}
