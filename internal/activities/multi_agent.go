package activities

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"cribug/internal/types"

	"go.temporal.io/sdk/activity"
)

type MultiAgentActivities struct {
	llmServiceURL string
	httpClient    *http.Client
	toolActivities *ToolActivities
}

func NewMultiAgentActivities(llmServiceURL string) *MultiAgentActivities {
	return &MultiAgentActivities{
		llmServiceURL: llmServiceURL,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		toolActivities: NewToolActivities(),
	}
}

func (a *MultiAgentActivities) RunAgent(ctx context.Context, input types.RunAgentActivityInput) (*types.RunAgentActivityOutput, error) {
	log.Printf("[INFO] RunAgentActivity: task=%s role=%s", input.TaskID, input.Role)

	step := types.AgentStep{
		TaskID: input.TaskID,
		Role:   input.Role,
		Input:  input.Query,
		Status: "completed",
	}

	switch input.Role {
	case types.AgentRolePlanner:
		step.Output = fmt.Sprintf("planned approach for task %s", input.TaskID)
	case types.AgentRoleResearcher:
		step.Output = fmt.Sprintf("researched context for task %s", input.TaskID)
	case types.AgentRoleCritic:
		step.Output = fmt.Sprintf("reviewed draft for task %s", input.TaskID)
	case types.AgentRoleSynthesizer:
		// Synthesizer is handled by RunSynthesizerAgent - should not reach here
		step.Output = "synthesizer should use RunSynthesizerAgent"
		step.Status = "failed"
		step.Error = "synthesizer must use RunSynthesizerAgent"
	default:
		step.Output = fmt.Sprintf("executed %s role for task %s", input.Role, input.TaskID)
	}

	log.Printf("[INFO] RunAgentActivity completed: task=%s role=%s output=%s", input.TaskID, input.Role, step.Output)

	return &types.RunAgentActivityOutput{Step: step}, nil
}

// RunSynthesizerAgent calls the Python LLM Service to synthesize from all agent outputs
func (a *MultiAgentActivities) RunSynthesizerAgent(ctx context.Context, input types.RunSynthesizerAgentInput) (*types.RunSynthesizerAgentOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("RunSynthesizerAgent started", "task_id", input.TaskID, "model", input.Model)

	// Check budget first - must be before LLM call
	if input.MaxCompletionTokens <= 0 {
		logger.Error("RunSynthesizerAgent: budget exceeded (max_completion_tokens=0)")
		return nil, fmt.Errorf("budget exceeded: max_completion_tokens is 0")
	}

	// Build synthesis prompt combining all agent outputs
	synthesisPrompt := fmt.Sprintf(`You are a synthesizer agent. Combine the following agent outputs into a final answer.

Original Query: %s

Planner Output: %s

Researcher Output: %s

Critic Output: %s

Please provide a comprehensive final answer that synthesizes all the above inputs.`, input.Query, input.PlannerOutput, input.ResearcherOutput, input.CriticOutput)

	messages := []types.LLMMessage{
		{Role: "system", Content: "You are a synthesizer agent. Combine multiple agent outputs into a final comprehensive answer."},
		{Role: "user", Content: synthesisPrompt},
	}

	reqBody := types.LLMRequest{
		TraceID:             input.TaskID,
		TaskID:              input.TaskID,
		Provider:            "openai_compatible",
		Model:               input.Model,
		Messages:            messages,
		Temperature:         input.Temperature,
		MaxCompletionTokens: input.MaxCompletionTokens,
	}
	reqBody.Metadata = map[string]any{
		"workflow_id": input.WorkflowID,
		"run_id":      input.RunID,
		"agent_role":  "synthesizer",
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	startTime := time.Now()

	resp, err := a.httpClient.Post(
		a.llmServiceURL+"/chat",
		"application/json",
		bytes.NewBuffer(jsonBody),
	)
	if err != nil {
		logger.Error("RunSynthesizerAgent: HTTP call failed", "error", err)
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

	latencyMS := time.Since(startTime).Milliseconds()

	logger.Info("RunSynthesizerAgent completed", "task_id", input.TaskID, "tokens", llmResp.Usage.TotalTokens, "latency_ms", latencyMS)

	return &types.RunSynthesizerAgentOutput{
		Step: types.AgentStep{
			TaskID:          input.TaskID,
			Role:            types.AgentRoleSynthesizer,
			Input:           synthesisPrompt,
			Output:          llmResp.Content,
			Status:          "completed",
			PromptTokens:    llmResp.Usage.PromptTokens,
			CompletionTokens: llmResp.Usage.CompletionTokens,
			TotalTokens:     llmResp.Usage.TotalTokens,
		},
		LLMOutput:         llmResp.Content,
		PromptTokens:      llmResp.Usage.PromptTokens,
		CompletionTokens: llmResp.Usage.CompletionTokens,
		TotalTokens:      llmResp.Usage.TotalTokens,
		LatencyMS:        latencyMS,
	}, nil
}

// RunCriticAgent calls the Python LLM Service to critique the current answer
func (a *MultiAgentActivities) RunCriticAgent(ctx context.Context, input types.RunCriticAgentInput) (*types.RunCriticAgentOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("RunCriticAgent started", "task_id", input.TaskID, "model", input.Model)

	// Check budget first - must be before LLM call
	if input.MaxCompletionTokens <= 0 {
		logger.Error("RunCriticAgent: budget exceeded (max_completion_tokens=0)")
		return nil, fmt.Errorf("budget exceeded: max_completion_tokens is 0")
	}

	// Build critic prompt including original query, planner/researcher output, and current answer
	criticPrompt := fmt.Sprintf(`You are a critic agent. Review the candidate answer and provide constructive feedback.

Original Query: %s

Planner Output: %s

Researcher Output: %s

Current Answer to Review: %s

Please identify:
1. Strengths of the current answer
2. Gaps, risks, or weak reasoning
3. Concrete improvements

Be concise but specific.`, input.Query, input.PlannerOutput, input.ResearcherOutput, input.CurrentAnswer)

	messages := []types.LLMMessage{
		{Role: "system", Content: "You are a critic agent. Review the candidate answer and identify gaps, risks, weak reasoning, and concrete improvements. Be concise but specific."},
		{Role: "user", Content: criticPrompt},
	}

	reqBody := types.LLMRequest{
		TraceID:             input.TaskID,
		TaskID:              input.TaskID,
		Provider:            "openai_compatible",
		Model:               input.Model,
		Messages:            messages,
		Temperature:         input.Temperature,
		MaxCompletionTokens: input.MaxCompletionTokens,
		Role:                "critic",
	}
	reqBody.Metadata = map[string]any{
		"workflow_id": input.WorkflowID,
		"run_id":      input.RunID,
		"agent_role":  "critic",
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	startTime := time.Now()

	resp, err := a.httpClient.Post(
		a.llmServiceURL+"/chat",
		"application/json",
		bytes.NewBuffer(jsonBody),
	)
	if err != nil {
		logger.Error("RunCriticAgent: HTTP call failed", "error", err)
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

	latencyMS := time.Since(startTime).Milliseconds()

	logger.Info("RunCriticAgent completed", "task_id", input.TaskID, "tokens", llmResp.Usage.TotalTokens, "latency_ms", latencyMS)

	return &types.RunCriticAgentOutput{
		Step: types.AgentStep{
			TaskID:          input.TaskID,
			Role:            types.AgentRoleCritic,
			Input:           criticPrompt,
			Output:          llmResp.Content,
			Status:          "completed",
			PromptTokens:    llmResp.Usage.PromptTokens,
			CompletionTokens: llmResp.Usage.CompletionTokens,
			TotalTokens:     llmResp.Usage.TotalTokens,
		},
		LLMOutput:         llmResp.Content,
		PromptTokens:      llmResp.Usage.PromptTokens,
		CompletionTokens: llmResp.Usage.CompletionTokens,
		TotalTokens:      llmResp.Usage.TotalTokens,
		LatencyMS:        latencyMS,
	}, nil
}

// RunResearcherAgent calls the Python LLM Service to research and gather information
func (a *MultiAgentActivities) RunResearcherAgent(ctx context.Context, input types.RunResearcherAgentInput) (*types.RunResearcherAgentOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("RunResearcherAgent started", "task_id", input.TaskID, "model", input.Model)

	// Check budget first - must be before LLM call
	if input.MaxCompletionTokens <= 0 {
		logger.Error("RunResearcherAgent: budget exceeded (max_completion_tokens=0)")
		return nil, fmt.Errorf("budget exceeded: max_completion_tokens is 0")
	}

	// Build researcher prompt
	researcherPrompt := fmt.Sprintf(`You are a researcher agent. Gather relevant points and produce useful supporting information.

Original Query: %s

Planner Output: %s

Please provide:
1. Key facts and evidence
2. Supporting context
3. Relevant considerations

Be thorough but concise.`, input.Query, input.PlannerOutput)

	messages := []types.LLMMessage{
		{Role: "system", Content: "You are a researcher agent. Gather relevant points and produce useful supporting information."},
		{Role: "user", Content: researcherPrompt},
	}

	reqBody := types.LLMRequest{
		TraceID:             input.TaskID,
		TaskID:              input.TaskID,
		Provider:            "openai_compatible",
		Model:               input.Model,
		Messages:            messages,
		Temperature:         input.Temperature,
		MaxCompletionTokens: input.MaxCompletionTokens,
		Role:                "researcher",
	}
	reqBody.Metadata = map[string]any{
		"workflow_id": input.WorkflowID,
		"run_id":      input.RunID,
		"agent_role":  "researcher",
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	startTime := time.Now()

	resp, err := a.httpClient.Post(
		a.llmServiceURL+"/chat",
		"application/json",
		bytes.NewBuffer(jsonBody),
	)
	if err != nil {
		logger.Error("RunResearcherAgent: HTTP call failed", "error", err)
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

	latencyMS := time.Since(startTime).Milliseconds()

	logger.Info("RunResearcherAgent completed", "task_id", input.TaskID, "tokens", llmResp.Usage.TotalTokens, "latency_ms", latencyMS)

	return &types.RunResearcherAgentOutput{
		Step: types.AgentStep{
			TaskID:          input.TaskID,
			Role:            types.AgentRoleResearcher,
			Input:           researcherPrompt,
			Output:          llmResp.Content,
			Status:          "completed",
			PromptTokens:    llmResp.Usage.PromptTokens,
			CompletionTokens: llmResp.Usage.CompletionTokens,
			TotalTokens:     llmResp.Usage.TotalTokens,
		},
		LLMOutput:         llmResp.Content,
		PromptTokens:      llmResp.Usage.PromptTokens,
		CompletionTokens: llmResp.Usage.CompletionTokens,
		TotalTokens:      llmResp.Usage.TotalTokens,
		LatencyMS:        latencyMS,
	}, nil
}

// RunResearcherWithTools calls the Python LLM Service to research with step-level tool tracking
func (a *MultiAgentActivities) RunResearcherWithTools(ctx context.Context, input types.RunResearcherWithToolsInput) (*types.RunResearcherWithToolsOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("RunResearcherWithTools started", "task_id", input.TaskID, "model", input.Model, "enable_tools", input.EnableTools)

	// Check budget first - must be before LLM call
	if input.MaxCompletionTokens <= 0 {
		logger.Error("RunResearcherWithTools: budget exceeded (max_completion_tokens=0)")
		return nil, fmt.Errorf("budget exceeded: max_completion_tokens is 0")
	}

	// Build researcher prompt
	researcherPrompt := fmt.Sprintf(`You are a researcher agent. Gather relevant points and produce useful supporting information.

Original Query: %s

Planner Output: %s

Please provide:
1. Key facts and evidence
2. Supporting context
3. Relevant considerations

Be thorough but concise.`, input.Query, input.PlannerOutput)

	messages := []types.LLMMessage{
		{Role: "system", Content: "You are a researcher agent. Gather relevant points and produce useful supporting information."},
		{Role: "user", Content: researcherPrompt},
	}

	reqBody := types.LLMRequest{
		TraceID:             input.TaskID,
		TaskID:              input.TaskID,
		Provider:            "openai_compatible",
		Model:               input.Model,
		Messages:            messages,
		Temperature:         input.Temperature,
		MaxCompletionTokens: input.MaxCompletionTokens,
		Role:                "researcher",
	}
	reqBody.Metadata = map[string]any{
		"workflow_id": input.WorkflowID,
		"run_id":      input.RunID,
		"agent_role":  "researcher",
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	startTime := time.Now()

	// Initialize tool steps tracking
	var toolSteps []types.AgentToolStep
	toolUsed := false

	// Detect tool intent if tools are enabled
	if input.EnableTools {
		toolDecision := types.DetectToolIntent(input.Query)
		if toolDecision.Matched {
			toolUsed = true
			stepID := 1
			toolStartTime := time.Now()

			// Create tool step record - started
			toolStep := types.AgentToolStep{
				TaskID:    input.TaskID,
				AgentRole: string(types.AgentRoleResearcher),
				StepID:    stepID,
				ToolName:  toolDecision.ToolName,
				Status:    "started",
			}
			if argsJSON, err := json.Marshal(toolDecision.Arguments); err == nil {
				toolStep.Arguments = string(argsJSON)
			}

			err = a.executeToolSafe(toolDecision.ToolName, toolDecision.Arguments, input.TaskID)
			if err != nil {
				toolStep.Status = "failed"
				toolStep.Error = err.Error()
				toolStep.LatencyMs = time.Since(toolStartTime).Milliseconds()
				toolStep.Timestamp = time.Now().UTC().Format(time.RFC3339)
				toolSteps = append(toolSteps, toolStep)
			} else {
				// Get tool result
				var toolResult *types.ToolResult
				toolResult, err = a.getToolResultSafe(input.TaskID)
				if err != nil || toolResult == nil {
					toolStep.Status = "completed"
					toolStep.Output = ""
					toolStep.LatencyMs = time.Since(toolStartTime).Milliseconds()
					toolStep.Timestamp = time.Now().UTC().Format(time.RFC3339)
				} else {
					toolStep.Status = "completed"
					toolStep.Output = toolResult.Output
					toolStep.LatencyMs = int64(toolResult.LatencyMs)
					toolStep.Timestamp = time.Now().UTC().Format(time.RFC3339)
				}
				toolSteps = append(toolSteps, toolStep)
			}
		}
	}

	resp, err := a.httpClient.Post(
		a.llmServiceURL+"/chat",
		"application/json",
		bytes.NewBuffer(jsonBody),
	)
	if err != nil {
		logger.Error("RunResearcherWithTools: HTTP call failed", "error", err)
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

	latencyMS := time.Since(startTime).Milliseconds()

	logger.Info("RunResearcherWithTools completed", "task_id", input.TaskID, "tokens", llmResp.Usage.TotalTokens, "latency_ms", latencyMS, "tool_used", toolUsed)

	return &types.RunResearcherWithToolsOutput{
		Step: types.AgentStep{
			TaskID:          input.TaskID,
			Role:            types.AgentRoleResearcher,
			Input:           researcherPrompt,
			Output:          llmResp.Content,
			Status:          "completed",
			PromptTokens:    llmResp.Usage.PromptTokens,
			CompletionTokens: llmResp.Usage.CompletionTokens,
			TotalTokens:     llmResp.Usage.TotalTokens,
			ToolSteps:       toolSteps,
		},
		LLMOutput:         llmResp.Content,
		PromptTokens:      llmResp.Usage.PromptTokens,
		CompletionTokens: llmResp.Usage.CompletionTokens,
		TotalTokens:      llmResp.Usage.TotalTokens,
		LatencyMS:        latencyMS,
		ToolUsed:         toolUsed,
	}, nil
}

// RunCriticWithTools calls the Python LLM Service to critique with step-level tool tracking
func (a *MultiAgentActivities) RunCriticWithTools(ctx context.Context, input types.RunCriticWithToolsInput) (*types.RunCriticWithToolsOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("RunCriticWithTools started", "task_id", input.TaskID, "model", input.Model, "enable_tools", input.EnableTools)

	// Check budget first - must be before LLM call
	if input.MaxCompletionTokens <= 0 {
		logger.Error("RunCriticWithTools: budget exceeded (max_completion_tokens=0)")
		return nil, fmt.Errorf("budget exceeded: max_completion_tokens is 0")
	}

	// Build critic prompt including original query, planner/researcher output, and current answer
	criticPrompt := fmt.Sprintf(`You are a critic agent. Review the candidate answer and provide constructive feedback.

Original Query: %s

Planner Output: %s

Researcher Output: %s

Current Answer to Review: %s

Please identify:
1. Strengths of the current answer
2. Gaps, risks, or weak reasoning
3. Concrete improvements

Be concise but specific.`, input.Query, input.PlannerOutput, input.ResearcherOutput, input.CurrentAnswer)

	messages := []types.LLMMessage{
		{Role: "system", Content: "You are a critic agent. Review the candidate answer and identify gaps, risks, weak reasoning, and concrete improvements. Be concise but specific."},
		{Role: "user", Content: criticPrompt},
	}

	reqBody := types.LLMRequest{
		TraceID:             input.TaskID,
		TaskID:              input.TaskID,
		Provider:            "openai_compatible",
		Model:               input.Model,
		Messages:            messages,
		Temperature:         input.Temperature,
		MaxCompletionTokens: input.MaxCompletionTokens,
		Role:                "critic",
	}
	reqBody.Metadata = map[string]any{
		"workflow_id": input.WorkflowID,
		"run_id":      input.RunID,
		"agent_role":  "critic",
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	startTime := time.Now()

	// Initialize tool steps tracking
	var toolSteps []types.AgentToolStep
	toolUsed := false

	// Detect tool intent if tools are enabled
	if input.EnableTools {
		toolDecision := types.DetectToolIntent(input.Query)
		if toolDecision.Matched {
			toolUsed = true
			stepID := 1
			toolStartTime := time.Now()

			// Create tool step record - started
			toolStep := types.AgentToolStep{
				TaskID:    input.TaskID,
				AgentRole: string(types.AgentRoleCritic),
				StepID:    stepID,
				ToolName:  toolDecision.ToolName,
				Status:    "started",
			}
			if argsJSON, err := json.Marshal(toolDecision.Arguments); err == nil {
				toolStep.Arguments = string(argsJSON)
			}

			// Execute tool via ExecuteToolActivity
			err = a.executeToolSafe(toolDecision.ToolName, toolDecision.Arguments, input.TaskID)
			if err != nil {
				toolStep.Status = "failed"
				toolStep.Error = err.Error()
				toolStep.LatencyMs = time.Since(toolStartTime).Milliseconds()
				toolStep.Timestamp = time.Now().UTC().Format(time.RFC3339)
				toolSteps = append(toolSteps, toolStep)
			} else {
				// Get tool result
				var toolResult *types.ToolResult
				toolResult, err = a.getToolResultSafe(input.TaskID)
				if err != nil || toolResult == nil {
					toolStep.Status = "completed"
					toolStep.Output = ""
					toolStep.LatencyMs = time.Since(toolStartTime).Milliseconds()
					toolStep.Timestamp = time.Now().UTC().Format(time.RFC3339)
				} else {
					toolStep.Status = "completed"
					toolStep.Output = toolResult.Output
					toolStep.LatencyMs = int64(toolResult.LatencyMs)
					toolStep.Timestamp = time.Now().UTC().Format(time.RFC3339)
				}
				toolSteps = append(toolSteps, toolStep)
			}
		}
	}

	resp, err := a.httpClient.Post(
		a.llmServiceURL+"/chat",
		"application/json",
		bytes.NewBuffer(jsonBody),
	)
	if err != nil {
		logger.Error("RunCriticWithTools: HTTP call failed", "error", err)
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

	latencyMS := time.Since(startTime).Milliseconds()

	logger.Info("RunCriticWithTools completed", "task_id", input.TaskID, "tokens", llmResp.Usage.TotalTokens, "latency_ms", latencyMS, "tool_used", toolUsed)

	return &types.RunCriticWithToolsOutput{
		Step: types.AgentStep{
			TaskID:          input.TaskID,
			Role:            types.AgentRoleCritic,
			Input:           criticPrompt,
			Output:          llmResp.Content,
			Status:          "completed",
			PromptTokens:    llmResp.Usage.PromptTokens,
			CompletionTokens: llmResp.Usage.CompletionTokens,
			TotalTokens:     llmResp.Usage.TotalTokens,
			ToolSteps:       toolSteps,
		},
		LLMOutput:         llmResp.Content,
		PromptTokens:      llmResp.Usage.PromptTokens,
		CompletionTokens: llmResp.Usage.CompletionTokens,
		TotalTokens:      llmResp.Usage.TotalTokens,
		LatencyMS:        latencyMS,
		ToolSteps:        toolSteps,
		ToolUsed:         toolUsed,
	}, nil
}

// RunSynthesizerWithTools calls the Python LLM Service to synthesize with step-level tool tracking
func (a *MultiAgentActivities) RunSynthesizerWithTools(ctx context.Context, input types.RunSynthesizerWithToolsInput) (*types.RunSynthesizerWithToolsOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("RunSynthesizerWithTools started", "task_id", input.TaskID, "model", input.Model, "enable_tools", input.EnableTools)

	// Check budget first - must be before LLM call
	if input.MaxCompletionTokens <= 0 {
		logger.Error("RunSynthesizerWithTools: budget exceeded (max_completion_tokens=0)")
		return nil, fmt.Errorf("budget exceeded: max_completion_tokens is 0")
	}

	// Build synthesis prompt combining all agent outputs
	synthesisPrompt := fmt.Sprintf(`You are a synthesizer agent. Combine the following agent outputs into a final answer.

Original Query: %s

Planner Output: %s

Researcher Output: %s

Critic Output: %s

Please provide a comprehensive final answer that synthesizes all the above inputs.`, input.Query, input.PlannerOutput, input.ResearcherOutput, input.CriticOutput)

	messages := []types.LLMMessage{
		{Role: "system", Content: "You are a synthesizer agent. Combine multiple agent outputs into a final comprehensive answer."},
		{Role: "user", Content: synthesisPrompt},
	}

	reqBody := types.LLMRequest{
		TraceID:             input.TaskID,
		TaskID:              input.TaskID,
		Provider:            "openai_compatible",
		Model:               input.Model,
		Messages:            messages,
		Temperature:         input.Temperature,
		MaxCompletionTokens: input.MaxCompletionTokens,
	}
	reqBody.Metadata = map[string]any{
		"workflow_id": input.WorkflowID,
		"run_id":      input.RunID,
		"agent_role":  "synthesizer",
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	startTime := time.Now()

	// Initialize tool steps tracking
	var toolSteps []types.AgentToolStep
	toolUsed := false

	// Detect tool intent if tools are enabled
	if input.EnableTools {
		toolDecision := types.DetectToolIntent(input.Query)
		if toolDecision.Matched {
			toolUsed = true
			stepID := 1
			toolStartTime := time.Now()

			// Create tool step record - started
			toolStep := types.AgentToolStep{
				TaskID:    input.TaskID,
				AgentRole: string(types.AgentRoleSynthesizer),
				StepID:    stepID,
				ToolName:  toolDecision.ToolName,
				Status:    "started",
			}
			if argsJSON, err := json.Marshal(toolDecision.Arguments); err == nil {
				toolStep.Arguments = string(argsJSON)
			}

			// Execute tool via ExecuteToolActivity
			err = a.executeToolSafe(toolDecision.ToolName, toolDecision.Arguments, input.TaskID)
			if err != nil {
				toolStep.Status = "failed"
				toolStep.Error = err.Error()
				toolStep.LatencyMs = time.Since(toolStartTime).Milliseconds()
				toolStep.Timestamp = time.Now().UTC().Format(time.RFC3339)
				toolSteps = append(toolSteps, toolStep)
			} else {
				// Get tool result
				var toolResult *types.ToolResult
				toolResult, err = a.getToolResultSafe(input.TaskID)
				if err != nil || toolResult == nil {
					toolStep.Status = "completed"
					toolStep.Output = ""
					toolStep.LatencyMs = time.Since(toolStartTime).Milliseconds()
					toolStep.Timestamp = time.Now().UTC().Format(time.RFC3339)
				} else {
					toolStep.Status = "completed"
					toolStep.Output = toolResult.Output
					toolStep.LatencyMs = int64(toolResult.LatencyMs)
					toolStep.Timestamp = time.Now().UTC().Format(time.RFC3339)
				}
				toolSteps = append(toolSteps, toolStep)
			}
		}
	}

	resp, err := a.httpClient.Post(
		a.llmServiceURL+"/chat",
		"application/json",
		bytes.NewBuffer(jsonBody),
	)
	if err != nil {
		logger.Error("RunSynthesizerWithTools: HTTP call failed", "error", err)
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

	latencyMS := time.Since(startTime).Milliseconds()

	logger.Info("RunSynthesizerWithTools completed", "task_id", input.TaskID, "tokens", llmResp.Usage.TotalTokens, "latency_ms", latencyMS, "tool_used", toolUsed)

	return &types.RunSynthesizerWithToolsOutput{
		Step: types.AgentStep{
			TaskID:          input.TaskID,
			Role:            types.AgentRoleSynthesizer,
			Input:           synthesisPrompt,
			Output:          llmResp.Content,
			Status:          "completed",
			PromptTokens:    llmResp.Usage.PromptTokens,
			CompletionTokens: llmResp.Usage.CompletionTokens,
			TotalTokens:     llmResp.Usage.TotalTokens,
			ToolSteps:       toolSteps,
		},
		LLMOutput:         llmResp.Content,
		PromptTokens:      llmResp.Usage.PromptTokens,
		CompletionTokens: llmResp.Usage.CompletionTokens,
		TotalTokens:      llmResp.Usage.TotalTokens,
		LatencyMS:        latencyMS,
		ToolSteps:        toolSteps,
		ToolUsed:         toolUsed,
	}, nil
}

// executeToolSafe executes a tool and returns an error (thread-safe)
func (a *MultiAgentActivities) executeToolSafe(toolName string, arguments map[string]interface{}, taskID string) error {
	start := time.Now()

	var result *types.ToolResult
	var err error

	switch toolName {
	case "calculator":
		result, err = a.toolActivities.executeCalculator(arguments, start)
	case "echo":
		result, err = a.toolActivities.executeEcho(arguments, start)
	default:
		result = &types.ToolResult{
			ToolName:  toolName,
			Error:     "unknown tool: " + toolName,
			LatencyMs: int(time.Since(start).Milliseconds()),
		}
	}

	// Store result in cache (thread-safe)
	toolCacheInstance.mu.Lock()
	toolCacheInstance.cache[taskID] = result
	toolCacheInstance.mu.Unlock()

	return err
}

// getToolResultSafe retrieves the tool result from cache (thread-safe)
func (a *MultiAgentActivities) getToolResultSafe(taskID string) (*types.ToolResult, error) {
	toolCacheInstance.mu.Lock()
	result, ok := toolCacheInstance.cache[taskID]
	if ok {
		delete(toolCacheInstance.cache, taskID)
	}
	toolCacheInstance.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("no tool result found for task %s", taskID)
	}
	return result, nil
}

// toolCache is a thread-safe cache for tool execution results
type toolCache struct {
	mu    sync.Mutex
	cache map[string]*types.ToolResult
}

var toolCacheInstance = &toolCache{
	cache: make(map[string]*types.ToolResult),
}