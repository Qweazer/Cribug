package activities

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"

	"cribug/internal/llm"
	"cribug/internal/types"

	"go.temporal.io/sdk/activity"
)

type DAGActivities struct {
	db           *sql.DB
	llmClient   *llm.Client
	redisClient *DAGRedisClient
}

func NewDAGActivities(db *sql.DB, llmServiceURL string, redisAddr, redisPass string, redisDB int, ttlSeconds int) *DAGActivities {
	return &DAGActivities{
		db:         db,
		llmClient: llm.NewClient(llmServiceURL),
		redisClient: NewDAGRedisClient(redisAddr, redisPass, redisDB, ttlSeconds),
	}
}

type ClassifyTaskInput struct {
	TaskID      string
	Query       string
	EnableTools bool
}

type ClassifyTaskOutput struct {
	Classification *types.TaskClassification
}

func (a *DAGActivities) ClassifyTask(ctx context.Context, input ClassifyTaskInput) (*ClassifyTaskOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("ClassifyTaskActivity started", "task_id", input.TaskID, "query_len", len(input.Query))

	query := strings.ToLower(input.Query)
	queryLen := len(input.Query)

	classification := &types.TaskClassification{
		TaskID:        input.TaskID,
		Query:         input.Query,
		RequiresTools: input.EnableTools,
		SuggestedMode: "single",
	}

	// Simple deterministic classification rules
	analysisKeywords := []string{"compare", "analyze", "research", "plan", "review", "evaluate", "assess", "investigate"}
	creativeKeywords := []string{"write", "create", "generate", "compose", "draft", "design", "story", "poem"}

	hasAnalysisKeyword := false
	for _, kw := range analysisKeywords {
		if strings.Contains(query, kw) {
			hasAnalysisKeyword = true
			break
		}
	}

	hasCreativeKeyword := false
	for _, kw := range creativeKeywords {
		if strings.Contains(query, kw) {
			hasCreativeKeyword = true
			break
		}
	}

	if hasAnalysisKeyword || queryLen > 200 {
		classification.Category = "analysis"
		classification.Complexity = 0.6
		classification.SuggestedMode = "multi"
	} else if hasCreativeKeyword {
		classification.Category = "creative"
		classification.Complexity = 0.5
	} else {
		classification.Category = "simple"
		classification.Complexity = 0.2
	}

	logger.Info("ClassifyTaskActivity completed",
		"task_id", input.TaskID,
		"category", classification.Category,
		"complexity", classification.Complexity)

	return &ClassifyTaskOutput{Classification: classification}, nil
}

type PlanDAGInput struct {
	TaskID         string
	Query          string
	Classification *types.TaskClassification
	ReactConfig    *types.ReactLoopConfig // nil means no ReAct, pointer enables conditional
}

type PlanDAGOutput struct {
	Plan *types.DAGPlan
}

func (a *DAGActivities) PlanDAG(ctx context.Context, input PlanDAGInput) (*PlanDAGOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("PlanDAGActivity started", "task_id", input.TaskID)

	// Generate 2-node DAG: analyze_input (mock) -> draft_answer (LLM)
	plan := &types.DAGPlan{
		TaskID: input.TaskID,
		Nodes: []types.DAGNode{
			{
				ID:        "analyze_input",
				Type:      "analysis",
				Name:      "Analyze Input",
				Input:     input.Query,
				DependsOn: []string{},
				UseLLM:    false, // mock node
			},
			{
				ID:        "draft_answer",
				Type:      "synthesis",
				Name:      "Draft Answer",
				Input:     "Synthesize analysis into answer",
				DependsOn: []string{"analyze_input"},
				UseLLM:    true, // LLM-backed node
				ReactConfig: input.ReactConfig,
			},
		},
		Edges: []types.DAGEdge{
			{From: "analyze_input", To: "draft_answer"},
		},
	}

	logger.Info("PlanDAGActivity completed",
		"task_id", input.TaskID,
		"node_count", len(plan.Nodes),
		"edge_count", len(plan.Edges))

	return &PlanDAGOutput{Plan: plan}, nil
}

type ExecuteDAGNodeInput struct {
	TaskID          string
	WorkflowID      string
	RunID           string
	Query           string
	Node            types.DAGNode
	UpstreamResults map[string]types.DAGNodeResult
	Model           string
	Temperature     float64
	MaxTokens       int
}

type ExecuteDAGNodeOutput struct {
	Result *types.DAGNodeResult
	Usage  *types.Usage // LLM usage if node called LLM
}

func (a *DAGActivities) ExecuteDAGNode(ctx context.Context, input ExecuteDAGNodeInput) (*ExecuteDAGNodeOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("ExecuteDAGNodeActivity started",
		"task_id", input.TaskID,
		"node_id", input.Node.ID,
		"node_type", input.Node.Type,
		"use_llm", input.Node.UseLLM)

	// Update Redis status to "running"
	if a.redisClient != nil {
		if err := a.redisClient.SetNodeStatus(ctx, input.TaskID, input.Node.ID, "running"); err != nil {
			logger.Warn("Failed to set node status in Redis", "error", err)
		}
	}

	// Debug hook: force node failure for testing
	if strings.Contains(input.Query, "__force_dag_node_failure__") {
		result := &types.DAGNodeResult{
			TaskID:   input.TaskID,
			NodeID:   input.Node.ID,
			NodeType: input.Node.Type,
			Status:   "failed",
			Error:    "debug: forced node failure for testing",
		}
		logger.Warn("ExecuteDAGNodeActivity: debug forced failure", "task_id", input.TaskID, "node_id", input.Node.ID)

		// 1. Update Redis with result
		if a.redisClient != nil {
			resultMap := map[string]interface{}{
				"node_id":   result.NodeID,
				"node_type": result.NodeType,
				"status":    result.Status,
				"output":    result.Output,
			}
			if result.Error != "" {
				resultMap["error"] = result.Error
			}
			if err := a.redisClient.SetNodeResult(ctx, input.TaskID, input.Node.ID, resultMap); err != nil {
				logger.Warn("Failed to set node result in Redis", "error", err)
			}
		}

		// 2. Update Redis status to "completed" or "failed"
		if a.redisClient != nil {
			status := "completed"
			if result.Error != "" {
				status = "failed"
			}
			if err := a.redisClient.SetNodeStatus(ctx, input.TaskID, input.Node.ID, status); err != nil {
				logger.Warn("Failed to update node status in Redis", "error", err)
			}
		}

		return &ExecuteDAGNodeOutput{Result: result}, nil
	}

	// Mock node (no LLM)
	if !input.Node.UseLLM {
		var output string
		switch input.Node.Type {
		case "analysis":
			output = "analyzed input for task " + input.TaskID
		default:
			output = "executed mock node " + input.Node.ID
		}

		result := &types.DAGNodeResult{
			TaskID:   input.TaskID,
			NodeID:   input.Node.ID,
			NodeType: input.Node.Type,
			Status:   "completed",
			Output:   output,
		}

		logger.Info("ExecuteDAGNodeActivity completed (mock)",
			"task_id", input.TaskID,
			"node_id", input.Node.ID,
			"status", result.Status)

		// 1. Update Redis with result
		if a.redisClient != nil {
			resultMap := map[string]interface{}{
				"node_id":   result.NodeID,
				"node_type": result.NodeType,
				"status":    result.Status,
				"output":    result.Output,
			}
			if result.Error != "" {
				resultMap["error"] = result.Error
			}
			if err := a.redisClient.SetNodeResult(ctx, input.TaskID, input.Node.ID, resultMap); err != nil {
				logger.Warn("Failed to set node result in Redis", "error", err)
			}
		}

		// 2. Update Redis status to "completed" or "failed"
		if a.redisClient != nil {
			status := "completed"
			if result.Error != "" {
				status = "failed"
			}
			if err := a.redisClient.SetNodeStatus(ctx, input.TaskID, input.Node.ID, status); err != nil {
				logger.Warn("Failed to update node status in Redis", "error", err)
			}
		}

		return &ExecuteDAGNodeOutput{Result: result}, nil
	}

	// LLM-backed node
	if a.llmClient == nil {
		return nil, fmt.Errorf("llm client not initialized")
	}

	// Build prompt from upstream results
	upstreamContext := ""
	for nodeID, result := range input.UpstreamResults {
		upstreamContext += fmt.Sprintf("[%s] %s\n", nodeID, result.Output)
	}
	if upstreamContext == "" {
		upstreamContext = "No upstream analysis available."
	}

	// Build LLM prompt
	prompt := fmt.Sprintf(`You are working on a DAG task.

Original query: %s

Upstream node results:
%s

Task: Complete the "%s" node (type: %s) by providing your output.

Output your answer directly:`, input.Query, upstreamContext, input.Node.Name, input.Node.Type)

	messages := []types.LLMMessage{
		{Role: "user", Content: prompt},
	}

	resp, err := a.llmClient.Call(ctx, llm.CallRequest{
		TraceID:             input.TaskID,
		TaskID:              input.TaskID,
		Provider:            "openai_compatible",
		Model:               input.Model,
		Messages:            messages,
		Temperature:         input.Temperature,
		MaxCompletionTokens: input.MaxTokens,
	})

	if err != nil {
		result := &types.DAGNodeResult{
			TaskID:   input.TaskID,
			NodeID:   input.Node.ID,
			NodeType: input.Node.Type,
			Status:   "failed",
			Error:    err.Error(),
		}

		// 1. Update Redis with result
		if a.redisClient != nil {
			resultMap := map[string]interface{}{
				"node_id":   result.NodeID,
				"node_type": result.NodeType,
				"status":    result.Status,
				"output":    result.Output,
			}
			if result.Error != "" {
				resultMap["error"] = result.Error
			}
			if err := a.redisClient.SetNodeResult(ctx, input.TaskID, input.Node.ID, resultMap); err != nil {
				logger.Warn("Failed to set node result in Redis", "error", err)
			}
		}

		// 2. Update Redis status to "completed" or "failed"
		if a.redisClient != nil {
			status := "completed"
			if result.Error != "" {
				status = "failed"
			}
			if err := a.redisClient.SetNodeStatus(ctx, input.TaskID, input.Node.ID, status); err != nil {
				logger.Warn("Failed to update node status in Redis", "error", err)
			}
		}

		return &ExecuteDAGNodeOutput{Result: result}, nil
	}

	result := &types.DAGNodeResult{
		TaskID:   input.TaskID,
		NodeID:   input.Node.ID,
		NodeType: input.Node.Type,
		Status:   "completed",
		Output:   resp.Content,
	}

	logger.Info("ExecuteDAGNodeActivity completed (LLM)",
		"task_id", input.TaskID,
		"node_id", input.Node.ID,
		"status", result.Status,
		"tokens", resp.Usage.TotalTokens)

	// 1. Update Redis with result
	if a.redisClient != nil {
		resultMap := map[string]interface{}{
			"node_id":   result.NodeID,
			"node_type": result.NodeType,
			"status":    result.Status,
			"output":    result.Output,
		}
		if result.Error != "" {
			resultMap["error"] = result.Error
		}
		if err := a.redisClient.SetNodeResult(ctx, input.TaskID, input.Node.ID, resultMap); err != nil {
			logger.Warn("Failed to set node result in Redis", "error", err)
		}
	}

	// 2. Update Redis status to "completed" or "failed"
	if a.redisClient != nil {
		status := "completed"
		if result.Error != "" {
			status = "failed"
		}
		if err := a.redisClient.SetNodeStatus(ctx, input.TaskID, input.Node.ID, status); err != nil {
			logger.Warn("Failed to update node status in Redis", "error", err)
		}
	}

	return &ExecuteDAGNodeOutput{
		Result: result,
		Usage:  &resp.Usage,
	}, nil
}

// RecordDAGNodeUsage records LLM usage for a DAG node
type RecordDAGNodeUsageInput struct {
	TaskID            string
	WorkflowID        string
	RunID             string
	NodeID            string
	Model             string
	Provider          string
	PromptTokens      int
	CompletionTokens  int
	TotalTokens       int
	LatencyMS         int64
	FinishReason      string
}

func (a *DAGActivities) RecordDAGNodeUsage(ctx context.Context, input RecordDAGNodeUsageInput) error {
	logger := activity.GetLogger(ctx)
	logger.Info("RecordDAGNodeUsage started",
		"task_id", input.TaskID,
		"node_id", input.NodeID,
		"total_tokens", input.TotalTokens)

	// call_id includes node_id for idempotency
	callID := fmt.Sprintf("%s:%s", input.TaskID, input.NodeID)

	query := `
		INSERT INTO llm_calls (
			id, call_id, task_id, workflow_id, run_id, node_id, provider, model,
			prompt_tokens, completion_tokens, total_tokens,
			latency_ms, finish_reason, created_at
		) VALUES (
			gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12, NOW()
		)
		ON CONFLICT (call_id) DO UPDATE SET
			prompt_tokens = EXCLUDED.prompt_tokens,
			completion_tokens = EXCLUDED.completion_tokens,
			total_tokens = EXCLUDED.total_tokens,
			latency_ms = EXCLUDED.latency_ms,
			finish_reason = EXCLUDED.finish_reason
		RETURNING id`

	var id string
	err := a.db.QueryRowContext(ctx, query,
		callID,
		input.TaskID,
		input.WorkflowID,
		input.RunID,
		input.NodeID,
		input.Provider,
		input.Model,
		input.PromptTokens,
		input.CompletionTokens,
		input.TotalTokens,
		input.LatencyMS,
		input.FinishReason,
	).Scan(&id)

	if err != nil {
		logger.Error("RecordDAGNodeUsage failed", "error", err)
		return fmt.Errorf("insert llm_calls: %w", err)
	}

	log.Printf("[INFO] RecordDAGNodeUsage: task=%s node=%s id=%s tokens=%d",
		input.TaskID, input.NodeID, id, input.TotalTokens)
	logger.Info("RecordDAGNodeUsage completed", "task_id", input.TaskID, "node_id", input.NodeID)
	return nil
}

// SynthesisActivity synthesizes DAG node results into a final answer
type SynthesisInput struct {
	TaskID       string
	Query        string
	Classification *types.TaskClassification
	Plan         *types.DAGPlan
	NodeResults  map[string]types.DAGNodeResult
}

type SynthesisOutput struct {
	Result *types.DAGSynthesisResult
}

func (a *DAGActivities) Synthesis(ctx context.Context, input SynthesisInput) (*SynthesisOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("SynthesisActivity started", "task_id", input.TaskID, "node_count", len(input.Plan.Nodes))

	// Aggregate results from node results map
	nodeCount := len(input.Plan.Nodes)
	completedNodes := 0
	var finalAnswerParts []string

	for _, node := range input.Plan.Nodes {
		result, ok := input.NodeResults[node.ID]
		if !ok {
			logger.Warn("Missing result for node", "node_id", node.ID)
			continue
		}
		if result.Status == "completed" {
			completedNodes++
			// Include draft_answer output in final answer
			if node.ID == "draft_answer" && result.Output != "" {
				finalAnswerParts = append(finalAnswerParts, result.Output)
			}
		}
	}

	// Check if all nodes completed
	if completedNodes < nodeCount {
		return nil, fmt.Errorf("synthesis failed: only %d/%d nodes completed", completedNodes, nodeCount)
	}

	// Query llm_calls for usage aggregation
	var totalPromptTokens, totalCompletionTokens, totalTokens, llmNodes int
	query := `SELECT
		COALESCE(SUM(prompt_tokens), 0) as prompt_tokens,
		COALESCE(SUM(completion_tokens), 0) as completion_tokens,
		COALESCE(SUM(total_tokens), 0) as total_tokens,
		COUNT(*) as llm_nodes
	FROM llm_calls WHERE task_id = $1`
	err := a.db.QueryRowContext(ctx, query, input.TaskID).Scan(
		&totalPromptTokens, &totalCompletionTokens, &totalTokens, &llmNodes)
	if err != nil && err != sql.ErrNoRows {
		logger.Error("Failed to aggregate usage from llm_calls", "error", err)
		// Continue with zeros rather than failing
	}

	// Build final answer
	var finalAnswer string
	if len(finalAnswerParts) > 0 {
		finalAnswer = finalAnswerParts[0]
	} else {
		finalAnswer = "dag synthesized: " + input.Query
	}

	result := &types.DAGSynthesisResult{
		TaskID:              input.TaskID,
		FinalAnswer:         finalAnswer,
		NodeCount:           nodeCount,
		CompletedNodes:      completedNodes,
		LLMNodes:           llmNodes,
		TotalPromptTokens:   totalPromptTokens,
		TotalCompletionTokens: totalCompletionTokens,
		TotalTokens:        totalTokens,
	}

	logger.Info("SynthesisActivity completed",
		"task_id", input.TaskID,
		"node_count", result.NodeCount,
		"completed_nodes", result.CompletedNodes,
		"llm_nodes", result.LLMNodes,
		"total_tokens", result.TotalTokens)

	return &SynthesisOutput{Result: result}, nil
}