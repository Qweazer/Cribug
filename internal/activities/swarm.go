package activities

import (
	"context"
	"fmt"
	"time"

	"cribug/internal/llm"
	"cribug/internal/types"

	"go.temporal.io/sdk/activity"
)

// SwarmActivities handles SwarmWorkflow worker agent execution (Phase 5A Slice 10)
type SwarmActivities struct {
	llmClient *llm.Client
}

func NewSwarmActivities(llmServiceURL string) *SwarmActivities {
	return &SwarmActivities{
		llmClient: llm.NewClient(llmServiceURL),
	}
}

// WorkerAgentActivity executes a single worker agent task within a SwarmWorkflow.
// Each worker gets an independent LLM call (or mock, depending on LLM service mode).
func (a *SwarmActivities) WorkerAgent(ctx context.Context, input types.WorkerAgentInput) (*types.WorkerAgentResult, error) {
	logger := activity.GetLogger(ctx)
	start := time.Now()

	logger.Info("WorkerAgentActivity started",
		"agent_id", input.AgentID,
		"role", input.Role,
		"task_len", len(input.Task))

	messages := []types.LLMMessage{
		{Role: "system", Content: fmt.Sprintf("You are a %s agent. %s", input.Role, input.Task)},
		{Role: "user", Content: input.Task},
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
			AgentID:   input.AgentID,
			Role:      input.Role,
			Status:    types.WorkerStatusFailed,
			Error:     err.Error(),
			LatencyMs: latency,
		}, nil // Don't return error — SwarmWorkflow handles it via status
	}

	logger.Info("WorkerAgentActivity completed",
		"agent_id", input.AgentID,
		"role", input.Role,
		"tokens", resp.Usage.TotalTokens,
		"latency_ms", latency)

	return &types.WorkerAgentResult{
		AgentID:          input.AgentID,
		Role:             input.Role,
		Status:           types.WorkerStatusCompleted,
		Result:           resp.Content,
		LatencyMs:        latency,
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}, nil
}
