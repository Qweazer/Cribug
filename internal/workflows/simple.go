package workflows

import (
	"cribug/internal/activities"
	"cribug/internal/events"
	"cribug/internal/types"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const EmptyWorkflowResult = "empty workflow completed"

type SimpleWorkflow struct{}

func NewSimpleWorkflow() *SimpleWorkflow {
	return &SimpleWorkflow{}
}

func (sw *SimpleWorkflow) Execute(ctx workflow.Context, req types.WorkflowTaskRequest) (*types.WorkflowTaskResult, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("SimpleWorkflow started", "task_id", req.TaskID, "workflow_id", req.WorkflowID)

	opts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, opts)

	err := workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewWorkflowStartedEvent(req.TaskID, req.WorkflowID),
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("EmitEventActivity (WORKFLOW_STARTED) failed", "error", err)
	}

	err = workflow.ExecuteActivity(ctx, "SaveResultActivity", activities.SaveResultInput{
		TaskID: req.TaskID,
		Result: EmptyWorkflowResult,
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

	err = workflow.ExecuteActivity(ctx, "RecordExecutionCompletedActivity", activities.RecordExecutionInput{
		TaskID:     req.TaskID,
		WorkflowID: req.WorkflowID,
		RunID:      req.RunID,
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("RecordExecutionCompletedActivity failed", "error", err)
	}

	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewTaskCompletedEvent(req.TaskID, req.WorkflowID),
	}).Get(ctx, nil)

	logger.Info("SimpleWorkflow completed", "task_id", req.TaskID)
	return &types.WorkflowTaskResult{
		TaskID: req.TaskID,
		Status: types.TaskStatusCompleted,
		Answer: EmptyWorkflowResult,
	}, nil
}