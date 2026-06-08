package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"cribug/internal/db"
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
	SessionID        string                          `json:"session_id"`
	WorkflowID       string                          `json:"workflow_id"`
	RunID            string                          `json:"run_id"`
	Decision         types.RoutingDecision           `json:"decision"`
	FrontendContract *types.FrontendRouterContract   `json:"frontend_contract,omitempty"`
	Status           string                          `json:"status"`
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
		SessionID:        sessionID,
		WorkflowID:       workflowID,
		RunID:            runID,
		Decision:         result.Decision,
		FrontendContract: BuildFrontendContract(result.Decision, result.Explanation),
		Status:           result.Status,
	})
}

// ─── POST /api/v1/tasks/execute-routed ─────────────────────────────────

// ExecuteRoutedResponse is the unified response shape for both sync and
// async execute-routed. In async mode (HTTP 202), FinalAnswerText is
// empty; the client polls GET /api/v1/tasks/{workflow_id}/result.
type ExecuteRoutedResponse struct {
	SessionID         string                        `json:"session_id"`
	TaskID            string                        `json:"task_id,omitempty"`
	WorkflowID        string                        `json:"workflow_id"`
	RunID             string                        `json:"run_id"`
	Decision          types.RoutingDecision         `json:"decision"`
	FrontendContract  *types.FrontendRouterContract `json:"frontend_contract,omitempty"`
	Status            string                        `json:"status"`
	FinalAnswerText   string                        `json:"final_answer_text,omitempty"`
	FinalAnswerRef    string                        `json:"final_answer_ref,omitempty"`
	PendingApprovalID string                        `json:"pending_approval_id,omitempty"`
	Reason            string                        `json:"reason,omitempty"`
	// Async-only fields
	Async        bool   `json:"async,omitempty"`
	ResultURL    string `json:"result_url,omitempty"`
	StatusURL    string `json:"status_url,omitempty"`
	Message      string `json:"message,omitempty"`
	// LLM metadata (sync mode mirrors what is persisted to the tasks row)
	Provider     string                 `json:"provider,omitempty"`
	ModelUsed    string                 `json:"model_used,omitempty"`
	Mode         string                 `json:"mode,omitempty"`
	Mock         bool                   `json:"mock,omitempty"`
	FallbackUsed bool                   `json:"fallback_used,omitempty"`
	TokensUsed   int                    `json:"tokens_used,omitempty"`
	CostUSD      float64                `json:"cost_usd,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
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

	// Phase 7E.5 async mode: ?async=true or ?wait=false returns 202
	// immediately and lets the client poll for the result.
	q := r.URL.Query()
	async := q.Get("async") == "true" || q.Get("async") == "1" || q.Get("wait") == "false"

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = uuid.New().String()
	}
	// Use workflow_id as the canonical task id so the polling API
	// (GET /api/v1/tasks/{workflow_id}/result) can find the row.
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
		WorkflowExecutionTimeout: 10 * time.Minute, // longer timeout for reflection/real LLM
	}
	wfRun, err := h.temporal.ExecuteWorkflow(ctx, startOpts, "AdvancedRoutingWorkflow", routeReq)
	if err != nil {
		log.Printf("[ERROR] start router workflow: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to start router workflow", types.ErrorTypeWorkflowStart)
		return
	}

	runID := wfRun.GetRunID()

	// Insert a task row in DB so async polling can read status before
	// the workflow completes. The router workflow's
	// PersistRoutedExecutionResultActivity will update this row when it
	// finishes.
	//
	// Note: tasks.id is a UUID column; we generate a fresh UUID for
	// the row's primary key and store the Temporal workflow_id in the
	// separate workflow_id column (UNIQUE). The async polling API
	// looks up by workflow_id.
	taskID := uuid.New().String()
	if _, dbErr := h.db.ExecContext(ctx, `
		INSERT INTO tasks (id, session_id, query, status, workflow_id, run_id, model, max_total_tokens, max_completion_tokens, created_at, updated_at)
		VALUES ($1, $2, $3, 'running', $4, $5, 'gpt-4o-mini', 8000, 1024, NOW(), NOW())
		ON CONFLICT (workflow_id) DO NOTHING
	`, taskID, sessionID, req.Query, workflowID, runID); dbErr != nil {
		log.Printf("[WARN] insert async task row: %v", dbErr)
	}

	if async {
		// Async mode: return 202 immediately. Do not block on
		// wfRun.Get — that is the long-poll that triggered the
		// original "Empty reply" failure on long-running workflows.
		WriteJSON(w, http.StatusAccepted, ExecuteRoutedResponse{
			SessionID:  sessionID,
			TaskID:     taskID,
			WorkflowID: workflowID,
			RunID:      runID,
			Status:     "running",
			Async:      true,
			ResultURL:  "/api/v1/tasks/" + taskID + "/result",
			StatusURL:  "/api/v1/tasks/" + taskID,
			Message:    "workflow started; poll ResultURL for completion",
			Decision:   types.RoutingDecision{RiskLevel: "low", WorkflowType: "AdvancedRoutingWorkflow"},
		})
		return
	}

	// Sync mode: wait for the workflow result. For long-running
	// workflows (Debate/Reflection/ToT/Research v2) this can still
	// suffer the underlying HTTP long-poll problem; clients should
	// prefer async=true.
	var result types.RoutedExecutionResult
	err = wfRun.Get(ctx, &result)
	if err != nil {
		log.Printf("[ERROR] router workflow failed: %v", err)
		WriteError(w, http.StatusInternalServerError, "router workflow failed: "+err.Error(), types.ErrorTypeWorkflow)
		return
	}

	WriteJSON(w, http.StatusOK, ExecuteRoutedResponse{
		SessionID:        sessionID,
		TaskID:           taskID,
		WorkflowID:       workflowID,
		RunID:            runID,
		Decision:         result.Decision,
		FrontendContract: BuildFrontendContract(result.Decision, result.Explanation),
		Status:           result.Status,
		FinalAnswerText:  result.FinalAnswerText,
		FinalAnswerRef:   result.FinalAnswerRef,
		PendingApprovalID: result.PendingApprovalID,
		Reason:           result.Reason,
		Provider:         result.Provider,
		ModelUsed:        result.ModelUsed,
		Mode:             result.Mode,
		Mock:             result.Mock,
		FallbackUsed:     result.FallbackUsed,
		TokensUsed:       result.TokensUsed,
		CostUSD:          result.CostUSD,
		Metadata:         result.Metadata,
	})
}

// ─── GET /api/v1/tasks/{id}/result (Phase 7E.5 async polling) ──────────

// TaskResultResponse is the body of GET /api/v1/tasks/{id}/result.
// HTTP 202 if still running; HTTP 200 with full result if completed;
// HTTP 200 with status="failed" if the workflow failed.
type TaskResultResponse struct {
	TaskID           string                        `json:"task_id"`
	WorkflowID       string                        `json:"workflow_id,omitempty"`
	RunID            string                        `json:"run_id,omitempty"`
	SessionID        string                        `json:"session_id,omitempty"`
	Status           string                        `json:"status"` // "running" | "completed" | "failed"
	Result           *types.RoutedExecutionResult  `json:"result,omitempty"`
	FrontendContract *types.FrontendRouterContract `json:"frontend_contract,omitempty"`
	Error            string                        `json:"error,omitempty"`
	ErrorType        string                        `json:"error_type,omitempty"`
	PolledAt         time.Time                     `json:"polled_at"`
}

// GetTaskResult returns the structured RoutedExecutionResult for a
// routed workflow. Reads from the tasks row that
// PersistRoutedExecutionResultActivity writes at the end of execution.
//
// Behaviour:
//   - row not found → 404
//   - status='running' → 202 with status="running" (poll again)
//   - status='completed' → 200 with full result + metadata
//   - status='failed'   → 200 with status="failed" + error message
func (h *RouteHandler) GetTaskResult(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		WriteError(w, http.StatusBadRequest, "task id is required", types.ErrorTypeValidation)
		return
	}
	ctx := r.Context()
	task, err := h.getTaskByAnyID(ctx, taskID)
	if err != nil {
		log.Printf("[ERROR] get task by id: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to get task", types.ErrorTypeDB)
		return
	}
	if task == nil {
		WriteError(w, http.StatusNotFound, "task not found", types.ErrorTypeValidation)
		return
	}

	resp := TaskResultResponse{
		TaskID:     task.ID,
		WorkflowID: task.WorkflowID,
		PolledAt:   time.Now().UTC(),
	}
	if task.RunID.Valid {
		resp.RunID = task.RunID.String
	}
	if task.SessionID.Valid {
		resp.SessionID = task.SessionID.String
	}

	switch task.Status {
	case "running", "pending":
		resp.Status = "running"
		WriteJSON(w, http.StatusAccepted, resp)
		return
	case "waiting_for_approval":
		resp.Status = "waiting_for_approval"
		// Surface approval details from metadata or from a separate
		// query. The metadata blob written by
		// UpdateTaskApprovalStatusActivity carries the approval_id,
		// approval_url, risk_level, mode, reason.
		if task.Metadata != "" {
			var md map[string]interface{}
			if json.Unmarshal([]byte(task.Metadata), &md) == nil {
				if aid, ok := md["approval_id"].(string); ok {
					resp.Result = &types.RoutedExecutionResult{
						Status:            "waiting_for_approval",
						PendingApprovalID: aid,
						Metadata:          md,
					}
				}
			}
		}
		WriteJSON(w, http.StatusAccepted, resp)
		return
	case "failed":
		resp.Status = "failed"
		if task.Error.Valid {
			resp.Error = task.Error.String
		}
		if task.ErrorType.Valid {
			resp.ErrorType = task.ErrorType.String
		}
		// Try to include structured metadata so the client can see
		// persist_error / debate_* etc. even on failure.
		if task.Metadata != "" {
			var md map[string]interface{}
			if json.Unmarshal([]byte(task.Metadata), &md) == nil {
				resp.Result = &types.RoutedExecutionResult{
					SessionID:  resp.SessionID,
					WorkflowID: resp.WorkflowID,
					RunID:      resp.RunID,
					Status:     "error",
					Metadata:   md,
				}
			}
		}
		WriteJSON(w, http.StatusOK, resp)
		return
	case "completed":
		resp.Status = "completed"
		var result types.RoutedExecutionResult
		result.SessionID = resp.SessionID
		result.WorkflowID = resp.WorkflowID
		result.RunID = resp.RunID
		result.Status = "ok"
		if task.Result.Valid {
			result.FinalAnswerText = task.Result.String
		}
		if task.UsageTotalTokens.Valid {
			result.TokensUsed = int(task.UsageTotalTokens.Int64)
		}
		if task.Model != "" {
			result.ModelUsed = task.Model
		}
		if task.Metadata != "" {
			var md map[string]interface{}
			if json.Unmarshal([]byte(task.Metadata), &md) == nil {
				result.Metadata = md
				// Promote top-level metadata fields into the typed
				// RoutedExecutionResult so clients can read them
				// without re-parsing the metadata JSON.
				if v, ok := md["provider"].(string); ok {
					result.Provider = v
				}
				if v, ok := md["model_used"].(string); ok {
					result.ModelUsed = v
				}
				if v, ok := md["mode"].(string); ok {
					result.Mode = v
				}
				if v, ok := md["mock"].(bool); ok {
					result.Mock = v
				}
				if v, ok := md["fallback_used"].(bool); ok {
					result.FallbackUsed = v
				}
				if v, ok := md["final_answer_ref"].(string); ok {
					result.FinalAnswerRef = v
				}
				if v, ok := md["cost_usd"].(float64); ok {
					result.CostUSD = v
				}
			}
		}
		resp.Result = &result
		// Phase 7I: build a frontend contract from the decision so the
		// client can render the explain panel without re-deriving
		// anything. The full Explanation (candidates / rejected_modes
		// / signals) is in the audit row, not the tasks row, so we
		// only expose the decision-level fields here.
		resp.FrontendContract = BuildFrontendContract(result.Decision, result.Explanation)
		WriteJSON(w, http.StatusOK, resp)
		return
	default:
		// Unknown status — treat as running for safety.
		resp.Status = task.Status
		WriteJSON(w, http.StatusAccepted, resp)
		return
	}
}

// getTaskByAnyID tries to look up a task by id (UUID) or by workflow_id
// (string). This makes the async polling API forgiving: clients may use
// either the workflow_id returned at submit time or the task id (which
// are equal for routed workflows). We split the two lookups because
// `tasks.id` is UUID and `tasks.workflow_id` is VARCHAR — comparing a
// VARCHAR input to a UUID column directly fails with
// "operator does not exist: character varying = uuid".
func (h *RouteHandler) getTaskByAnyID(ctx context.Context, id string) (*types.Task, error) {
	query := `
		SELECT id, session_id, query, status, result, error_type, error,
			   max_total_tokens, max_completion_tokens, model, workflow_id,
			   run_id, usage_prompt_tokens, usage_completion_tokens,
			   usage_total_tokens, created_at, updated_at, metadata
		FROM tasks
		WHERE workflow_id = $1
		LIMIT 1`
	row := h.db.QueryRowContext(ctx, query, id)
	t := &types.Task{}
	var metadata sql.NullString
	if err := row.Scan(
		&t.ID, &t.SessionID, &t.Query, &t.Status, &t.Result, &t.ErrorType, &t.Error,
		&t.MaxTotalTokens, &t.MaxCompletionTokens, &t.Model, &t.WorkflowID, &t.RunID,
		&t.UsagePromptTokens, &t.UsageCompletionTokens, &t.UsageTotalTokens,
		&t.CreatedAt, &t.UpdatedAt, &metadata,
	); err != nil {
		if err != sql.ErrNoRows {
			return nil, err
		}
		// Fall through and try the UUID-column lookup. Guarded by a
		// parse to avoid the type-mismatch error when the id is a
		// workflow_id-shaped string (e.g. "route-exec-...").
	}
	if t.ID == "" {
		// No match on workflow_id. Try id only if it parses as UUID.
		if !looksLikeUUID(id) {
			return nil, nil
		}
		row := h.db.QueryRowContext(ctx, `
			SELECT id, session_id, query, status, result, error_type, error,
				   max_total_tokens, max_completion_tokens, model, workflow_id,
				   run_id, usage_prompt_tokens, usage_completion_tokens,
				   usage_total_tokens, created_at, updated_at, metadata
			FROM tasks WHERE id = $1 LIMIT 1`, id)
		if err := row.Scan(
			&t.ID, &t.SessionID, &t.Query, &t.Status, &t.Result, &t.ErrorType, &t.Error,
			&t.MaxTotalTokens, &t.MaxCompletionTokens, &t.Model, &t.WorkflowID, &t.RunID,
			&t.UsagePromptTokens, &t.UsageCompletionTokens, &t.UsageTotalTokens,
			&t.CreatedAt, &t.UpdatedAt, &metadata,
		); err != nil {
			if err == sql.ErrNoRows {
				return nil, nil
			}
			return nil, err
		}
	}
	if metadata.Valid {
		t.Metadata = metadata.String
	}
	return t, nil
}

// looksLikeUUID is a permissive check: returns true if the string is
// 36 chars and contains dashes at the standard UUID positions. Used to
// avoid a Postgres type error when polling by workflow_id (which is
// not a UUID).
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		}
	}
	return true
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
		// Production default: approval gate enabled. Smoke/test
		// environments can set ROUTER_REQUIRE_APPROVAL=false to
		// disable the gate so async workflows complete without a
		// human signal. This is a test-only override; do NOT set
		// this in production.
		RequireApproval: getEnvBoolDefault("ROUTER_REQUIRE_APPROVAL", true),
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

// ─── POST /api/v1/tasks/{id}/approve (Phase 7F Approval UX) ────────────

// ApproveTask is a convenience endpoint that approves a task waiting
// in the approval gate. It looks up the workflow_id from the tasks
// row, finds the pending approval in the approvals table, validates
// state, and sends the Temporal signal to the waiting workflow.
func (h *RouteHandler) ApproveTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		WriteError(w, http.StatusBadRequest, "task id is required", types.ErrorTypeValidation)
		return
	}
	ctx := r.Context()

	task, err := h.getTaskByAnyID(ctx, taskID)
	if err != nil || task == nil {
		WriteError(w, http.StatusNotFound, "task not found", types.ErrorTypeValidation)
		return
	}

	// Find the pending approval for this workflow.
	var approvalID, currentStatus, apprRunID string
	err = h.db.QueryRowContext(ctx, `
		SELECT approval_id, status, COALESCE(run_id,'') FROM approvals
		WHERE workflow_id = $1 AND status = 'pending'
		ORDER BY requested_at DESC LIMIT 1`,
		task.WorkflowID,
	).Scan(&approvalID, &currentStatus, &apprRunID)
	if err == sql.ErrNoRows {
		WriteError(w, http.StatusNotFound, "no pending approval found for this task", types.ErrorTypeValidation)
		return
	}
	if err != nil {
		log.Printf("[ERROR] query approval for task: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to query approval", types.ErrorTypeDB)
		return
	}

	// Send the approval signal via Temporal.
	runID := apprRunID
	if task.RunID.Valid && task.RunID.String != "" {
		runID = task.RunID.String
	}
	signalName := types.ApprovalSignalName(approvalID)
	signalPayload := types.ApprovalSignalPayload{
		ApprovalID: approvalID,
		WorkflowID: task.WorkflowID,
		RunID:      runID,
		Approved:   true,
		ApprovedBy: "api-task-approve",
	}

	if hErr := h.sendApprovalSignal(ctx, task.WorkflowID, runID, signalName, signalPayload); hErr != nil {
		WriteError(w, http.StatusInternalServerError, hErr.Error(), types.ErrorTypeWorkflow)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"status":      "approved",
		"task_id":     taskID,
		"approval_id": approvalID,
		"workflow_id": task.WorkflowID,
	})
}

// ─── POST /api/v1/tasks/{id}/reject (Phase 7F Approval UX) ─────────────

// RejectTask rejects a task waiting in the approval gate. Same lookup
// as ApproveTask but signals approved=false.
func (h *RouteHandler) RejectTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		WriteError(w, http.StatusBadRequest, "task id is required", types.ErrorTypeValidation)
		return
	}
	ctx := r.Context()

	task, err := h.getTaskByAnyID(ctx, taskID)
	if err != nil || task == nil {
		WriteError(w, http.StatusNotFound, "task not found", types.ErrorTypeValidation)
		return
	}

	var approvalID, apprRunID string
	err = h.db.QueryRowContext(ctx, `
		SELECT approval_id, COALESCE(run_id,'') FROM approvals
		WHERE workflow_id = $1 AND status = 'pending'
		ORDER BY requested_at DESC LIMIT 1`,
		task.WorkflowID,
	).Scan(&approvalID, &apprRunID)
	if err == sql.ErrNoRows {
		WriteError(w, http.StatusNotFound, "no pending approval found for this task", types.ErrorTypeValidation)
		return
	}
	if err != nil {
		log.Printf("[ERROR] query approval for task: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to query approval", types.ErrorTypeDB)
		return
	}

	runID := apprRunID
	if task.RunID.Valid && task.RunID.String != "" {
		runID = task.RunID.String
	}
	signalName := types.ApprovalSignalName(approvalID)
	signalPayload := types.ApprovalSignalPayload{
		ApprovalID: approvalID,
		WorkflowID: task.WorkflowID,
		RunID:      runID,
		Approved:   false,
		ApprovedBy: "api-task-reject",
	}

	if hErr := h.sendApprovalSignal(ctx, task.WorkflowID, runID, signalName, signalPayload); hErr != nil {
		WriteError(w, http.StatusInternalServerError, hErr.Error(), types.ErrorTypeWorkflow)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"status":      "rejected",
		"task_id":     taskID,
		"approval_id": approvalID,
		"workflow_id": task.WorkflowID,
	})
}

// sendApprovalSignal dispatches a Temporal signal to the given workflow.
func (h *RouteHandler) sendApprovalSignal(ctx context.Context, workflowID, runID, signalName string, payload types.ApprovalSignalPayload) error {
	if h.temporal == nil {
		return fmt.Errorf("temporal client not available")
	}
	return h.temporal.SignalWorkflow(ctx, workflowID, runID, signalName, payload)
}

// ─── GET /api/v1/workspace?ref=... (Phase 7G Workspace Store) ──────────

// GetWorkspaceByRef returns a workspace entry by its ref query parameter.
func (h *RouteHandler) GetWorkspaceByRef(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		WriteError(w, http.StatusBadRequest, "ref query param is required", types.ErrorTypeValidation)
		return
	}
	we, err := db.GetWorkspaceEntryByRef(r.Context(), h.db, ref)
	if err != nil {
		log.Printf("[ERROR] get workspace by ref: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to read workspace", types.ErrorTypeDB)
		return
	}
	if we == nil {
		WriteError(w, http.StatusNotFound, "workspace entry not found for ref="+ref, types.ErrorTypeValidation)
		return
	}
	WriteJSON(w, http.StatusOK, we.ToDomain())
}

// ─── GET /api/v1/workspace/{id} (Phase 7G) ─────────────────────────────

// GetWorkspaceByID returns a workspace entry by its DB id.
func (h *RouteHandler) GetWorkspaceByID(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		WriteError(w, http.StatusBadRequest, "id is required", types.ErrorTypeValidation)
		return
	}
	we, err := db.GetWorkspaceEntryByRef(r.Context(), h.db, id)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "failed to read workspace", types.ErrorTypeDB)
		return
	}
	if we == nil {
		WriteError(w, http.StatusNotFound, "workspace entry not found", types.ErrorTypeValidation)
		return
	}
	WriteJSON(w, http.StatusOK, we.ToDomain())
}

// ─── GET /api/v1/tasks/{id}/workspace (Phase 7G) ───────────────────────

// ListTaskWorkspace returns all workspace entries for a task (identified
// by task id or workflow_id).
func (h *RouteHandler) ListTaskWorkspace(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		WriteError(w, http.StatusBadRequest, "task id is required", types.ErrorTypeValidation)
		return
	}
	entries, err := db.ListWorkspaceEntriesByWorkflow(r.Context(), h.db, id)
	if err != nil {
		log.Printf("[ERROR] list workspace: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to list workspace", types.ErrorTypeDB)
		return
	}
	domains := make([]db.WorkspaceEntryDomain, 0, len(entries))
	for _, e := range entries {
		domains = append(domains, e.ToDomain())
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"entries": domains,
		"count":   len(domains),
	})
}

// ─── FrontendRouterContract helper (Phase 7I v2) ──────────────────────

// BuildFrontendContract constructs the API-facing contract from a
// RoutingDecision and (optionally) the audit-side Explanation. Either
// argument may be nil/empty; missing values are rendered as zero values
// (empty strings, empty slices, 0) — never as JSON null — so the
// frontend can consume the response shape unconditionally.
func BuildFrontendContract(decision types.RoutingDecision, explanation *types.RouterDecisionExplanation) *types.FrontendRouterContract {
	contract := &types.FrontendRouterContract{
		SelectedMode:               decision.Mode,
		PlannedMode:                decision.PlannedMode,
		ExecutedMode:               decision.Mode,
		FallbackReason:             decision.FallbackReason,
		ApprovalRequired:           decision.RequiresApproval,
		ApprovalReason:             decision.FallbackReason, // FallbackReason doubles as approval reason when gate fires
		RiskLevel:                  decision.RiskLevel,
		ModelTier:                  decision.ModelTier,
		EstimatedCostUSD:           decision.V2EstimatedCostUSD,
		EstimatedLatencyMs:         decision.V2EstimatedLatencyMs,
		AsyncRequired:              decision.V2AsyncRequired,
		AuditRequired:              decision.V2AuditRequired,
		PolicyVersion:              decision.V2PolicyVersion,
		WorkspaceArtifactsExpected: []string{},
		AddonCapabilities:          []types.Capability{},
		RequiredCapabilities:       []types.Capability{},
		DisabledCapabilities:       []types.Capability{},
		Candidates:                 []types.ModeCandidate{},
		RejectedModes:              []types.RejectedMode{},
		ScoreBreakdown:             map[string]float64{},
		ReasonCodes:                []string{},
	}

	// Convert string addons from Decision (legacy string list) into
	// typed []Capability so the contract is consistent.
	if len(decision.V2AddonCapabilities) > 0 {
		contract.AddonCapabilities = make([]types.Capability, 0, len(decision.V2AddonCapabilities))
		for _, s := range decision.V2AddonCapabilities {
			contract.AddonCapabilities = append(contract.AddonCapabilities, types.Capability(s))
		}
	}
	if len(decision.V2RequiredCapabilities) > 0 {
		contract.RequiredCapabilities = make([]types.Capability, 0, len(decision.V2RequiredCapabilities))
		for _, s := range decision.V2RequiredCapabilities {
			contract.RequiredCapabilities = append(contract.RequiredCapabilities, types.Capability(s))
		}
	}
	if len(decision.V2DisabledCapabilities) > 0 {
		contract.DisabledCapabilities = make([]types.Capability, 0, len(decision.V2DisabledCapabilities))
		for _, s := range decision.V2DisabledCapabilities {
			contract.DisabledCapabilities = append(contract.DisabledCapabilities, types.Capability(s))
		}
	}
	if len(decision.V2WorkspaceArtifactsExpected) > 0 {
		contract.WorkspaceArtifactsExpected = decision.V2WorkspaceArtifactsExpected
	}

	// From the audit-side explanation: signals-derived fields and
	// per-candidate breakdown. When explanation is nil (legacy path),
	// these stay at their zero-value defaults.
	if explanation != nil {
		if len(explanation.WorkspaceArtifactsExpected) > 0 {
			contract.WorkspaceArtifactsExpected = explanation.WorkspaceArtifactsExpected
		}
		if explanation.PolicyVersion != "" {
			contract.PolicyVersion = explanation.PolicyVersion
		}
		if explanation.EstimatedCostUSD > 0 {
			contract.EstimatedCostUSD = explanation.EstimatedCostUSD
		}
		if explanation.EstimatedLatencyMs > 0 {
			contract.EstimatedLatencyMs = explanation.EstimatedLatencyMs
		}
		if explanation.AsyncRequired {
			contract.AsyncRequired = true
		}
		if explanation.AuditRequired {
			contract.AuditRequired = true
		}
		if len(explanation.ScoreBreakdown) > 0 {
			contract.ScoreBreakdown = make(map[string]float64, len(explanation.ScoreBreakdown))
			for k, v := range explanation.ScoreBreakdown {
				contract.ScoreBreakdown[string(k)] = v
			}
		}
		if len(explanation.Candidates) > 0 {
			contract.Candidates = explanation.Candidates
		}
		if len(explanation.RejectedModes) > 0 {
			contract.RejectedModes = explanation.RejectedModes
		}
		contract.SelectedMode = explanation.SelectedMode
		contract.ClassifierUsed = explanation.Signals.ClassifierUsed
		// Human-readable summary
		contract.FrontendExplanation = buildFrontendExplanation(explanation, decision)
		if explanation.SelectedMode != "" {
			contract.ReasonCodes = append(contract.ReasonCodes, "mode_"+string(explanation.SelectedMode))
		}
		for _, m := range explanation.RejectedModes {
			contract.ReasonCodes = append(contract.ReasonCodes, "rejected_"+string(m.Mode)+":"+string(m.Reason))
		}
	}
	if contract.SelectedMode == "" {
		contract.SelectedMode = decision.Mode
	}
	// Always populate FrontendExplanation — callers may pass a nil
	// explanation (e.g. preview / sync execute-routed before the
	// audit row exists). Without this, the field is omitted and
	// the frontend has to special-case its presence.
	if contract.FrontendExplanation == nil {
		contract.FrontendExplanation = buildFrontendExplanation(nil, decision)
	}
	// Score breakdown: prefer the explanation's full map; otherwise
	// surface a minimal {selected: 1.0} so the field is never empty
	// in the JSON (the frontend can always expect an object).
	if len(contract.ScoreBreakdown) == 0 {
		contract.ScoreBreakdown = map[string]float64{string(contract.SelectedMode): 1.0}
	}
	// Phase 7I Fix-3: rejected_modes.reason plumbing. The
	// explanation's Candidates carry a RejectReason per mode. Pull
	// those into the contract so budget / latency / allow-flag
	// rejections are surfaced (previously only forbidden-combination
	// rejections made it through).
	if explanation != nil {
		for _, c := range explanation.Candidates {
			if c.Rejected && c.RejectReason != "" {
				contract.RejectedModes = append(contract.RejectedModes, types.RejectedMode{
					Mode:   c.Mode,
					Reason: c.RejectReason,
				})
			}
		}
	}
	return contract
}

// buildFrontendExplanation synthesises the human-readable explanation
// from the audit-side explanation. Kept in this file so the contract
// stays self-contained in the API layer.
//
// The function is tolerant of nil explanation: when the caller only
// has the decision (e.g. preview mode / sync execute-routed before
// the workflow has finished writing the audit row), it still
// produces a non-nil FrontendExplanation with the data the decision
// carries.
func buildFrontendExplanation(expl *types.RouterDecisionExplanation, decision types.RoutingDecision) *types.FrontendExplanation {
	var (
		why           string
		selectedMode  types.RoutingMode
		caps          []types.Capability
		costUSD       float64
		latencyMs     int
	)
	if expl != nil {
		why = expl.SelectedReason
		selectedMode = expl.SelectedMode
		caps = expl.AddonCapabilities
		costUSD = expl.EstimatedCostUSD
		latencyMs = expl.EstimatedLatencyMs
	}
	if why == "" {
		why = decision.Reason
	}
	if why == "" {
		why = "heuristic policy routing"
	}
	if selectedMode == "" {
		selectedMode = decision.Mode
	}
	if caps == nil {
		caps = make([]types.Capability, 0, len(decision.V2AddonCapabilities))
		for _, s := range decision.V2AddonCapabilities {
			caps = append(caps, types.Capability(s))
		}
	}
	if costUSD == 0 {
		costUSD = decision.V2EstimatedCostUSD
	}
	if latencyMs == 0 {
		latencyMs = decision.V2EstimatedLatencyMs
	}
	capabilitySummary := make([]string, 0, len(caps))
	for _, c := range caps {
		capabilitySummary = append(capabilitySummary, capabilityHuman(c))
	}
	costEstimate := fmt.Sprintf("约 $%.3f，预计 %d ms", costUSD, latencyMs)
	return &types.FrontendExplanation{
		SelectedModeHuman: modeHuman(selectedMode),
		Why:               why,
		CapabilitySummary: capabilitySummary,
		CostEstimate:      costEstimate,
		ApprovalNeeded:    decision.RequiresApproval,
	}
}

func modeHuman(m types.RoutingMode) string {
	switch m {
	case types.RouteDirectAnswer:
		return "直接回答"
	case types.RouteRAGAnswer:
		return "知识库检索"
	case types.RouteReActTool:
		return "工具调用"
	case types.RouteDAGWorkflow:
		return "DAG 多步工作流"
	case types.RouteReflection:
		return "反思改进"
	case types.RouteTreeOfThoughts:
		return "多路径探索"
	case types.RouteDebate:
		return "正反观点对比"
	case types.RouteResearchV2:
		return "研究综合"
	case types.RouteSwarmWorkflow:
		return "多 Agent 协作"
	case types.RouteSandboxExecution:
		return "沙箱代码执行"
	default:
		return string(m)
	}
}

func capabilityHuman(c types.Capability) string {
	switch c {
	case types.CapRAG:
		return "本地知识库检索"
	case types.CapSandbox:
		return "沙箱执行"
	case types.CapSkills:
		return "预设技能"
	case types.CapMCPTools:
		return "MCP 工具调用"
	case types.CapCitations:
		return "引用支持"
	case types.CapWebSearch:
		return "联网搜索"
	case types.CapWorkspace:
		return "Workspace 存储"
	case types.CapAudit:
		return "审计记录"
	case types.CapApproval:
		return "等待人工审批"
	case types.CapReflection:
		return "反思 pass"
	case types.CapDebate:
		return "辩论 pass"
	default:
		return string(c)
	}
}
