package events

import "time"

const (
	EventTypeTaskCreated      = "TASK_CREATED"
	EventTypeWorkflowStarted  = "WORKFLOW_STARTED"
	EventTypeTaskCompleted    = "TASK_COMPLETED"
	EventTypeTaskFailed       = "TASK_FAILED"
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