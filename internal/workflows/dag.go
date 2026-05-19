package workflows

import (
	"fmt"

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

	// 4. ClassifyTaskActivity
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

	// 5. Emit TASK_CLASSIFIED event
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewTaskClassifiedEvent(req.TaskID, classification.Category, classification.Complexity, classification.RequiresTools),
	}).Get(ctx, nil)

	// 6. PlanDAGActivity
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

	// 7. Emit DAG_PLANNED event
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewDAGPlannedEvent(req.TaskID, len(plan.Nodes), len(plan.Edges)),
	}).Get(ctx, nil)

	// 8. Execute DAG nodes in order
	nodeResults := make(map[string]types.DAGNodeResult)
	completedNodes := 0

	for _, node := range plan.Nodes {
		logger.Info("Executing DAG node", "node_id", node.ID, "depends_on", node.DependsOn)

		// Emit DAG_NODE_STARTED
		workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
			TaskID: req.TaskID,
			Event:  events.NewDAGNodeStartedEvent(req.TaskID, node.ID, node.Type),
		}).Get(ctx, nil)

		// Build upstream results map
		upstreamResults := make(map[string]types.DAGNodeResult)
		for _, depID := range node.DependsOn {
			if result, ok := nodeResults[depID]; ok {
				upstreamResults[depID] = result
			}
		}

		// Execute the node
		var nodeOutput *activities.ExecuteDAGNodeOutput
		err = workflow.ExecuteActivity(ctx, "ExecuteDAGNodeActivity", activities.ExecuteDAGNodeInput{
			TaskID:          req.TaskID,
			Query:           req.Query,
			Node:            node,
			UpstreamResults: upstreamResults,
		}).Get(ctx, &nodeOutput)
		if err != nil {
			logger.Error("ExecuteDAGNodeActivity failed", "node_id", node.ID, "error", err)
			// Continue with failed node result
			nodeResults[node.ID] = types.DAGNodeResult{
				TaskID:   req.TaskID,
				NodeID:   node.ID,
				NodeType: node.Type,
				Status:   "failed",
				Error:    err.Error(),
			}
		} else {
			nodeResults[node.ID] = *nodeOutput.Result
			completedNodes++
		}

		// Emit DAG_NODE_COMPLETED
		workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
			TaskID: req.TaskID,
			Event:  events.NewDAGNodeCompletedEvent(
				req.TaskID,
				node.ID,
				node.Type,
				nodeResults[node.ID].Status,
				nodeResults[node.ID].Output,
			),
		}).Get(ctx, nil)
	}

	// 9. Build result with execution info
	result := fmt.Sprintf("dag executed: category=%s, node_count=%d, completed_nodes=%d",
		classification.Category, len(plan.Nodes), completedNodes)

	err = workflow.ExecuteActivity(ctx, "SaveResultActivity", activities.SaveResultInput{
		TaskID: req.TaskID,
		Result: result,
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

	// 10. RecordExecutionCompletedActivity
	err = workflow.ExecuteActivity(ctx, "RecordExecutionCompletedActivity", activities.RecordExecutionInput{
		TaskID:     req.TaskID,
		WorkflowID: req.WorkflowID,
		RunID:      req.RunID,
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("RecordExecutionCompletedActivity failed", "error", err)
	}

	// 11. Emit TASK_COMPLETED
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewTaskCompletedEvent(req.TaskID, req.WorkflowID),
	}).Get(ctx, nil)

	logger.Info("DAGWorkflow execution completed", "task_id", req.TaskID, "result", result)
	return &types.WorkflowTaskResult{
		TaskID: req.TaskID,
		Status: types.TaskStatusCompleted,
		Answer: result,
	}, nil
}