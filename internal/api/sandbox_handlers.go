package api

import (
	"encoding/json"
	"log"
	"net/http"

	"cribug/internal/db"
	"cribug/internal/types"

	"github.com/google/uuid"
)

type SandboxHandler struct {
	db *db.Postgres
}

func NewSandboxHandler(database *db.Postgres) *SandboxHandler {
	return &SandboxHandler{db: database}
}

// POST /api/v1/sandbox/execute
func (h *SandboxHandler) execute(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code       string               `json:"code"`
		Language   string               `json:"language"`
		Stdin      string               `json:"stdin,omitempty"`
		Policy     *types.SandboxPolicy `json:"policy,omitempty"`
		AgentID    string               `json:"agent_id"`
		WorkflowID string               `json:"workflow_id"`
		RunID      string               `json:"run_id"`
		TaskID     string               `json:"task_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), types.ErrorTypeValidation)
		return
	}

	if body.Code == "" {
		WriteError(w, http.StatusBadRequest, "code is required", types.SandboxErrorTypeInvalidInput)
		return
	}
	if body.Language == "" {
		body.Language = "wasi"
	}

	requestID := uuid.New().String()
	if body.Policy == nil {
		body.Policy = &types.SandboxPolicy{}
	}

	WriteJSON(w, http.StatusAccepted, map[string]interface{}{
		"request_id": requestID,
		"language":   body.Language,
		"code_len":   len(body.Code),
		"input":      body,
	})
}

// GET /api/v1/sandbox/audit
func (h *SandboxHandler) listAudit(w http.ResponseWriter, r *http.Request) {
	workflowID := r.URL.Query().Get("workflow_id")
	if workflowID == "" {
		WriteError(w, http.StatusBadRequest, "workflow_id query param required", types.ErrorTypeValidation)
		return
	}

	ctx := r.Context()
	logs, err := h.db.GetSandboxAuditLogsByWorkflowID(ctx, workflowID)
	if err != nil {
		log.Printf("[ERROR] get sandbox audit: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to get audit logs", types.ErrorTypeDB)
		return
	}
	if logs == nil {
		logs = []types.SandboxAuditLog{}
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"audit_logs": logs,
	})
}
