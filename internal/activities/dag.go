package activities

import (
	"context"
	"strings"

	"cribug/internal/types"

	"go.temporal.io/sdk/activity"
)

type DAGActivities struct{}

func NewDAGActivities() *DAGActivities {
	return &DAGActivities{}
}

type ClassifyTaskInput struct {
	TaskID        string
	Query         string
	EnableTools   bool
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
	TaskID        string
	Query         string
	Classification *types.TaskClassification
}

type PlanDAGOutput struct {
	Plan *types.DAGPlan
}

func (a *DAGActivities) PlanDAG(ctx context.Context, input PlanDAGInput) (*PlanDAGOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("PlanDAGActivity started", "task_id", input.TaskID)

	// Always generate minimal 2-node DAG
	plan := &types.DAGPlan{
		TaskID: input.TaskID,
		Nodes: []types.DAGNode{
			{
				ID:        "analyze_input",
				Type:      "analysis",
				Name:      "Analyze Input",
				Input:     input.Query,
				DependsOn: []string{},
			},
			{
				ID:        "draft_answer",
				Type:      "synthesis",
				Name:      "Draft Answer",
				Input:     "Synthesize analysis into answer",
				DependsOn: []string{"analyze_input"},
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
	Query           string
	Node            types.DAGNode
	UpstreamResults map[string]types.DAGNodeResult
}

type ExecuteDAGNodeOutput struct {
	Result *types.DAGNodeResult
}

func (a *DAGActivities) ExecuteDAGNode(ctx context.Context, input ExecuteDAGNodeInput) (*ExecuteDAGNodeOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("ExecuteDAGNodeActivity started",
		"task_id", input.TaskID,
		"node_id", input.Node.ID,
		"node_type", input.Node.Type)

	var output string

	switch input.Node.Type {
	case "analysis":
		output = "analyzed input for task " + input.TaskID
	case "synthesis":
		// Build context from upstream results
		upstreamContext := ""
		for nodeID, result := range input.UpstreamResults {
			upstreamContext += nodeID + ": " + result.Output + "; "
		}
		if upstreamContext == "" {
			upstreamContext = "no upstream results"
		}
		output = "drafted answer from DAG node results: " + upstreamContext
	default:
		output = "executed node " + input.Node.ID
	}

	result := &types.DAGNodeResult{
		TaskID:   input.TaskID,
		NodeID:   input.Node.ID,
		NodeType: input.Node.Type,
		Status:   "completed",
		Output:   output,
	}

	logger.Info("ExecuteDAGNodeActivity completed",
		"task_id", input.TaskID,
		"node_id", input.Node.ID,
		"status", result.Status)

	return &ExecuteDAGNodeOutput{Result: result}, nil
}