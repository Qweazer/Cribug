package api

import (
	"database/sql"
	"encoding/json"
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

	if err := h.db.UpdateTaskRunning(ctx, taskID, runID); err != nil {
		log.Printf("[WARN] update task to running: %v", err)
	}

	execID := uuid.New().String()
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

	if taskDetail != nil {
		WriteJSON(w, http.StatusOK, taskDetail)
		return
	}

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

	WriteJSON(w, http.StatusOK, h.toTaskDetailResponse(task))
}

func (h *Handler) toTaskDetailResponse(task *types.Task) *types.TaskDetailResponse {
	resp := &types.TaskDetailResponse{
		TaskID:              task.ID,
		WorkflowID:          task.WorkflowID,
		Status:              task.Status,
		Model:               task.Model,
		MaxTotalTokens:       task.MaxTotalTokens,
		MaxCompletionTokens:  task.MaxCompletionTokens,
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
	if task.UsageTotalTokens.Valid {
		promptTokens := toPtr(int(task.UsagePromptTokens.Int64))
		completionTokens := toPtr(int(task.UsageCompletionTokens.Int64))
		totalTokens := toPtr(int(task.UsageTotalTokens.Int64))
		resp.Usage = &types.TaskUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      totalTokens,
		}
	}

	return resp
}

func toPtr[T any](v T) *T {
	return &v
}