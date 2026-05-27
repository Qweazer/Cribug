package api

import (
	"encoding/json"
	"net/http"

	"cribug/internal/activities"
	"cribug/internal/skillclient"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type SkillsHandler struct {
	client     *skillclient.Client
	activities *activities.SkillActivities
}

func NewSkillsHandler(client *skillclient.Client, acts *activities.SkillActivities) *SkillsHandler {
	return &SkillsHandler{client: client, activities: acts}
}

func (h *SkillsHandler) RegisterRoutes(r chi.Router) {
	r.Get("/api/v1/skills", h.listSkills)
	r.Get("/api/v1/skills/{skill_name}", h.getSkill)
	r.Post("/api/v1/skills/{skill_name}/execute", h.executeSkill)
	r.Get("/api/v1/skills/audit", h.getAudit)
}

func (h *SkillsHandler) listSkills(w http.ResponseWriter, r *http.Request) {
	skills, err := h.client.ListSkills(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error(), "internal_error")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"skills": skills})
}

func (h *SkillsHandler) getSkill(w http.ResponseWriter, r *http.Request) {
	skillName := chi.URLParam(r, "skill_name")
	if skillName == "" {
		WriteError(w, http.StatusBadRequest, "skill_name required", "invalid_arguments")
		return
	}

	metadata, err := h.client.GetSkillMetadata(r.Context(), skillName)
	if err != nil {
		WriteError(w, http.StatusNotFound, err.Error(), "tool_not_found")
		return
	}
	WriteJSON(w, http.StatusOK, metadata)
}

func (h *SkillsHandler) executeSkill(w http.ResponseWriter, r *http.Request) {
	skillName := chi.URLParam(r, "skill_name")
	if skillName == "" {
		WriteError(w, http.StatusBadRequest, "skill_name required", "invalid_arguments")
		return
	}

	var req struct {
		Parameters map[string]interface{} `json:"parameters"`
		AgentID    string                 `json:"agent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request body", "invalid_arguments")
		return
	}

	requestID := uuid.New().String()
	workflowID := uuid.New().String()

	output, err := h.activities.ExecuteSkillActivity(r.Context(), activities.ExecuteSkillInput{
		SkillName:  skillName,
		Parameters: req.Parameters,
		AgentID:    req.AgentID,
		WorkflowID: workflowID,
		RequestID:  requestID,
	})

	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error(), "internal_error")
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"request_id":       requestID,
		"workflow_id":      workflowID,
		"success":          output.Success,
		"output":           output.Output,
		"error":            output.Error,
		"execution_time_ms": output.ExecutionTimeMs,
	})
}

func (h *SkillsHandler) getAudit(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"message": "query skill_audit_logs table for audit records",
	})
}
