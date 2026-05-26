package activities

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"cribug/internal/types"
)

// ── Stdio MCP Client ───────────────────────────────────────────

// StdioMCPClient implements a minimal JSON-RPC MCP client over stdin/stdout.
type StdioMCPClient struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	stderr  io.ReadCloser
	mu      sync.Mutex
	nextID  int64
	timeout time.Duration
}

// NewStdioMCPClient starts an MCP server process and wraps it as an StdioMCPClient.
func NewStdioMCPClient(command string, args []string, env []string, timeout time.Duration) (*StdioMCPClient, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	cmd := exec.Command(command, args...)
	cmd.Env = append(os.Environ(), env...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start MCP server: %w", err)
	}

	client := &StdioMCPClient{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  bufio.NewReader(stdout),
		stderr:  stderr,
		nextID:  1,
		timeout: timeout,
	}
	return client, nil
}

// Initialize sends the MCP initialize request and waits for the response.
func (c *StdioMCPClient) Initialize() (map[string]interface{}, error) {
	resp, err := c.sendRequest("initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "cribug",
			"version": "0.1.0",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	result, _ := resp["result"].(map[string]interface{})
	if result == nil {
		result = resp
	}
	return result, nil
}

// ListTools sends tools/list and returns the discovered tools.
func (c *StdioMCPClient) ListTools() ([]map[string]interface{}, error) {
	resp, err := c.sendRequest("tools/list", map[string]interface{}{})
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}

	result, _ := resp["result"].(map[string]interface{})
	if result == nil {
		result = resp
	}

	toolsRaw, ok := result["tools"]
	if !ok {
		return nil, fmt.Errorf("tools/list response missing 'tools' field")
	}

	toolsList, ok := toolsRaw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("tools/list: expected array, got %T", toolsRaw)
	}

	tools := make([]map[string]interface{}, 0, len(toolsList))
	for _, item := range toolsList {
		tool, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

// CallTool sends tools/call and returns the result content.
func (c *StdioMCPClient) CallTool(toolName string, arguments map[string]interface{}) (string, error) {
	resp, err := c.sendRequest("tools/call", map[string]interface{}{
		"name":      toolName,
		"arguments": arguments,
	})
	if err != nil {
		return "", fmt.Errorf("call tool %s: %w", toolName, err)
	}

	result, _ := resp["result"].(map[string]interface{})
	if result == nil {
		result = resp
	}

	content, ok := result["content"]
	if !ok {
		return fmt.Sprintf("%v", resp), nil
	}

	switch v := content.(type) {
	case string:
		return v, nil
	case []interface{}:
		// MCP content array: [{type: "text", text: "..."}]
		var result string
		for _, item := range v {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if text, ok := itemMap["text"].(string); ok {
				result += text
			}
		}
		return result, nil
	default:
		b, _ := json.Marshal(v)
		return string(b), nil
	}
}

func (c *StdioMCPClient) sendRequest(method string, params map[string]interface{}) (map[string]interface{}, error) {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	c.mu.Unlock()

	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	reqBytes = append(reqBytes, '\n')

	if _, err := c.stdin.Write(reqBytes); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	// Read response with timeout
	type result struct {
		resp map[string]interface{}
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, readErr := c.stdout.ReadBytes('\n')
		if readErr != nil {
			ch <- result{err: fmt.Errorf("read response: %w", readErr)}
			return
		}
		var resp map[string]interface{}
		if parseErr := json.Unmarshal(line, &resp); parseErr != nil {
			ch <- result{err: fmt.Errorf("parse response: %w", parseErr)}
			return
		}
		ch <- result{resp: resp}
	}()

	select {
	case <-time.After(c.timeout):
		return nil, fmt.Errorf("MCP request timeout after %v", c.timeout)
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		if errObj, ok := r.resp["error"]; ok {
			errMap, _ := errObj.(map[string]interface{})
			errMsg, _ := errMap["message"].(string)
			if errMsg == "" {
				errMsg = fmt.Sprintf("%v", errObj)
			}
			return nil, fmt.Errorf("MCP error: %s", errMsg)
		}
		return r.resp, nil
	}
}

// Close terminates the MCP server process and cleans up resources.
func (c *StdioMCPClient) Close() error {
	c.stdin.Close()
	done := make(chan error, 1)
	go func() {
		done <- c.cmd.Wait()
	}()
	select {
	case <-time.After(5 * time.Second):
		c.cmd.Process.Kill()
		return fmt.Errorf("MCP server did not exit gracefully, killed")
	case err := <-done:
		return err
	}
}

// ── Stdio MCP Client Adapter (implements MCPClient) ────────────

// StdioMCPAdapter wraps StdioMCPClient to satisfy the MCPClient interface
// used by MCPActivities. It lazily creates and initializes the stdio client.
type StdioMCPAdapter struct {
	Command string
	Args    []string
	Env     []string
	Timeout time.Duration
}

func NewStdioMCPAdapter(command string, args, env []string) *StdioMCPAdapter {
	return &StdioMCPAdapter{
		Command: command,
		Args:    args,
		Env:     env,
		Timeout: 30 * time.Second,
	}
}

func (a *StdioMCPAdapter) CallTool(server types.MCPServer, toolName string, arguments map[string]interface{}, timeoutSec int) (string, error) {
	timeout := a.Timeout
	if timeoutSec > 0 {
		timeout = time.Duration(timeoutSec) * time.Second
	}

	client, err := NewStdioMCPClient(a.Command, a.Args, a.Env, timeout)
	if err != nil {
		return "", fmt.Errorf("start MCP server: %w", err)
	}
	defer client.Close()

	if _, err := client.Initialize(); err != nil {
		return "", fmt.Errorf("initialize: %w", err)
	}

	return client.CallTool(toolName, arguments)
}

func (a *StdioMCPAdapter) DiscoverTools(server types.MCPServer) ([]types.MCPTool, error) {
	client, err := NewStdioMCPClient(a.Command, a.Args, a.Env, a.Timeout)
	if err != nil {
		return nil, fmt.Errorf("start MCP server: %w", err)
	}
	defer client.Close()

	if _, err := client.Initialize(); err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}

	tools, err := client.ListTools()
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}

	result := make([]types.MCPTool, 0, len(tools))
	for _, t := range tools {
		tool := types.MCPTool{
			Name:        getStringField(t, "name"),
			Description: getStringField(t, "description"),
		}
		if schema, ok := t["inputSchema"]; ok {
			schemaMap, _ := schema.(map[string]interface{})
			tool.InputSchema = schemaMap
		}
		result = append(result, tool)
	}
	return result, nil
}

func getStringField(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		s, _ := v.(string)
		return s
	}
	return ""
}

// ── Dynamic MCP Client Factory ─────────────────────────────────

// NewMCPClientForServer creates the appropriate MCP client for a server.
// If CRIBUG_REAL_MCP_TEST=1 and the server has a command, uses stdio transport.
// Otherwise falls back to MockMCPClient.
func NewMCPClientForServer(server types.MCPServer) MCPClient {
	if os.Getenv("CRIBUG_REAL_MCP_TEST") == "1" && server.Command != "" {
		return NewStdioMCPAdapter(server.Command, server.Args, server.EnvRaw)
	}
	return &MockMCPClient{}
}
