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

// DAGFallbackActivities handles DAG node failure and dynamic replanning (Slice 13)
type DAGFallbackActivities struct {
	redisClient *redis.Client
	ttl         int
}

func NewDAGFallbackActivities(redisAddr, redisPass string, redisDB, ttlSeconds int) *DAGFallbackActivities {
	return &DAGFallbackActivities{
		redisClient: redis.NewClient(&redis.Options{
			Addr:     redisAddr,
			Password: redisPass,
			DB:       redisDB,
		}),
		ttl: ttlSeconds,
	}
}

// HandleDAGNodeFailureInput is in types package — we reuse types.HandleDAGNodeFailureInput

// HandleDAGNodeFailureActivity handles a failed DAG node:
// 1. Mark failed node in Redis with error
// 2. Find all direct+transitive dependents and mark them skipped
// 3. Update Redis meta counters
// 4. Emit DAG_NODE_FAILED / DAG_NODE_SKIPPED / DAG_REPLAN_SUMMARY events
func (a *DAGFallbackActivities) HandleDAGNodeFailure(ctx context.Context, input types.HandleDAGNodeFailureInput) (*types.HandleDAGNodeFailureOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("HandleDAGNodeFailureActivity started",
		"workflow_id", input.WorkflowID,
		"failed_node_id", input.FailedNodeID)

	nodesKey := fmt.Sprintf("dag:%s:nodes", input.WorkflowID)
	metaKey := fmt.Sprintf("dag:%s:meta", input.WorkflowID)

	// 1. Read all existing node statuses
	existingNodes, err := a.redisClient.HGetAll(ctx, nodesKey).Result()
	if err != nil && err != redis.Nil {
		logger.Warn("Failed to read existing nodes, creating fresh", "error", err)
	}
	if existingNodes == nil {
		existingNodes = make(map[string]string)
	}

	// Parse existing nodes into DAGNodeStatus structs
	nodeStatuses := make(map[string]types.DAGNodeStatus)
	var allNodeIDs []string
	for nodeID, raw := range existingNodes {
		var ns types.DAGNodeStatus
		if err := json.Unmarshal([]byte(raw), &ns); err == nil {
			nodeStatuses[nodeID] = ns
			allNodeIDs = append(allNodeIDs, nodeID)
		}
	}

	// Build dependency map from the full DAG plan (not Redis, which may not have all nodes yet)
	dependents := make(map[string][]string)
	for _, node := range input.AllNodes {
		for _, dep := range node.DependsOn {
			dependents[dep] = append(dependents[dep], node.ID)
		}
	}
	// Ensure all nodes are in nodeStatuses (including ones not yet written to Redis)
	for _, node := range input.AllNodes {
		if _, ok := nodeStatuses[node.ID]; !ok {
			nodeStatuses[node.ID] = types.DAGNodeStatus{
				NodeID:       node.ID,
				Dependencies: node.DependsOn,
			}
		} else {
			// Merge dependencies from plan into existing status
			ns := nodeStatuses[node.ID]
			if len(ns.Dependencies) == 0 {
				ns.Dependencies = node.DependsOn
				nodeStatuses[node.ID] = ns
			}
		}
	}

	// 2. Mark failed node
	failedAt := input.FailedAtNs
	if failedAt == 0 {
		failedAt = time.Now().UnixNano()
	}
	if ns, ok := nodeStatuses[input.FailedNodeID]; ok {
		ns.Status = types.NodeStatusFailed
		ns.Error = input.Error
		ns.CompletedAtNs = failedAt
		nodeStatuses[input.FailedNodeID] = ns
	} else {
		nodeStatuses[input.FailedNodeID] = types.DAGNodeStatus{
			NodeID:        input.FailedNodeID,
			Status:        types.NodeStatusFailed,
			Error:         input.Error,
			CompletedAtNs: failedAt,
		}
	}

	// 3. Propagate skip: BFS from failed node through dependents
	skippedSet := make(map[string]bool)
	affectedSet := make(map[string]bool)
	queue := []string{input.FailedNodeID}
	affectedSet[input.FailedNodeID] = true

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		for _, dep := range dependents[current] {
			if skippedSet[dep] || dep == input.FailedNodeID {
				continue
			}
			skippedSet[dep] = true
			affectedSet[dep] = true
			queue = append(queue, dep)

			if ns, ok := nodeStatuses[dep]; ok {
				ns.Status = types.NodeStatusSkipped
				ns.Error = fmt.Sprintf("skipped: upstream node '%s' failed — %s", current, input.Error)
				ns.CompletedAtNs = failedAt
				nodeStatuses[dep] = ns
			} else {
				nodeStatuses[dep] = types.DAGNodeStatus{
					NodeID:        dep,
					Status:        types.NodeStatusSkipped,
					Error:         fmt.Sprintf("skipped: upstream node '%s' failed — %s", current, input.Error),
					CompletedAtNs: failedAt,
				}
			}
		}
	}

	var skippedNodes, affectedNodes []string
	for n := range skippedSet {
		skippedNodes = append(skippedNodes, n)
	}
	for n := range affectedSet {
		affectedNodes = append(affectedNodes, n)
	}
	logger.Info("Skip propagation complete",
		"failed_node", input.FailedNodeID,
		"skipped", len(skippedNodes),
		"affected", len(affectedNodes))

	// 4. Write all updated node statuses back to Redis
	pipe := a.redisClient.Pipeline()
	for nodeID, ns := range nodeStatuses {
		raw, _ := json.Marshal(ns)
		pipe.HSet(ctx, nodesKey, nodeID, string(raw))
	}
	pipe.Expire(ctx, nodesKey, time.Duration(a.ttl)*time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		logger.Error("Failed to write node statuses to Redis", "error", err)
		return nil, fmt.Errorf("redis write: %w", err)
	}

	// 5. Update meta: count completed/failed/skipped
	var completed, failed, skipped, total int
	for _, ns := range nodeStatuses {
		total++
		switch ns.Status {
		case types.NodeStatusCompleted:
			completed++
		case types.NodeStatusFailed:
			failed++
		case types.NodeStatusSkipped:
			skipped++
		}
	}

	metaFields := map[string]interface{}{
		"task_id":         input.TaskID,
		"workflow_id":     input.WorkflowID,
		"total_nodes":     total,
		"completed_nodes": completed,
		"failed_nodes":    failed,
		"skipped_nodes":   skipped,
		"updated_at_ns":   failedAt,
	}
	if err := a.redisClient.HSet(ctx, metaKey, metaFields).Err(); err != nil {
		logger.Warn("Failed to write meta to Redis", "error", err)
	}
	a.redisClient.Expire(ctx, metaKey, time.Duration(a.ttl)*time.Second)

	// 6. Emit events via Redis stream
	eventsKey := fmt.Sprintf("task:%s:events", input.TaskID)

	// DAG_NODE_FAILED
	failedEvent := events.NewDAGNodeFailedEvent(input.TaskID, input.WorkflowID, input.FailedNodeID, input.Error, failedAt)
	emitToStream(ctx, a.redisClient, eventsKey, failedEvent)

	// DAG_NODE_SKIPPED for each skipped node
	for _, n := range skippedNodes {
		skipReason := fmt.Sprintf("upstream node '%s' failed", input.FailedNodeID)
		skipEvent := events.NewDAGNodeSkippedEvent(input.TaskID, input.WorkflowID, n, skipReason, failedAt)
		emitToStream(ctx, a.redisClient, eventsKey, skipEvent)
	}

	// DAG_REPLAN_SUMMARY
	replanEvent := events.NewDAGReplanSummaryEvent(input.TaskID, input.WorkflowID,
		input.FailedNodeID, skippedNodes, affectedNodes, total-completed-failed-skipped)
	emitToStream(ctx, a.redisClient, eventsKey, replanEvent)

	logger.Info("HandleDAGNodeFailureActivity completed",
		"skipped", len(skippedNodes),
		"affected", len(affectedNodes))

	return &types.HandleDAGNodeFailureOutput{
		FailedNodeID:  input.FailedNodeID,
		SkippedNodes:  skippedNodes,
		AffectedNodes: affectedNodes,
		ReplanApplied: true,
	}, nil
}

func emitToStream(ctx context.Context, client *redis.Client, key string, event events.AgentEvent) {
	payload, _ := json.Marshal(event)
	client.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		MaxLen: 200,
		Approx: true,
		Values: map[string]interface{}{
			"event_type": event.EventType,
			"payload":    string(payload),
			"created_at": event.CreatedAt,
		},
	})
}
