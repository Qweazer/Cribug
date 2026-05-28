package activities

import (
	"context"
	"fmt"
	"time"

	hookspkg "cribug/internal/hooks"
	"cribug/internal/llm"
	"cribug/internal/types"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
)

type SwarmActivities struct {
	llmClient   *llm.Client
	hookRuntime *hookspkg.HookRuntime
}

func NewSwarmActivities(llmServiceURL string) *SwarmActivities {
	return &SwarmActivities{llmClient: llm.NewClient(llmServiceURL)}
}

// SetHookRuntime injects the hook runtime for inline LLM hooks.
func (a *SwarmActivities) SetHookRuntime(runtime *hookspkg.HookRuntime) {
	a.hookRuntime = runtime
}

func (a *SwarmActivities) WorkerAgent(ctx context.Context, input types.WorkerAgentInput) (*types.WorkerAgentResult, error) {
	logger := activity.GetLogger(ctx)
	start := time.Now()

	logger.Info("WorkerAgentActivity started",
		"agent_id", input.AgentID, "role", input.Role,
		"round", input.Round, "inbox", len(input.InboxMessages))

	// Build system message with role context
	systemContent := fmt.Sprintf("You are a %s agent.", input.Role)
	if input.RetrievalContext != "" {
		systemContent += fmt.Sprintf("\n\nUse the following retrieved context to inform your analysis:\n%s", input.RetrievalContext)
	}

	// Build prompt with inbox context
	prompt := input.Task
	if len(input.InboxMessages) > 0 {
		prompt += "\n\nInbox messages from other agents:\n"
		for _, m := range input.InboxMessages {
			prompt += fmt.Sprintf("[%s→%s, type=%s] %s\n", m.FromRole, m.ToRole, m.MessageType, m.Content)
		}
	}

	messages := []types.LLMMessage{
		{Role: "system", Content: systemContent},
		{Role: "user", Content: prompt},
	}

	// Inline before_llm_call hook
	if a.hookRuntime != nil {
		event := hookspkg.HookEvent{
			EventID:         uuid.New().String(),
			HookPoint:       hookspkg.HookPointBeforeLLMCall,
			AgentID:         input.AgentID,
			WorkflowID:      input.WorkflowID,
			TenantID:        "00000000-0000-0000-0000-000000000000",
			Timestamp:       time.Now().UTC(),
			SourceComponent: "llm",
			Payload: map[string]interface{}{
				"model":     input.Model,
				"provider":  "openai_compatible",
				"msg_count": len(messages),
			},
		}
		decision := a.hookRuntime.EmitAndExecute(ctx, event)
		if decision.Denied {
			return nil, fmt.Errorf("%s: %s", decision.RejectCode, decision.RejectReason)
		}
	}

	resp, err := a.llmClient.Call(ctx, llm.CallRequest{
		TaskID:              input.TaskID,
		Provider:            "openai_compatible",
		Model:               input.Model,
		Messages:            messages,
		Temperature:         input.Temperature,
		MaxCompletionTokens: input.MaxTokens,
	})

	latency := time.Since(start).Milliseconds()

	if err != nil {
		logger.Error("WorkerAgentActivity failed", "agent_id", input.AgentID, "error", err)
		return &types.WorkerAgentResult{
			AgentID:  input.AgentID, Role: input.Role,
			Status: types.WorkerStatusFailed, Error: err.Error(), LatencyMs: latency,
		}, nil
	}

	// Inline after_llm_call hook (non-blocking, fire-and-forget)
	if a.hookRuntime != nil {
		event := hookspkg.HookEvent{
			EventID:         uuid.New().String(),
			HookPoint:       hookspkg.HookPointAfterLLMCall,
			AgentID:         input.AgentID,
			WorkflowID:      input.WorkflowID,
			TenantID:        "00000000-0000-0000-0000-000000000000",
			Timestamp:       time.Now().UTC(),
			SourceComponent: "llm",
			Payload: map[string]interface{}{
				"model":         resp.Model,
				"provider":      resp.Provider,
				"token_count":   resp.Usage.TotalTokens,
				"latency_ms":    resp.LatencyMS,
				"finish_reason": resp.FinishReason,
			},
		}
		a.hookRuntime.EmitAndExecute(ctx, event)
		// after_llm_call is always non-blocking; ignore decision
	}

	// Generate deterministic outbound messages based on role (Phase 5B)
	outbound, wsAppends := generateOutboundMessages(input, resp.Content)

	// Generate handoff request for researcher role (Phase 5G)
	var handoff *types.HandoffRequest
	if input.Role == "researcher" && input.Round == 0 {
		handoff = &types.HandoffRequest{
			WorkflowID: input.WorkflowID, TaskID: input.TaskID,
			SourceAgentID: input.AgentID, TargetAgentID: "worker-2",
			Reason: "handing off research to critic",
			ContextSnapshot: abbreviate(resp.Content, 200),
			PartialResult: abbreviate(resp.Content, 200),
		}
	}

	logger.Info("WorkerAgentActivity completed",
		"agent_id", input.AgentID, "role", input.Role,
		"tokens", resp.Usage.TotalTokens, "outbound", len(outbound))

	return &types.WorkerAgentResult{
		AgentID:          input.AgentID,
		Role:             input.Role,
		Status:           types.WorkerStatusCompleted,
		Result:           resp.Content,
		LatencyMs:        latency,
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
		OutboundMessages: outbound,
		InboxCount:       len(input.InboxMessages),
		OutboxCount:      len(outbound),
				WorkspaceAppends:  wsAppends,
				WorkspaceReads:    collectWorkspaceReads(input),
				WorkspaceUsedIDs:  collectWorkspaceReads(input),
				HandoffRequest:    handoff,
	}, nil
}

// generateOutboundMessages creates deterministic P2P messages based on agent role.
// This is a deterministic fixture — doesn't depend on LLM output precision.
func generateOutboundMessages(input types.WorkerAgentInput, llmOutput string) ([]types.AgentMessage, []types.WorkspaceItem) {
	// Only generate outbound in round 1 (not recursive)
	if input.Round != 0 {
		return nil, nil
	}

	msgID := func(idx int) string {
		return fmt.Sprintf("%s-r%d-%s-%d", input.WorkflowID, input.Round, input.AgentID, idx)
	}

	var out []types.AgentMessage
	var ws []types.WorkspaceItem

	switch input.Role {
	case "researcher":
	wi := types.WorkspaceItem{
		ItemID: fmt.Sprintf("%s-ws-r%d-%s-1", input.WorkflowID, input.Round, input.AgentID),
		WorkflowID: input.WorkflowID, TaskID: input.TaskID,
		AgentID: input.AgentID, Role: input.Role, ItemType: types.WSTypeObservation,
		Title: "Research Findings", Content: abbreviate(llmOutput, 300),
		Round: input.Round, Status: types.WSStatusCreated,
	}
	ws = append(ws, wi)
		out = append(out, types.AgentMessage{
			MessageID: msgID(1), WorkflowID: input.WorkflowID, TaskID: input.TaskID,
			FromAgentID: input.AgentID, ToAgentID: "worker-2", FromRole: "researcher", ToRole: "critic",
			MessageType: types.MsgTypeObservation, Content: "Research findings: " + abbreviate(llmOutput, 200),
			Status: types.MsgStatusCreated, Round: input.Round,
		})
	case "analyst":
		out = append(out, types.AgentMessage{
			MessageID: msgID(1), WorkflowID: input.WorkflowID, TaskID: input.TaskID,
			FromAgentID: input.AgentID, ToAgentID: "worker-3", FromRole: "analyst", ToRole: "synthesizer",
			MessageType: types.MsgTypeObservation, Content: "Analysis results: " + abbreviate(llmOutput, 200),
			Status: types.MsgStatusCreated, Round: input.Round,
		})
	case "critic":
	wi := types.WorkspaceItem{
		ItemID: fmt.Sprintf("%s-ws-r%d-%s-1", input.WorkflowID, input.Round, input.AgentID),
		WorkflowID: input.WorkflowID, TaskID: input.TaskID,
		AgentID: input.AgentID, Role: input.Role, ItemType: types.WSTypeCritique,
		Title: "Critique", Content: abbreviate(llmOutput, 300),
		Round: input.Round, Status: types.WSStatusCreated,
	}
	if len(input.WorkspaceItems) > 0 {
		wi.ParentItemID = input.WorkspaceItems[0].ItemID
	}
	ws = append(ws, wi)
		out = append(out, types.AgentMessage{
			MessageID: msgID(1), WorkflowID: input.WorkflowID, TaskID: input.TaskID,
			FromAgentID: input.AgentID, ToAgentID: "worker-3", FromRole: "critic", ToRole: "synthesizer",
			MessageType: types.MsgTypeCritique, Content: "Critique: " + abbreviate(llmOutput, 200),
			Status: types.MsgStatusCreated, Round: input.Round,
		})
	case "synthesizer":
	wi := types.WorkspaceItem{
		ItemID: fmt.Sprintf("%s-ws-r%d-%s-1", input.WorkflowID, input.Round, input.AgentID),
		WorkflowID: input.WorkflowID, TaskID: input.TaskID,
		AgentID: input.AgentID, Role: input.Role, ItemType: types.WSTypeFinal,
		Title: "Final Synthesis", Content: abbreviate(llmOutput, 300),
		Round: input.Round, Status: types.WSStatusCreated,
	}
	ws = append(ws, wi)
		out = append(out, types.AgentMessage{
			MessageID: msgID(1), WorkflowID: input.WorkflowID, TaskID: input.TaskID,
			FromAgentID: input.AgentID, ToAgentID: "", FromRole: "synthesizer", ToRole: "lead",
			MessageType: types.MsgTypeFinal, Content: "Synthesis: " + abbreviate(llmOutput, 200),
			Status: types.MsgStatusCreated, Round: input.Round,
		})
	case "reviewer":
		out = append(out, types.AgentMessage{
			MessageID: msgID(1), WorkflowID: input.WorkflowID, TaskID: input.TaskID,
			FromAgentID: input.AgentID, ToAgentID: "worker-3", FromRole: "reviewer", ToRole: "synthesizer",
			MessageType: types.MsgTypeCritique, Content: "Review: " + abbreviate(llmOutput, 200),
			Status: types.MsgStatusCreated, Round: input.Round,
		})
	}
	return out, ws
}

func abbreviate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// AuthorizeTeamAction enforces role-based access control (Phase 5F)
func (a *SwarmActivities) AuthorizeTeamAction(ctx context.Context, input types.TeamActionInput) (*types.TeamActionDecision, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("AuthorizeTeamAction", "agent", input.AgentID, "role", input.AgentRole, "action", input.Action)

	allowed := false
	reason := ""

	switch input.AgentRole {
	case "lead":
		allowed = true
	case "researcher", "analyst", "critic", "synthesizer", "reviewer":
		switch input.Action {
		case "execute_task", "send_message", "modify_workspace":
			allowed = true
		default:
			reason = fmt.Sprintf("action '%s' not allowed for worker role '%s'", input.Action, input.AgentRole)
		}
	default:
		reason = fmt.Sprintf("unknown role '%s'", input.AgentRole)
	}

	return &types.TeamActionDecision{Allowed: allowed, Reason: reason}, nil
}

func collectWorkspaceReads(input types.WorkerAgentInput) []string {
	var ids []string
	for _, w := range input.WorkspaceItems {
		ids = append(ids, w.ItemID)
	}
	return ids
}
