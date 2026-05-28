package hooks

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
)

// ── EmitHookEventActivity ─────────────────────────────────────────

// EmitHookEventActivityInput is the Temporal Activity input.
type EmitHookEventActivityInput struct {
	HookPoint       HookPoint              `json:"hook_point"`
	AgentID         string                 `json:"agent_id"`
	WorkflowID      string                 `json:"workflow_id"`
	TenantID        string                 `json:"tenant_id"`
	CorrelationID   string                 `json:"correlation_id"`
	SourceComponent string                 `json:"source_component"`
	HookOrigin      bool                   `json:"hook_origin"`
	RecursionDepth  int                    `json:"recursion_depth"`
	Payload         map[string]interface{} `json:"payload"`
}

// EmitHookEventActivityResult is what the Activity returns to the Workflow.
type EmitHookEventActivityResult struct {
	Decision HookDecision `json:"decision"`
}

// HookActivities wraps the hook runtime as a Temporal Activity.
type HookActivities struct {
	runtime *HookRuntime
}

// NewHookActivities creates a new HookActivities with the given runtime.
func NewHookActivities(runtime *HookRuntime) *HookActivities {
	return &HookActivities{runtime: runtime}
}

// EmitHookEvent is the Temporal Activity. Workflows call this to emit a hook event.
// This is the ONLY Temporal Activity for hooks — no nested Activity calls.
func (a *HookActivities) EmitHookEvent(ctx context.Context, input EmitHookEventActivityInput) (*EmitHookEventActivityResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("EmitHookEventActivity",
		"hook_point", string(input.HookPoint),
		"agent_id", input.AgentID,
		"workflow_id", input.WorkflowID,
		"source", input.SourceComponent,
	)

	event := HookEvent{
		EventID:         uuid.New().String(),
		HookPoint:       input.HookPoint,
		AgentID:         input.AgentID,
		WorkflowID:      input.WorkflowID,
		TenantID:        input.TenantID,
		CorrelationID:   input.CorrelationID,
		Timestamp:       time.Now().UTC(),
		SourceComponent: input.SourceComponent,
		HookOrigin:      input.HookOrigin,
		RecursionDepth:  input.RecursionDepth,
		Payload:         input.Payload,
	}

	if event.TenantID == "" {
		event.TenantID = "00000000-0000-0000-0000-000000000000"
	}

	decision := a.runtime.EmitAndExecute(ctx, event)

	logger.Info("EmitHookEventActivity completed",
		"event_id", event.EventID,
		"denied", decision.Denied,
		"handler_count", len(decision.Results),
	)

	return &EmitHookEventActivityResult{Decision: decision}, nil
}
