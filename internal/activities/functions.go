package activities

import (
	"context"

	"cribug/internal/events"
	"cribug/internal/types"
)

type EmitEventFn func(ctx context.Context, taskID string, event events.AgentEvent) error
type SaveResultFn func(ctx context.Context, input SaveResultInput) error
type SaveFailureFn func(ctx context.Context, input SaveFailureInput) error
type RecordExecutionCompletedFn func(ctx context.Context, input RecordExecutionInput) error
type RecordExecutionFailedFn func(ctx context.Context, input RecordExecutionFailedInput) error

func NewSaveResultInput(taskID, result string) SaveResultInput {
	return SaveResultInput{
		TaskID: taskID,
		Result: result,
	}
}

func NewSaveFailureInput(taskID, errorType, errorMsg string) SaveFailureInput {
	return SaveFailureInput{
		TaskID:    taskID,
		ErrorType: errorType,
		ErrorMsg:  errorMsg,
	}
}

func NewRecordExecutionInput(taskID, workflowID, runID string) RecordExecutionInput {
	return RecordExecutionInput{
		TaskID:     taskID,
		WorkflowID: workflowID,
		RunID:      runID,
	}
}

func NewRecordExecutionFailedInput(taskID, workflowID, runID, errorType, errorMsg string) RecordExecutionFailedInput {
	return RecordExecutionFailedInput{
		TaskID:     taskID,
		WorkflowID: workflowID,
		RunID:      runID,
		ErrorType:  errorType,
		ErrorMsg:   errorMsg,
	}
}

type WorkflowTaskResult = types.WorkflowTaskResult