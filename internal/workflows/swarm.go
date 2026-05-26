package workflows

import (
	"fmt"
	"time"

	"cribug/internal/activities"
	"cribug/internal/events"
	"cribug/internal/types"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const SwarmWorkflowName = "SwarmWorkflow"

type SwarmWorkflow struct{}

func NewSwarmWorkflow() *SwarmWorkflow {
	return &SwarmWorkflow{}
}

func (sw *SwarmWorkflow) Execute(ctx workflow.Context, req types.SwarmWorkflowInput) (*types.SwarmWorkflowResult, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("SwarmWorkflow started", "task_id", req.TaskID, "workers", req.WorkerCount)

	// Set default activity options
	defaultOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 90 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    500 * time.Millisecond,
			BackoffCoefficient: 2.0,
			MaximumInterval:    5 * time.Second,
			MaximumAttempts:    1,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, defaultOpts)

	// Defaults
	if req.WorkerCount <= 0 {
		req.WorkerCount = 3
	}
	if req.WorkerTimeout <= 0 {
		req.WorkerTimeout = 60
	}

	// Activity options
	actOpts := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(req.WorkerTimeout) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    500 * time.Millisecond,
			BackoffCoefficient: 2.0,
			MaximumInterval:    5 * time.Second,
			MaximumAttempts:    1, // No retry for workers — handled by SwarmWorkflow
		},
	}
	actCtx := workflow.WithActivityOptions(ctx, actOpts)

	// Emit SWARM_STARTED
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewSwarmStartedEvent(req.TaskID, req.WorkflowID, req.WorkerCount),
	}).Get(ctx, nil)

	// ── Decompose tasks into worker assignments ──────────────────
	type workerTask struct {
		AgentID string
		Role    string
		Task    string
	}

	roles := []string{"researcher", "analyst", "critic", "synthesizer", "reviewer"}
	tasks := make([]workerTask, req.WorkerCount)
	for i := 0; i < req.WorkerCount; i++ {
		role := roles[i%len(roles)]
		tasks[i] = workerTask{
			AgentID: fmt.Sprintf("worker-%d", i+1),
			Role:    role,
			Task:    fmt.Sprintf("Analyze the following from the perspective of a %s: %s", role, req.Query),
		}
	}

	// ── Launch all workers concurrently ──────────────────────────
	workerFutures := make(map[string]workflow.Future)
	for _, wt := range tasks {
		workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
			TaskID: req.TaskID,
			Event:  events.NewWorkerAssignedEvent(req.TaskID, req.WorkflowID, wt.AgentID, wt.Role, wt.Task),
		}).Get(ctx, nil)
		workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
			TaskID: req.TaskID,
			Event:  events.NewWorkerStartedEvent(req.TaskID, req.WorkflowID, wt.AgentID, wt.Role),
		}).Get(ctx, nil)

		workerFutures[wt.AgentID] = workflow.ExecuteActivity(actCtx, "WorkerAgentActivity", types.WorkerAgentInput{
			TaskID:      req.TaskID,
			WorkflowID:  req.WorkflowID,
			RunID:       req.RunID,
			AgentID:     wt.AgentID,
			Role:        wt.Role,
			Task:        wt.Task,
			Model:       req.Model,
			Temperature: req.Temperature,
			MaxTokens:   req.MaxTokens,
		})
	}

	// ── Use Selector + Timer to wait for workers ─────────────────
	var results []types.WorkerAgentResult
	timeout := time.Duration(req.WorkerTimeout) * time.Second
	var succeeded, failed, timedOut, totalTokens int
	var totalLatency int64

	for i := 0; i < len(tasks); i++ {
		sel := workflow.NewSelector(ctx)
		timer := workflow.NewTimer(ctx, timeout)

		// Register remaining futures
		for agentID, f := range workerFutures {
			aid := agentID
			sel.AddFuture(f, func(ff workflow.Future) {
				var workerResult types.WorkerAgentResult
				err := ff.Get(ctx, &workerResult)

				// Find role for this agent
				role := ""
				for _, wt := range tasks {
					if wt.AgentID == aid {
						role = wt.Role
						break
					}
				}

				if err != nil {
					logger.Error("Worker failed", "agent_id", aid, "error", err)
					workerResult = types.WorkerAgentResult{
						AgentID: aid, Role: role, Status: types.WorkerStatusFailed,
						Error: err.Error(),
					}
					failed++
					workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
						TaskID: req.TaskID,
						Event:  events.NewWorkerFailedEvent(req.TaskID, req.WorkflowID, aid, role, err.Error()),
					}).Get(ctx, nil)
				} else {
					succeeded++
					totalTokens += workerResult.TotalTokens
					totalLatency += workerResult.LatencyMs
					workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
						TaskID: req.TaskID,
						Event:  events.NewWorkerCompletedEvent(req.TaskID, req.WorkflowID, aid, role, workerResult.Result, workerResult.TotalTokens, int(workerResult.LatencyMs)),
					}).Get(ctx, nil)
				}
				results = append(results, workerResult)
				delete(workerFutures, aid)
			})
		}

		// Register timer
		timerExpired := false
		sel.AddFuture(timer, func(ff workflow.Future) {
			timerExpired = true
			ff.Get(ctx, nil)
		})

		sel.Select(ctx)

		// Handle timeout: mark remaining workers as timed out
		if timerExpired && len(workerFutures) > 0 {
			for aid := range workerFutures {
				role := ""
				for _, wt := range tasks {
					if wt.AgentID == aid {
						role = wt.Role
						break
					}
				}
				timedOut++
				results = append(results, types.WorkerAgentResult{
					AgentID: aid, Role: role, Status: types.WorkerStatusTimeout,
					Error: "worker timed out after " + fmt.Sprint(req.WorkerTimeout) + "s",
				})
				workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
					TaskID: req.TaskID,
					Event:  events.NewWorkerTimeoutEvent(req.TaskID, req.WorkflowID, aid, role),
				}).Get(ctx, nil)
			}
			break // Stop waiting — all remaining are timed out
		}
	}

	// ── Synthesize final answer ───────────────────────────────────
	finalAnswer := fmt.Sprintf("Swarm complete: %d succeeded, %d failed, %d timeout out of %d workers",
		succeeded, failed, timedOut, len(tasks))

	swarmStatus := types.SwarmStatusCompleted
	if failed > 0 || timedOut > 0 {
		if succeeded > 0 {
			swarmStatus = types.SwarmStatusPartialSuccess
		} else {
			swarmStatus = types.SwarmStatusFailed
		}
	}

	// Emit final event
	if swarmStatus == types.SwarmStatusFailed {
		workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
			TaskID: req.TaskID,
			Event:  events.NewSwarmFailedEvent(req.TaskID, req.WorkflowID, "all workers failed or timed out"),
		}).Get(ctx, nil)
	} else {
		workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
			TaskID: req.TaskID,
			Event:  events.NewSwarmCompletedEvent(req.TaskID, req.WorkflowID, succeeded, failed, timedOut, totalTokens),
		}).Get(ctx, nil)
	}

	// Save result
	promptTokens := totalTokens * 6 / 10
	completionTokens := totalTokens - promptTokens
	workflow.ExecuteActivity(ctx, "SaveResultActivity", activities.SaveResultInput{
		TaskID:           req.TaskID,
		Result:           finalAnswer,
		PromptTokens:     &promptTokens,
		CompletionTokens: &completionTokens,
		TotalTokens:      &totalTokens,
	}).Get(ctx, nil)
	workflow.ExecuteActivity(ctx, "RecordExecutionCompletedActivity", activities.RecordExecutionInput{
		TaskID: req.TaskID, WorkflowID: req.WorkflowID, RunID: req.RunID,
	}).Get(ctx, nil)
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewTaskCompletedEvent(req.TaskID, req.WorkflowID),
	}).Get(ctx, nil)

	logger.Info("SwarmWorkflow completed",
		"succeeded", succeeded, "failed", failed, "timeout", timedOut)

	return &types.SwarmWorkflowResult{
		TaskID:        req.TaskID,
		WorkflowID:    req.WorkflowID,
		Status:        swarmStatus,
		TotalWorkers:  len(tasks),
		Succeeded:     succeeded,
		Failed:        failed,
		Timeout:       timedOut,
		TotalTokens:   totalTokens,
		TotalLatencyMs: totalLatency,
		FinalAnswer:   finalAnswer,
		WorkerResults: results,
	}, nil
}
