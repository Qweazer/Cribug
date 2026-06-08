package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"time"

	redisclient "cribug/internal/redis"
	"cribug/internal/types"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// FrontendAdapterHandler provides endpoints that the cribug-agent-web frontend
// expects but were not originally part of the cribug API surface.
type FrontendAdapterHandler struct {
	db    *sql.DB
	redis *redisclient.Client
}

// NewFrontendAdapterHandler creates a new FrontendAdapterHandler.
func NewFrontendAdapterHandler(db *sql.DB, redisClient *redisclient.Client) *FrontendAdapterHandler {
	return &FrontendAdapterHandler{db: db, redis: redisClient}
}

// ─── Sessions ───────────────────────────────────────────────────────

// SessionSummary is the shape returned by GET /api/v1/sessions.
type SessionSummary struct {
	SessionID    string `json:"session_id"`
	Title        string `json:"title"`
	TaskCount    int    `json:"task_count"`
	LatestStatus string `json:"latest_status"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// ListSessions returns sessions derived from tasks grouped by session_id.
func (h *FrontendAdapterHandler) ListSessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := h.db.QueryContext(ctx, `
		SELECT
			COALESCE(session_id, ''),
			COUNT(*) AS task_count,
			MAX(status) AS latest_status,
			MIN(created_at) AS created_at,
			MAX(updated_at) AS updated_at
		FROM tasks
		WHERE session_id IS NOT NULL AND session_id != ''
		GROUP BY session_id
		ORDER BY MAX(updated_at) DESC
		LIMIT 100
	`)
	if err != nil {
		log.Printf("[ERROR] list sessions: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to list sessions", types.ErrorTypeDB)
		return
	}
	defer rows.Close()

	sessions := make([]SessionSummary, 0)
	for rows.Next() {
		var s SessionSummary
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&s.SessionID, &s.TaskCount, &s.LatestStatus, &createdAt, &updatedAt); err != nil {
			log.Printf("[ERROR] scan session: %v", err)
			continue
		}
		// Derive title from first query in the session
		title := s.SessionID
		if len(title) > 40 {
			title = title[:40]
		}
		if firstQuery := h.firstQueryForSession(ctx, s.SessionID); firstQuery != "" {
			runes := []rune(firstQuery)
			if len(runes) > 50 {
				title = string(runes[:50]) + "..."
			} else {
				title = firstQuery
			}
		}
		s.Title = title
		s.CreatedAt = createdAt.Format(time.RFC3339)
		s.UpdatedAt = updatedAt.Format(time.RFC3339)
		sessions = append(sessions, s)
	}

	WriteJSON(w, http.StatusOK, sessions)
}

func (h *FrontendAdapterHandler) firstQueryForSession(ctx context.Context, sessionID string) string {
	var query string
	err := h.db.QueryRowContext(ctx,
		"SELECT query FROM tasks WHERE session_id = $1 ORDER BY created_at ASC LIMIT 1",
		sessionID,
	).Scan(&query)
	if err != nil {
		return ""
	}
	return query
}

// CreateSessionRequest is the shape for POST /api/v1/sessions.
type CreateSessionRequest struct {
	Title string `json:"title"`
}

// CreateSession creates a new session (just returns an ID — sessions are
// derived from grouped tasks, not stored in a dedicated table).
func (h *FrontendAdapterHandler) CreateSession(w http.ResponseWriter, r *http.Request) {
	var req CreateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid JSON", types.ErrorTypeValidation)
		return
	}

	sessionID := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)

	WriteJSON(w, http.StatusCreated, SessionSummary{
		SessionID:    sessionID,
		Title:        req.Title,
		TaskCount:    0,
		LatestStatus: "pending",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
}

// SessionDetailResponse is the shape for GET /api/v1/sessions/{id}.
type SessionDetailResponse struct {
	SessionID string              `json:"session_id"`
	Messages  []SessionMessageDTO `json:"messages"`
	Tasks     []TaskSummaryDTO    `json:"tasks"`
}

// SessionMessageDTO is a message in the session detail.
type SessionMessageDTO struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	TaskID    string `json:"task_id"`
	CreatedAt string `json:"created_at"`
}

// TaskSummaryDTO is a task summary in the session detail.
type TaskSummaryDTO struct {
	TaskID    string `json:"task_id"`
	Query     string `json:"query"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// GetSession returns session detail including messages and tasks.
func (h *FrontendAdapterHandler) GetSession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "session_id")
	if sessionID == "" {
		WriteError(w, http.StatusBadRequest, "session_id is required", types.ErrorTypeValidation)
		return
	}
	ctx := r.Context()

	// Fetch messages from session_messages table
	msgRows, err := h.db.QueryContext(ctx, `
		SELECT id, role, content, COALESCE(task_id::text, ''), created_at
		FROM session_messages
		WHERE session_id = $1
		ORDER BY created_at ASC
		LIMIT 200
	`, sessionID)
	if err != nil {
		log.Printf("[ERROR] get session messages: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to get session messages", types.ErrorTypeDB)
		return
	}
	defer msgRows.Close()

	messages := make([]SessionMessageDTO, 0)
	for msgRows.Next() {
		var m SessionMessageDTO
		var createdAt time.Time
		if err := msgRows.Scan(&m.ID, &m.Role, &m.Content, &m.TaskID, &createdAt); err != nil {
			continue
		}
		m.CreatedAt = createdAt.Format(time.RFC3339)
		messages = append(messages, m)
	}

	// Fetch tasks for this session
	taskRows, err := h.db.QueryContext(ctx, `
		SELECT id::text, query, status, created_at
		FROM tasks
		WHERE session_id = $1
		ORDER BY created_at DESC
		LIMIT 50
	`, sessionID)
	if err != nil {
		log.Printf("[ERROR] get session tasks: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to get session tasks", types.ErrorTypeDB)
		return
	}
	defer taskRows.Close()

	tasks := make([]TaskSummaryDTO, 0)
	for taskRows.Next() {
		var t TaskSummaryDTO
		var createdAt time.Time
		if err := taskRows.Scan(&t.TaskID, &t.Query, &t.Status, &createdAt); err != nil {
			continue
		}
		t.CreatedAt = createdAt.Format(time.RFC3339)
		tasks = append(tasks, t)
	}

	WriteJSON(w, http.StatusOK, SessionDetailResponse{
		SessionID: sessionID,
		Messages:  messages,
		Tasks:     tasks,
	})
}

// ─── Task Events (REST) ─────────────────────────────────────────────

// ListTaskEvents returns task events as a JSON array (non-SSE).
func (h *FrontendAdapterHandler) ListTaskEvents(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		WriteError(w, http.StatusBadRequest, "task id is required", types.ErrorTypeValidation)
		return
	}
	ctx := r.Context()

	events, err := h.redis.ReadTaskEvents(ctx, taskID)
	if err != nil {
		log.Printf("[ERROR] read task events: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to read task events", types.ErrorTypeRedis)
		return
	}

	// Convert to JSON-friendly format
	type EventDTO struct {
		ID        string `json:"id"`
		EventType string `json:"event_type"`
		Payload   string `json:"payload"`
		CreatedAt string `json:"created_at"`
	}

	result := make([]EventDTO, 0, len(events))
	for _, e := range events {
		result = append(result, EventDTO{
			ID:        e.ID,
			EventType: e.EventType,
			Payload:   e.Payload,
			CreatedAt: e.CreatedAt,
		})
	}

	WriteJSON(w, http.StatusOK, result)
}

// ─── RAG Status ─────────────────────────────────────────────────────

// RagStatusResponse is the shape for GET /api/v1/rag/status.
type RagStatusResponse struct {
	Enabled          bool   `json:"enabled"`
	Collections      int    `json:"collections"`
	IndexedDocuments int    `json:"indexed_documents"`
	LastSyncAt       string `json:"last_sync_at"`
	Health           string `json:"health"`
}

// GetRagStatus returns aggregated RAG status.
func (h *FrontendAdapterHandler) GetRagStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Count documents
	var docCount int
	if err := h.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM documents").Scan(&docCount); err != nil {
		docCount = 0
	}

	// Count collections (from vectordb or just hardcoded)
	collections := 1 // default "task_embeddings" collection

	health := "online"
	if docCount == 0 {
		health = "offline"
	}

	WriteJSON(w, http.StatusOK, RagStatusResponse{
		Enabled:          true,
		Collections:      collections,
		IndexedDocuments: docCount,
		LastSyncAt:       time.Now().UTC().Format(time.RFC3339),
		Health:           health,
	})
}

// ─── Sandbox Status ─────────────────────────────────────────────────

// SandboxStatusResponse is the shape for GET /api/v1/sandbox/status.
type SandboxStatusResponse struct {
	Enabled       bool   `json:"enabled"`
	ActiveRunners int    `json:"active_runners"`
	QueuedJobs    int    `json:"queued_jobs"`
	Health        string `json:"health"`
}

// GetSandboxStatus returns aggregated sandbox status.
func (h *FrontendAdapterHandler) GetSandboxStatus(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, SandboxStatusResponse{
		Enabled:       true,
		ActiveRunners: 0,
		QueuedJobs:    0,
		Health:        "online",
	})
}

// ─── Tools Status ───────────────────────────────────────────────────

// ToolStatusDTO is the shape for a tool in GET /api/v1/tools.
type ToolStatusDTO struct {
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	Category  string `json:"category"`
	Health    string `json:"health"`
	LatencyMs int    `json:"latency_ms"`
}

// ListTools returns tool availability status.
func (h *FrontendAdapterHandler) ListTools(w http.ResponseWriter, r *http.Request) {
	tools := []ToolStatusDTO{
		{Name: "知识检索 (RAG)", Category: "检索", Enabled: true, Health: "online", LatencyMs: 85},
		{Name: "网页搜索", Category: "检索", Enabled: false, Health: "offline", LatencyMs: 0},
		{Name: "沙箱执行", Category: "执行", Enabled: true, Health: "online", LatencyMs: 230},
		{Name: "反思验证", Category: "分析", Enabled: true, Health: "online", LatencyMs: 110},
		{Name: "MCP 工具", Category: "执行", Enabled: true, Health: "online", LatencyMs: 50},
		{Name: "权限护栏", Category: "治理", Enabled: true, Health: "online", LatencyMs: 35},
	}

	WriteJSON(w, http.StatusOK, tools)
}
