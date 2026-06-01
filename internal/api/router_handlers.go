package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"cribug/internal/types"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.temporal.io/sdk/client"
)

// RouteHandler handles Phase 7A Advanced Router API endpoints.
type RouteHandler struct {
	temporal client.Client
	db       *sql.DB
}

// NewRouteHandler creates a new RouteHandler.
func NewRouteHandler(temporalClient client.Client, db *sql.DB) *RouteHandler {
	return &RouteHandler{temporal: temporalClient, db: db}
}

// ─── POST /api/v1/tasks/route (preview only – no dispatch) ────────────

type RouteRequest struct {
	SessionID        string   `json:"session_id"`
	Query            string   `json:"query"`
	UserIntent       string   `json:"user_intent,omitempty"`
	AvailableTools   []string `json:"available_tools,omitempty"`
	BudgetUSD        float64  `json:"budget_usd"`
	MaxLatencyMs     int      `json:"max_latency_ms"`
	RequireCitations bool     `json:"require_citations"`
	AllowTools       bool     `json:"allow_tools"`
	AllowSandbox     bool     `json:"allow_sandbox"`
	AllowResearch    bool     `json:"allow_research"`
	RiskLevelHint    string   `json:"risk_level_hint,omitempty"`
}

type RouteResponse struct {
	SessionID  string                `json:"session_id"`
	WorkflowID string                `json:"workflow_id"`
	RunID      string                `json:"run_id"`
	Decision   types.RoutingDecision `json:"decision"`
	Status     string                `json:"status"`
}

func (h *RouteHandler) Route(w http.ResponseWriter, r *http.Request) {
	var req RouteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid JSON", types.ErrorTypeValidation)
		return
	}
	if req.Query == "" {
		WriteError(w, http.StatusBadRequest, "query is required", types.ErrorTypeValidation)
		return
	}
	if req.BudgetUSD <= 0 {
		req.BudgetUSD = 0.5
	}

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = uuid.New().String()
	}
	workflowID := "route-" + uuid.New().String()

	cfg := RouterConfigFromEnv()

	routeReq := types.RouteRequest{
		SessionID:        sessionID,
		Query:            req.Query,
		UserIntent:       req.UserIntent,
		AvailableTools:   req.AvailableTools,
		BudgetUSD:        req.BudgetUSD,
		MaxLatencyMs:     req.MaxLatencyMs,
		RequireCitations: req.RequireCitations,
		AllowTools:       req.AllowTools,
		AllowSandbox:     req.AllowSandbox,
		AllowResearch:    req.AllowResearch,
		RiskLevelHint:    req.RiskLevelHint,
		RouterConfig:     cfg,
		PreviewOnly:      true, // route preview MUST NOT dispatch downstream
	}

	ctx := r.Context()
	startOpts := client.StartWorkflowOptions{
		TaskQueue:                "orchestrator-task-queue",
		ID:                       workflowID,
		WorkflowExecutionTimeout: 30 * time.Second, // short timeout for preview
	}
	wfRun, err := h.temporal.ExecuteWorkflow(ctx, startOpts, "AdvancedRoutingWorkflow", routeReq)
	if err != nil {
		log.Printf("[ERROR] start router workflow: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to start router workflow", types.ErrorTypeWorkflowStart)
		return
	}

	runID := wfRun.GetRunID()

	// Synchronous wait for preview is acceptable (classify+policy+cost+audit only, no LLM calls)
	var result types.RoutedExecutionResult
	err = wfRun.Get(ctx, &result)
	if err != nil {
		log.Printf("[ERROR] router workflow failed: %v", err)
		WriteError(w, http.StatusInternalServerError, "router workflow failed: "+err.Error(), types.ErrorTypeWorkflow)
		return
	}

	WriteJSON(w, http.StatusOK, RouteResponse{
		SessionID:  sessionID,
		WorkflowID: workflowID,
		RunID:      runID,
		Decision:   result.Decision,
		Status:     result.Status,
	})
}

// ─── POST /api/v1/tasks/execute-routed ─────────────────────────────────

type ExecuteRoutedResponse struct {
	SessionID         string                `json:"session_id"`
	WorkflowID        string                `json:"workflow_id"`
	RunID             string                `json:"run_id"`
	Decision          types.RoutingDecision `json:"decision"`
	Status            string                `json:"status"`
	FinalAnswerText   string                `json:"final_answer_text,omitempty"`
	PendingApprovalID string                `json:"pending_approval_id,omitempty"`
	Reason            string                `json:"reason,omitempty"`
}

func (h *RouteHandler) ExecuteRouted(w http.ResponseWriter, r *http.Request) {
	var req RouteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid JSON", types.ErrorTypeValidation)
		return
	}
	if req.Query == "" {
		WriteError(w, http.StatusBadRequest, "query is required", types.ErrorTypeValidation)
		return
	}
	if req.BudgetUSD <= 0 {
		req.BudgetUSD = 0.5
	}

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = uuid.New().String()
	}
	workflowID := "route-exec-" + uuid.New().String()

	cfg := RouterConfigFromEnv()

	routeReq := types.RouteRequest{
		SessionID:        sessionID,
		Query:            req.Query,
		UserIntent:       req.UserIntent,
		AvailableTools:   req.AvailableTools,
		BudgetUSD:        req.BudgetUSD,
		MaxLatencyMs:     req.MaxLatencyMs,
		RequireCitations: req.RequireCitations,
		AllowTools:       req.AllowTools,
		AllowSandbox:     req.AllowSandbox,
		AllowResearch:    req.AllowResearch,
		RiskLevelHint:    req.RiskLevelHint,
		RouterConfig:     cfg,
		PreviewOnly:      false, // execute-routed WILL dispatch downstream
	}

	ctx := r.Context()
	startOpts := client.StartWorkflowOptions{
		TaskQueue:                "orchestrator-task-queue",
		ID:                       workflowID,
		WorkflowExecutionTimeout: 5 * time.Minute, // longer timeout for execution
	}
	wfRun, err := h.temporal.ExecuteWorkflow(ctx, startOpts, "AdvancedRoutingWorkflow", routeReq)
	if err != nil {
		log.Printf("[ERROR] start router workflow: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to start router workflow", types.ErrorTypeWorkflowStart)
		return
	}

	runID := wfRun.GetRunID()

	// NOTE: MVP synchronous wait — acknowledged tech debt.
	// Future: return workflow_id immediately and let caller poll / SSE.
	// Timeout is bounded by WorkflowExecutionTimeout above.
	var result types.RoutedExecutionResult
	err = wfRun.Get(ctx, &result)
	if err != nil {
		log.Printf("[ERROR] router workflow failed: %v", err)
		WriteError(w, http.StatusInternalServerError, "router workflow failed: "+err.Error(), types.ErrorTypeWorkflow)
		return
	}

	WriteJSON(w, http.StatusOK, ExecuteRoutedResponse{
		SessionID:         sessionID,
		WorkflowID:        workflowID,
		RunID:             runID,
		Decision:          result.Decision,
		Status:            result.Status,
		FinalAnswerText:   result.FinalAnswerText,
		PendingApprovalID: result.PendingApprovalID,
		Reason:            result.Reason,
	})
}

// ─── GET /api/v1/tasks/{workflow_id}/routing-decision ──────────────────

type RoutingDecisionResponse struct {
	WorkflowID         string  `json:"workflow_id"`
	PlannedMode        string  `json:"planned_mode"`
	Mode               string  `json:"mode"`
	FallbackReason     string  `json:"fallback_reason,omitempty"`
	ComplexityScore    float64 `json:"complexity_score"`
	RiskLevel          string  `json:"risk_level"`
	RequiresApproval   bool    `json:"requires_approval"`
	RequiresRAG        bool    `json:"requires_rag"`
	RequiresTools      bool    `json:"requires_tools"`
	RequiresSandbox    bool    `json:"requires_sandbox"`
	RequiresReflection bool    `json:"requires_reflection"`
	RequiresDebate     bool    `json:"requires_debate"`
	RequiresToT        bool    `json:"requires_tot"`
	RequiresResearchV2 bool    `json:"requires_research_v2"`
	PolicyTraceRef     string  `json:"policy_trace_ref,omitempty"`
	CreatedAt          string  `json:"created_at"`
}

func (h *RouteHandler) GetRoutingDecision(w http.ResponseWriter, r *http.Request) {
	workflowID := chi.URLParam(r, "workflow_id")
	if workflowID == "" {
		WriteError(w, http.StatusBadRequest, "workflow_id is required", types.ErrorTypeValidation)
		return
	}

	if h.db == nil {
		WriteError(w, http.StatusServiceUnavailable, "database not available", types.ErrorTypeDB)
		return
	}

	var resp RoutingDecisionResponse
	err := h.db.QueryRowContext(r.Context(), `
		SELECT workflow_id, planned_mode, mode, COALESCE(fallback_reason, ''),
		       COALESCE(complexity_score, 0), COALESCE(risk_level, ''),
		       COALESCE(requires_approval, false), COALESCE(requires_rag, false),
		       COALESCE(requires_tools, false), COALESCE(requires_sandbox, false),
		       COALESCE(requires_reflection, false), COALESCE(requires_debate, false),
		       COALESCE(requires_tot, false), COALESCE(requires_research_v2, false),
		       COALESCE(policy_trace_ref, ''), created_at
		FROM routing_audit_logs
		WHERE workflow_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, workflowID).Scan(
		&resp.WorkflowID, &resp.PlannedMode, &resp.Mode, &resp.FallbackReason,
		&resp.ComplexityScore, &resp.RiskLevel,
		&resp.RequiresApproval, &resp.RequiresRAG,
		&resp.RequiresTools, &resp.RequiresSandbox,
		&resp.RequiresReflection, &resp.RequiresDebate,
		&resp.RequiresToT, &resp.RequiresResearchV2,
		&resp.PolicyTraceRef, &resp.CreatedAt,
	)
	if err == sql.ErrNoRows {
		WriteError(w, http.StatusNotFound, fmt.Sprintf("routing decision not found for workflow %s", workflowID), types.ErrorTypeValidation)
		return
	}
	if err != nil {
		log.Printf("[ERROR] query routing decision: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to query routing decision", types.ErrorTypeDB)
		return
	}

	WriteJSON(w, http.StatusOK, resp)
}

// ─── GET /api/v1/tasks/{workflow_id}/routing-events ────────────────────

func (h *RouteHandler) GetRoutingEvents(w http.ResponseWriter, r *http.Request) {
	// Slice 23 MVP: routing events stream not yet wired.
	// Will be backed by Redis SSE event stream when routing event infrastructure matures.
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"events": []string{},
		"note":   "routing events stream not yet wired in Slice 23 MVP (requires Redis SSE event infrastructure)",
	})
}

// RouterConfigFromEnv builds a RouterConfigSnapshot from environment variables.
// This is called by the Gateway before starting the Workflow.
func RouterConfigFromEnv() types.RouterConfigSnapshot {
	return types.RouterConfigSnapshot{
		ClassifierMode:           getEnvDefault("ROUTER_CLASSIFIER_MODE", "heuristic"),
		ComplexityDAGThreshold:   0.45,
		ComplexitySwarmThreshold: 0.65,
		ComplexityToTThreshold:   0.75,
		ResearchV2Threshold:      0.70,
		ApprovalRiskThreshold:    getEnvDefault("ROUTER_APPROVAL_RISK_THRESHOLD", "high"),
		MaxClassificationTokens:  800,
		DecisionAuditEnabled:     getEnvBoolDefault("ROUTER_DECISION_AUDIT_ENABLED", true),
		DefaultMode:              "direct_answer",
		Enabled:                  getEnvBoolDefault("ENABLE_ADVANCED_ROUTER", false),
		EnableReflection:         getEnvBoolDefault("ENABLE_REFLECTION", false),
		EnableToT:                getEnvBoolDefault("ENABLE_TOT", false),
		EnableDebate:             getEnvBoolDefault("ENABLE_DEBATE", false),
		EnableResearchV2:         getEnvBoolDefault("ENABLE_RESEARCH_V2", false),
	}
}

func getEnvDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func getEnvBoolDefault(key string, defaultVal bool) bool {
	if v := os.Getenv(key); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
		return v == "1" || v == "true" || v == "yes"
	}
	return defaultVal
}
