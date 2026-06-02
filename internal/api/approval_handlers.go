package api

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"cribug/internal/types"

	"github.com/go-chi/chi/v5"
	"go.temporal.io/sdk/client"
)

// ApprovalHandler handles Phase 7B HITL/Approval API endpoints.
type ApprovalHandler struct {
	temporal client.Client
	db       *sql.DB
}

// NewApprovalHandler creates a new ApprovalHandler.
func NewApprovalHandler(temporalClient client.Client, db *sql.DB) *ApprovalHandler {
	return &ApprovalHandler{temporal: temporalClient, db: db}
}

// ─── GET /api/v1/approvals/pending ─────────────────────────────────────

type PendingApprovalItem struct {
	ApprovalID  string `json:"approval_id"`
	WorkflowID  string `json:"workflow_id"`
	RunID       string `json:"run_id"`
	SessionID   string `json:"session_id"`
	Query       string `json:"query"`
	Reason      string `json:"reason"`
	RiskLevel   string `json:"risk_level"`
	Mode        string `json:"mode"`
	Status      string `json:"status"`
	RequestedAt string `json:"requested_at"`
	ExpiresAt   string `json:"expires_at"`
}

func (h *ApprovalHandler) ListPending(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		WriteError(w, http.StatusServiceUnavailable, "database not available", types.ErrorTypeDB)
		return
	}

	rows, err := h.db.QueryContext(r.Context(), `
		SELECT approval_id, workflow_id, COALESCE(run_id,''), COALESCE(session_id,''),
		       COALESCE(query,''), COALESCE(reason,''), COALESCE(risk_level,'medium'),
		       COALESCE(mode,''), status,
		       COALESCE(requested_at, NOW())::text, COALESCE(expires_at, NOW())::text
		FROM approvals
		WHERE status = 'pending'
		ORDER BY requested_at DESC
		LIMIT 100
	`)
	if err != nil {
		log.Printf("[ERROR] list pending approvals: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to list approvals", types.ErrorTypeDB)
		return
	}
	defer rows.Close()

	var items []PendingApprovalItem
	for rows.Next() {
		var item PendingApprovalItem
		if err := rows.Scan(&item.ApprovalID, &item.WorkflowID, &item.RunID,
			&item.SessionID, &item.Query, &item.Reason, &item.RiskLevel,
			&item.Mode, &item.Status, &item.RequestedAt, &item.ExpiresAt); err != nil {
			log.Printf("[ERROR] scan approval: %v", err)
			continue
		}
		items = append(items, item)
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"approvals": items,
		"count":     len(items),
	})
}

// ─── GET /api/v1/approvals/{approval_id} ────────────────────────────────

func (h *ApprovalHandler) GetApproval(w http.ResponseWriter, r *http.Request) {
	approvalID := chi.URLParam(r, "approval_id")
	if approvalID == "" {
		WriteError(w, http.StatusBadRequest, "approval_id is required", types.ErrorTypeValidation)
		return
	}

	if h.db == nil {
		WriteError(w, http.StatusServiceUnavailable, "database not available", types.ErrorTypeDB)
		return
	}

	var item PendingApprovalItem
	err := h.db.QueryRowContext(r.Context(), `
		SELECT approval_id, workflow_id, COALESCE(run_id,''), COALESCE(session_id,''),
		       COALESCE(query,''), COALESCE(reason,''), COALESCE(risk_level,'medium'),
		       COALESCE(mode,''), status,
		       COALESCE(requested_at, NOW())::text, COALESCE(expires_at, NOW())::text
		FROM approvals
		WHERE approval_id = $1
	`, approvalID).Scan(&item.ApprovalID, &item.WorkflowID, &item.RunID,
		&item.SessionID, &item.Query, &item.Reason, &item.RiskLevel,
		&item.Mode, &item.Status, &item.RequestedAt, &item.ExpiresAt)
	if err == sql.ErrNoRows {
		WriteError(w, http.StatusNotFound, "approval not found", types.ErrorTypeValidation)
		return
	}
	if err != nil {
		log.Printf("[ERROR] get approval: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to get approval", types.ErrorTypeDB)
		return
	}

	WriteJSON(w, http.StatusOK, item)
}

// ─── POST /api/v1/approvals/{approval_id}/respond ───────────────────────

type RespondApprovalRequest struct {
	WorkflowID     string                 `json:"workflow_id"`
	RunID          string                 `json:"run_id"`
	Approved       bool                   `json:"approved"`
	Feedback       string                 `json:"feedback_summary,omitempty"`
	ModifiedAction map[string]interface{} `json:"modified_action,omitempty"`
	ApprovedBy     string                 `json:"approved_by,omitempty"`
}

func (h *ApprovalHandler) Respond(w http.ResponseWriter, r *http.Request) {
	approvalID := chi.URLParam(r, "approval_id")
	if approvalID == "" {
		WriteError(w, http.StatusBadRequest, "approval_id is required", types.ErrorTypeValidation)
		return
	}

	var req RespondApprovalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid JSON", types.ErrorTypeValidation)
		return
	}
	if req.WorkflowID == "" {
		WriteError(w, http.StatusBadRequest, "workflow_id is required", types.ErrorTypeValidation)
		return
	}
	if req.ApprovedBy == "" {
		req.ApprovedBy = "api-user"
	}

	// Validate modified action whitelist
	if req.ModifiedAction != nil {
		if err := types.ValidateModifiedAction(req.ModifiedAction); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid modified_action: "+err.Error(), types.ErrorTypeValidation)
			return
		}
		// Limit feedback to 500 chars for DB storage
		if len(req.Feedback) > 500 {
			req.Feedback = req.Feedback[:500]
		}
	}

	ctx := r.Context()

	// State validation: query DB for current approval status
	if h.db == nil {
		WriteError(w, http.StatusServiceUnavailable, "database not available", types.ErrorTypeDB)
		return
	}

	var currentStatus string
	var expiresAt time.Time
	err := h.db.QueryRowContext(ctx,
		"SELECT status, COALESCE(expires_at, NOW()) FROM approvals WHERE approval_id = $1",
		approvalID,
	).Scan(&currentStatus, &expiresAt)
	if err == sql.ErrNoRows {
		WriteError(w, http.StatusNotFound, "approval not found: "+approvalID, types.ErrorTypeValidation)
		return
	}
	if err != nil {
		log.Printf("[ERROR] query approval status: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to query approval", types.ErrorTypeDB)
		return
	}

	// Validate state: only pending approvals can be responded to
	switch currentStatus {
	case types.ApprovalStatusPending:
		// OK, proceed
	case types.ApprovalStatusApproved:
		WriteError(w, http.StatusConflict, "approval already approved", types.ErrorTypeValidation)
		return
	case types.ApprovalStatusRejected:
		WriteError(w, http.StatusConflict, "approval already rejected", types.ErrorTypeValidation)
		return
	case types.ApprovalStatusModified:
		WriteError(w, http.StatusConflict, "approval already modified", types.ErrorTypeValidation)
		return
	case types.ApprovalStatusTimeout:
		WriteError(w, http.StatusGone, "approval has timed out; respond after timeout is rejected", types.ErrorTypeValidation)
		return
	case types.ApprovalStatusCancelled:
		WriteError(w, http.StatusGone, "approval was cancelled", types.ErrorTypeValidation)
		return
	default:
		WriteError(w, http.StatusConflict, "approval status is "+currentStatus+", cannot respond", types.ErrorTypeValidation)
		return
	}

	// Validate not expired
	if time.Now().After(expiresAt) {
		WriteError(w, http.StatusGone, "approval has expired; respond after timeout is rejected", types.ErrorTypeValidation)
		return
	}

	// Send Temporal Signal to the waiting workflow
	signalName := types.ApprovalSignalName(approvalID)
	signalPayload := types.ApprovalSignalPayload{
		ApprovalID:     approvalID,
		WorkflowID:     req.WorkflowID,
		RunID:          req.RunID,
		Approved:       req.Approved,
		Feedback:       req.Feedback,
		ModifiedAction: req.ModifiedAction,
		ApprovedBy:     req.ApprovedBy,
	}

	if err := h.temporal.SignalWorkflow(ctx, req.WorkflowID, req.RunID, signalName, signalPayload); err != nil {
		log.Printf("[ERROR] signal workflow: %v", err)
		WriteError(w, http.StatusInternalServerError, "failed to signal workflow: "+err.Error(), types.ErrorTypeWorkflow)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"status":      "sent",
		"approval_id": approvalID,
		"workflow_id": req.WorkflowID,
		"run_id":      req.RunID,
		"signal":      signalName,
		"sent_at":     time.Now().UTC().Format(time.RFC3339),
	})
}
