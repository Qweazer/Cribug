package api

import (
	"encoding/json"
	"log"
	"net/http"

	"cribug/internal/activities"
	"cribug/internal/db"
	"cribug/internal/types"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type MCPHandler struct {
	db *db.Postgres
}

func NewMCPHandler(database *db.Postgres) *MCPHandler {
	return &MCPHandler{db: database}
}

// POST /api/v1/mcp/servers
func (h *MCPHandler) registerServer(w http.ResponseWriter, r *http.Request) {
	var input types.MCPRegisterServerInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), types.ErrorTypeValidation)
		return
	}

	if input.Name == "" {
		WriteError(w, http.StatusBadRequest, "name is required", types.MCPErrorTypeInvalidInput)
		return
	}

	ctx := r.Context()

	// Check for duplicate name
	existing, err := h.db.GetMCPServerByName(ctx, input.Name)
	if err != nil {
		log.Printf("[ERROR] check duplicate mcp_server: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to check server name", types.ErrorTypeDB)
		return
	}
	if existing != nil {
		WriteError(w, http.StatusConflict,
			"MCP server with name '"+input.Name+"' already exists",
			types.MCPErrorTypeRegistrationFailed)
		return
	}

	srv, err := h.db.CreateMCPServer(ctx, input)
	if err != nil {
		log.Printf("[ERROR] create mcp_server: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to create MCP server", types.ErrorTypeDB)
		return
	}

	// Auto-discover tools on registration
	client := &MockMCPHandlerClient{}
	tools, discErr := client.DiscoverTools(*srv)
	if discErr != nil {
		log.Printf("[WARN] auto-discover tools for server %s: %v", srv.Name, discErr)
	} else if len(tools) > 0 {
		for i := range tools {
			tools[i].ID = uuid.New().String()
		}
		if _, saveErr := h.db.SaveMCPTools(ctx, srv.ID, tools); saveErr != nil {
			log.Printf("[WARN] save discovered tools for server %s: %v", srv.Name, saveErr)
		}
	}

	WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"server": srv,
	})
}

// GET /api/v1/mcp/servers
func (h *MCPHandler) listServers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	servers, err := h.db.ListMCPServers(ctx)
	if err != nil {
		log.Printf("[ERROR] list mcp_servers: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to list MCP servers", types.ErrorTypeDB)
		return
	}
	if servers == nil {
		servers = []types.MCPServer{}
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"servers": servers,
	})
}

// GET /api/v1/mcp/servers/{server_id}
func (h *MCPHandler) getServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "server_id")
	if !isValidUUID(serverID) {
		WriteError(w, http.StatusNotFound, "MCP server not found", types.MCPErrorTypeServerNotFound)
		return
	}
	ctx := r.Context()

	srv, err := h.db.GetMCPServerByID(ctx, serverID)
	if err != nil {
		log.Printf("[ERROR] get mcp_server: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to get MCP server", types.ErrorTypeDB)
		return
	}
	if srv == nil {
		WriteError(w, http.StatusNotFound, "MCP server not found", types.MCPErrorTypeServerNotFound)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"server": srv,
	})
}

// GET /api/v1/mcp/servers/{server_id}/tools
func (h *MCPHandler) listTools(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "server_id")
	if !isValidUUID(serverID) {
		WriteError(w, http.StatusNotFound, "MCP server not found", types.MCPErrorTypeServerNotFound)
		return
	}
	ctx := r.Context()

	srv, err := h.db.GetMCPServerByID(ctx, serverID)
	if err != nil {
		log.Printf("[ERROR] get mcp_server: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to get MCP server", types.ErrorTypeDB)
		return
	}
	if srv == nil {
		WriteError(w, http.StatusNotFound, "MCP server not found", types.MCPErrorTypeServerNotFound)
		return
	}

	tools, err := h.db.GetMCPToolsByServerID(ctx, serverID)
	if err != nil {
		log.Printf("[ERROR] list mcp_tools: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to list tools", types.ErrorTypeDB)
		return
	}
	if tools == nil {
		tools = []types.MCPTool{}
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"server_id": serverID,
		"tools":     tools,
	})
}

// POST /api/v1/mcp/tools/{tool_id}/call
func (h *MCPHandler) callTool(w http.ResponseWriter, r *http.Request) {
	toolID := chi.URLParam(r, "tool_id")

	var body struct {
		Arguments  map[string]interface{} `json:"arguments"`
		Timeout    int                    `json:"timeout"`
		AgentID    string                 `json:"agent_id"`
		ServerID   string                 `json:"server_id"`
		WorkflowID string                 `json:"workflow_id"`
		RunID      string                 `json:"run_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), types.ErrorTypeValidation)
		return
	}

	if body.ServerID == "" {
		WriteError(w, http.StatusBadRequest, "server_id is required", types.MCPErrorTypeInvalidInput)
		return
	}

	ctx := r.Context()

	tool, err := h.db.GetMCPToolByID(ctx, toolID)
	if err != nil {
		log.Printf("[ERROR] get mcp_tool: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to get tool", types.ErrorTypeDB)
		return
	}
	if tool == nil {
		WriteError(w, http.StatusNotFound, "MCP tool not found", types.MCPErrorTypeToolNotFound)
		return
	}

	requestID := uuid.New().String()
	if body.Timeout <= 0 {
		body.Timeout = types.DefaultMCPTimeoutSec
	}

	// Build the input and return it for workflow execution
	// In the API handler, we provide the structured call input
	// The actual execution happens through the Temporal workflow
	callInput := map[string]interface{}{
		"tool_id":    tool.ID,
		"server_id":  body.ServerID,
		"tool_name":  tool.Name,
		"arguments":  body.Arguments,
		"timeout":    body.Timeout,
		"request_id": requestID,
		"agent_id":   body.AgentID,
		"workflow_id": body.WorkflowID,
		"run_id":     body.RunID,
	}

	WriteJSON(w, http.StatusAccepted, map[string]interface{}{
		"tool_call": callInput,
	})
}

// ── Helpers ────────────────────────────────────────────────────

func isValidUUID(id string) bool {
	_, err := uuid.Parse(id)
	return err == nil
}

// MockMCPHandlerClient is a lightweight mock used by the API handler for auto-discovery.
type MockMCPHandlerClient struct{}

func (m *MockMCPHandlerClient) DiscoverTools(server types.MCPServer) ([]types.MCPTool, error) {
	mockClient := &activities.MockMCPClient{}
	return mockClient.DiscoverTools(server)
}
