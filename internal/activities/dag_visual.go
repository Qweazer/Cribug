package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"cribug/internal/events"
	"cribug/internal/types"

	"github.com/redis/go-redis/v9"
	"go.temporal.io/sdk/activity"
)

// DAGVisualActivities handles DAG visualization state tracking
type DAGVisualActivities struct {
	redisClient *redis.Client
	ttl         int // seconds
}

// NewDAGVisualActivities creates a new DAGVisualActivities
func NewDAGVisualActivities(redisAddr, redisPass string, redisDB int, ttlSeconds int) *DAGVisualActivities {
	client := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: redisPass,
		DB:       redisDB,
	})
	return &DAGVisualActivities{
		redisClient: client,
		ttl:         ttlSeconds,
	}
}

// RecordDAGNodeStatusInput is the input for RecordDAGNodeStatusActivity
type RecordDAGNodeStatusInput struct {
	TaskID         string
	WorkflowID     string
	NodeID         string
	Status         string // pending, running, completed, failed
	Layer          int
	Dependencies   []string
	StartedAtNs    int64
	CompletedAtNs  int64
	Error          string
	WorkerID       string
}

// RecordDAGNodeStatusActivity records a DAG node status to Redis and emits SSE event
func (a *DAGVisualActivities) RecordDAGNodeStatus(ctx context.Context, input RecordDAGNodeStatusInput) error {
	logger := activity.GetLogger(ctx)
	logger.Info("RecordDAGNodeStatusActivity started",
		"task_id", input.TaskID,
		"node_id", input.NodeID,
		"status", input.Status)

	// Build node status JSON
	nodeStatus := types.DAGNodeStatus{
		NodeID:        input.NodeID,
		Status:        input.Status,
		Layer:         input.Layer,
		Dependencies:  input.Dependencies,
		StartedAtNs:   input.StartedAtNs,
		CompletedAtNs: input.CompletedAtNs,
	}
	if input.Error != "" {
		nodeStatus.Error = input.Error
	}

	nodeJSON, err := json.Marshal(nodeStatus)
	if err != nil {
		logger.Error("Failed to marshal node status", "error", err)
		return err
	}

	// Write to Redis: dag:{workflow_id}:nodes
	nodesKey := fmt.Sprintf("dag:%s:nodes", input.WorkflowID)
	pipe := a.redisClient.Pipeline()
	pipe.HSet(ctx, nodesKey, input.NodeID, string(nodeJSON))
	pipe.Expire(ctx, nodesKey, time.Duration(a.ttl)*time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		logger.Error("Failed to write node status to Redis", "error", err)
		return err
	}

	// Update meta: dag:{workflow_id}:meta
	metaKey := fmt.Sprintf("dag:%s:meta", input.WorkflowID)

	// Get current meta to increment counters
	metaMap, err := a.redisClient.HGetAll(ctx, metaKey).Result()
	if err != nil && err != redis.Nil {
		logger.Warn("Failed to get current meta", "error", err)
	}

	// Update meta fields
	metaFields := map[string]interface{}{
		"task_id":      input.TaskID,
		"workflow_id":   input.WorkflowID,
		"updated_at_ns": input.CompletedAtNs,
	}

	// Increment status counters based on current status
	pendingCount := a.intFromMap(metaMap, "pending_nodes")
	runningCount := a.intFromMap(metaMap, "running_nodes")
	completedCount := a.intFromMap(metaMap, "completed_nodes")

	if input.Status == types.NodeStatusPending {
		metaFields["pending_nodes"] = pendingCount + 1
	} else if input.Status == types.NodeStatusRunning {
		metaFields["pending_nodes"] = pendingCount - 1
		metaFields["running_nodes"] = runningCount + 1
	} else if input.Status == types.NodeStatusCompleted {
		metaFields["running_nodes"] = runningCount - 1
		metaFields["completed_nodes"] = completedCount + 1
		// Set total = total completed so far (including this one)
		// At this point, completedCount is the value BEFORE this update
		metaFields["total_nodes"] = completedCount + 1
	} else if input.Status == types.NodeStatusFailed {
		metaFields["running_nodes"] = runningCount - 1
		metaFields["failed_nodes"] = a.intFromMap(metaMap, "failed_nodes") + 1
		// Set total = total completed so far (including this one)
		metaFields["total_nodes"] = completedCount + 1
	}

	pipe = a.redisClient.Pipeline()
	pipe.HSet(ctx, metaKey, metaFields)
	pipe.Expire(ctx, metaKey, time.Duration(a.ttl)*time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		logger.Error("Failed to update meta in Redis", "error", err)
		return err
	}

	logger.Info("RecordDAGNodeStatusActivity completed",
		"node_id", input.NodeID,
		"status", input.Status)

	return nil
}

// BuildDAGVisualSnapshotInput is the input for BuildDAGVisualSnapshotActivity
type BuildDAGVisualSnapshotInput struct {
	WorkflowID string
}

// BuildDAGVisualSnapshotActivity reads DAG visualization state from Redis
func (a *DAGVisualActivities) BuildDAGVisualSnapshot(ctx context.Context, input BuildDAGVisualSnapshotInput) (*types.DAGVisualSnapshot, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("BuildDAGVisualSnapshotActivity started", "workflow_id", input.WorkflowID)

	metaKey := fmt.Sprintf("dag:%s:meta", input.WorkflowID)
	nodesKey := fmt.Sprintf("dag:%s:nodes", input.WorkflowID)

	// Get meta
	metaMap, err := a.redisClient.HGetAll(ctx, metaKey).Result()
	if err != nil {
		logger.Error("Failed to get meta", "error", err)
		return nil, err
	}

	// Get nodes
	nodesMap, err := a.redisClient.HGetAll(ctx, nodesKey).Result()
	if err != nil {
		logger.Error("Failed to get nodes", "error", err)
		return nil, err
	}

	// Build meta struct
	meta := types.DAGVisualMeta{
		WorkflowID: input.WorkflowID,
	}
	if v, ok := metaMap["task_id"]; ok {
		meta.TaskID = v
	}
	if v, ok := metaMap["total_nodes"]; ok {
		fmt.Sscanf(v, "%d", &meta.TotalNodes)
	}
	if v, ok := metaMap["pending_nodes"]; ok {
		fmt.Sscanf(v, "%d", &meta.PendingNodes)
	}
	if v, ok := metaMap["running_nodes"]; ok {
		fmt.Sscanf(v, "%d", &meta.RunningNodes)
	}
	if v, ok := metaMap["completed_nodes"]; ok {
		fmt.Sscanf(v, "%d", &meta.CompletedNodes)
	}
	if v, ok := metaMap["failed_nodes"]; ok {
		fmt.Sscanf(v, "%d", &meta.FailedNodes)
	}
	if v, ok := metaMap["updated_at_ns"]; ok {
		fmt.Sscanf(v, "%d", &meta.UpdatedAtNs)
	}

	// Build nodes map
	nodes := make(map[string]types.DAGNodeStatus)
	for nodeID, nodeJSON := range nodesMap {
		var node types.DAGNodeStatus
		if err := json.Unmarshal([]byte(nodeJSON), &node); err != nil {
			logger.Warn("Failed to unmarshal node", "node_id", nodeID, "error", err)
			continue
		}
		nodes[nodeID] = node
	}

	snapshot := &types.DAGVisualSnapshot{
		TaskID:     meta.TaskID,
		WorkflowID:  input.WorkflowID,
		Meta:        meta,
		Nodes:       nodes,
	}

	logger.Info("BuildDAGVisualSnapshotActivity completed",
		"workflow_id", input.WorkflowID,
		"node_count", len(nodes))

	return snapshot, nil
}

// Helper functions for field manipulation
func incrementField(m map[string]string, field string, delta int) int {
	v, _ := fmt.Sscan(m[field], new(int))
	return v + delta
}

func decrementField(m map[string]string, field string, delta int) int {
	v, _ := fmt.Sscan(m[field], new(int))
	return v - delta
}

func (a *DAGVisualActivities) intFromMap(m map[string]string, field string) int {
	v, ok := m[field]
	if !ok {
		return 0
	}
	i, _ := fmt.Sscan(v, new(int))
	return i
}

// EmitDAGNodeEvent emits a DAG node event for SSE
func EmitDAGNodeEvent(redisClient *redis.Client, ctx context.Context, taskID, workflowID, nodeID string, eventType string, timestampNs int64, metadata map[string]interface{}) error {
	var event events.AgentEvent
	switch eventType {
	case events.EventTypeDAGNodePending:
		event = events.NewDAGNodePendingEvent(taskID, workflowID, nodeID, 0, nil, timestampNs)
	case events.EventTypeDAGNodeRunning:
		workerID, _ := metadata["worker_id"].(string)
		event = events.NewDAGNodeRunningEvent(taskID, workflowID, nodeID, workerID, timestampNs)
	case events.EventTypeDAGNodeCompleted:
		// Use the original DAG_NODE_COMPLETED event for backward compatibility
		nodeType, _ := metadata["node_type"].(string)
		status, _ := metadata["status"].(string)
		output, _ := metadata["output"].(string)
		event = events.NewDAGNodeCompletedEvent(taskID, nodeID, nodeType, status, output)
	case events.EventTypeDAGNodeFailed:
		errMsg, _ := metadata["error"].(string)
		event = events.NewDAGNodeFailedEvent(taskID, workflowID, nodeID, errMsg, timestampNs)
	default:
		return fmt.Errorf("unknown DAG node event type: %s", eventType)
	}

	// Store in Redis stream for SSE
	key := fmt.Sprintf("task:%s:events", taskID)
	eventJSON, _ := json.Marshal(event)
	redisClient.RPush(ctx, key, string(eventJSON))

	return nil
}