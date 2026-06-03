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
			ComplexitySummary: complexityResult.Summary,
			RequiresTools:    capabilityResult.RequiresTools,
			RequiresSandbox:  capabilityResult.RequiresSandbox,
			RequiresRAG:      capabilityResult.RequiresRAG,
			RequiresResearch: capabilityResult.RequiresResearch,
			RequireCitations: input.RequireCitations,
			BudgetUSD:        input.BudgetUSD,
			RouterConfig:     input.RouterConfig,
			Query:            input.Query,
			UserIntent:       input.UserIntent,
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
			// Test-only override: ROUTER_REQUIRE_APPROVAL=false in
			// the gateway env disables the gate so async workflows
			// can complete without a human signal. Default true.
			RequireApproval: input.RouterConfig.RequireApproval,
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
				// Phase 7I v2: persist signals + explanation into the
				// 016 JSONB columns. They are written only when the
				// activity returned them (legacy callers leave them nil
				// and the columns stay NULL — backward compatible).
				Signals:     policyResult.Signals,
				Explanation: policyResult.Explanation,
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
		result, err := executeWithApprovalGate(ctx, input, decision, workflowID, runID, capabilityResult.DetectedTools)
		persistRoutedResult(ctx, workflowID, runID, input.SessionID, result, err)
		return result, err
	}

	// ── Step 10: Dispatch by mode ────────────────────────────────────
	result, err := dispatchByMode(ctx, input, decision, workflowID, runID)
	persistRoutedResult(ctx, workflowID, runID, input.SessionID, result, err)
	return result, err
}

// persistRoutedResult calls PersistRoutedExecutionResultActivity to
// record the final RoutedExecutionResult in the tasks table. This is
// the bridge for async execute-routed polling (Phase 7E.5).
//
// Non-fatal: if the persist call fails, we annotate the result with
// metadata["persist_error"] and continue. The polling API can still
// answer "failed_with_persist_error" instead of hanging at "running".
func persistRoutedResult(ctx workflow.Context, workflowID, runID, sessionID string, result *types.RoutedExecutionResult, wfErr error) {
	logger := workflow.GetLogger(ctx)
	status := "completed"
	errType := ""
	errMsg := ""
	if wfErr != nil || result == nil {
		status = "failed"
		if wfErr != nil {
			errType = "workflow_error"
			errMsg = wfErr.Error()
		} else {
			errType = "nil_result"
			errMsg = "AdvancedRoutingWorkflow returned nil result"
		}
	}
	if result == nil {
		result = &types.RoutedExecutionResult{
			SessionID:  sessionID,
			WorkflowID: workflowID,
			RunID:      runID,
			Status:     status,
		}
	}
	if result.Metadata == nil {
		result.Metadata = map[string]interface{}{}
	}
	if status == "failed" {
		result.Metadata["persist_error"] = errMsg
	}
	var err error
	for attempt := 1; attempt <= 2; attempt++ {
		err = workflow.ExecuteActivity(ctx, "PersistRoutedExecutionResultActivity",
			activities.PersistRoutedExecutionResultInput{
				WorkflowID: workflowID,
				SessionID:  sessionID,
				RunID:      runID,
				Status:     status,
				Result:     result,
				ErrorType:  errType,
				ErrorMsg:   errMsg,
			},
		).Get(ctx, nil)
		if err == nil {
			return
		}
		logger.Warn("PersistRoutedExecutionResult failed", "attempt", attempt, "error", err)
	}
	// Persist still failed; record warning so the polling API can
	// surface a structured error to the client.
	if result.Metadata == nil {
		result.Metadata = map[string]interface{}{}
	}
	result.Metadata["persist_error"] = "persist_activity_failed: " + err.Error()
	logger.Error("PersistRoutedExecutionResult permanently failed", "workflow_id", workflowID, "error", err)
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
	case types.RouteTreeOfThoughts:
		return dispatchToT(ctx, input, decision, workflowID, runID)
	case types.RouteDebate:
		return dispatchDebate(ctx, input, decision, workflowID, runID)
	case types.RouteResearchV2:
		return dispatchResearchV2(ctx, input, decision, workflowID, runID)
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

	// Step B.5: Update the tasks row to "waiting_for_approval" so the
	// async poll API returns approval details instead of "running".
	_ = workflow.ExecuteActivity(actx, "UpdateTaskApprovalStatusActivity",
		activities.UpdateTaskApprovalStatusInput{
			WorkflowID:  workflowID,
			Status:      "waiting_for_approval",
			ApprovalID:  approvalID,
			ApprovalURL: "/api/v1/tasks/" + workflowID + "/result",
			RiskLevel:   decision.RiskLevel,
			Mode:        string(decision.Mode),
			Reason:      decision.Reason,
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

func dispatchToT(ctx workflow.Context, input types.RouteRequest, decision types.RoutingDecision, workflowID, runID string) (*types.RoutedExecutionResult, error) {
	cwo := workflow.ChildWorkflowOptions{WorkflowID: workflowID + ":tot"}
	cctx := workflow.WithChildOptions(ctx, cwo)
	var result types.ToTResult
	err := workflow.ExecuteChildWorkflow(cctx, TreeOfThoughtsWorkflowName, types.ToTWorkflowInput{
		TaskID: workflowID, WorkflowID: workflowID, RunID: runID,
		SessionID: input.SessionID, Query: input.Query,
		Config: types.TreeOfThoughtsConfig{
			MaxDepth: 2, BranchingFactor: 2, MaxTotalNodes: 8,
			TokenBudget: 3000, PruningThreshold: 0.3,
			EvaluationMethod: "scoring", MockLLM: false,
			ModelTier: "small",
		},
	}).Get(cctx, &result)
	if err != nil {
		return &types.RoutedExecutionResult{
			SessionID: input.SessionID, WorkflowID: workflowID, RunID: runID,
			Decision: decision, Status: types.RoutedStatusError,
			Reason: fmt.Sprintf("tot_workflow_failed: %v", err),
		}, err
	}
	return &types.RoutedExecutionResult{
		SessionID: input.SessionID, WorkflowID: workflowID, RunID: runID,
		Decision: decision, FinalAnswerText: result.SolutionSummary,
		FinalAnswerRef: result.SolutionRef,
		Status: types.RoutedStatusOK, CostUSD: decision.CostBudgetUSD,
		TokensUsed: result.TotalTokens,
		Provider: result.Provider, ModelUsed: result.ModelUsed,
		Mode: result.Mode, Mock: result.Mock, FallbackUsed: result.FallbackUsed,
		LLMCalls: result.LLMCalls,
		// Typed ToT fields (Phase 7J) — survive serialization better
		// than map[string]interface{} metadata sub-keys.
		TotalThoughts:  result.TotalThoughts,
		TreeDepth:      result.TreeDepth,
		BestPathCount:  len(result.BestPath),
		SolutionRef:    result.SolutionRef,
		ExplorationRef: result.ExplorationTreeRef,
		ToTConfidence:  result.Confidence,
		Metadata: map[string]interface{}{
			"llm_calls":             result.LLMCalls,
			"total_thoughts":        result.TotalThoughts,
			"tree_depth":            result.TreeDepth,
			"best_path":             result.BestPath,
			"solution_ref":          result.SolutionRef,
			"exploration_tree_ref":  result.ExplorationTreeRef,
			"confidence":            result.Confidence,
			"pruned_count":          result.PrunedCount,
		},
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
			Model:               "",    // resolved via ConfigResolver in ReflectionWorkflow Step 0
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
		TokensUsed:      result.TotalTokens,
		Provider:        result.Provider,
		ModelUsed:       result.ModelUsed,
		Mode:            result.Mode,
		Mock:            result.FallbackUsed, // "mock" == fallback was used
		FallbackUsed:    result.FallbackUsed,
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

// dispatchDebate routes a debate-mode request to DebateWorkflow (Slice 27).
// Debate is suitable for "compare / vs / pros and cons" queries. The router
// already populates decision.Mode = debate via the heuristic keyword rules
// (see activities/router.go). Here we just spin up the child Workflow and
// surface its structured result.
func dispatchDebate(ctx workflow.Context, input types.RouteRequest, decision types.RoutingDecision, workflowID, runID string) (*types.RoutedExecutionResult, error) {
	cwo := workflow.ChildWorkflowOptions{WorkflowID: workflowID + ":debate"}
	cctx := workflow.WithChildOptions(ctx, cwo)

	// Bounded defaults for a routed debate: small model, 1-3 rounds.
	maxRounds := 2
	if decision.TokenBudget > 0 && decision.TokenBudget < 2000 {
		maxRounds = 1
	}

	var result types.DebateResult
	err := workflow.ExecuteChildWorkflow(cctx, DebateWorkflowName, types.DebateWorkflowInput{
		TaskID:     workflowID,
		WorkflowID: workflowID,
		RunID:      runID,
		SessionID:  input.SessionID,
		Query:      input.Query,
		Config: types.DebateConfig{
			NumDebaters:      2,
			MaxRounds:        maxRounds,
			Perspectives:     []string{types.DebatePositionPro, types.DebatePositionCon},
			ModeratorEnabled: true,
			ModelTier:        "small",
			RoundTimeoutSecs: 60,
			MockLLM:          false, // Activities decide mock vs real based on profile
		},
	}).Get(cctx, &result)
	if err != nil {
		return &types.RoutedExecutionResult{
			SessionID:  input.SessionID,
			WorkflowID: workflowID,
			RunID:      runID,
			Decision:   decision,
			Status:     types.RoutedStatusError,
			Reason:     fmt.Sprintf("debate_workflow_failed: %v", err),
		}, err
	}

	finalText := result.FinalAnswerText
	if len(finalText) > 2000 {
		finalText = truncateTo(finalText, 2000)
	}
	return &types.RoutedExecutionResult{
		SessionID:       input.SessionID,
		WorkflowID:      workflowID,
		RunID:           runID,
		Decision:        decision,
		FinalAnswerRef:  result.FinalAnswerRef,
		FinalAnswerText: finalText,
		Status:          types.RoutedStatusOK,
		CostUSD:         decision.CostBudgetUSD,
		TokensUsed:      result.TotalTokens,
		Provider:        result.Provider,
		ModelUsed:       result.ModelUsed,
		Mode:            result.Mode,
		// Promote the boolean into the typed field. RoutedExecutionResult
		// treats mock as a proper JSON bool (Phase 7E.6 polish).
		Mock:         result.Mock,
		FallbackUsed: result.FallbackUsed,
		LLMCalls:     result.LLMCalls,
		Metadata: map[string]interface{}{
			"llm_calls":                result.LLMCalls,
			"debate_rounds":            result.Rounds,
			"debate_final_position":    result.FinalPosition,
			"debate_consensus":         result.ConsensusReached,
			"debate_transcript_ref":    result.TranscriptRef,
			"debate_verdict_ref":       result.VerdictRef,
			"debate_judge_parse":       result.JudgeParseSource,
			"debate_confidence_source": result.ConfidenceSource,
		},
	}, nil
}

// dispatchResearchV2 routes a research-v2 request to
// ResearchSynthesisV2Workflow (Phase 7F Slice 28). Multi-source
// retrieval, credibility scoring, contradiction detection, citation
// chain, and optional reflection/debate are computed in Activities.
// Workflow result only carries refs + short metadata.
func dispatchResearchV2(ctx workflow.Context, input types.RouteRequest, decision types.RoutingDecision, workflowID, runID string) (*types.RoutedExecutionResult, error) {
	cwo := workflow.ChildWorkflowOptions{WorkflowID: workflowID + ":research_v2"}
	cctx := workflow.WithChildOptions(ctx, cwo)

	// Bounded defaults for a routed research v2: small model, 1
	// iteration, few sources. Real LLM smoke uses these to stay within
	// the cost cap.
	maxSources := 3
	if decision.TokenBudget >= 4000 {
		maxSources = 4
	}

	var result types.ResearchV2WorkflowResult
	err := workflow.ExecuteChildWorkflow(cctx, ResearchSynthesisV2WorkflowName, types.ResearchV2WorkflowInput{
		TaskID:     workflowID,
		WorkflowID: workflowID,
		RunID:      runID,
		SessionID:  input.SessionID,
		Query:      input.Query,
		Config: types.ResearchV2Config{
			MaxSources:                    maxSources,
			MaxEvidenceItems:              10,
			MaxSubqueries:                 3,
			MaxIterations:                 1,
			TokenBudget:                   decision.TokenBudget,
			CredibilityThreshold:          0.4,
			EnableContradictionDetection:  true,
			RequireCitations:              true,
			RequireApprovalBeforePublish:  false,
			SourceTypes:                   []string{types.SourceTypeLocalRAG},
			ModelTier:                     "small",
			MockLLM:                       false, // Activities decide mock vs real
		},
	}).Get(cctx, &result)
	if err != nil {
		return &types.RoutedExecutionResult{
			SessionID:  input.SessionID,
			WorkflowID: workflowID,
			RunID:      runID,
			Decision:   decision,
			Status:     types.RoutedStatusError,
			Reason:     fmt.Sprintf("research_v2_workflow_failed: %v", err),
		}, err
	}

	finalText := result.FinalAnswerText
	if len(finalText) > 2000 {
		finalText = truncateTo(finalText, 2000)
	}
	return &types.RoutedExecutionResult{
		SessionID:       input.SessionID,
		WorkflowID:      workflowID,
		RunID:           runID,
		Decision:        decision,
		FinalAnswerRef:  result.FinalAnswerRef,
		FinalAnswerText: finalText,
		Status:          types.RoutedStatusOK,
		CostUSD:         decision.CostBudgetUSD,
		TokensUsed:      result.TotalTokens,
		Provider:        result.Provider,
		ModelUsed:       result.ModelUsed,
		Mode:            result.Mode,
		Mock:            result.Mock,
		FallbackUsed:    result.FallbackUsed,
		LLMCalls:        result.LLMCalls,
		Metadata: map[string]interface{}{
			"research_v2_source_count":        result.SourceCount,
			"research_v2_evidence_count":      result.EvidenceCount,
			"research_v2_contradiction_count":  result.ContradictionCount,
			"research_v2_report_ref":          result.ReportRef,
			"research_v2_evidence_ref":        result.EvidenceRef,
			"research_v2_synthesis_ref":       result.SynthesisRef,
			"research_v2_final_answer_ref":    result.FinalAnswerRef,
			"research_v2_workspace_topic":     result.WorkspaceTopic,
		},
	}, nil
}
