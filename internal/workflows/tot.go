package workflows

import (
	"fmt"
	"time"

	"cribug/internal/activities"
	"cribug/internal/types"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const TreeOfThoughtsWorkflowName = "TreeOfThoughtsWorkflow"

// TreeOfThoughtsWorkflow implements BFS-based bounded Tree-of-Thoughts search.
//
// Constraints:
//   - Bounded: max_depth, max_total_nodes, token_budget enforced.
//   - Workflow history stores only summary + ref + score; full thought text via ThoughtRef.
//   - Pruning: score below pruning_threshold → skipped.
//   - All LLM calls via Activities.
//   - No hidden chain-of-thought saved.
func TreeOfThoughtsWorkflow(ctx workflow.Context, input types.ToTWorkflowInput) (*types.ToTResult, error) {
	workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
	logger := workflow.GetLogger(ctx)

	cfg := input.Config
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = 5
	}
	if cfg.BranchingFactor <= 0 {
		cfg.BranchingFactor = 3
	}
	if cfg.MaxTotalNodes <= 0 {
		cfg.MaxTotalNodes = 50
	}
	if cfg.TokenBudget <= 0 {
		cfg.TokenBudget = 5000
	}
	if cfg.PruningThreshold <= 0 {
		cfg.PruningThreshold = 0.3
	}
	if cfg.EvaluationMethod == "" {
		cfg.EvaluationMethod = "scoring"
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 90 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    2,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Resolve effective LLM config via Activity
	var llmCfg activities.LLMConfigSnapshot
	err := workflow.ExecuteActivity(ctx, "ResolveEffectiveLLMConfigActivity",
		activities.ResolveEffectiveLLMConfigInput{},
	).Get(ctx, &llmCfg)
	if err != nil || llmCfg.ChatModel == "" {
		logger.Warn("ResolveEffectiveLLMConfig failed, using safe default", "error", err)
		llmCfg = activities.LLMConfigSnapshot{
			Provider: "openai_compatible", ChatModel: "gpt-4o-mini",
			BaseURL: "http://127.0.0.1:8000",
		}
	}
	effectiveModel := llmCfg.ChatModel

	// Initialize root
	root := types.ThoughtNode{
		ID: "root", ParentID: "", Score: 1.0, Depth: 0,
		IsTerminal: false, Status: types.ThoughtStatusActive,
		Summary:   truncateTo(input.Query, 200),
		ThoughtRef: fmt.Sprintf("tot:%s:root", workflowID[:8]),
	}
	allNodes := []types.ThoughtNode{root}
	nodeMap := map[string]*types.ThoughtNode{"root": &root}
	totalTokens := 0
	prunedCount := 0
	usedMode := "unknown"

	// BFS
	for depth := 0; depth < cfg.MaxDepth; depth++ {
		currentNodes := nodesAtDepth(allNodes, depth)
		if len(currentNodes) == 0 {
			break
		}
		if totalTokens >= cfg.TokenBudget {
			break
		}

		for _, node := range currentNodes {
			if node.IsTerminal {
				continue
			}
			if len(allNodes) >= cfg.MaxTotalNodes {
				break
			}

			var genResult activities.GenerateThoughtsResult
			err := workflow.ExecuteActivity(ctx, "GenerateThoughtsActivity",
				activities.GenerateThoughtsInput{
					ParentThoughtRef: node.ThoughtRef,
					ParentSummary:    node.Summary,
					ParentID:         node.ID,
					Query:            input.Query,
					Depth:            depth + 1,
					Count:            cfg.BranchingFactor,
					Model:            effectiveModel,
					MockLLM:          cfg.MockLLM,
					TaskID:           input.TaskID,
					WorkflowID:       input.WorkflowID,
					RunID:            input.RunID,
				},
			).Get(ctx, &genResult)
			if err != nil {
				continue
			}
			totalTokens += genResult.TokensUsed
			if genResult.Mode != "" {
				usedMode = genResult.Mode
			}

			for _, thought := range genResult.Thoughts {
				var scoreResult activities.ScoreThoughtResult
				err := workflow.ExecuteActivity(ctx, "ScoreThoughtActivity",
					activities.ScoreThoughtInput{
						ThoughtRef:     thought.ThoughtRef,
						ThoughtSummary: thought.Summary,
						Query:          input.Query,
						ParentScore:    node.Score,
						Depth:          thought.Depth,
						Method:         cfg.EvaluationMethod,
						MockLLM:        cfg.MockLLM,
						Model:          effectiveModel,
						TaskID:         input.TaskID,
						WorkflowID:     input.WorkflowID,
						RunID:          input.RunID,
					},
				).Get(ctx, &scoreResult)
				if err != nil {
					continue
				}
				totalTokens += scoreResult.TokensUsed
				if scoreResult.Mode != "" {
					usedMode = scoreResult.Mode
				}

				if scoreResult.Score < cfg.PruningThreshold {
					prunedCount++
					continue
				}

				thought.Score = scoreResult.Score
				thought.Explanation = scoreResult.Explanation

				// thought stored via ref (ThoughtRef in ThoughtNode); no need to duplicate via WorkspaceAppend

				if parent, ok := nodeMap[node.ID]; ok {
					parent.Children = append(parent.Children, thought.ID)
				}
				allNodes = append(allNodes, thought)
				nodeMap[thought.ID] = &allNodes[len(allNodes)-1]
			}
		}
	}

	// Find best path
	var bestPathResult activities.FindBestPathResult
	err = workflow.ExecuteActivity(ctx, "FindBestPathActivity",
		activities.FindBestPathInput{
			TreeID: fmt.Sprintf("tot:%s:tree", workflowID[:8]),
			Nodes:  allNodes,
		},
	).Get(ctx, &bestPathResult)
	if err != nil {
		return &types.ToTResult{
			TotalThoughts: len(allNodes), TreeDepth: maxNodeDepth(allNodes),
			TotalTokens: totalTokens, PrunedCount: prunedCount,
		}, err
	}

	// Synthesize
	var synthResult activities.SynthesizeToTResultResult
	err = workflow.ExecuteActivity(ctx, "SynthesizeToTResultActivity",
		activities.SynthesizeToTResultInput{
			BestPathRef: bestPathResult.BestPathRef,
			Nodes:       allNodes, BestPath: bestPathResult.BestPath,
			Query: input.Query, Model: effectiveModel,
			MockLLM: cfg.MockLLM, TaskID: input.TaskID,
			WorkflowID: input.WorkflowID, RunID: input.RunID,
		},
	).Get(ctx, &synthResult)
	if err != nil {
		return &types.ToTResult{
			BestPath: bestPathResult.BestPath, TotalThoughts: len(allNodes),
			TreeDepth: maxNodeDepth(allNodes), TotalTokens: totalTokens,
			Confidence: bestPathResult.Confidence, PrunedCount: prunedCount,
		}, err
	}
	if synthResult.Mode != "" {
		usedMode = synthResult.Mode
	}
	if usedMode == "" {
		usedMode = "mock"
	}

	return &types.ToTResult{
		BestPath:           bestPathResult.BestPath,
		SolutionRef:        synthResult.SolutionRef,
		SolutionSummary:    synthResult.SolutionSummary,
		TotalThoughts:      len(allNodes),
		TreeDepth:          maxNodeDepth(allNodes),
		TotalTokens:        totalTokens,
		ExplorationTreeRef: fmt.Sprintf("tot:%s:tree", workflowID[:8]),
		Confidence:         bestPathResult.Confidence,
		PrunedCount:        prunedCount,
		Provider:           llmCfg.Provider,
		ModelUsed:          effectiveModel,
		Mode:               usedMode,
		Mock:               usedMode == "mock",
		FallbackUsed:       false,
	}, nil
}

func nodesAtDepth(nodes []types.ThoughtNode, depth int) []types.ThoughtNode {
	var result []types.ThoughtNode
	for _, n := range nodes {
		if n.Depth == depth {
			result = append(result, n)
		}
	}
	return result
}

func maxNodeDepth(nodes []types.ThoughtNode) int {
	m := 0
	for _, n := range nodes {
		if n.Depth > m {
			m = n.Depth
		}
	}
	return m
}

func truncateTo(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
