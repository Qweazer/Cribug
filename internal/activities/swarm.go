package activities

import (
	"context"
	"fmt"
	"time"

	"cribug/internal/llm"
	"cribug/internal/types"

	"go.temporal.io/sdk/activity"
)

type SwarmActivities struct {
	llmClient *llm.Client
}

func NewSwarmActivities(llmServiceURL string) *SwarmActivities {
	return &SwarmActivities{llmClient: llm.NewClient(llmServiceURL)}
}

func (a *SwarmActivities) WorkerAgent(ctx context.Context, input types.WorkerAgentInput) (*types.WorkerAgentResult, error) {
	logger := activity.GetLogger(ctx)
	start := time.Now()

	logger.Info("WorkerAgentActivity started",
		"agent_id", input.AgentID, "role", input.Role,
		"round", input.Round, "inbox", len(input.InboxMessages))

	// Build prompt with inbox context
	prompt := input.Task
	if len(input.InboxMessages) > 0 {
		prompt += "\n\nInbox messages from other agents:\n"
		for _, m := range input.InboxMessages {
			prompt += fmt.Sprintf("[%s→%s, type=%s] %s\n", m.FromRole, m.ToRole, m.MessageType, m.Content)
		}
	}

	messages := []types.LLMMessage{
		{Role: "system", Content: fmt.Sprintf("You are a %s agent.", input.Role)},
		{Role: "user", Content: prompt},
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

	// Generate deterministic outbound messages based on role (Phase 5B)
	outbound := generateOutboundMessages(input, resp.Content)

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
	}, nil
}

// generateOutboundMessages creates deterministic P2P messages based on agent role.
// This is a deterministic fixture — doesn't depend on LLM output precision.
func generateOutboundMessages(input types.WorkerAgentInput, llmOutput string) []types.AgentMessage {
	// Only generate outbound in round 1 (not recursive)
	if input.Round != 0 {
		return nil
	}

	msgID := func(idx int) string {
		return fmt.Sprintf("%s-r%d-%s-%d", input.WorkflowID, input.Round, input.AgentID, idx)
	}

	var out []types.AgentMessage

	switch input.Role {
	case "researcher":
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
		out = append(out, types.AgentMessage{
			MessageID: msgID(1), WorkflowID: input.WorkflowID, TaskID: input.TaskID,
			FromAgentID: input.AgentID, ToAgentID: "worker-3", FromRole: "critic", ToRole: "synthesizer",
			MessageType: types.MsgTypeCritique, Content: "Critique: " + abbreviate(llmOutput, 200),
			Status: types.MsgStatusCreated, Round: input.Round,
		})
	case "synthesizer":
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
	return out
}

func abbreviate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
