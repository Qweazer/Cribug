package api

import (
	"encoding/json"
	"log"
	"net/http"

	"cribug/internal/db"
	"cribug/internal/hooks"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type HooksHandler struct {
	db                  *db.Postgres
	allowedInternalHn  []string
	allowedHTTPHosts   []string
}

func NewHooksHandler(database *db.Postgres, allowedInternalHandlers, allowedHTTPHosts []string) *HooksHandler {
	return &HooksHandler{
		db:                 database,
		allowedInternalHn:  allowedInternalHandlers,
		allowedHTTPHosts:   allowedHTTPHosts,
	}
}

// POST /api/v1/hooks
func (h *HooksHandler) register(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name       string                 `json:"name"`
		HookPoint  string                 `json:"hook_point"`
		HandlerURL string                 `json:"handler_url"`
		Blocking   bool                   `json:"blocking"`
		Filter     map[string]interface{} `json:"filter,omitempty"`
		TenantID   string                 `json:"tenant_id,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "validation_error")
		return
	}

	if body.TenantID == "" {
		body.TenantID = "00000000-0000-0000-0000-000000000000"
	}

	hp := hooks.HookPoint(body.HookPoint)
	if !hp.IsValid() {
		validPoints := make([]string, 0)
		for _, p := range hooks.AllHookPoints() {
			validPoints = append(validPoints, string(p))
		}
		WriteJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":      "invalid hook_point",
			"valid":      validPoints,
			"error_type": "validation_error",
		})
		return
	}

	reg := hooks.HookRegistration{
		ID:         uuid.New().String(),
		TenantID:   body.TenantID,
		Name:       body.Name,
		HookPoint:  hp,
		HandlerURL: body.HandlerURL,
		Blocking:   body.Blocking,
		Filter:     body.Filter,
		Enabled:    true,
	}

	if err := hooks.ValidateHookRegistration(reg, h.allowedInternalHn, h.allowedHTTPHosts); err != nil {
		WriteError(w, http.StatusBadRequest, err.Error(), "validation_error")
		return
	}

	ctx := r.Context()
	created, err := h.db.CreateHook(ctx, reg)
	if err != nil {
		log.Printf("[ERROR] create hook: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to create hook: "+err.Error(), "db_error")
		return
	}

	WriteJSON(w, http.StatusCreated, created)
}

// GET /api/v1/hooks
func (h *HooksHandler) list(w http.ResponseWriter, r *http.Request) {
	tenantID := r.URL.Query().Get("tenant_id")
	hookPoint := r.URL.Query().Get("hook_point")

	if hp := hooks.HookPoint(hookPoint); hookPoint != "" && !hp.IsValid() {
		WriteError(w, http.StatusBadRequest, "invalid hook_point filter", "validation_error")
		return
	}

	ctx := r.Context()
	hooksList, err := h.db.ListHooks(ctx, tenantID, hookPoint)
	if err != nil {
		log.Printf("[ERROR] list hooks: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to list hooks", "db_error")
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"hooks": hooksList,
	})
}

// GET /api/v1/hooks/{hook_id}
func (h *HooksHandler) get(w http.ResponseWriter, r *http.Request) {
	hookID := chi.URLParam(r, "hook_id")
	if hookID == "" {
		WriteError(w, http.StatusBadRequest, "hook_id is required", "validation_error")
		return
	}

	ctx := r.Context()
	reg, err := h.db.GetHook(ctx, hookID)
	if err != nil {
		log.Printf("[ERROR] get hook: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to get hook", "db_error")
		return
	}
	if reg == nil {
		WriteError(w, http.StatusNotFound, "hook not found", "not_found")
		return
	}

	WriteJSON(w, http.StatusOK, reg)
}

// DELETE /api/v1/hooks/{hook_id}
func (h *HooksHandler) delete(w http.ResponseWriter, r *http.Request) {
	hookID := chi.URLParam(r, "hook_id")
	if hookID == "" {
		WriteError(w, http.StatusBadRequest, "hook_id is required", "validation_error")
		return
	}

	ctx := r.Context()
	if err := h.db.DeleteHook(ctx, hookID); err != nil {
		log.Printf("[ERROR] delete hook: %v", err)
		WriteError(w, http.StatusInternalServerError, err.Error(), "db_error")
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// PATCH /api/v1/hooks/{hook_id}/enable
func (h *HooksHandler) enable(w http.ResponseWriter, r *http.Request) {
	h.setEnabled(w, r, true)
}

// PATCH /api/v1/hooks/{hook_id}/disable
func (h *HooksHandler) disable(w http.ResponseWriter, r *http.Request) {
	h.setEnabled(w, r, false)
}

func (h *HooksHandler) setEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	hookID := chi.URLParam(r, "hook_id")
	if hookID == "" {
		WriteError(w, http.StatusBadRequest, "hook_id is required", "validation_error")
		return
	}

	ctx := r.Context()
	if err := h.db.UpdateHookEnabled(ctx, hookID, enabled); err != nil {
		log.Printf("[ERROR] update hook enabled: %v", err)
		WriteError(w, http.StatusInternalServerError, err.Error(), "db_error")
		return
	}

	status := "disabled"
	if enabled {
		status = "enabled"
	}
	WriteJSON(w, http.StatusOK, map[string]string{"status": status})
}

// GET /api/v1/hooks/audit
func (h *HooksHandler) getAudit(w http.ResponseWriter, r *http.Request) {
	workflowID := r.URL.Query().Get("workflow_id")
	if workflowID == "" {
		WriteError(w, http.StatusBadRequest, "workflow_id query param required", "validation_error")
		return
	}

	ctx := r.Context()
	logs, err := h.db.GetHookAuditLogsByWorkflowID(ctx, workflowID)
	if err != nil {
		log.Printf("[ERROR] get hook audit logs: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to get audit logs", "db_error")
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"audit_logs": logs,
	})
}
