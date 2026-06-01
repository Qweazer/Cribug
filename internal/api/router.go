package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"cribug/internal/types"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func NewRouter(h *Handler) *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)

	r.Get("/health", healthHandler(h.db.Ping, h.redis.Ping))
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/tasks", h.createTask)
		r.Get("/tasks/{id}", h.getTask)
		r.Get("/tasks/{id}/dag", h.getDAG)
		r.Get("/stream/sse", h.streamTaskEvents)

		// Phase 6A: MCP Tool Runtime
		mcpH := NewMCPHandler(h.db)
		r.Post("/mcp/servers", mcpH.registerServer)
		r.Get("/mcp/servers", mcpH.listServers)
		r.Get("/mcp/servers/{server_id}", mcpH.getServer)
		r.Get("/mcp/servers/{server_id}/tools", mcpH.listTools)
		r.Post("/mcp/tools/{tool_id}/call", mcpH.callTool)

		// Phase 6B: Sandbox Runtime
		sandboxH := NewSandboxHandler(h.db)
		r.Post("/sandbox/execute", sandboxH.execute)
		r.Get("/sandbox/audit", sandboxH.listAudit)

		// Phase 6C: Skills System
		skillsH := NewSkillsHandler(h.skillClient, h.skillActivities)
		r.Get("/skills", skillsH.listSkills)
		r.Get("/skills/{skill_name}", skillsH.getSkill)
		r.Post("/skills/{skill_name}/execute", skillsH.executeSkill)
		r.Get("/skills/audit", skillsH.getAudit)

		// Phase 6D: Hooks Event System
		hooksH := NewHooksHandler(h.db, h.cfg.HookAllowedInternalHandlers, h.cfg.HookAllowedHTTPHosts)
		r.Post("/hooks", hooksH.register)
		r.Get("/hooks", hooksH.list)
		r.Get("/hooks/{hook_id}", hooksH.get)
		r.Delete("/hooks/{hook_id}", hooksH.delete)
		r.Patch("/hooks/{hook_id}/enable", hooksH.enable)
		r.Patch("/hooks/{hook_id}/disable", hooksH.disable)
		r.Get("/hooks/audit", hooksH.getAudit)

		// Phase 7A: Advanced Strategy Router
		routeH := NewRouteHandler(h.temporal, h.db.Stdlib())
		r.Post("/tasks/route", routeH.Route)
		r.Post("/tasks/execute-routed", routeH.ExecuteRouted)
		r.Get("/tasks/{workflow_id}/routing-decision", routeH.GetRoutingDecision)
		r.Get("/tasks/{workflow_id}/routing-events", routeH.GetRoutingEvents)

		// Phase 7B: HITL / Approval / UI Control
		approvalH := NewApprovalHandler(h.temporal, h.db.Stdlib())
		r.Get("/approvals/pending", approvalH.ListPending)
		r.Get("/approvals/{approval_id}", approvalH.GetApproval)
		r.Post("/approvals/{approval_id}/respond", approvalH.Respond)
	})

	return r
}

func WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("[ERROR] encode json: %v", err)
	}
}

func WriteError(w http.ResponseWriter, status int, message, errorType string) {
	WriteJSON(w, status, types.ErrorResponse{
		Error:     message,
		ErrorType: errorType,
	})
}

func healthHandler(dbPing func(ctx context.Context) error, redisPing func(ctx context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		deps := map[string]string{
			"postgres": "connected",
			"redis":    "connected",
			"temporal": "not_checked",
		}
		status := http.StatusOK

		if err := dbPing(ctx); err != nil {
			deps["postgres"] = "disconnected"
			status = http.StatusServiceUnavailable
		}

		if err := redisPing(ctx); err != nil {
			deps["redis"] = "disconnected"
			if status == http.StatusOK {
				status = http.StatusServiceUnavailable
			}
		}

		WriteJSON(w, status, types.HealthResponse{
			Status:       "healthy",
			Dependencies: deps,
		})
	}
}
