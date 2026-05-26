package events

import "time"

const (
	EventTypeTaskCreated           = "TASK_CREATED"
	EventTypeWorkflowStarted       = "WORKFLOW_STARTED"
	EventTypeTaskCompleted         = "TASK_COMPLETED"
	EventTypeTaskFailed            = "TASK_FAILED"
	EventTypeLLMStarted            = "LLM_STARTED"
	EventTypeLLMCompleted          = "LLM_COMPLETED"
	EventTypeSessionLoaded         = "SESSION_LOADED"
	EventTypeUsageRecorded         = "USAGE_RECORDED"
	EventTypeTaskBudgetExceeded    = "TASK_BUDGET_EXCEEDED"
	EventTypeTaskClassified        = "TASK_CLASSIFIED"
	EventTypeDAGPlanned            = "DAG_PLANNED"
	EventTypeDAGNodeStarted        = "DAG_NODE_STARTED"
	EventTypeDAGNodeCompleted      = "DAG_NODE_COMPLETED"
	EventTypeDAGSynthesized        = "DAG_SYNTHESIZED"
	EventTypeAgentStarted          = "AGENT_STARTED"
	EventTypeAgentCompleted        = "AGENT_COMPLETED"
	EventTypeMultiAgentSynthesized = "MULTI_AGENT_SYNTHESIZED"
	EventTypeCriticReviewed        = "CRITIC_REVIEWED"
	EventTypeToolStarted           = "TOOL_STARTED"
	EventTypeToolCompleted         = "TOOL_COMPLETED"
	EventTypeToolFailed            = "TOOL_FAILED"
	EventTypeToolStepStarted       = "TOOL_STEP_STARTED"
	EventTypeToolStepCompleted     = "TOOL_STEP_COMPLETED"
	EventTypeToolStepFailed        = "TOOL_STEP_FAILED"
	// ReAct events
	EventTypeReActStep             = "REACT_STEP"
	EventTypeReActCompleted        = "REACT_COMPLETED"
	// DAG Visualization events (Slice 10)
	EventTypeDAGNodePending        = "DAG_NODE_PENDING"
	EventTypeDAGNodeRunning        = "DAG_NODE_RUNNING"
	EventTypeDAGNodeFailed         = "DAG_NODE_FAILED"
	// Agent Metrics events
	EventTypeAgentMetricsSummary   = "AGENT_METRICS_SUMMARY"
	// DAG Dynamic Replanning events (Slice 13)
	EventTypeDAGNodeSkipped        = "DAG_NODE_SKIPPED"
	EventTypeDAGReplanSummary      = "DAG_REPLAN_SUMMARY"
	// DAG Concurrency Control (Slice 14)
	EventTypeDAGConcurrencyLimitApplied = "DAG_CONCURRENCY_LIMIT_APPLIED"
	EventTypeDAGNodeRetrying            = "DAG_NODE_RETRYING"
	// Swarm Workflow events (Phase 5A Slice 10)
	EventTypeSwarmStarted   = "SWARM_STARTED"
	EventTypeWorkerAssigned = "WORKER_ASSIGNED"
	EventTypeWorkerStarted  = "WORKER_STARTED"
	EventTypeWorkerCompleted = "WORKER_COMPLETED"
	EventTypeWorkerFailed   = "WORKER_FAILED"
	EventTypeWorkerTimeout  = "WORKER_TIMEOUT"
	EventTypeSwarmCompleted = "SWARM_COMPLETED"
	EventTypeSwarmFailed    = "SWARM_FAILED"
	// P2P Communication events (Phase 5B Slice 11)
	EventTypeAgentMessageCreated   = "AGENT_MESSAGE_CREATED"
	EventTypeAgentMessageRouted    = "AGENT_MESSAGE_ROUTED"
	EventTypeAgentMessageDelivered = "AGENT_MESSAGE_DELIVERED"
	EventTypeAgentMessageFailed    = "AGENT_MESSAGE_FAILED"
	EventTypeAgentMessageDropped   = "AGENT_MESSAGE_DROPPED"
	EventTypeP2PRoundStarted       = "P2P_ROUND_STARTED"
	EventTypeP2PRoundCompleted     = "P2P_ROUND_COMPLETED"
	// Workspace events (Phase 5C Slice 12)
	EventTypeWorkspaceItemCreated     = "WORKSPACE_ITEM_CREATED"
	EventTypeWorkspaceItemAppended    = "WORKSPACE_ITEM_APPENDED"
	EventTypeWorkspaceItemRead        = "WORKSPACE_ITEM_READ"
	EventTypeWorkspaceItemUsed        = "WORKSPACE_ITEM_USED"
	EventTypeWorkspaceItemFailed      = "WORKSPACE_ITEM_FAILED"
	EventTypeWorkspaceSummaryUpdated  = "WORKSPACE_SUMMARY_UPDATED"
)

type AgentEvent struct {
	EventType string                 `json:"event_type"`
	Payload   map[string]interface{} `json:"payload"`
	CreatedAt string                 `json:"created_at"`
}

func NewAgentEvent(eventType string, payload map[string]interface{}) AgentEvent {
	return AgentEvent{
		EventType: eventType,
		Payload:   payload,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

func NewTaskCreatedEvent(taskID, workflowID, query string) AgentEvent {
	return NewAgentEvent(EventTypeTaskCreated, map[string]interface{}{
		"task_id":      taskID,
		"workflow_id":  workflowID,
		"query":        query,
		"timestamp":    time.Now().UTC().Format(time.RFC3339),
	})
}

func NewWorkflowStartedEvent(taskID, workflowID string) AgentEvent {
	return NewAgentEvent(EventTypeWorkflowStarted, map[string]interface{}{
		"task_id":     taskID,
		"workflow_id": workflowID,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
	})
}

func NewTaskCompletedEvent(taskID, workflowID string) AgentEvent {
	return NewAgentEvent(EventTypeTaskCompleted, map[string]interface{}{
		"task_id":     taskID,
		"workflow_id": workflowID,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
	})
}

func NewTaskFailedEvent(taskID, workflowID, errorMsg string) AgentEvent {
	return NewAgentEvent(EventTypeTaskFailed, map[string]interface{}{
		"task_id":     taskID,
		"workflow_id": workflowID,
		"error":       errorMsg,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
	})
}

func NewLLMStartedEvent(taskID, model string) AgentEvent {
	return NewAgentEvent(EventTypeLLMStarted, map[string]interface{}{
		"task_id":   taskID,
		"model":     model,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewLLMCompletedEvent(taskID, model, finishReason string, latencyMS int64) AgentEvent {
	return NewAgentEvent(EventTypeLLMCompleted, map[string]interface{}{
		"task_id":       taskID,
		"model":         model,
		"finish_reason": finishReason,
		"latency_ms":    latencyMS,
		"timestamp":     time.Now().UTC().Format(time.RFC3339),
	})
}

func NewSessionLoadedEvent(taskID string, messageCount int) AgentEvent {
	return NewAgentEvent(EventTypeSessionLoaded, map[string]interface{}{
		"task_id":       taskID,
		"message_count": messageCount,
		"timestamp":     time.Now().UTC().Format(time.RFC3339),
	})
}

func NewUsageRecordedEvent(taskID string, totalTokens int) AgentEvent {
	return NewAgentEvent(EventTypeUsageRecorded, map[string]interface{}{
		"task_id":      taskID,
		"total_tokens": totalTokens,
		"timestamp":    time.Now().UTC().Format(time.RFC3339),
	})
}

func NewTaskBudgetExceededEvent(taskID, reason string, estimatedPromptTokens, maxTotalTokens int) AgentEvent {
	return NewAgentEvent(EventTypeTaskBudgetExceeded, map[string]interface{}{
		"task_id":                 taskID,
		"reason":                  reason,
		"estimated_prompt_tokens": estimatedPromptTokens,
		"max_total_tokens":        maxTotalTokens,
		"timestamp":               time.Now().UTC().Format(time.RFC3339),
	})
}

func NewTaskClassifiedEvent(taskID, category string, complexity float64, requiresTools bool) AgentEvent {
	return NewAgentEvent(EventTypeTaskClassified, map[string]interface{}{
		"task_id":        taskID,
		"category":       category,
		"complexity":     complexity,
		"requires_tools": requiresTools,
		"timestamp":      time.Now().UTC().Format(time.RFC3339),
	})
}

func NewDAGPlannedEvent(taskID string, nodeCount, edgeCount int) AgentEvent {
	return NewAgentEvent(EventTypeDAGPlanned, map[string]interface{}{
		"task_id":    taskID,
		"node_count": nodeCount,
		"edge_count": edgeCount,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
	})
}

func NewDAGNodeStartedEvent(taskID, nodeID, nodeType string) AgentEvent {
	return NewAgentEvent(EventTypeDAGNodeStarted, map[string]interface{}{
		"task_id":   taskID,
		"node_id":   nodeID,
		"node_type": nodeType,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewDAGNodeCompletedEvent(taskID, nodeID, nodeType, status, output string) AgentEvent {
	return NewAgentEvent(EventTypeDAGNodeCompleted, map[string]interface{}{
		"task_id":   taskID,
		"node_id":   nodeID,
		"node_type": nodeType,
		"status":    status,
		"output":    output,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewDAGSynthesizedEvent(taskID string, nodeCount, completedNodes, llmNodes, totalTokens int) AgentEvent {
	return NewAgentEvent(EventTypeDAGSynthesized, map[string]interface{}{
		"task_id":         taskID,
		"node_count":      nodeCount,
		"completed_nodes":  completedNodes,
		"llm_nodes":       llmNodes,
		"total_tokens":    totalTokens,
		"timestamp":       time.Now().UTC().Format(time.RFC3339),
	})
}

func NewAgentStartedEvent(taskID, role string, stepIndex int) AgentEvent {
	return NewAgentEvent(EventTypeAgentStarted, map[string]interface{}{
		"task_id":    taskID,
		"role":       role,
		"step_index": stepIndex,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
	})
}

func NewAgentCompletedEvent(taskID, role, status, output string) AgentEvent {
	return NewAgentEvent(EventTypeAgentCompleted, map[string]interface{}{
		"task_id":   taskID,
		"role":      role,
		"status":    status,
		"output":    output,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewMultiAgentSynthesizedEvent(taskID string, agentCount, completedAgents, totalTokens int) AgentEvent {
	return NewAgentEvent(EventTypeMultiAgentSynthesized, map[string]interface{}{
		"task_id":          taskID,
		"agent_count":      agentCount,
		"completed_agents": completedAgents,
		"total_tokens":     totalTokens,
		"timestamp":        time.Now().UTC().Format(time.RFC3339),
	})
}

func NewCriticReviewedEvent(taskID, critique string) AgentEvent {
	return NewAgentEvent(EventTypeCriticReviewed, map[string]interface{}{
		"task_id":   taskID,
		"critique":  critique,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewToolStartedEvent(taskID, toolName string, arguments map[string]interface{}) AgentEvent {
	return NewAgentEvent(EventTypeToolStarted, map[string]interface{}{
		"task_id":    taskID,
		"tool_name":  toolName,
		"arguments":  arguments,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
	})
}

func NewToolCompletedEvent(taskID, toolName, output string, latencyMs int) AgentEvent {
	return NewAgentEvent(EventTypeToolCompleted, map[string]interface{}{
		"task_id":    taskID,
		"tool_name":  toolName,
		"output":     output,
		"latency_ms": latencyMs,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
	})
}

func NewToolFailedEvent(taskID, toolName, errorMsg string) AgentEvent {
	return NewAgentEvent(EventTypeToolFailed, map[string]interface{}{
		"task_id":    taskID,
		"tool_name":  toolName,
		"error":      errorMsg,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
	})
}

func NewToolUsageSummaryEvent(taskID, agentRole string, callCount, successCount, failureCount, totalLatencyMs int, toolNames []string) AgentEvent {
	return NewAgentEvent("TOOL_USAGE_SUMMARY", map[string]interface{}{
		"task_id":        taskID,
		"agent_role":     agentRole,
		"call_count":     callCount,
		"success_count":  successCount,
		"failure_count":  failureCount,
		"total_latency":  totalLatencyMs,
		"tool_names":     toolNames,
		"timestamp":      time.Now().UTC().Format(time.RFC3339),
	})
}

func NewToolStepStartedEvent(taskID, agentRole string, stepID int, toolName string, arguments map[string]interface{}) AgentEvent {
	return NewAgentEvent(EventTypeToolStepStarted, map[string]interface{}{
		"task_id":    taskID,
		"agent_role": agentRole,
		"step_id":    stepID,
		"tool_name":  toolName,
		"arguments":  arguments,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
	})
}

func NewToolStepCompletedEvent(taskID, agentRole string, stepID int, toolName, output string, latencyMs int64, promptTokens, completionTokens, totalTokens int) AgentEvent {
	return NewAgentEvent(EventTypeToolStepCompleted, map[string]interface{}{
		"task_id":          taskID,
		"agent_role":       agentRole,
		"step_id":          stepID,
		"tool_name":         toolName,
		"output":           output,
		"latency_ms":       latencyMs,
		"prompt_tokens":    promptTokens,
		"completion_tokens": completionTokens,
		"total_tokens":     totalTokens,
		"timestamp":        time.Now().UTC().Format(time.RFC3339),
	})
}

func NewToolStepFailedEvent(taskID, agentRole string, stepID int, toolName, errorMsg string, latencyMs int64) AgentEvent {
	return NewAgentEvent(EventTypeToolStepFailed, map[string]interface{}{
		"task_id":    taskID,
		"agent_role": agentRole,
		"step_id":    stepID,
		"tool_name":  toolName,
		"error":      errorMsg,
		"latency_ms": latencyMs,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
	})
}

func NewReActStepEvent(taskID, nodeID string, iteration int, thought, action, observation string) AgentEvent {
	return NewAgentEvent(EventTypeReActStep, map[string]interface{}{
		"task_id":     taskID,
		"node_id":     nodeID,
		"iteration":   iteration,
		"thought":     thought,
		"action":      action,
		"observation": observation,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
	})
}

func NewReActCompletedEvent(taskID, nodeID string, iterations int, finalAnswer string) AgentEvent {
	return NewAgentEvent(EventTypeReActCompleted, map[string]interface{}{
		"task_id":       taskID,
		"node_id":       nodeID,
		"iterations":    iterations,
		"final_answer":  finalAnswer,
		"timestamp":     time.Now().UTC().Format(time.RFC3339),
	})
}

// DAG Visualization events
func NewDAGNodePendingEvent(taskID, workflowID, nodeID string, layer int, dependencies []string, timestampNs int64) AgentEvent {
	return NewAgentEvent(EventTypeDAGNodePending, map[string]interface{}{
		"task_id":      taskID,
		"workflow_id":  workflowID,
		"node_id":      nodeID,
		"layer":        layer,
		"dependencies": dependencies,
		"status":       "pending",
		"timestamp_ns": timestampNs,
	})
}

func NewDAGNodeRunningEvent(taskID, workflowID, nodeID string, workerID string, timestampNs int64) AgentEvent {
	return NewAgentEvent(EventTypeDAGNodeRunning, map[string]interface{}{
		"task_id":      taskID,
		"workflow_id":  workflowID,
		"node_id":      nodeID,
		"worker_id":    workerID,
		"status":       "running",
		"timestamp_ns": timestampNs,
	})
}

// NewDAGNodeCompletedVisualEvent creates a DAG node completed event for visualization
// This is separate from the original NewDAGNodeCompletedEvent for backward compatibility
func NewDAGNodeCompletedVisualEvent(taskID, workflowID, nodeID string, timestampNs int64) AgentEvent {
	return NewAgentEvent(EventTypeDAGNodeCompleted, map[string]interface{}{
		"task_id":      taskID,
		"workflow_id":  workflowID,
		"node_id":      nodeID,
		"status":       "completed",
		"timestamp_ns": timestampNs,
	})
}

func NewDAGNodeFailedEvent(taskID, workflowID, nodeID, errorMsg string, timestampNs int64) AgentEvent {
	return NewAgentEvent(EventTypeDAGNodeFailed, map[string]interface{}{
		"task_id":      taskID,
		"workflow_id":  workflowID,
		"node_id":      nodeID,
		"status":       "failed",
		"error":        errorMsg,
		"timestamp_ns": timestampNs,
	})
}

// NewDAGNodeSkippedEvent creates a DAG node skipped event (Slice 13)
func NewDAGNodeSkippedEvent(taskID, workflowID, nodeID, reason string, timestampNs int64) AgentEvent {
	return NewAgentEvent(EventTypeDAGNodeSkipped, map[string]interface{}{
		"task_id":      taskID,
		"workflow_id":  workflowID,
		"node_id":      nodeID,
		"status":       "skipped",
		"skipped_reason": reason,
		"timestamp_ns": timestampNs,
	})
}

// NewDAGReplanSummaryEvent creates a DAG replan summary event (Slice 13)
func NewDAGReplanSummaryEvent(taskID, workflowID, failedNodeID string, skippedNodes, affectedNodes []string, remainingCount int) AgentEvent {
	return NewAgentEvent(EventTypeDAGReplanSummary, map[string]interface{}{
		"task_id":             taskID,
		"workflow_id":         workflowID,
		"failed_node_id":      failedNodeID,
		"skipped_nodes":       skippedNodes,
		"affected_nodes":      affectedNodes,
		"remaining_executable": remainingCount,
		"replan_applied":      true,
		"timestamp":           time.Now().UTC().Format(time.RFC3339),
	})
}

// NewDAGConcurrencyLimitAppliedEvent (Slice 14)
func NewDAGConcurrencyLimitAppliedEvent(taskID, workflowID string, maxParallel, active, peak int) AgentEvent {
	return NewAgentEvent(EventTypeDAGConcurrencyLimitApplied, map[string]interface{}{
		"task_id":              taskID,
		"workflow_id":          workflowID,
		"max_parallel_agents":  maxParallel,
		"active_nodes":         active,
		"peak_parallel_nodes":  peak,
		"timestamp":            time.Now().UTC().Format(time.RFC3339),
	})
}

// NewDAGNodeRetryingEvent (Slice 14)
func NewDAGNodeRetryingEvent(taskID, workflowID, nodeID string, attempt int32, errorMsg string) AgentEvent {
	return NewAgentEvent(EventTypeDAGNodeRetrying, map[string]interface{}{
		"task_id":     taskID,
		"workflow_id": workflowID,
		"node_id":     nodeID,
		"attempt":     attempt,
		"error":       errorMsg,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
	})
}

// Swarm Workflow events (Phase 5A Slice 10)

func NewSwarmStartedEvent(taskID, workflowID string, workerCount int) AgentEvent {
	return NewAgentEvent(EventTypeSwarmStarted, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID, "worker_count": workerCount,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewWorkerAssignedEvent(taskID, workflowID, agentID, role, task string) AgentEvent {
	return NewAgentEvent(EventTypeWorkerAssigned, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID, "agent_id": agentID, "role": role, "task": task,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewWorkerStartedEvent(taskID, workflowID, agentID, role string) AgentEvent {
	return NewAgentEvent(EventTypeWorkerStarted, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID, "agent_id": agentID, "role": role,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewWorkerCompletedEvent(taskID, workflowID, agentID, role, result string, tokens, latencyMs int) AgentEvent {
	return NewAgentEvent(EventTypeWorkerCompleted, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID, "agent_id": agentID, "role": role,
		"result_len": len(result), "total_tokens": tokens, "latency_ms": latencyMs,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewWorkerFailedEvent(taskID, workflowID, agentID, role, errorMsg string) AgentEvent {
	return NewAgentEvent(EventTypeWorkerFailed, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID, "agent_id": agentID, "role": role,
		"error": errorMsg, "timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewWorkerTimeoutEvent(taskID, workflowID, agentID, role string) AgentEvent {
	return NewAgentEvent(EventTypeWorkerTimeout, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID, "agent_id": agentID, "role": role,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewSwarmCompletedEvent(taskID, workflowID string, succeeded, failed, timeout, totalTokens int) AgentEvent {
	return NewAgentEvent(EventTypeSwarmCompleted, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID,
		"succeeded": succeeded, "failed": failed, "timeout": timeout, "total_tokens": totalTokens,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewSwarmFailedEvent(taskID, workflowID, errorMsg string) AgentEvent {
	return NewAgentEvent(EventTypeSwarmFailed, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID, "error": errorMsg,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// P2P Communication events (Phase 5B Slice 11)

func NewAgentMessageCreatedEvent(taskID, workflowID, messageID, fromAgent, toAgent, msgType string, round int) AgentEvent {
	return NewAgentEvent(EventTypeAgentMessageCreated, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID, "message_id": messageID,
		"from_agent_id": fromAgent, "to_agent_id": toAgent, "message_type": msgType, "round": round,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewAgentMessageRoutedEvent(taskID, workflowID, messageID, fromAgent, toAgent string, round int) AgentEvent {
	return NewAgentEvent(EventTypeAgentMessageRouted, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID, "message_id": messageID,
		"from_agent_id": fromAgent, "to_agent_id": toAgent, "round": round,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewAgentMessageDeliveredEvent(taskID, workflowID, messageID, fromAgent, toAgent string, round int) AgentEvent {
	return NewAgentEvent(EventTypeAgentMessageDelivered, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID, "message_id": messageID,
		"from_agent_id": fromAgent, "to_agent_id": toAgent, "round": round,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewAgentMessageFailedEvent(taskID, workflowID, messageID, fromAgent, toAgent string, round int) AgentEvent {
	return NewAgentEvent(EventTypeAgentMessageFailed, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID, "message_id": messageID,
		"from_agent_id": fromAgent, "to_agent_id": toAgent, "round": round,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewAgentMessageDroppedEvent(taskID, workflowID, messageID, fromAgent, toAgent string, round int) AgentEvent {
	return NewAgentEvent(EventTypeAgentMessageDropped, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID, "message_id": messageID,
		"from_agent_id": fromAgent, "to_agent_id": toAgent, "round": round,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewP2PRoundStartedEvent(taskID, workflowID string, round, workerCount int) AgentEvent {
	return NewAgentEvent(EventTypeP2PRoundStarted, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID, "round": round, "worker_count": workerCount,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewP2PRoundCompletedEvent(taskID, workflowID string, round, messages int) AgentEvent {
	return NewAgentEvent(EventTypeP2PRoundCompleted, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID, "round": round, "total_messages": messages,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// Workspace events (Phase 5C Slice 12)

func NewWorkspaceItemCreatedEvent(taskID, workflowID, itemID, agentID, role, itemType string, round int) AgentEvent {
	return NewAgentEvent(EventTypeWorkspaceItemCreated, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID, "item_id": itemID,
		"agent_id": agentID, "role": role, "item_type": itemType, "round": round,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewWorkspaceItemAppendedEvent(taskID, workflowID, itemID, agentID string, round int) AgentEvent {
	return NewAgentEvent(EventTypeWorkspaceItemAppended, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID, "item_id": itemID,
		"agent_id": agentID, "round": round,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewWorkspaceItemReadEvent(taskID, workflowID, itemID, agentID, role string, round int) AgentEvent {
	return NewAgentEvent(EventTypeWorkspaceItemRead, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID, "item_id": itemID,
		"agent_id": agentID, "role": role, "round": round,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewWorkspaceItemUsedEvent(taskID, workflowID, itemID, agentID string, round int) AgentEvent {
	return NewAgentEvent(EventTypeWorkspaceItemUsed, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID, "item_id": itemID,
		"agent_id": agentID, "round": round,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewWorkspaceItemFailedEvent(taskID, workflowID, itemID, agentID string, round int) AgentEvent {
	return NewAgentEvent(EventTypeWorkspaceItemFailed, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID, "item_id": itemID,
		"agent_id": agentID, "round": round,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func NewWorkspaceSummaryUpdatedEvent(taskID, workflowID string, totalItems, createdItems, readItems int) AgentEvent {
	return NewAgentEvent(EventTypeWorkspaceSummaryUpdated, map[string]interface{}{
		"task_id": taskID, "workflow_id": workflowID,
		"total_items": totalItems, "created_items": createdItems, "read_items": readItems,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// Agent Metrics Summary event
func NewAgentMetricsSummaryEvent(taskID, workflowID string, totalCallCount, totalTokens int, agentMetrics []map[string]interface{}) AgentEvent {
	payload := map[string]interface{}{
		"task_id":          taskID,
		"workflow_id":      workflowID,
		"total_call_count": totalCallCount,
		"total_tokens":     totalTokens,
		"timestamp":        time.Now().UTC().Format(time.RFC3339),
	}
	if len(agentMetrics) > 0 {
		payload["agent_metrics"] = agentMetrics
	}
	return NewAgentEvent(EventTypeAgentMetricsSummary, payload)
}