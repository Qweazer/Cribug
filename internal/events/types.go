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