// Fake MCP stdio server for testing Phase 6A.
// Reads JSON-RPC from stdin, responds on stdout, logs to stderr.
//
// Build: go build -o testdata/mcp/fake_server testdata/mcp/fake_mcp_server.go
// Test:  echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' | ./testdata/mcp/fake_server

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req map[string]interface{}
		if err := json.Unmarshal(line, &req); err != nil {
			fmt.Fprintf(os.Stderr, "[fake-mcp] parse error: %v\n", err)
			continue
		}

		method, _ := req["method"].(string)
		id, _ := req["id"].(float64)

		fmt.Fprintf(os.Stderr, "[fake-mcp] received: %s\n", method)

		var response map[string]interface{}

		switch method {
		case "initialize":
			response = map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities": map[string]interface{}{
						"tools": map[string]interface{}{},
					},
					"serverInfo": map[string]interface{}{
						"name":    "fake-mcp-server",
						"version": "0.1.0",
					},
				},
			}

		case "tools/list":
			response = map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]interface{}{
					"tools": []interface{}{
						map[string]interface{}{
							"name":        "echo",
							"description": "Echo back the input",
							"inputSchema": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"message": map[string]interface{}{"type": "string"},
								},
								"required": []interface{}{"message"},
							},
						},
						map[string]interface{}{
							"name":        "get_time",
							"description": "Get current server time",
							"inputSchema": map[string]interface{}{
								"type":       "object",
								"properties": map[string]interface{}{},
							},
						},
						map[string]interface{}{
							"name":        "add",
							"description": "Add two numbers",
							"inputSchema": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"a": map[string]interface{}{"type": "number"},
									"b": map[string]interface{}{"type": "number"},
								},
								"required": []interface{}{"a", "b"},
							},
						},
					},
				},
			}

		case "tools/call":
			params, _ := req["params"].(map[string]interface{})
			toolName, _ := params["name"].(string)
			arguments, _ := params["arguments"].(map[string]interface{})

			switch toolName {
			case "echo":
				msg, _ := arguments["message"].(string)
				response = toolCallResult(id, fmt.Sprintf("echo: %s", msg))
			case "get_time":
				response = toolCallResult(id, fmt.Sprintf("server time: %s", time.Now().UTC().Format(time.RFC3339)))
			case "add":
				a, _ := toFloat(arguments["a"])
				b, _ := toFloat(arguments["b"])
				response = toolCallResult(id, fmt.Sprintf("%v + %v = %v", a, b, a+b))
			default:
				response = map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      id,
					"error": map[string]interface{}{
						"code":    -32601,
						"message": fmt.Sprintf("unknown tool: %s", toolName),
					},
				}
			}

		default:
			response = map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      id,
				"error": map[string]interface{}{
					"code":    -32601,
					"message": fmt.Sprintf("unknown method: %s", method),
				},
			}
		}

		respBytes, _ := json.Marshal(response)
		fmt.Println(string(respBytes))
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "[fake-mcp] scanner error: %v\n", err)
		os.Exit(1)
	}
}

func toolCallResult(id float64, text string) map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{
					"type": "text",
					"text": text,
				},
			},
		},
	}
}

func toFloat(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case json.Number:
		f, err := val.Float64()
		return f, err == nil
	}
	return 0, false
}
