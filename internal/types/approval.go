package types

import (
	"fmt"
	"time"
)

// AllowedModifiedActionFields is the whitelist of fields that can be modified via approval.
var AllowedModifiedActionFields = map[string]bool{
	"mode":   true,
	"addons": true,
}

// ValidateModifiedAction returns an error if the modified action contains non-whitelisted fields.
func ValidateModifiedAction(action map[string]interface{}) error {
	for k := range action {
		if !AllowedModifiedActionFields[k] {
			return fmt.Errorf("field %q is not in the modify whitelist (allowed: mode, addons)", k)
		}
	}
	if _, ok := action["mode"]; !ok {
		if _, ok2 := action["addons"]; !ok2 {
			return fmt.Errorf("modified_action must contain at least one whitelisted field (mode, addons)")
		}
	}
	return nil
}

// Approval status constants
const (
	ApprovalStatusPending   = "pending"
	ApprovalStatusApproved  = "approved"
	ApprovalStatusRejected  = "rejected"
	ApprovalStatusModified  = "modified"
	ApprovalStatusTimeout   = "timeout"
	ApprovalStatusCancelled = "cancelled"
)

// ApprovalRequest is created by the Workflow when approval is required.
type ApprovalRequest struct {
	ApprovalID     string                 `json:"approval_id"`
	WorkflowID     string                 `json:"workflow_id"`
	RunID          string                 `json:"run_id"`
	SessionID      string                 `json:"session_id"`
	Query          string                 `json:"query"`
	Context        map[string]interface{} `json:"context"`
	ProposedAction map[string]interface{} `json:"proposed_action"`
	Reason         string                 `json:"reason"`
	RiskLevel      string                 `json:"risk_level"`
	Mode           string                 `json:"mode"` // planned routing mode
	Status         string                 `json:"status"`
	RequestedAt    time.Time              `json:"requested_at"`
	ExpiresAt      time.Time              `json:"expires_at"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

// ApprovalResponse is the human response to an approval request.
type ApprovalResponse struct {
	ApprovalID     string                 `json:"approval_id"`
	WorkflowID     string                 `json:"workflow_id"`
	RunID          string                 `json:"run_id"`
	Approved       bool                   `json:"approved"`
	Feedback       string                 `json:"feedback_summary,omitempty"` // short summary ≤500 chars
	FeedbackRef    string                 `json:"feedback_ref,omitempty"`     // full feedback via Workspace
	ModifiedAction map[string]interface{} `json:"modified_action,omitempty"`  // modified mode/input
	ApprovedBy     string                 `json:"approved_by,omitempty"`
	RespondedAt    time.Time              `json:"responded_at"`
}

// ApprovalSignalPayload is the payload sent via Temporal Signal to the Workflow.
type ApprovalSignalPayload struct {
	ApprovalID     string                 `json:"approval_id"`
	WorkflowID     string                 `json:"workflow_id"`
	RunID          string                 `json:"run_id"`
	Approved       bool                   `json:"approved"`
	Feedback       string                 `json:"feedback_summary,omitempty"`
	FeedbackRef    string                 `json:"feedback_ref,omitempty"`
	ModifiedAction map[string]interface{} `json:"modified_action,omitempty"`
	ApprovedBy     string                 `json:"approved_by,omitempty"`
}

// ApprovalSignalName returns the Temporal Signal name for a given approval ID.
func ApprovalSignalName(approvalID string) string {
	return "human-approval-" + approvalID
}
