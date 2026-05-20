package workflows

import (
	"cribug/internal/activities"
	"cribug/internal/events"
	"cribug/internal/types"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const DAGWorkflowName = "DAGWorkflow"

type DAGWorkflow struct{}

func NewDAGWorkflow() *DAGWorkflow {
	return &DAGWorkflow{}
}

func (dw *DAGWorkflow) Execute(ctx workflow.Context, req types.WorkflowTaskRequest) (*types.WorkflowTaskResult, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("DAGWorkflow started", "task_id", req.TaskID, "workflow_id", req.WorkflowID)

	opts := workflow.ActivityOptions{
		StartToCloseTimeout: 90 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, opts)

	// 1. Emit WORKFLOW_STARTED
	err := workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewWorkflowStartedEvent(req.TaskID, req.WorkflowID),
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("EmitEventActivity (WORKFLOW_STARTED) failed", "error", err)
	}

	// 2. LoadSessionActivity
	var sessionOutput *activities.LoadSessionOutput
	err = workflow.ExecuteActivity(ctx, "LoadSessionActivity", activities.LoadSessionInput{
		TaskID:    req.TaskID,
		SessionID: req.SessionID,
	}).Get(ctx, &sessionOutput)
	if err != nil {
		logger.Error("LoadSessionActivity failed", "error", err)
	}

	// 3. Emit SESSION_LOADED
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewSessionLoadedEvent(req.TaskID, sessionOutput.MessageCount),
	}).Get(ctx, nil)

	// 4. EstimatePromptTokensActivity for budget check
	var estOutput *activities.EstimatePromptTokensOutput
	err = workflow.ExecuteActivity(ctx, "EstimatePromptTokensActivity", activities.EstimatePromptTokensInput{
		Model: req.Model,
		Text:  req.Query,
	}).Get(ctx, &estOutput)
	if err != nil {
		logger.Error("EstimatePromptTokensActivity failed", "error", err)
	}

	// 5. CheckBudgetActivity
	var budgetOutput *activities.CheckBudgetOutput
	err = workflow.ExecuteActivity(ctx, "CheckBudgetActivity", activities.CheckBudgetInput{
		EstimatedPromptTokens: estOutput.EstimatedPromptTokens,
		MaxTotalTokens:        req.MaxTotalTokens,
		MaxCompletionTokens:   req.MaxCompletionTokens,
	}).Get(ctx, &budgetOutput)
	if err != nil {
		logger.Error("CheckBudgetActivity failed", "error", err)
	}

	// 6. If budget not allowed, return budget_exceeded
	if !budgetOutput.Allowed {
		logger.Warn("Budget exceeded in DAGWorkflow", "reason", budgetOutput.Reason)

		workflow.ExecuteActivity(ctx, "SaveFailureActivity", activities.SaveFailureInput{
			TaskID:    req.TaskID,
			ErrorType: types.TaskStatusBudgetExceeded,
			ErrorMsg:  budgetOutput.Reason,
		}).Get(ctx, nil)

		workflow.ExecuteActivity(ctx, "RecordExecutionFailedActivity", activities.RecordExecutionFailedInput{
			TaskID:     req.TaskID,
			WorkflowID: req.WorkflowID,
			RunID:      req.RunID,
			ErrorType:  types.TaskStatusBudgetExceeded,
			ErrorMsg:   budgetOutput.Reason,
		}).Get(ctx, nil)

		workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
			TaskID: req.TaskID,
			Event:  events.NewTaskBudgetExceededEvent(req.TaskID, budgetOutput.Reason, estOutput.EstimatedPromptTokens, req.MaxTotalTokens),
		}).Get(ctx, nil)

		return &types.WorkflowTaskResult{
			TaskID: req.TaskID,
			Status: types.TaskStatusBudgetExceeded,
			Answer: "",
			Error:  budgetOutput.Reason,
		}, nil
	}

	// 7. ClassifyTaskActivity
	var classifyOutput *activities.ClassifyTaskOutput
	err = workflow.ExecuteActivity(ctx, "ClassifyTaskActivity", activities.ClassifyTaskInput{
		TaskID:      req.TaskID,
		Query:       req.Query,
		EnableTools: false, // tools not enabled by default
	}).Get(ctx, &classifyOutput)
	if err != nil {
		logger.Error("ClassifyTaskActivity failed", "error", err)
	}

	classification := classifyOutput.Classification

	// 8. Emit TASK_CLASSIFIED event
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewTaskClassifiedEvent(req.TaskID, classification.Category, classification.Complexity, classification.RequiresTools),
	}).Get(ctx, nil)

	// 9. PlanDAGActivity
	var planOutput *activities.PlanDAGOutput
	err = workflow.ExecuteActivity(ctx, "PlanDAGActivity", activities.PlanDAGInput{
		TaskID:         req.TaskID,
		Query:          req.Query,
		Classification: classification,
	}).Get(ctx, &planOutput)
	if err != nil {
		logger.Error("PlanDAGActivity failed", "error", err)
	}

	plan := planOutput.Plan

	// 10. Emit DAG_PLANNED event
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewDAGPlannedEvent(req.TaskID, len(plan.Nodes), len(plan.Edges)),
	}).Get(ctx, nil)

	// 11. Execute DAG nodes with concurrent execution per layer
	nodeResults := make(map[string]types.DAGNodeResult)
	llmNodes := 0
	totalTokens := 0
	var totalPromptTokens, totalCompletionTokens int

	// ========== 阶段 2：并发执行阶段 ===========
	// 并发度由 req.MaxParallelAgents 控制，每层节点会并发执行

	// Determine concurrency level from request (for logging)
	maxParallel := 1
	if req.MaxParallelAgents > 0 {
		maxParallel = req.MaxParallelAgents
	}

	logger.Info("Starting concurrent node execution", "max_parallel", maxParallel)

	// ========== 按拓扑顺序启动可并发的节点 ===========
	// 1. Group nodes by "layer" (nodes with same dependencies can run concurrently)
	nodeLayers := groupNodesByLayer(plan.Nodes)
	logger.Info("Nodes grouped into layers", "num_layers", len(nodeLayers))

	// 2. Execute each layer concurrently
	for _, layer := range nodeLayers {
		layerFutures := make(map[string]workflow.Future)

		logger.Info("Executing DAG node layer", "layer_size", len(layer))

		// Start all nodes in this layer concurrently
		for _, node := range layer {
			logger.Info("Executing DAG node", "node_id", node.ID, "use_llm", node.UseLLM)

			// Build upstream results
			upstreamResults := make(map[string]types.DAGNodeResult)
			for _, depID := range node.DependsOn {
				if result, ok := nodeResults[depID]; ok {
					upstreamResults[depID] = result
				}
			}

			// If LLM node, emit LLM_STARTED
			if node.UseLLM {
				workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
					TaskID: req.TaskID,
					Event:  events.NewLLMStartedEvent(req.TaskID, req.Model),
				}).Get(ctx, nil)
			}

			// Emit DAG_NODE_STARTED
			workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
				TaskID: req.TaskID,
				Event:  events.NewDAGNodeStartedEvent(req.TaskID, node.ID, node.Type),
			}).Get(ctx, nil)

			// Start node execution
			layerFutures[node.ID] = workflow.ExecuteActivity(ctx, "ExecuteDAGNodeActivity", activities.ExecuteDAGNodeInput{
				TaskID:          req.TaskID,
				WorkflowID:      req.WorkflowID,
				RunID:           req.RunID,
				Query:           req.Query,
				Node:            node,
				UpstreamResults: upstreamResults,
				Model:           req.Model,
				Temperature:     req.Temperature,
				MaxTokens:       req.MaxCompletionTokens,
			})
		}

		// Wait for all nodes in this layer to complete
		for nodeID, future := range layerFutures {
			var nodeOutput *activities.ExecuteDAGNodeOutput
			err := future.Get(ctx, &nodeOutput)

			if err != nil {
				logger.Error("ExecuteDAGNodeActivity failed", "node_id", nodeID, "error", err)
				nodeResults[nodeID] = types.DAGNodeResult{
					TaskID:   req.TaskID,
					NodeID:   nodeID,
					NodeType: "", // Will be populated from plan lookup
					Status:   "failed",
					Error:    err.Error(),
				}
			} else {
				nodeResults[nodeID] = *nodeOutput.Result

				// If LLM node, record usage and emit LLM_COMPLETED
				if nodeOutput.Result.NodeType == "llm" && nodeOutput.Usage != nil {
					llmNodes++
					totalTokens += nodeOutput.Usage.TotalTokens
					totalPromptTokens += nodeOutput.Usage.PromptTokens
					totalCompletionTokens += nodeOutput.Usage.CompletionTokens

					finishReason := "stop"
					if nodeOutput.Result.Error != "" {
						finishReason = "error"
					}

					// Record LLM usage for node
					workflow.ExecuteActivity(ctx, "RecordDAGNodeUsageActivity", activities.RecordDAGNodeUsageInput{
						TaskID:           req.TaskID,
						WorkflowID:       req.WorkflowID,
						RunID:            req.RunID,
						NodeID:           nodeID,
						Model:            req.Model,
						Provider:         "openai_compatible",
						PromptTokens:     nodeOutput.Usage.PromptTokens,
						CompletionTokens: nodeOutput.Usage.CompletionTokens,
						TotalTokens:      nodeOutput.Usage.TotalTokens,
						LatencyMS:        0, // not tracked per-node
						FinishReason:     finishReason,
					}).Get(ctx, nil)

					// Emit LLM_COMPLETED
					workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
						TaskID: req.TaskID,
						Event:  events.NewLLMCompletedEvent(req.TaskID, req.Model, finishReason, 0),
					}).Get(ctx, nil)

					// Emit USAGE_RECORDED
					workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
						TaskID: req.TaskID,
						Event:  events.NewUsageRecordedEvent(req.TaskID, totalTokens),
					}).Get(ctx, nil)
				}
			}

			// Emit DAG_NODE_COMPLETED
			workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
				TaskID: req.TaskID,
				Event:  events.NewDAGNodeCompletedEvent(
					req.TaskID,
					nodeID,
					nodeResults[nodeID].NodeType,
					nodeResults[nodeID].Status,
					nodeResults[nodeID].Output,
				),
			}).Get(ctx, nil)
		}
	}

	// 12. SynthesisActivity
	var synthesisOutput *activities.SynthesisOutput
	err = workflow.ExecuteActivity(ctx, "SynthesisActivity", activities.SynthesisInput{
		TaskID:         req.TaskID,
		Query:          req.Query,
		Classification: classification,
		Plan:           plan,
		NodeResults:    nodeResults,
	}).Get(ctx, &synthesisOutput)

	if err != nil {
		logger.Error("SynthesisActivity failed", "error", err)

		workflow.ExecuteActivity(ctx, "SaveFailureActivity", activities.SaveFailureInput{
			TaskID:    req.TaskID,
			ErrorType: types.ErrorTypeWorkflow,
			ErrorMsg:  err.Error(),
		}).Get(ctx, nil)

		workflow.ExecuteActivity(ctx, "RecordExecutionFailedActivity", activities.RecordExecutionFailedInput{
			TaskID:     req.TaskID,
			WorkflowID: req.WorkflowID,
			RunID:      req.RunID,
			ErrorType:  types.ErrorTypeWorkflow,
			ErrorMsg:   err.Error(),
		}).Get(ctx, nil)

		workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
			TaskID: req.TaskID,
			Event:  events.NewTaskFailedEvent(req.TaskID, req.WorkflowID, err.Error()),
		}).Get(ctx, nil)

		return nil, err
	}

	// 13. Emit DAG_SYNTHESIZED event
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewDAGSynthesizedEvent(
			req.TaskID,
			synthesisOutput.Result.NodeCount,
			synthesisOutput.Result.CompletedNodes,
			synthesisOutput.Result.LLMNodes,
			synthesisOutput.Result.TotalTokens,
		),
	}).Get(ctx, nil)

	// 14. SaveResultActivity with usage
	err = workflow.ExecuteActivity(ctx, "SaveResultActivity", activities.SaveResultInput{
		TaskID:           req.TaskID,
		Result:          synthesisOutput.Result.FinalAnswer,
		PromptTokens:     &synthesisOutput.Result.TotalPromptTokens,
		CompletionTokens: &synthesisOutput.Result.TotalCompletionTokens,
		TotalTokens:      &synthesisOutput.Result.TotalTokens,
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("SaveResultActivity failed", "error", err)

		workflow.ExecuteActivity(ctx, "SaveFailureActivity", activities.SaveFailureInput{
			TaskID:    req.TaskID,
			ErrorType: types.ErrorTypeWorkflow,
			ErrorMsg:  err.Error(),
		}).Get(ctx, nil)

		workflow.ExecuteActivity(ctx, "RecordExecutionFailedActivity", activities.RecordExecutionFailedInput{
			TaskID:     req.TaskID,
			WorkflowID: req.WorkflowID,
			RunID:      req.RunID,
			ErrorType:  types.ErrorTypeWorkflow,
			ErrorMsg:   err.Error(),
		}).Get(ctx, nil)

		workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
			TaskID: req.TaskID,
			Event:  events.NewTaskFailedEvent(req.TaskID, req.WorkflowID, err.Error()),
		}).Get(ctx, nil)

		return nil, err
	}

	// 15. RecordExecutionCompletedActivity
	err = workflow.ExecuteActivity(ctx, "RecordExecutionCompletedActivity", activities.RecordExecutionInput{
		TaskID:     req.TaskID,
		WorkflowID: req.WorkflowID,
		RunID:      req.RunID,
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("RecordExecutionCompletedActivity failed", "error", err)
	}

	// 16. Emit TASK_COMPLETED
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewTaskCompletedEvent(req.TaskID, req.WorkflowID),
	}).Get(ctx, nil)

	logger.Info("DAGWorkflow synthesis completed", "task_id", req.TaskID, "result", synthesisOutput.Result.FinalAnswer)
	return &types.WorkflowTaskResult{
		TaskID: req.TaskID,
		Status: types.TaskStatusCompleted,
		Answer: synthesisOutput.Result.FinalAnswer,
	}, nil
}

// groupNodesByLayer groups nodes so that nodes in the same layer can run concurrently
// Nodes are in the same layer if they have no dependencies on each other
func groupNodesByLayer(nodes []types.DAGNode) [][]types.DAGNode {
	// Create dependency sets
	deps := make(map[string]map[string]bool)
	for _, node := range nodes {
		deps[node.ID] = make(map[string]bool)
		for _, dep := range node.DependsOn {
			deps[node.ID][dep] = true
		}
	}

	var layers [][]types.DAGNode
	remaining := make(map[string]types.DAGNode)
	for _, node := range nodes {
		remaining[node.ID] = node
	}

	for len(remaining) > 0 {
		var currentLayer []types.DAGNode
		var toRemove []string

		for id, node := range remaining {
			// Check if all dependencies are satisfied (not in remaining)
			allSatisfied := true
			for _, dep := range node.DependsOn {
				if _, ok := remaining[dep]; ok {
					allSatisfied = false
					break
				}
			}
			if allSatisfied {
				currentLayer = append(currentLayer, node)
				toRemove = append(toRemove, id)
			}
		}

		if len(currentLayer) == 0 {
			// Circular dependency detected, fall back to single layer
			for id, node := range remaining {
				currentLayer = append(currentLayer, node)
				toRemove = append(toRemove, id)
			}
		}

		for _, id := range toRemove {
			delete(remaining, id)
		}
		layers = append(layers, currentLayer)
	}

	return layers
}
