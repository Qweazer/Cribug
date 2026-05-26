package activities

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"cribug/internal/types"
)

// findFakeServer returns the path to the fake MCP stdio server binary.
func findFakeServer(t *testing.T) string {
	t.Helper()
	path := "testdata/mcp/fake_server"
	if _, err := os.Stat(path); err == nil {
		return path
	}
	path = "../testdata/mcp/fake_server"
	if _, err := os.Stat(path); err == nil {
		return path
	}
	path = "../../testdata/mcp/fake_server"
	if _, err := os.Stat(path); err == nil {
		return path
	}
	// Try to build it
	cmd := exec.Command("go", "build", "-o", "/tmp/fake_mcp_server", "testdata/mcp/fake_mcp_server.go")
	if err := cmd.Run(); err != nil {
		cmd = exec.Command("go", "build", "-o", "/tmp/fake_mcp_server", "../testdata/mcp/fake_mcp_server.go")
		if err := cmd.Run(); err != nil {
			cmd = exec.Command("go", "build", "-o", "/tmp/fake_mcp_server", "../../testdata/mcp/fake_mcp_server.go")
			if err := cmd.Run(); err != nil {
				t.Skipf("cannot find or build fake MCP server: %v", err)
			}
		}
	}
	return "/tmp/fake_mcp_server"
}

func TestRealMCPStdio_Initialize(t *testing.T) {
	serverPath := findFakeServer(t)

	client, err := NewStdioMCPClient(serverPath, nil, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("failed to start MCP client: %v", err)
	}
	defer client.Close()

	resp, err := client.Initialize()
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}

	protocol, _ := resp["protocolVersion"].(string)
	if protocol == "" {
		t.Error("expected protocolVersion in initialize response")
	}
	capabilities, ok := resp["capabilities"]
	if !ok {
		t.Error("expected capabilities in initialize response")
	}
	_ = capabilities
}

func TestRealMCPStdio_ListTools(t *testing.T) {
	serverPath := findFakeServer(t)

	client, err := NewStdioMCPClient(serverPath, nil, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("failed to start MCP client: %v", err)
	}
	defer client.Close()

	if _, err := client.Initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	tools, err := client.ListTools()
	if err != nil {
		t.Fatalf("list tools failed: %v", err)
	}

	if len(tools) < 2 {
		t.Errorf("expected at least 2 tools, got %d", len(tools))
	}

	foundNames := make(map[string]bool)
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		foundNames[name] = true
	}
	for _, expected := range []string{"echo", "get_time", "add"} {
		if !foundNames[expected] {
			t.Errorf("expected tool '%s' not found", expected)
		}
	}
}

func TestRealMCPStdio_CallTool(t *testing.T) {
	serverPath := findFakeServer(t)

	client, err := NewStdioMCPClient(serverPath, nil, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("failed to start MCP client: %v", err)
	}
	defer client.Close()

	if _, err := client.Initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	t.Run("echo", func(t *testing.T) {
		result, err := client.CallTool("echo", map[string]interface{}{"message": "hello"})
		if err != nil {
			t.Fatalf("echo call failed: %v", err)
		}
		if result != "echo: hello" {
			t.Errorf("expected 'echo: hello', got '%s'", result)
		}
	})

	t.Run("get_time", func(t *testing.T) {
		result, err := client.CallTool("get_time", map[string]interface{}{})
		if err != nil {
			t.Fatalf("get_time call failed: %v", err)
		}
		if result == "" {
			t.Error("expected non-empty result")
		}
	})

	t.Run("add", func(t *testing.T) {
		result, err := client.CallTool("add", map[string]interface{}{"a": json.Number("3"), "b": json.Number("4")})
		if err != nil {
			t.Fatalf("add call failed: %v", err)
		}
		if result != "3 + 4 = 7" {
			t.Errorf("expected '3 + 4 = 7', got '%s'", result)
		}
	})

	t.Run("unknown tool", func(t *testing.T) {
		_, err := client.CallTool("nonexistent_tool", map[string]interface{}{})
		if err == nil {
			t.Error("expected error for unknown tool")
		}
	})
}

func TestRealMCPStdio_Timeout(t *testing.T) {
	serverPath := findFakeServer(t)

	client, err := NewStdioMCPClient(serverPath, nil, nil, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("failed to start MCP client: %v", err)
	}
	defer client.Close()

	if _, err := client.Initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Should succeed with reasonable timeout
	_, err = client.CallTool("echo", map[string]interface{}{"message": "quick"})
	if err != nil {
		t.Errorf("echo with 100ms timeout failed: %v", err)
	}
}

func TestRealMCPStdio_CloseCleanup(t *testing.T) {
	serverPath := findFakeServer(t)

	client, err := NewStdioMCPClient(serverPath, nil, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("failed to start MCP client: %v", err)
	}

	if _, err := client.Initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	err = client.Close()
	if err != nil {
		t.Errorf("close failed: %v", err)
	}
}

func TestStdioMCPAdapter(t *testing.T) {
	serverPath := findFakeServer(t)

	adapter := NewStdioMCPAdapter(serverPath, nil, nil)
	adapter.Timeout = 5 * time.Second

	srv := types.MCPServer{ID: "test", Name: "fake-server"}

	t.Run("discover", func(t *testing.T) {
		tools, err := adapter.DiscoverTools(srv)
		if err != nil {
			t.Fatalf("discover failed: %v", err)
		}
		if len(tools) < 2 {
			t.Errorf("expected at least 2 tools, got %d", len(tools))
		}
	})

	t.Run("call", func(t *testing.T) {
		result, err := adapter.CallTool(srv, "echo", map[string]interface{}{"message": "pong"}, 10)
		if err != nil {
			t.Fatalf("call failed: %v", err)
		}
		if result == "" {
			t.Error("expected non-empty result")
		}
	})
}
