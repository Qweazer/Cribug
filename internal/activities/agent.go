package activities

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	hookspkg "cribug/internal/hooks"
	"cribug/internal/types"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
)

type AgentActivities struct {
	llmServiceURL string
	httpClient    *http.Client
	hookRuntime   *hookspkg.HookRuntime
}

func NewAgentActivities(llmServiceURL string) *AgentActivities {
	return &AgentActivities{
		llmServiceURL: llmServiceURL,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// SetHookRuntime injects the hook runtime for inline LLM hooks.
func (a *AgentActivities) SetHookRuntime(runtime *hookspkg.HookRuntime) {
	a.hookRuntime = runtime
}

type AgentActivityInput struct {
	TaskID                string
	WorkflowID            string
	RunID                 string
	Query                 string
	SessionID             string
	Model                 string
	Temperature           float64
	MaxCompletionTokens   int
	SessionMessages       []types.LLMMessage
	AllowedCompletionTokens int
}

type AgentActivityOutput struct {
	Answer       string
	Usage        types.Usage
	Model        string
	Provider     string
	LatencyMS    int64
	FinishReason string
}

func (a *AgentActivities) CallLLM(ctx context.Context, input AgentActivityInput) (*AgentActivityOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("AgentActivity started", "task_id", input.TaskID, "model", input.Model,
		"session_messages", len(input.SessionMessages), "allowed_completion", input.AllowedCompletionTokens)

	// Inline before_llm_call hook
	if a.hookRuntime != nil {
		event := hookspkg.HookEvent{
			EventID:         uuid.New().String(),
			HookPoint:       hookspkg.HookPointBeforeLLMCall,
			AgentID:         input.TaskID,
			WorkflowID:      input.WorkflowID,
			TenantID:        "00000000-0000-0000-0000-000000000000",
			Timestamp:       time.Now().UTC(),
			SourceComponent: "llm",
			Payload: map[string]interface{}{
				"model":     input.Model,
				"provider":  "openai_compatible",
				"msg_count": len(input.SessionMessages) + 1,
			},
		}
		decision := a.hookRuntime.EmitAndExecute(ctx, event)
		if decision.Denied {
			return nil, fmt.Errorf("%s: %s", decision.RejectCode, decision.RejectReason)
		}
	}

	// Check budget
	if input.AllowedCompletionTokens <= 0 {
		return nil, fmt.Errorf("allowed_completion_tokens is 0, budget exceeded")
	}

	// Build messages: session messages + current query
	messages := make([]types.LLMMessage, 0, len(input.SessionMessages)+1)
	messages = append(messages, input.SessionMessages...)
	messages = append(messages, types.LLMMessage{Role: "user", Content: input.Query})

	reqBody := types.LLMRequest{
		TraceID:             input.TaskID,
		TaskID:              input.TaskID,
		Provider:            "openai_compatible",
		Model:               input.Model,
		Messages:            messages,
		Temperature:         input.Temperature,
		MaxCompletionTokens: input.AllowedCompletionTokens,
	}
	if input.SessionID != "" {
		reqBody.SessionID = &input.SessionID
	}
	reqBody.Metadata = map[string]any{
		"workflow_id": input.WorkflowID,
		"run_id":      input.RunID,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := a.httpClient.Post(
		a.llmServiceURL+"/chat",
		"application/json",
		bytes.NewBuffer(jsonBody),
	)
	if err != nil {
		log.Printf("[ERROR] AgentActivity: HTTP call failed: %v", err)
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm service returned status %d", resp.StatusCode)
	}

	var llmResp types.LLMResponse
	if err := json.NewDecoder(resp.Body).Decode(&llmResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if llmResp.Error != nil && *llmResp.Error != "" {
		return nil, fmt.Errorf("llm error: %s", *llmResp.Error)
	}

	if llmResp.Content == "" {
		return nil, fmt.Errorf("empty content from llm")
	}

	// Inline after_llm_call hook
	if a.hookRuntime != nil {
		event := hookspkg.HookEvent{
			EventID:         uuid.New().String(),
			HookPoint:       hookspkg.HookPointAfterLLMCall,
			AgentID:         input.TaskID,
			WorkflowID:      input.WorkflowID,
			TenantID:        "00000000-0000-0000-0000-000000000000",
			Timestamp:       time.Now().UTC(),
			SourceComponent: "llm",
			Payload: map[string]interface{}{
				"model":         llmResp.Model,
				"provider":      llmResp.Provider,
				"token_count":   llmResp.Usage.TotalTokens,
				"latency_ms":    llmResp.LatencyMS,
				"finish_reason": llmResp.FinishReason,
			},
		}
		a.hookRuntime.EmitAndExecute(ctx, event)
		// after_llm_call is always non-blocking; ignore decision
	}

	logger.Info("AgentActivity completed", "task_id", input.TaskID, "latency_ms", llmResp.LatencyMS)

	return &AgentActivityOutput{
		Answer:       llmResp.Content,
		Usage:        llmResp.Usage,
		Model:        llmResp.Model,
		Provider:     llmResp.Provider,
		LatencyMS:    llmResp.LatencyMS,
		FinishReason: llmResp.FinishReason,
	}, nil
}