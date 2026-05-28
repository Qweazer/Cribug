package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	hookspkg "cribug/internal/hooks"
	"cribug/internal/llm"
	"cribug/internal/types"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.temporal.io/sdk/activity"
)

type ReActActivities struct {
	llmClient   *llm.Client
	redisClient *redis.Client
	hookRuntime *hookspkg.HookRuntime
}

func NewReActActivities(llmServiceURL string, redisAddr, redisPass string, redisDB int) *ReActActivities {
	return &ReActActivities{
		llmClient:   llm.NewClient(llmServiceURL),
		redisClient: redis.NewClient(&redis.Options{
			Addr:     redisAddr,
			Password: redisPass,
			DB:       redisDB,
		}),
	}
}

// SetHookRuntime injects the hook runtime for inline LLM hooks.
func (a *ReActActivities) SetHookRuntime(runtime *hookspkg.HookRuntime) {
	a.hookRuntime = runtime
}

type ExecuteReActNodeInput struct {
	TaskID       string
	NodeID       string
	Prompt       string
	Model        string
	Temperature  float64
	MaxTokens    int
	ReactConfig  types.ReactLoopConfig
	TTLSeconds   int
}

type ExecuteReActNodeOutput struct {
	Result *types.ReactResult
	Usage  *types.Usage
}

func (a *ReActActivities) ExecuteReActNode(ctx context.Context, input ExecuteReActNodeInput) (*ExecuteReActNodeOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("ExecuteReActNodeActivity started",
		"task_id", input.TaskID,
		"node_id", input.NodeID,
		"enable_react", input.ReactConfig.EnableReAct)

	// Non-ReAct mode: just call LLM once
	if !input.ReactConfig.EnableReAct {
		return a.executeSimpleNode(ctx, input)
	}

	// ReAct mode: reasoning loop
	return a.executeReActLoop(ctx, input)
}

func (a *ReActActivities) executeSimpleNode(ctx context.Context, input ExecuteReActNodeInput) (*ExecuteReActNodeOutput, error) {
	messages := []types.LLMMessage{
		{Role: "user", Content: input.Prompt},
	}

	// Inline before_llm_call hook
	if a.hookRuntime != nil {
		event := hookspkg.HookEvent{
			EventID:         uuid.New().String(),
			HookPoint:       hookspkg.HookPointBeforeLLMCall,
			AgentID:         input.TaskID,
			WorkflowID:      "",
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
		MaxCompletionTokens:  input.MaxTokens,
	})

	if err != nil {
		return nil, fmt.Errorf("llm call failed: %w", err)
	}

	// Inline after_llm_call hook (non-blocking, fire-and-forget)
	if a.hookRuntime != nil {
		event := hookspkg.HookEvent{
			EventID:         uuid.New().String(),
			HookPoint:       hookspkg.HookPointAfterLLMCall,
			AgentID:         input.TaskID,
			WorkflowID:      "",
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

	return &ExecuteReActNodeOutput{
		Result: &types.ReactResult{
			FinalAnswer:    resp.Content,
			IterationsUsed: 0,
			Steps:          nil,
		},
		Usage: &resp.Usage,
	}, nil
}

func (a *ReActActivities) executeReActLoop(ctx context.Context, input ExecuteReActNodeInput) (*ExecuteReActNodeOutput, error) {
	logger := activity.GetLogger(ctx)

	// Initialize messages with system prompt and task
	systemPrompt := `You are a ReAct agent. For each iteration:
1. THINK: Analyze the current state and determine what to do next
2. ACT: Either call a tool (format: TOOL:tool_name:args_json) or provide text response
3. OBSERVE: Wait for the observation to continue reasoning

Continue until you have a final answer. Format your final answer as: FINAL: your answer`

	messages := []types.LLMMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: input.Prompt},
	}

	var steps []types.ReactStep
	maxIterations := input.ReactConfig.MaxIterations
	if maxIterations <= 0 {
		maxIterations = 3
	}
	if maxIterations > 10 {
		maxIterations = 10
	}

	for i := 0; i < maxIterations; i++ {
		iteration := i + 1
		logger.Info("ReAct iteration", "iteration", iteration, "max", maxIterations)

		// Inline before_llm_call hook
		if a.hookRuntime != nil {
			event := hookspkg.HookEvent{
				EventID:         uuid.New().String(),
				HookPoint:       hookspkg.HookPointBeforeLLMCall,
				AgentID:         input.TaskID,
				WorkflowID:      "",
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
				logger.Error("ReAct LLM call denied by hook", "iteration", iteration, "code", decision.RejectCode, "reason", decision.RejectReason)
				return nil, fmt.Errorf("%s: %s", decision.RejectCode, decision.RejectReason)
			}
		}

		// Call LLM
		resp, err := a.llmClient.Call(ctx, llm.CallRequest{
			TaskID:              input.TaskID,
			Provider:            "openai_compatible",
			Model:               input.Model,
			Messages:            messages,
			Temperature:         input.Temperature,
			MaxCompletionTokens:  input.MaxTokens,
		})

		if err != nil {
			logger.Error("ReAct LLM call failed", "iteration", iteration, "error", err)
			// Return what we have so far
			break
		}

		// Parse response to extract thought/action/observation
		thought, action, observation := parseReActResponse(resp.Content)

		// Create step record
		step := types.ReactStep{
			Iteration:   iteration,
			Thought:     thought,
			Action:      action,
			Observation: observation,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
		}
		steps = append(steps, step)

		// Async write to Redis (goroutine, non-blocking)
		go func(s types.ReactStep) {
			if a.redisClient != nil {
				key := fmt.Sprintf("react:%s:%s:steps", input.TaskID, input.NodeID)
				data, _ := json.Marshal(s)
				pipe := a.redisClient.Pipeline()
				pipe.RPush(context.Background(), key, string(data))
				pipe.Expire(context.Background(), key, time.Duration(input.TTLSeconds)*time.Second)
				if _, err := pipe.Exec(context.Background()); err != nil {
					log.Printf("Failed to write ReAct step to Redis: %v", err)
				}
			}
		}(step)

		// Update messages for next iteration
		messages = append(messages,
			types.LLMMessage{Role: "assistant", Content: thought + "\n" + action},
			types.LLMMessage{Role: "user", Content: observation},
		)

		// Early stop check
		if input.ReactConfig.EarlyStopOnAnswer && isFinalAnswer(action) {
			logger.Info("ReAct early stop", "iteration", iteration, "reason", "final_answer_found")
			break
		}
	}

	// Extract final answer
	finalAnswer := parseFinalAnswer(steps)

	// Calculate total usage
	var totalUsage types.Usage
	// Usage would need to be accumulated from LLM calls

	return &ExecuteReActNodeOutput{
		Result: &types.ReactResult{
			Steps:          steps,
			FinalAnswer:    finalAnswer,
			IterationsUsed: len(steps),
		},
		Usage: &totalUsage,
	}, nil
}

// parseReActResponse extracts thought, action, and observation from LLM response
func parseReActResponse(content string) (thought, action, observation string) {
	// Simple parsing - look for FINAL: marker first
	if strings.Contains(content, "FINAL:") {
		idx := strings.Index(content, "FINAL:")
		action = content[idx:] // Everything from FINAL: onward is the action
		thought = content[:idx]
		return
	}

	// No FINAL marker, treat entire response as final answer
	action = content
	return
}

// isFinalAnswer checks if the action contains a final answer
func isFinalAnswer(action string) bool {
	return strings.Contains(strings.ToUpper(action), "FINAL:")
}

// parseFinalAnswer extracts the final answer from steps
func parseFinalAnswer(steps []types.ReactStep) string {
	for i := len(steps) - 1; i >= 0; i-- {
		step := steps[i]
		if strings.Contains(strings.ToUpper(step.Action), "FINAL:") {
			// Extract text after FINAL:
			idx := strings.Index(step.Action, "FINAL:")
			answer := strings.TrimSpace(step.Action[idx+6:])
			// Also include observation if present
			if step.Observation != "" {
				return answer + "\n" + step.Observation
			}
			return answer
		}
	}
	// No FINAL marker found, use last observation or last action
	if len(steps) > 0 {
		lastStep := steps[len(steps)-1]
		if lastStep.Observation != "" {
			return lastStep.Observation
		}
		return lastStep.Action
	}
	return ""
}