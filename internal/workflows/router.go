package workflows

import (
	"fmt"
	"time"

	"cribug/internal/activities"
	"cribug/internal/types"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const AdvancedRoutingWorkflowName = "AdvancedRoutingWorkflow"

// AdvancedRoutingWorkflow is the Phase 7A unified entry point.
// It classifies tasks, evaluates routing policies, and dispatches to the appropriate
// child Workflow (or returns a routed decision for external dispatch).
//
// Constraints:
//   - Workflow does NOT directly access DB/Redis/HTTP/LLM/Workspace.
//   - All IO goes through Activities.
//   - Long explanations go via PolicyTraceRef; Workflow history stores only short summaries.
func AdvancedRoutingWorkflow(ctx workflow.Context, input types.RouteRequest) (*types.RoutedExecutionResult, error) {
	workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
	runID := workflow.GetInfo(ctx).WorkflowExecution.RunID
	logger := workflow.GetLogger(ctx)

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    2,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// ── Step 1: Classify task complexity ──────────────────────────────
	var complexityResult activities.ClassifyTaskComplexityResult
	err := workflow.ExecuteActivity(ctx, "ClassifyTaskComplexityActivity",
		activities.ClassifyTaskComplexityInput{
			Query:      input.Query,
			UserIntent: input.UserIntent,
		},
	).Get(ctx, &complexityResult)
	if err != nil {
		logger.Warn("ClassifyTaskComplexity failed, using default", "error", err)
		complexityResult = activities.ClassifyTaskComplexityResult{
			ComplexityScore: 0.0,
			RiskLevel:       "low",
			Summary:         "classification_failed",
		}
	}

	// ── Step 2: Detect required capabilities ─────────────────────────
	var capabilityResult activities.DetectTaskCapabilitiesResult
	err = workflow.ExecuteActivity(ctx, "DetectTaskCapabilitiesActivity",
		activities.DetectTaskCapabilitiesInput{
			Query:          input.Query,
			AvailableTools: input.AvailableTools,
			AllowTools:     input.AllowTools,
			AllowSandbox:   input.AllowSandbox,
			AllowResearch:  input.AllowResearch,
		},
	).Get(ctx, &capabilityResult)
	if err != nil {
		logger.Warn("DetectTaskCapabilities failed", "error", err)
		capabilityResult = activities.DetectTaskCapabilitiesResult{
			Summary: "capability_detection_failed",
		}
	}

	// ── Step 3: Evaluate routing policy ──────────────────────────────
	var policyResult activities.EvaluateRoutingPolicyResult
	err = workflow.ExecuteActivity(ctx, "EvaluateRoutingPolicyActivity",
		activities.EvaluateRoutingPolicyInput{
			ComplexityScore:  complexityResult.ComplexityScore,
			RiskLevel:        complexityResult.RiskLevel,
			RequiresTools:    capabilityResult.RequiresTools,
			RequiresSandbox:  capabilityResult.RequiresSandbox,
			RequiresRAG:      capabilityResult.RequiresRAG,
			RequiresResearch: capabilityResult.RequiresResearch,
			RequireCitations: input.RequireCitations,
			BudgetUSD:        input.BudgetUSD,
			RouterConfig:     input.RouterConfig,
		},
	).Get(ctx, &policyResult)
	if err != nil {
		return &types.RoutedExecutionResult{
			Status: types.RoutedStatusError,
			Reason: "routing_policy_evaluation_failed",
		}, err
	}

	decision := policyResult.Decision

	// ── Step 4: Estimate cost ────────────────────────────────────────
	var costResult activities.EstimateRouteCostResult
	_ = workflow.ExecuteActivity(ctx, "EstimateRouteCostActivity",
		activities.EstimateRouteCostInput{
			Mode:            decision.Mode,
			ModelTier:       decision.ModelTier,
			ComplexityScore: decision.ComplexityScore,
		},
	).Get(ctx, &costResult)
	decision.TokenBudget = costResult.TokenBudget
	decision.CostBudgetUSD = costResult.CostBudgetUSD

	// ── Step 5: Write policy trace (Activity, not direct IO) ───────────
	if input.RouterConfig.DecisionAuditEnabled {
		var policyTrace activities.WriteRoutingPolicyTraceResult
		errTrace := workflow.ExecuteActivity(ctx, "WriteRoutingPolicyTraceActivity",
			activities.WriteRoutingPolicyTraceInput{
				WorkflowID:        workflowID,
				Decision:          decision,
				ComplexitySummary: complexityResult.Summary,
				CapabilitySummary: capabilityResult.Summary,
			},
		).Get(ctx, &policyTrace)
		if errTrace != nil {
			logger.Warn("WriteRoutingPolicyTrace failed", "error", errTrace)
		} else {
			decision.PolicyTraceRef = policyTrace.PolicyTraceRef
		}
	}

	// ── Step 6: Evaluate approval policy (fail-closed for high-risk) ──
	var approvalEval activities.EvaluateApprovalPolicyResult
	errApproval := workflow.ExecuteActivity(ctx, "EvaluateApprovalPolicyActivity",
		activities.EvaluateApprovalPolicyInput{
			ComplexityScore: decision.ComplexityScore,
			TokenBudgetUSD:  decision.CostBudgetUSD,
			Tools:           capabilityResult.DetectedTools,
			RiskLevel:       decision.RiskLevel,
			Mode:            string(decision.Mode),
			RequiresSandbox: decision.RequiresSandbox,
			RequiresPublish: decision.RequiresResearchV2,
		},
	).Get(ctx, &approvalEval)

	if errApproval != nil {
		if types.IsHighRiskMode(decision.Mode) || decision.RequiresSandbox || decision.RequiresResearchV2 {
			return &types.RoutedExecutionResult{
				Status:            types.RoutedStatusError,
				Reason:            "approval_policy_evaluation_failed_on_high_risk",
				Decision:          decision,
				FeedbackSummary:   "Approval policy evaluation failed; high-risk task blocked pending human review",
				PendingApprovalID: "policy-eval-failed-" + workflowID,
			}, nil
		}
		decision.RequiresApproval = false
	} else {
		decision.RequiresApproval = approvalEval.Required
		if decision.RequiresApproval {
			decision.Reason = decision.Reason + "; " + approvalEval.Reason
		}
	}

	// ── Step 7: Audit routing decision ───────────────────────────────
	if input.RouterConfig.DecisionAuditEnabled {
		_ = workflow.ExecuteActivity(ctx, "AuditRoutingDecisionActivity",
			activities.AuditRoutingDecisionInput{
				SessionID:   input.SessionID,
				WorkflowID:  workflowID,
				RunID:       runID,
				Decision:    decision,
				PolicyTrace: decision.Reason,
			},
		).Get(ctx, nil)
	}

	// ── Step 8: Emit routing event ───────────────────────────────────
	_ = workflow.ExecuteActivity(ctx, "EmitRoutingEventActivity",
		activities.EmitRoutingEventInput{
			WorkflowID: workflowID,
			EventType:  "ROUTING_DECIDED",
			Decision:   decision,
		},
	).Get(ctx, nil)

	// ── Step 8.5: Preview-only gate ──────────────────────────────────
	// When PreviewOnly=true, MUST NOT dispatch any downstream workflow.
	// This gate ensures /api/v1/tasks/route is a pure routing preview.
	if input.PreviewOnly {
		return &types.RoutedExecutionResult{
			SessionID:  input.SessionID,
			WorkflowID: workflowID,
			RunID:      runID,
			Decision:   decision,
			Status:     types.RoutedStatusPreview,
			CostUSD:    decision.CostBudgetUSD,
		}, nil
	}

	// ── Step 9: Handle approval gate or dispatch ─────────────────────
	// Slice 24: Real approval flow with Temporal Signal wait.
	// Preview only never reaches here (gated at Step 8.5).
	if decision.RequiresApproval {
		return executeWithApprovalGate(ctx, input, decision, workflowID, runID, capabilityResult.DetectedTools)
	}

	// ── Step 10: Dispatch by mode ────────────────────────────────────
	return dispatchByMode(ctx, input, decision, workflowID, runID)
}

// dispatchByMode routes to the appropriate child Workflow or returns the routing decision.
// Slice 23: only Phase 4-6 workflows are actually called.
// Slice 25-28 modes return mode_disabled/not_implemented when feature flags are off.
func dispatchByMode(ctx workflow.Context, input types.RouteRequest, decision types.RoutingDecision, workflowID, runID string) (*types.RoutedExecutionResult, error) {
	switch decision.Mode {
	case types.RouteDirectAnswer:
		return dispatchSimple(ctx, input, decision, workflowID, runID)
	case types.RouteRAGAnswer:
		return dispatchRAG(ctx, input, decision, workflowID, runID)
	case types.RouteReActTool, types.RouteDAGWorkflow:
		return dispatchDAG(ctx, input, decision, workflowID, runID)
	case types.RouteSandboxExecution:
		return dispatchSandbox(ctx, input, decision, workflowID, runID)
	case types.RouteSwarmWorkflow:
		return dispatchSwarm(ctx, input, decision, workflowID, runID)
	case types.RouteResearchV1:
		return dispatchResearchV1(ctx, input, decision, workflowID, runID)
	case types.RouteReflection:
		return dispatchReflection(ctx, input, decision, workflowID, runID)
	case types.RouteTreeOfThoughts, types.RouteDebate, types.RouteResearchV2:
		// Slice 26-28 not yet implemented
		return &types.RoutedExecutionResult{
			SessionID:  input.SessionID,
			WorkflowID: workflowID,
			RunID:      runID,
			Decision:   decision,
			Status:     types.RoutedStatusModeDisabled,
			Reason:     fmt.Sprintf("mode %s not yet implemented", decision.Mode),
		}, nil
	case types.RouteModeDisabled, types.RouteNotImplemented:
		return &types.RoutedExecutionResult{
			SessionID:  input.SessionID,
			WorkflowID: workflowID,
			RunID:      runID,
			Decision:   decision,
			Status:     types.RoutedStatusModeDisabled,
			Reason:     decision.FallbackReason,
		}, nil
	default:
		return dispatchSimple(ctx, input, decision, workflowID, runID)
	}
}

// ─── Approval Gate (Slice 24) ──────────────────────────────────────────

// executeWithApprovalGate creates an approval request, waits for a Temporal Signal,
// and then continues or exits based on the human response.
//
// State machine:
//
//	Approved  → dispatchByMode(ctx, input, decision, workflowID, runID)
//	Rejected  → RoutedExecutionResult{Status: "rejected"}
//	Modified  → update decision → dispatchByMode(...)
//	Timeout   → RoutedExecutionResult{Status: "timeout"}
func executeWithApprovalGate(ctx workflow.Context, input types.RouteRequest, decision types.RoutingDecision, workflowID, runID string, detectedTools []string) (*types.RoutedExecutionResult, error) {
	logger := workflow.GetLogger(ctx)

	// Approval timeout: default 3600s (1 hour), configurable via env
	approvalTimeoutSec := 3600
	requestedAt := workflow.Now(ctx)
	expiresAt := requestedAt.Add(time.Duration(approvalTimeoutSec) * time.Second)

	// Step A: Create approval request in DB (Activity)
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	}
	actx := workflow.WithActivityOptions(ctx, ao)

	var approvalReq activities.RequestApprovalResult
	err := workflow.ExecuteActivity(actx, "RequestApprovalActivity",
		activities.RequestApprovalInput{
			SessionID:      input.SessionID,
			WorkflowID:     workflowID,
			RunID:          runID,
			Query:          input.Query,
			Context:        map[string]interface{}{"mode": string(decision.Mode), "risk": decision.RiskLevel},
			ProposedAction: map[string]interface{}{"mode": string(decision.Mode), "addons": decision.AddonCapabilities},
			Reason:         decision.Reason,
			RiskLevel:      decision.RiskLevel,
			Mode:           string(decision.Mode),
			TimeoutSeconds: approvalTimeoutSec,
			RequestedAt:    requestedAt,
			ExpiresAt:      expiresAt,
		},
	).Get(actx, &approvalReq)
	if err != nil {
		return &types.RoutedExecutionResult{
			Status:          types.RoutedStatusError,
			Reason:          "failed_to_create_approval_request",
			Decision:        decision,
			FeedbackSummary: fmt.Sprintf("RequestApprovalActivity failed: %v", err),
		}, err
	}

	approvalID := approvalReq.ApprovalID
	logger.Info("Approval requested", "approval_id", approvalID, "workflow_id", workflowID)

	// Step B: Emit approval event
	_ = workflow.ExecuteActivity(actx, "EmitApprovalEventActivity",
		activities.EmitApprovalEventInput{
			WorkflowID: workflowID,
			ApprovalID: approvalID,
			EventType:  "APPROVAL_REQUESTED",
			Status:     types.ApprovalStatusPending,
		},
	).Get(actx, nil)

	// Step C: Wait for approval signal or timeout
	signalName := types.ApprovalSignalName(approvalID)
	signalChan := workflow.GetSignalChannel(ctx, signalName)

	var signalPayload types.ApprovalSignalPayload
	deadline := expiresAt
	received := false

	for !received {
		remaining := deadline.Sub(workflow.Now(ctx))
		if remaining <= 0 {
			// Timeout
			logger.Warn("Approval timed out", "approval_id", approvalID)
			_ = workflow.ExecuteActivity(actx, "MarkApprovalTimeoutActivity",
				activities.MarkApprovalTimeoutInput{
					ApprovalID:  approvalID,
					WorkflowID:  workflowID,
					RequestedAt: requestedAt,
				},
			).Get(actx, nil)

			_ = workflow.ExecuteActivity(actx, "EmitApprovalEventActivity",
				activities.EmitApprovalEventInput{
					WorkflowID: workflowID,
					ApprovalID: approvalID,
					EventType:  "APPROVAL_TIMEOUT",
					Status:     types.ApprovalStatusTimeout,
				},
			).Get(actx, nil)

			return &types.RoutedExecutionResult{
				SessionID:         input.SessionID,
				WorkflowID:        workflowID,
				RunID:             runID,
				Decision:          decision,
				Status:            types.RoutedStatusApprovalTimeout,
				Reason:            "approval_timeout",
				PendingApprovalID: approvalID,
				FeedbackSummary:   fmt.Sprintf("Approval %s timed out after %ds", approvalID, approvalTimeoutSec),
			}, nil
		}

		selector := workflow.NewSelector(ctx)
		selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, more bool) {
			c.Receive(ctx, &signalPayload)
			received = true
		})
		selector.AddFuture(workflow.NewTimer(ctx, remaining), func(f workflow.Future) {
			// timeout branch — will be caught on next iteration when remaining <= 0
		})
		selector.Select(ctx)
	}

	respondedAt := workflow.Now(ctx)

	// Step D: Validate signal payload
	if signalPayload.ApprovalID != approvalID {
		logger.Warn("Approval signal mismatch", "expected", approvalID, "got", signalPayload.ApprovalID)
		return &types.RoutedExecutionResult{
			Status:            types.RoutedStatusError,
			Reason:            "approval_signal_mismatch",
			Decision:          decision,
			PendingApprovalID: approvalID,
		}, nil
	}

	// Step E: Record response and audit
	_ = workflow.ExecuteActivity(actx, "RecordApprovalResponseActivity",
		activities.RecordApprovalResponseInput{
			ApprovalID:     approvalID,
			WorkflowID:     workflowID,
			Approved:       signalPayload.Approved,
			Feedback:       signalPayload.Feedback,
			FeedbackRef:    signalPayload.FeedbackRef,
			ModifiedAction: signalPayload.ModifiedAction,
			ApprovedBy:     signalPayload.ApprovedBy,
			RespondedAt:    respondedAt,
			RequestedAt:    requestedAt,
		},
	).Get(actx, nil)

	eventType := "APPROVAL_APPROVED"
	if !signalPayload.Approved {
		eventType = "APPROVAL_REJECTED"
	}
	_ = workflow.ExecuteActivity(actx, "EmitApprovalEventActivity",
		activities.EmitApprovalEventInput{
			WorkflowID: workflowID,
			ApprovalID: approvalID,
			EventType:  eventType,
			Status:     signalPayload.Feedback,
			ApprovedBy: signalPayload.ApprovedBy,
		},
	).Get(actx, nil)

	// Step F: Rejected → safe exit
	if !signalPayload.Approved {
		status := types.RoutedStatusRejected
		if signalPayload.ModifiedAction != nil {
			status = types.RoutedStatusPartial // rejected with modification suggestion
		}
		return &types.RoutedExecutionResult{
			SessionID:         input.SessionID,
			WorkflowID:        workflowID,
			RunID:             runID,
			Decision:          decision,
			Status:            status,
			Reason:            "approval_rejected",
			PendingApprovalID: approvalID,
			FeedbackSummary:   signalPayload.Feedback,
			FeedbackRef:       signalPayload.FeedbackRef,
		}, nil
	}

	// Step G: Modified -- validate, re-check safety, then apply
	if signalPayload.ModifiedAction != nil {
		// G.1: Whitelist check -- only "mode" and "addons" are modifiable
		if err := types.ValidateModifiedAction(signalPayload.ModifiedAction); err != nil {
			logger.Warn("Modified action validation failed", "error", err)
			return &types.RoutedExecutionResult{
				SessionID:         input.SessionID,
				WorkflowID:        workflowID,
				RunID:             runID,
				Decision:          decision,
				Status:            types.RoutedStatusRejected,
				Reason:            "modification_invalid: " + err.Error(),
				PendingApprovalID: approvalID,
				FeedbackSummary:   signalPayload.Feedback,
			}, nil
		}

		// G.2: Apply whitelisted modifications
		prevMode := decision.Mode
		if newMode, ok := signalPayload.ModifiedAction["mode"].(string); ok && newMode != "" {
			decision.Mode = types.RoutingMode(newMode)
		}
		if newAddons, ok := signalPayload.ModifiedAction["addons"].([]interface{}); ok {
			for _, a := range newAddons {
				if aStr, ok := a.(string); ok {
					decision.AddonCapabilities = append(decision.AddonCapabilities, types.RouteAddon(aStr))
				}
			}
		}

		// G.3: Re-check risk after modification -- must not bypass approval
		if decision.Mode != prevMode {
			decision.FallbackReason = "modified_by_approval"
			if types.IsHighRiskMode(decision.Mode) || decision.RequiresSandbox {
				return &types.RoutedExecutionResult{
					SessionID:         input.SessionID,
					WorkflowID:        workflowID,
					RunID:             runID,
					Decision:          decision,
					Status:            types.RoutedStatusRejected,
					Reason:            "modification_requires_re_approval",
					PendingApprovalID: approvalID,
					FeedbackSummary:   "Modified mode is still high-risk; re-approval not supported in Slice 24",
				}, nil
			}
		}
	}

	// Step H: Approved -- continue to original (or safely modified) routed mode
	logger.Info("Approval granted, dispatching", "approval_id", approvalID, "mode", decision.Mode)
	return dispatchByMode(ctx, input, decision, workflowID, runID)
}

// ─── Dispatch helpers ──────────────────────────────────────────────────

func dispatchSimple(ctx workflow.Context, input types.RouteRequest, decision types.RoutingDecision, workflowID, runID string) (*types.RoutedExecutionResult, error) {
	cwo := workflow.ChildWorkflowOptions{
		WorkflowID: workflowID + ":simple",
	}
	cctx := workflow.WithChildOptions(ctx, cwo)

	var result types.WorkflowTaskResult
	err := workflow.ExecuteChildWorkflow(cctx, "SimpleWorkflow", types.WorkflowTaskRequest{
		TaskID:              uuid.New().String(),
		Query:               input.Query,
		SessionID:           input.SessionID,
		Model:               "gpt-4o-mini",
		Temperature:         0.7,
		MaxTotalTokens:      decision.TokenBudget,
		MaxCompletionTokens: 1024,
		WorkflowID:          workflowID,
		RunID:               runID,
	}).Get(cctx, &result)
	if err != nil {
		return &types.RoutedExecutionResult{
			SessionID:  input.SessionID,
			WorkflowID: workflowID,
			RunID:      runID,
			Decision:   decision,
			Status:     types.RoutedStatusError,
			Reason:     fmt.Sprintf("simple_workflow_failed: %v", err),
		}, err
	}

	return &types.RoutedExecutionResult{
		SessionID:       input.SessionID,
		WorkflowID:      workflowID,
		RunID:           runID,
		Decision:        decision,
		FinalAnswerText: result.Answer,
		Status:          types.RoutedStatusOK,
		CostUSD:         decision.CostBudgetUSD,
	}, nil
}

func dispatchRAG(ctx workflow.Context, input types.RouteRequest, decision types.RoutingDecision, workflowID, runID string) (*types.RoutedExecutionResult, error) {
	cwo := workflow.ChildWorkflowOptions{
		WorkflowID: workflowID + ":rag",
	}
	cctx := workflow.WithChildOptions(ctx, cwo)

	type ragResult struct {
		Answer string `json:"answer"`
	}
	var result ragResult
	err := workflow.ExecuteChildWorkflow(cctx, RAGQueryWorkflowName, map[string]interface{}{
		"task_id":    workflowID,
		"query":      input.Query,
		"top_k":      5,
		"max_tokens": decision.TokenBudget,
	}).Get(cctx, &result)
	if err != nil {
		return &types.RoutedExecutionResult{
			SessionID:  input.SessionID,
			WorkflowID: workflowID,
			RunID:      runID,
			Decision:   decision,
			Status:     types.RoutedStatusError,
			Reason:     fmt.Sprintf("rag_workflow_failed: %v", err),
		}, err
	}

	return &types.RoutedExecutionResult{
		SessionID:       input.SessionID,
		WorkflowID:      workflowID,
		RunID:           runID,
		Decision:        decision,
		FinalAnswerText: result.Answer,
		Status:          types.RoutedStatusOK,
		CostUSD:         decision.CostBudgetUSD,
	}, nil
}

func dispatchDAG(ctx workflow.Context, input types.RouteRequest, decision types.RoutingDecision, workflowID, runID string) (*types.RoutedExecutionResult, error) {
	cwo := workflow.ChildWorkflowOptions{
		WorkflowID: workflowID + ":dag",
	}
	cctx := workflow.WithChildOptions(ctx, cwo)

	var result types.WorkflowTaskResult
	err := workflow.ExecuteChildWorkflow(cctx, DAGWorkflowName, types.WorkflowTaskRequest{
		TaskID:              uuid.New().String(),
		Query:               input.Query,
		SessionID:           input.SessionID,
		Model:               "gpt-4o-mini",
		Temperature:         0.7,
		MaxTotalTokens:      decision.TokenBudget,
		MaxCompletionTokens: 2048,
		WorkflowID:          workflowID,
		RunID:               runID,
	}).Get(cctx, &result)
	if err != nil {
		return &types.RoutedExecutionResult{
			SessionID:  input.SessionID,
			WorkflowID: workflowID,
			RunID:      runID,
			Decision:   decision,
			Status:     types.RoutedStatusError,
			Reason:     fmt.Sprintf("dag_workflow_failed: %v", err),
		}, err
	}

	return &types.RoutedExecutionResult{
		SessionID:       input.SessionID,
		WorkflowID:      workflowID,
		RunID:           runID,
		Decision:        decision,
		FinalAnswerText: result.Answer,
		Status:          types.RoutedStatusOK,
		CostUSD:         decision.CostBudgetUSD,
	}, nil
}

func dispatchSandbox(ctx workflow.Context, input types.RouteRequest, decision types.RoutingDecision, workflowID, runID string) (*types.RoutedExecutionResult, error) {
	// Sandbox: if approval is required, return approval_required
	if decision.RequiresApproval {
		return &types.RoutedExecutionResult{
			SessionID:         input.SessionID,
			WorkflowID:        workflowID,
			RunID:             runID,
			Decision:          decision,
			Status:            types.RoutedStatusApprovalRequired,
			Reason:            "sandbox_requires_approval",
			PendingApprovalID: fmt.Sprintf("approval-%s", workflowID),
			CostUSD:           decision.CostBudgetUSD,
		}, nil
	}

	cwo := workflow.ChildWorkflowOptions{
		WorkflowID: workflowID + ":sandbox",
	}
	cctx := workflow.WithChildOptions(ctx, cwo)

	var result types.WorkflowTaskResult
	err := workflow.ExecuteChildWorkflow(cctx, SandboxWorkflowName, map[string]interface{}{
		"task_id":  workflowID,
		"code":     input.Query,
		"language": "auto",
	}).Get(cctx, &result)
	if err != nil {
		return &types.RoutedExecutionResult{
			SessionID:  input.SessionID,
			WorkflowID: workflowID,
			RunID:      runID,
			Decision:   decision,
			Status:     types.RoutedStatusError,
			Reason:     fmt.Sprintf("sandbox_workflow_failed: %v", err),
		}, err
	}

	return &types.RoutedExecutionResult{
		SessionID:       input.SessionID,
		WorkflowID:      workflowID,
		RunID:           runID,
		Decision:        decision,
		FinalAnswerText: result.Answer,
		Status:          types.RoutedStatusOK,
		CostUSD:         decision.CostBudgetUSD,
	}, nil
}

func dispatchSwarm(ctx workflow.Context, input types.RouteRequest, decision types.RoutingDecision, workflowID, runID string) (*types.RoutedExecutionResult, error) {
	cwo := workflow.ChildWorkflowOptions{
		WorkflowID: workflowID + ":swarm",
	}
	cctx := workflow.WithChildOptions(ctx, cwo)

	var result types.WorkflowTaskResult
	err := workflow.ExecuteChildWorkflow(cctx, SwarmWorkflowName, types.SwarmWorkflowInput{
		TaskID:        workflowID,
		WorkflowID:    workflowID,
		RunID:         runID,
		Query:         input.Query,
		Model:         "gpt-4o-mini",
		Temperature:   0.7,
		MaxTokens:     decision.TokenBudget,
		WorkerCount:   3,
		WorkerTimeout: 60,
	}).Get(cctx, &result)
	if err != nil {
		return &types.RoutedExecutionResult{
			SessionID:  input.SessionID,
			WorkflowID: workflowID,
			RunID:      runID,
			Decision:   decision,
			Status:     types.RoutedStatusError,
			Reason:     fmt.Sprintf("swarm_workflow_failed: %v", err),
		}, err
	}

	return &types.RoutedExecutionResult{
		SessionID:       input.SessionID,
		WorkflowID:      workflowID,
		RunID:           runID,
		Decision:        decision,
		FinalAnswerText: result.Answer,
		Status:          types.RoutedStatusOK,
		CostUSD:         decision.CostBudgetUSD,
	}, nil
}

func dispatchReflection(ctx workflow.Context, input types.RouteRequest, decision types.RoutingDecision, workflowID, runID string) (*types.RoutedExecutionResult, error) {
	cwo := workflow.ChildWorkflowOptions{
		WorkflowID: workflowID + ":reflection",
	}
	cctx := workflow.WithChildOptions(ctx, cwo)

	var result types.ReflectionResult
	err := workflow.ExecuteChildWorkflow(cctx, ReflectionWorkflowName, types.ReflectionRequest{
		TaskID:       workflowID,
		WorkflowID:   workflowID,
		RunID:        runID,
		SessionID:    input.SessionID,
		Query:        input.Query,
		RouterConfig: input.RouterConfig,
		Config: types.ReflectionConfig{
			MaxIterations:       2,
			MinScoreThreshold:   0.75,
			EvaluationCriteria:  []string{"clarity", "accuracy", "completeness"},
			MockLLM:             false, // let Activities decide mock vs real
			Model:               "gpt-4o-mini",
			Temperature:         0.7,
			MaxCompletionTokens: 1024,
		},
	}).Get(cctx, &result)
	if err != nil {
		return &types.RoutedExecutionResult{
			SessionID:  input.SessionID,
			WorkflowID: workflowID,
			RunID:      runID,
			Decision:   decision,
			Status:     types.RoutedStatusError,
			Reason:     fmt.Sprintf("reflection_workflow_failed: %v", err),
		}, err
	}

	return &types.RoutedExecutionResult{
		SessionID:       input.SessionID,
		WorkflowID:      workflowID,
		RunID:           runID,
		Decision:        decision,
		FinalAnswerText: result.FinalAnswerSummary,
		Status:          types.RoutedStatusOK,
		CostUSD:         decision.CostBudgetUSD,
	}, nil
}

func dispatchResearchV1(ctx workflow.Context, input types.RouteRequest, decision types.RoutingDecision, workflowID, runID string) (*types.RoutedExecutionResult, error) {
	cwo := workflow.ChildWorkflowOptions{
		WorkflowID: workflowID + ":research",
	}
	cctx := workflow.WithChildOptions(ctx, cwo)

	var result types.WorkflowTaskResult
	err := workflow.ExecuteChildWorkflow(cctx, ResearchSynthesisWorkflowName, types.WorkflowTaskRequest{
		TaskID:              uuid.New().String(),
		Query:               input.Query,
		SessionID:           input.SessionID,
		Model:               "gpt-4o-mini",
		Temperature:         0.7,
		MaxTotalTokens:      decision.TokenBudget,
		MaxCompletionTokens: 2048,
		WorkflowID:          workflowID,
		RunID:               runID,
	}).Get(cctx, &result)
	if err != nil {
		return &types.RoutedExecutionResult{
			SessionID:  input.SessionID,
			WorkflowID: workflowID,
			RunID:      runID,
			Decision:   decision,
			Status:     types.RoutedStatusError,
			Reason:     fmt.Sprintf("research_workflow_failed: %v", err),
		}, err
	}

	return &types.RoutedExecutionResult{
		SessionID:       input.SessionID,
		WorkflowID:      workflowID,
		RunID:           runID,
		Decision:        decision,
		FinalAnswerText: result.Answer,
		Status:          types.RoutedStatusOK,
		CostUSD:         decision.CostBudgetUSD,
	}, nil
}
