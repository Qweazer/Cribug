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
}

func NewHandler(database *db.Postgres, redisClient *redisclient.Client, temporalClient client.Client) *Handler {
	return &Handler{
		db:      database,
		redis:   redisClient,
		temporal: temporalClient,
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

	taskID := uuid.New().String()
	workflowID := "task-" + taskID

	maxTotalTokens, maxCompletionTokens, model, temperature := types.NormalizeConfig(req.Config)

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
	}

	if h.temporal == nil {
		log.Printf("[ERROR] temporal client is not connected")
		h.db.UpdateTaskError(ctx, taskID, types.ErrorTypeWorkflowStart, "temporal client not connected")
		h.redis.UpdateTaskError(ctx, taskID, types.ErrorTypeWorkflowStart, "temporal client not connected")
		WriteError(w, http.StatusServiceUnavailable, "temporal service unavailable", types.ErrorTypeWorkflowStart)
		return
	}

	startOpts := client.StartWorkflowOptions{
		TaskQueue: "orchestrator-task-queue",
		ID:        workflowID,
	}

	wfRun, err := h.temporal.ExecuteWorkflow(ctx, startOpts, "SimpleWorkflow", workflowReq)
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