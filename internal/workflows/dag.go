package workflows

import (
	"fmt"
	"cribug/internal/activities"
	"cribug/internal/events"
	"cribug/internal/types"
	"cribug/internal/workflows/patterns"
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

	// Build ReactConfig from request flags
	var reactConfig *types.ReactLoopConfig
	if req.EnableReAct {
		reactConfig = &types.ReactLoopConfig{
			EnableReAct:       true,
			MaxIterations:    req.ReActMaxIterations,
			EarlyStopOnAnswer: true,
		}
	}

	// 9. PlanDAGActivity (pass ReactConfig if enabled)
	var planOutput *activities.PlanDAGOutput
	err = workflow.ExecuteActivity(ctx, "PlanDAGActivity", activities.PlanDAGInput{
		TaskID:         req.TaskID,
		Query:          req.Query,
		Classification: classification,
		ReactConfig:    reactConfig,
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
	for layerIdx, layer := range nodeLayers {
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

			// ========== DAG Visualization: Record DAG_NODE_PENDING ==========
			nowNs := workflow.Now(ctx).UnixNano()
			workflow.ExecuteActivity(ctx, "RecordDAGNodeStatus", activities.RecordDAGNodeStatusInput{
				TaskID:       req.TaskID,
				WorkflowID:   req.WorkflowID,
				NodeID:       node.ID,
				Status:       types.NodeStatusPending,
				Layer:        layerIdx,
				Dependencies: node.DependsOn,
				StartedAtNs: nowNs,
			}).Get(ctx, nil)

			// If LLM node, emit LLM_STARTED
			if node.UseLLM {
				workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
					TaskID: req.TaskID,
					Event:  events.NewLLMStartedEvent(req.TaskID, req.Model),
				}).Get(ctx, nil)
			}

			// ========== DAG Visualization: Record DAG_NODE_RUNNING ==========
			workflow.ExecuteActivity(ctx, "RecordDAGNodeStatus", activities.RecordDAGNodeStatusInput{
				TaskID:       req.TaskID,
				WorkflowID:   req.WorkflowID,
				NodeID:       node.ID,
				Status:       types.NodeStatusRunning,
				Layer:        layerIdx,
				Dependencies: node.DependsOn,
				StartedAtNs:  nowNs,
			}).Get(ctx, nil)

			// Emit DAG_NODE_STARTED (kept for backward compatibility)
			workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
				TaskID: req.TaskID,
				Event:  events.NewDAGNodeStartedEvent(req.TaskID, node.ID, node.Type),
			}).Get(ctx, nil)

			// Build prompt from upstream results for LLM nodes
			upstreamContext := ""
			for depID, result := range upstreamResults {
				upstreamContext += fmt.Sprintf("[%s] %s\n", depID, result.Output)
			}
			if upstreamContext == "" {
				upstreamContext = "No upstream analysis available."
			}

			// Build full prompt for this node
			prompt := fmt.Sprintf(`You are working on a DAG task.

Original query: %s

Upstream node results:
%s

Task: Complete the "%s" node (type: %s) by providing your output.

Output your answer directly:`, req.Query, upstreamContext, node.Name, node.Type)

			// Start node execution - use ReAct if enabled, otherwise use regular DAG node
			if node.UseLLM && node.ReactConfig != nil && node.ReactConfig.EnableReAct {
				// Workflow-level ReAct node: call ReactLoop (Workflow function, not Activity)
				// Each Reason/Act/Synthesis step is a separate AgentActivity call
				reactConfig := types.ReactConfig{
					MaxIterations:     node.ReactConfig.MaxIterations,
					MinIterations:     1,
					ObservationWindow: 3,
					MaxObservations:   10,
					MaxThoughts:       10,
					MaxActions:        10,
				}
				if reactConfig.MaxIterations <= 0 {
					reactConfig.MaxIterations = 3
				}

				reactResult, reactErr := patterns.ReactLoop(
					ctx,
					prompt,
					"",          // baseContext
					req.SessionID,
					req.TaskID,
					req.WorkflowID,
					req.RunID,
					req.Model,
					req.Temperature,
					req.MaxCompletionTokens,
					reactConfig,
				)

				nowNs := workflow.Now(ctx).UnixNano()
				if reactErr != nil {
					logger.Error("ReAct node execution failed", "node_id", node.ID, "error", reactErr)
					nodeResults[node.ID] = types.DAGNodeResult{
						TaskID:   req.TaskID,
						NodeID:   node.ID,
						NodeType: "llm",
						Status:   "failed",
						Error:    reactErr.Error(),
					}
					workflow.ExecuteActivity(ctx, "RecordDAGNodeStatus", activities.RecordDAGNodeStatusInput{
						TaskID:        req.TaskID,
						WorkflowID:    req.WorkflowID,
						NodeID:        node.ID,
						Status:        types.NodeStatusFailed,
						Layer:         layerIdx,
						CompletedAtNs: nowNs,
						Error:         reactErr.Error(),
					}).Get(ctx, nil)
					workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
						TaskID: req.TaskID,
						Event:  events.NewDAGNodeCompletedEvent(req.TaskID, node.ID, "llm", "failed", reactErr.Error()),
					}).Get(ctx, nil)
				} else {
					nodeResults[node.ID] = types.DAGNodeResult{
						TaskID:   req.TaskID,
						NodeID:   node.ID,
						NodeType: "llm",
						Status:   "completed",
						Output:   reactResult.FinalResult,
					}
					workflow.ExecuteActivity(ctx, "RecordDAGNodeStatus", activities.RecordDAGNodeStatusInput{
						TaskID:        req.TaskID,
						WorkflowID:    req.WorkflowID,
						NodeID:        node.ID,
						Status:        types.NodeStatusCompleted,
						Layer:         layerIdx,
						CompletedAtNs: nowNs,
					}).Get(ctx, nil)
					workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
						TaskID: req.TaskID,
						Event:  events.NewDAGNodeCompletedEvent(req.TaskID, node.ID, "llm", "completed", reactResult.FinalResult),
					}).Get(ctx, nil)
					// Track ReAct usage for this node
					if reactResult.TotalTokens > 0 {
						llmNodes++
						totalTokens += reactResult.TotalTokens
						totalPromptTokens += reactResult.TotalTokens * 6 / 10
						totalCompletionTokens += reactResult.TotalTokens - (reactResult.TotalTokens * 6 / 10)

						workflow.ExecuteActivity(ctx, "RecordDAGNodeUsageActivity", activities.RecordDAGNodeUsageInput{
							TaskID:           req.TaskID,
							WorkflowID:       req.WorkflowID,
							RunID:            req.RunID,
							NodeID:           node.ID,
							Model:            req.Model,
							Provider:         "openai_compatible",
							PromptTokens:     totalPromptTokens,
							CompletionTokens: totalCompletionTokens,
							TotalTokens:      reactResult.TotalTokens,
							LatencyMS:        0,
							FinishReason:     "stop",
						}).Get(ctx, nil)
					}
				}
				// ReAct nodes are handled synchronously (no Future in layerFutures)
			} else {
				// Regular node: call ExecuteDAGNodeActivity
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
		}

		// Wait for all nodes in this layer to complete
		for nodeID, future := range layerFutures {
			// Try to get as DAG node result first
			var dagOutput *activities.ExecuteDAGNodeOutput
			err := future.Get(ctx, &dagOutput)

			if err != nil {
				logger.Error("Node execution failed", "node_id", nodeID, "error", err)
				nodeResults[nodeID] = types.DAGNodeResult{
					TaskID:   req.TaskID,
					NodeID:   nodeID,
					NodeType: "", // Will be populated from plan lookup
					Status:   "failed",
					Error:    err.Error(),
				}
			} else {
				nodeResults[nodeID] = *dagOutput.Result

				// If node has LLM usage, record it (node types are "analysis"/"synthesis"/"review", not "llm")
				if dagOutput.Usage != nil && dagOutput.Usage.TotalTokens > 0 {
					llmNodes++
					totalTokens += dagOutput.Usage.TotalTokens
					totalPromptTokens += dagOutput.Usage.PromptTokens
					totalCompletionTokens += dagOutput.Usage.CompletionTokens

					finishReason := "stop"
					if dagOutput.Result.Error != "" {
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
						PromptTokens:     dagOutput.Usage.PromptTokens,
						CompletionTokens: dagOutput.Usage.CompletionTokens,
						TotalTokens:      dagOutput.Usage.TotalTokens,
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

			// Emit DAG_NODE_COMPLETED or DAG_NODE_FAILED based on status
			nowNs := workflow.Now(ctx).UnixNano()
			if nodeResults[nodeID].Status == "failed" {
				// ========== DAG Visualization: Record DAG_NODE_FAILED ==========
				workflow.ExecuteActivity(ctx, "RecordDAGNodeStatus", activities.RecordDAGNodeStatusInput{
					TaskID:        req.TaskID,
					WorkflowID:    req.WorkflowID,
					NodeID:        nodeID,
					Status:        types.NodeStatusFailed,
					Layer:         layerIdx,
					CompletedAtNs: nowNs,
					Error:         nodeResults[nodeID].Error,
				}).Get(ctx, nil)

				// Emit DAG_NODE_COMPLETED event for failure (backward compatibility)
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
			} else {
				// ========== DAG Visualization: Record DAG_NODE_COMPLETED ==========
				workflow.ExecuteActivity(ctx, "RecordDAGNodeStatus", activities.RecordDAGNodeStatusInput{
					TaskID:        req.TaskID,
					WorkflowID:    req.WorkflowID,
					NodeID:        nodeID,
					Status:        types.NodeStatusCompleted,
					Layer:         layerIdx,
					CompletedAtNs: nowNs,
				}).Get(ctx, nil)

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
