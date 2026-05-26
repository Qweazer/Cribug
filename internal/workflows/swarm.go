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

func NewSwarmWorkflow() *SwarmWorkflow { return &SwarmWorkflow{} }

func (sw *SwarmWorkflow) Execute(ctx workflow.Context, req types.SwarmWorkflowInput) (*types.SwarmWorkflowResult, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("SwarmWorkflow started", "task_id", req.TaskID, "workers", req.WorkerCount)

	if req.WorkerCount <= 0 {
		req.WorkerCount = 3
	}
	if req.WorkerTimeout <= 0 {
		req.WorkerTimeout = 60
	}
	if req.MaxP2PRounds <= 0 {
		req.MaxP2PRounds = 2
	}

	defaultOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 90 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: 500 * time.Millisecond, BackoffCoefficient: 2.0,
			MaximumInterval: 5 * time.Second, MaximumAttempts: 1,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, defaultOpts)

	// Emit SWARM_STARTED
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewSwarmStartedEvent(req.TaskID, req.WorkflowID, req.WorkerCount),
	}).Get(ctx, nil)

	// ── Worker definitions ──────────────────────────────────────
	type workerDef struct {
		AgentID string
		Role    string
		Task    string
	}
	roles := []string{"researcher", "analyst", "critic", "synthesizer", "reviewer"}
	workers := make([]workerDef, req.WorkerCount)
	for i := 0; i < req.WorkerCount; i++ {
		role := roles[i%len(roles)]
		workers[i] = workerDef{
			AgentID: fmt.Sprintf("worker-%d", i+1),
			Role:    role,
			Task:    fmt.Sprintf("Analyze from the perspective of a %s: %s", role, req.Query),
		}
	}
	agentIDs := make(map[string]workerDef)
	for _, w := range workers {
		agentIDs[w.AgentID] = w
	}

	// ── State ────────────────────────────────────────────────────
	var allResults []types.WorkerAgentResult
	var allMessages []types.AgentMessage
	var succeeded, failed, timedOut, totalTokens int
	var totalLatency int64

	// Per-worker inbox for next round: agentID → messages
	inboxes := make(map[string][]types.AgentMessage)

	// ── Execute rounds ───────────────────────────────────────────
	for round := 0; round <= req.MaxP2PRounds; round++ {
		// Determine which workers to run this round
		type roundTask struct {
			def    workerDef
			inbox  []types.AgentMessage
		}
		var roundTasks []roundTask
		if round == 0 {
			// Round 0: all workers
			for _, w := range workers {
				roundTasks = append(roundTasks, roundTask{def: w})
			}
		} else {
			// Subsequent rounds: only workers with inbox messages
			for _, w := range workers {
				if msgs, ok := inboxes[w.AgentID]; ok && len(msgs) > 0 {
					roundTasks = append(roundTasks, roundTask{def: w, inbox: msgs})
				}
			}
			if len(roundTasks) == 0 {
				break // No more inbox messages — done
			}
		}

		// Emit P2P_ROUND_STARTED
		workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
			TaskID: req.TaskID,
			Event:  events.NewP2PRoundStartedEvent(req.TaskID, req.WorkflowID, round, len(roundTasks)),
		}).Get(ctx, nil)

		// Launch workers for this round
		roundFutures := make(map[string]workflow.Future)
		for _, rt := range roundTasks {
			w := rt.def
			workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
				TaskID: req.TaskID,
				Event:  events.NewWorkerAssignedEvent(req.TaskID, req.WorkflowID, w.AgentID, w.Role, w.Task),
			}).Get(ctx, nil)
			workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
				TaskID: req.TaskID,
				Event:  events.NewWorkerStartedEvent(req.TaskID, req.WorkflowID, w.AgentID, w.Role),
			}).Get(ctx, nil)

			roundFutures[w.AgentID] = workflow.ExecuteActivity(ctx, "WorkerAgentActivity", types.WorkerAgentInput{
				TaskID:     req.TaskID, WorkflowID: req.WorkflowID, RunID: req.RunID,
				AgentID:    w.AgentID, Role: w.Role, Task: w.Task,
				Model: req.Model, Temperature: req.Temperature, MaxTokens: req.MaxTokens,
				InboxMessages: rt.inbox, Round: round,
			})
		}

		// Wait using Selector + Timer
		timeout := time.Duration(req.WorkerTimeout) * time.Second
		for i := 0; i < len(roundTasks); i++ {
			sel := workflow.NewSelector(ctx)
			timer := workflow.NewTimer(ctx, timeout)

			for aid, f := range roundFutures {
				agentID := aid
				sel.AddFuture(f, func(ff workflow.Future) {
					var wr types.WorkerAgentResult
					err := ff.Get(ctx, &wr)
					if err != nil {
						failed++
						workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
							TaskID: req.TaskID,
							Event:  events.NewWorkerFailedEvent(req.TaskID, req.WorkflowID, agentID, "", err.Error()),
						}).Get(ctx, nil)
						wr = types.WorkerAgentResult{AgentID: agentID, Status: types.WorkerStatusFailed, Error: err.Error()}
					} else {
						succeeded++
						totalTokens += wr.TotalTokens
						totalLatency += wr.LatencyMs
						workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
							TaskID: req.TaskID,
							Event:  events.NewWorkerCompletedEvent(req.TaskID, req.WorkflowID, agentID, wr.Role, wr.Result, wr.TotalTokens, int(wr.LatencyMs)),
						}).Get(ctx, nil)
					}
					allResults = append(allResults, wr)
					delete(roundFutures, agentID)

					// Route outbound messages
					for _, msg := range wr.OutboundMessages {
						msg.Status = types.MsgStatusCreated
						msg.Round = round
						workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
							TaskID: req.TaskID,
							Event:  events.NewAgentMessageCreatedEvent(req.TaskID, req.WorkflowID, msg.MessageID, msg.FromAgentID, msg.ToAgentID, msg.MessageType, round),
						}).Get(ctx, nil)

						if msg.ToAgentID == "" || msg.ToRole == "lead" {
							allMessages = append(allMessages, msg)
							continue
						}
						if _, ok := agentIDs[msg.ToAgentID]; !ok {
							msg.Status = types.MsgStatusDropped
							allMessages = append(allMessages, msg)
							workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
								TaskID: req.TaskID,
								Event:  events.NewAgentMessageDroppedEvent(req.TaskID, req.WorkflowID, msg.MessageID, msg.FromAgentID, msg.ToAgentID, round),
							}).Get(ctx, nil)
							continue
						}
						msg.Status = types.MsgStatusRouted
						workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
							TaskID: req.TaskID,
							Event:  events.NewAgentMessageRoutedEvent(req.TaskID, req.WorkflowID, msg.MessageID, msg.FromAgentID, msg.ToAgentID, round),
						}).Get(ctx, nil)

						inboxes[msg.ToAgentID] = append(inboxes[msg.ToAgentID], msg)
						msg.Status = types.MsgStatusDelivered
						workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
							TaskID: req.TaskID,
							Event:  events.NewAgentMessageDeliveredEvent(req.TaskID, req.WorkflowID, msg.MessageID, msg.FromAgentID, msg.ToAgentID, round),
						}).Get(ctx, nil)
					}
				})
			}

			timerExpired := false
			sel.AddFuture(timer, func(ff workflow.Future) {
				timerExpired = true; ff.Get(ctx, nil)
			})
			sel.Select(ctx)

			if timerExpired && len(roundFutures) > 0 {
				for aid := range roundFutures {
					timedOut++
					workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
						TaskID: req.TaskID,
						Event:  events.NewWorkerTimeoutEvent(req.TaskID, req.WorkflowID, aid, ""),
					}).Get(ctx, nil)
					allResults = append(allResults, types.WorkerAgentResult{
						AgentID: aid, Status: types.WorkerStatusTimeout,
						Error: fmt.Sprintf("timed out after %ds", req.WorkerTimeout),
					})
				}
				break
			}
		}

		// Emit P2P_ROUND_COMPLETED
		workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
			TaskID: req.TaskID,
			Event:  events.NewP2PRoundCompletedEvent(req.TaskID, req.WorkflowID, round, len(allMessages)),
		}).Get(ctx, nil)
	}

	// ── Compute P2P summary ─────────────────────────────────────
	var routed, delivered, dropped, msgFailed int
	for _, m := range allMessages {
		switch m.Status {
		case types.MsgStatusRouted:
			routed++ // counted as routed but not yet delivered in same iteration
		case types.MsgStatusDelivered:
			delivered++
			routed++ // delivered implies routed
		case types.MsgStatusDropped:
			dropped++
		case types.MsgStatusFailed:
			msgFailed++
		}
	}

	p2p := &types.P2PSummary{
		TotalMessages:    len(allMessages),
		RoutedMessages:   routed,
		DeliveredMessages: delivered,
		FailedMessages:   msgFailed,
		DroppedMessages:  dropped,
		P2PRounds:        req.MaxP2PRounds,
		Messages:         allMessages,
	}

	// ── Final answer ─────────────────────────────────────────────
	finalAnswer := fmt.Sprintf("Swarm+P2P complete: %d succeeded, %d failed, %d timeout, %d messages routed",
		succeeded, failed, timedOut, delivered)

	swarmStatus := types.SwarmStatusCompleted
	if failed > 0 || timedOut > 0 {
		if succeeded > 0 {
			swarmStatus = types.SwarmStatusPartialSuccess
		} else {
			swarmStatus = types.SwarmStatusFailed
		}
	}

	// Save result
	promptTokens := totalTokens * 6 / 10
	completionTokens := totalTokens - promptTokens
	workflow.ExecuteActivity(ctx, "SaveResultActivity", activities.SaveResultInput{
		TaskID: req.TaskID, Result: finalAnswer,
		PromptTokens: &promptTokens, CompletionTokens: &completionTokens, TotalTokens: &totalTokens,
	}).Get(ctx, nil)
	workflow.ExecuteActivity(ctx, "RecordExecutionCompletedActivity", activities.RecordExecutionInput{
		TaskID: req.TaskID, WorkflowID: req.WorkflowID, RunID: req.RunID,
	}).Get(ctx, nil)

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
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewTaskCompletedEvent(req.TaskID, req.WorkflowID),
	}).Get(ctx, nil)

	logger.Info("SwarmWorkflow completed", "succeeded", succeeded, "failed", failed, "timeout", timedOut,
		"p2p_messages", len(allMessages), "p2p_delivered", delivered)

	return &types.SwarmWorkflowResult{
		TaskID: req.TaskID, WorkflowID: req.WorkflowID, Status: swarmStatus,
		TotalWorkers: len(workers), Succeeded: succeeded, Failed: failed, Timeout: timedOut,
		TotalTokens: totalTokens, TotalLatencyMs: totalLatency, FinalAnswer: finalAnswer,
		WorkerResults: allResults, P2PSummary: p2p,
	}, nil
}
