package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"cribug/internal/activities"
	"cribug/internal/config"
	"cribug/internal/db"
	"cribug/internal/events"
	redisclient "cribug/internal/redis"
	"cribug/internal/types"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.temporal.io/sdk/client"
)

type Handler struct {
	db      *db.Postgres
	redis   *redisclient.Client
	temporal client.Client
	cfg     *config.Config
}

func NewHandler(database *db.Postgres, redisClient *redisclient.Client, temporalClient client.Client, cfg *config.Config) *Handler {
	return &Handler{
		db:      database,
		redis:   redisClient,
		temporal: temporalClient,
		cfg:     cfg,
	}
}

func (h *Handler) createTask(w http.ResponseWriter, r *http.Request) {
	var req types.CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid JSON", types.ErrorTypeValidation)
		return
	}

	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		WriteError(w, http.StatusBadRequest, "query is required", types.ErrorTypeValidation)
		return
	}

	// Validate mode and feature flags
	workflowMode, err := h.validateTaskMode(req.Config)
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error(), types.ErrorTypeValidation)
		return
	}

	taskID := uuid.New().String()
	workflowID := "task-" + taskID

	maxTotalTokens, maxCompletionTokens, model, temperature := types.NormalizeConfig(req.Config)
	maxParallelAgents, enableReAct, reactMaxIterations := h.extractConcurrencyConfig(req.Config)

	task := &types.Task{
		ID:                 taskID,
		Query:              req.Query,
		Status:             types.TaskStatusPending,
		WorkflowID:         workflowID,
		Model:              model,
		MaxTotalTokens:      maxTotalTokens,
		MaxCompletionTokens: maxCompletionTokens,
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
	}

	if req.SessionID != "" {
		task.SessionID = sql.NullString{String: req.SessionID, Valid: true}
	}

	ctx := r.Context()
	if err := h.db.CreateTask(ctx, task); err != nil {
		log.Printf("[ERROR] create task in db: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to create task", types.ErrorTypeDB)
		return
	}

	h.redis.SetTaskStatus(ctx, task)

	workflowReq := types.WorkflowTaskRequest{
		TaskID:              taskID,
		Query:               req.Query,
		SessionID:           req.SessionID,
		Model:               model,
		Temperature:         temperature,
		MaxTotalTokens:      maxTotalTokens,
		MaxCompletionTokens: maxCompletionTokens,
		WorkflowID:          workflowID,
		RunID:               "",
		Config:              req.Config,
		MaxParallelAgents:   maxParallelAgents,
		EnableReAct:         enableReAct,
		ReActMaxIterations:  reactMaxIterations,
			TestFailNodeID:      getTestFailNodeID(req.Config),
	}

	if h.temporal == nil {
		log.Printf("[ERROR] temporal client is not connected")
		h.db.UpdateTaskError(ctx, taskID, types.ErrorTypeWorkflowStart, "temporal client not connected")
		h.redis.UpdateTaskError(ctx, taskID, types.ErrorTypeWorkflowStart, "temporal client not connected")
		WriteError(w, http.StatusServiceUnavailable, "temporal service unavailable", types.ErrorTypeWorkflowStart)
		return
	}

	// Determine workflow name based on mode
	workflowName := "SimpleWorkflow"
	if workflowMode == types.WorkflowModeDAG {
		workflowName = "DAGWorkflow"
	} else if workflowMode == types.WorkflowModeMultiAgent {
		workflowName = "MultiAgentWorkflow"
	}

	startOpts := client.StartWorkflowOptions{
		TaskQueue: "orchestrator-task-queue",
		ID:        workflowID,
	}

	wfRun, err := h.temporal.ExecuteWorkflow(ctx, startOpts, workflowName, workflowReq)
	if err != nil {
		log.Printf("[ERROR] start workflow: %v", err)
		h.db.UpdateTaskError(ctx, taskID, types.ErrorTypeWorkflowStart, err.Error())
		h.redis.UpdateTaskError(ctx, taskID, types.ErrorTypeWorkflowStart, err.Error())
		WriteError(w, http.StatusInternalServerError, "failed to start workflow", types.ErrorTypeWorkflowStart)
		return
	}

	runID := wfRun.GetRunID()
	workflowReq.RunID = runID

	if err := h.db.UpdateTaskRunning(ctx, taskID, runID); err != nil {
		log.Printf("[WARN] update task to running: %v", err)
	}

	execID := uuid.New().String()
	log.Printf("[DEBUG] creating execution: id=%s task_id=%s workflow_id=%s run_id=%s", execID, taskID, workflowID, runID)
	if err := activities.CreateExecution(h.db.Stdlib(), execID, taskID, workflowID, runID); err != nil {
		log.Printf("[WARN] create execution: %v", err)
	}

	h.redis.UpdateTaskRunning(ctx, taskID, runID)

	event := events.NewTaskCreatedEvent(taskID, workflowID, req.Query)
	h.redis.AppendTaskEvent(ctx, taskID, event)

	log.Printf("[INFO] workflow started: task=%s workflow=%s run=%s", taskID, workflowID, runID)

	WriteJSON(w, http.StatusAccepted, types.CreateTaskResponse{
		TaskID:     taskID,
		WorkflowID: workflowID,
		RunID:      &runID,
		Status:     types.TaskStatusRunning,
		StreamURL:  "/api/v1/stream/sse?task_id=" + taskID,
	})
}

// validateTaskMode validates the mode and feature flags, returns the validated mode
func (h *Handler) validateTaskMode(cfg *types.TaskConfig) (string, error) {
	// Default mode is "simple"
	mode := types.WorkflowModeSimple

	if cfg != nil && cfg.Mode != nil {
		mode = *cfg.Mode
	}

	// Validate mode value
	switch mode {
	case types.WorkflowModeSimple:
		// OK
	case types.WorkflowModeDAG:
		if !h.cfg.EnableDAGWorkflow {
			return "", fmt.Errorf("mode 'dag' is not enabled: set ENABLE_DAG_WORKFLOW=true to enable")
		}
	case types.WorkflowModeMultiAgent:
		if !h.cfg.EnableMultiAgent {
			return "", fmt.Errorf("mode 'multi_agent' is not enabled: set ENABLE_MULTI_AGENT=true to enable")
		}
	default:
		return "", fmt.Errorf("invalid mode '%s': must be 'simple', 'dag', or 'multi_agent'", mode)
	}

	// Validate enable_tools
	if cfg != nil && cfg.EnableTools != nil && *cfg.EnableTools {
		if !h.cfg.EnableTools {
			return "", fmt.Errorf("enable_tools=true is not enabled: set ENABLE_TOOLS=true to enable")
		}
	}

	return mode, nil
}

// extractConcurrencyConfig extracts DAG concurrency and ReAct config from TaskConfig
func (h *Handler) extractConcurrencyConfig(cfg *types.TaskConfig) (maxParallelAgents int, enableReAct bool, reactMaxIterations int) {
	maxParallelAgents = 1 // default: sequential
	enableReAct = false
	reactMaxIterations = 3

	if cfg == nil {
		return
	}

	if cfg.MaxParallelAgents != nil {
		maxParallelAgents = *cfg.MaxParallelAgents
	}
	if cfg.EnableReAct != nil {
		enableReAct = *cfg.EnableReAct
	}
	if cfg.ReActMaxIterations != nil {
		reactMaxIterations = *cfg.ReActMaxIterations
		if reactMaxIterations <= 0 {
			reactMaxIterations = 3
		}
		if reactMaxIterations > 10 {
			reactMaxIterations = 10
		}
	}

	return
}

func getTestFailNodeID(cfg *types.TaskConfig) string {
	if cfg != nil && cfg.TestFailNodeID != nil {
		return *cfg.TestFailNodeID
	}
	return ""
}

func (h *Handler) getTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		WriteError(w, http.StatusBadRequest, "task id is required", types.ErrorTypeValidation)
		return
	}

	ctx := r.Context()

	taskDetail, err := h.redis.GetTaskStatus(ctx, taskID)
	if err != nil {
		log.Printf("[WARN] get task from redis: %v", err)
	}

	// Always fetch from DB for usage fields
	task, err := h.db.GetTaskByID(ctx, taskID)
	if err != nil {
		log.Printf("[ERROR] get task from db: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to get task", types.ErrorTypeDB)
		return
	}

	if task == nil {
		WriteError(w, http.StatusNotFound, "task not found", types.ErrorTypeValidation)
		return
	}

	// Use DB task as base, but enrich with Redis data if available
	resp := h.toTaskDetailResponse(task)

	if taskDetail != nil {
		// Only update non-usage fields from Redis (status, result, etc.)
		if taskDetail.Status != "" {
			resp.Status = taskDetail.Status
		}
		if taskDetail.Result != nil {
			resp.Result = taskDetail.Result
		}
		if taskDetail.Error != nil {
			resp.Error = taskDetail.Error
		}
		if taskDetail.ErrorType != nil {
			resp.ErrorType = taskDetail.ErrorType
		}
	}

	WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) getDAG(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		WriteError(w, http.StatusBadRequest, "task id is required", types.ErrorTypeValidation)
		return
	}

	ctx := r.Context()

	// Get workflow ID from Redis task status
	taskStatus, _ := h.redis.GetTaskStatus(ctx, taskID)
	workflowID := ""
	if taskStatus != nil {
		workflowID = taskStatus.WorkflowID
	}
	if workflowID == "" {
		workflowID = "task-" + taskID
	}

	// Read DAG node statuses from Redis
	// Keys follow pattern: dag:{workflow_id}:nodes:status or dag:{task_id}:nodes:status
	statusKey := "dag:" + workflowID + ":nodes:status"
	nodes, _ := h.redis.HGetAll(ctx, statusKey)
	if len(nodes) == 0 {
		statusKey = "dag:" + taskID + ":nodes:status"
		nodes, _ = h.redis.HGetAll(ctx, statusKey)
	}

	// Read DAG meta
	metaKey := "dag:" + workflowID + ":meta"
	meta, _ := h.redis.HGetAll(ctx, metaKey)

	// Read node details (dependencies, layer etc.)
	nodesDetailKey := "dag:" + workflowID + ":nodes"
	nodesDetail, _ := h.redis.HGetAll(ctx, nodesDetailKey)

	// Build response
	dagResponse := map[string]interface{}{
		"task_id":     taskID,
		"workflow_id": workflowID,
		"meta":         meta,
		"nodes_status": nodes,
		"nodes_detail": nodesDetail,
	}

	WriteJSON(w, http.StatusOK, dagResponse)
}

func (h *Handler) toTaskDetailResponse(task *types.Task) *types.TaskDetailResponse {
	resp := &types.TaskDetailResponse{
		TaskID:              task.ID,
		WorkflowID:          task.WorkflowID,
		Status:              task.Status,
		Model:               task.Model,
		MaxTotalTokens:      task.MaxTotalTokens,
		MaxCompletionTokens: task.MaxCompletionTokens,
		CreatedAt:           task.CreatedAt,
		UpdatedAt:           task.UpdatedAt,
	}

	if task.RunID.Valid {
		resp.RunID = &task.RunID.String
	}
	if task.SessionID.Valid {
		resp.SessionID = &task.SessionID.String
	}
	if task.Result.Valid {
		resp.Result = &task.Result.String
	}
	if task.Error.Valid {
		resp.Error = &task.Error.String
	}
	if task.ErrorType.Valid {
		resp.ErrorType = &task.ErrorType.String
	}

	// Always include usage if we have total_tokens
	if task.UsageTotalTokens.Valid && task.UsageTotalTokens.Int64 > 0 {
		promptVal := int(task.UsagePromptTokens.Int64)
		completionVal := int(task.UsageCompletionTokens.Int64)
		totalVal := int(task.UsageTotalTokens.Int64)
		resp.Usage = &types.TaskUsage{
			PromptTokens:     &promptVal,
			CompletionTokens: &completionVal,
			TotalTokens:      &totalVal,
		}
	}

	return resp
}

func toPtr[T any](v T) *T {
	return &v
}

func (h *Handler) streamTaskEvents(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("task_id")
	if taskID == "" {
		WriteError(w, http.StatusBadRequest, "task_id is required", types.ErrorTypeValidation)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, http.StatusInternalServerError, "streaming unsupported", types.ErrorTypeUnknown)
		return
	}

	ctx := r.Context()

	events, err := h.redis.ReadTaskEvents(ctx, taskID)
	if err != nil {
		log.Printf("[ERROR] read existing task events failed: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to read task events", types.ErrorTypeRedis)
		return
	}

	lastID := "0"
	terminal := false

	for _, e := range events {
		lastID = e.ID
		writeSSE(w, e.EventType, e.Payload)
		if isTerminalEvent(e.EventType) {
			terminal = true
		}
	}
	flusher.Flush()

	if terminal {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		newEvents, err := h.redis.ReadTaskEventsBlocking(ctx, taskID, lastID, 30*time.Second)
		if err != nil {
			log.Printf("[ERROR] blocking read task events failed: %v", err)
			writeSSE(w, "ERROR", `{"error":"redis stream read failed"}`)
			flusher.Flush()
			return
		}

		if len(newEvents) == 0 {
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
			continue
		}

		for _, e := range newEvents {
			lastID = e.ID
			writeSSE(w, e.EventType, e.Payload)
			flusher.Flush()

			if isTerminalEvent(e.EventType) {
				return
			}
		}
	}
}

func writeSSE(w http.ResponseWriter, eventType string, payload string) {
	fmt.Fprintf(w, "event: %s\n", eventType)
	fmt.Fprintf(w, "data: %s\n\n", payload)
}

func isTerminalEvent(eventType string) bool {
	switch eventType {
	case events.EventTypeTaskCompleted, events.EventTypeTaskFailed, events.EventTypeTaskBudgetExceeded:
		return true
	default:
		return false
	}
}