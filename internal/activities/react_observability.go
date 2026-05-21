package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"cribug/internal/events"
	"cribug/internal/types"

	"github.com/redis/go-redis/v9"
	"go.temporal.io/sdk/activity"
)

// ReActObservabilityActivities handles agent metrics tracking
type ReActObservabilityActivities struct {
	redisClient *redis.Client
	ttl         int // seconds
}

// NewReActObservabilityActivities creates a new ReActObservabilityActivities
func NewReActObservabilityActivities(redisAddr, redisPass string, redisDB int, ttlSeconds int) *ReActObservabilityActivities {
	client := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: redisPass,
		DB:       redisDB,
	})
	return &ReActObservabilityActivities{
		redisClient: client,
		ttl:         ttlSeconds,
	}
}

// RecordAgentMetricsInput is the input for RecordAgentMetricsActivity
type RecordAgentMetricsInput struct {
	TaskID       string
	WorkflowID   string
	AgentRole    string
	Success      bool
	LatencyMs    int64
	PromptTokens int
	CompletionTokens int
	TotalTokens  int
	ToolCallCount int
}

// RecordAgentMetricsActivity records metrics for a single agent call
func (a *ReActObservabilityActivities) RecordAgentMetrics(ctx context.Context, input RecordAgentMetricsInput) error {
	logger := activity.GetLogger(ctx)
	logger.Info("RecordAgentMetricsActivity started",
		"task_id", input.TaskID,
		"agent_role", input.AgentRole,
		"success", input.Success)

	// Key for agent metrics
	key := fmt.Sprintf("react_metrics:%s:%s", input.TaskID, input.AgentRole)

	// Get current values
	current, err := a.redisClient.HGetAll(ctx, key).Result()
	if err != nil && err != redis.Nil {
		logger.Warn("Failed to get current metrics", "error", err)
	}

	// Calculate new values
	callCount := a.intFromMap(current, "call_count") + 1
	var successCount, failureCount int
	if input.Success {
		successCount = a.intFromMap(current, "success_count") + 1
		failureCount = a.intFromMap(current, "failure_count")
	} else {
		successCount = a.intFromMap(current, "success_count")
		failureCount = a.intFromMap(current, "failure_count") + 1
	}
	totalLatencyMs := a.int64FromMap(current, "total_latency_ms") + input.LatencyMs
	avgLatencyMs := totalLatencyMs / int64(callCount)
	totalTokens := a.intFromMap(current, "total_tokens") + input.TotalTokens
	toolCallCount := a.intFromMap(current, "tool_call_count") + input.ToolCallCount

	// Write updated metrics
	fields := map[string]interface{}{
		"agent_role":       input.AgentRole,
		"call_count":       callCount,
		"success_count":    successCount,
		"failure_count":    failureCount,
		"total_latency_ms": totalLatencyMs,
		"avg_latency_ms":   avgLatencyMs,
		"total_tokens":     totalTokens,
		"tool_call_count":  toolCallCount,
	}

	pipe := a.redisClient.Pipeline()
	pipe.HSet(ctx, key, fields)
	pipe.Expire(ctx, key, time.Duration(a.ttl)*time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		logger.Error("Failed to write metrics to Redis", "error", err)
		return err
	}

	logger.Info("RecordAgentMetricsActivity completed",
		"agent_role", input.AgentRole,
		"call_count", callCount,
		"avg_latency_ms", avgLatencyMs)

	return nil
}

// AggregateAgentMetricsInput is the input for AggregateAgentMetricsActivity
type AggregateAgentMetricsInput struct {
	TaskID     string
	WorkflowID string
	AgentRoles []string
}

// AggregateAgentMetricsActivity aggregates all agent metrics into a summary
func (a *ReActObservabilityActivities) AggregateAgentMetrics(ctx context.Context, input AggregateAgentMetricsInput) (*types.MetricsSummary, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("AggregateAgentMetricsActivity started", "task_id", input.TaskID)

	var agentMetrics []types.AgentMetrics
	var totalCallCount, totalTokens int

	for _, role := range input.AgentRoles {
		key := fmt.Sprintf("react_metrics:%s:%s", input.TaskID, role)
		current, err := a.redisClient.HGetAll(ctx, key).Result()
		if err != nil && err != redis.Nil {
			logger.Warn("Failed to get metrics for role", "role", role, "error", err)
			continue
		}

		if len(current) == 0 {
			continue
		}

		metrics := types.AgentMetrics{
			AgentRole:      role,
			CallCount:      a.intFromMap(current, "call_count"),
			SuccessCount:   a.intFromMap(current, "success_count"),
			FailureCount:   a.intFromMap(current, "failure_count"),
			TotalLatencyMs: a.int64FromMap(current, "total_latency_ms"),
			AvgLatencyMs:   a.int64FromMap(current, "avg_latency_ms"),
			TotalTokens:    a.intFromMap(current, "total_tokens"),
			ToolCallCount:  a.intFromMap(current, "tool_call_count"),
		}
		agentMetrics = append(agentMetrics, metrics)
		totalCallCount += metrics.CallCount
		totalTokens += metrics.TotalTokens
	}

	summary := &types.MetricsSummary{
		TaskID:         input.TaskID,
		WorkflowID:     input.WorkflowID,
		AgentMetrics:   agentMetrics,
		TotalCallCount: totalCallCount,
		TotalTokens:    totalTokens,
	}

	// Write summary to Redis
	summaryKey := fmt.Sprintf("react_metrics:%s:summary", input.TaskID)
	summaryJSON, _ := json.Marshal(summary)
	a.redisClient.Set(ctx, summaryKey, string(summaryJSON), time.Duration(a.ttl)*time.Second)

	logger.Info("AggregateAgentMetricsActivity completed",
		"agent_count", len(agentMetrics),
		"total_call_count", totalCallCount,
		"total_tokens", totalTokens)

	return summary, nil
}

// EmitMetricsSummaryInput is the input for EmitMetricsSummaryActivity
type EmitMetricsSummaryInput struct {
	TaskID     string
	WorkflowID string
	Summary    *types.MetricsSummary
}

// EmitMetricsSummaryActivity emits metrics summary as SSE event
func (a *ReActObservabilityActivities) EmitMetricsSummary(ctx context.Context, input EmitMetricsSummaryInput) error {
	logger := activity.GetLogger(ctx)
	logger.Info("EmitMetricsSummaryActivity started", "task_id", input.TaskID)

	// Build agent metrics payload
	var agentMetricsPayload []map[string]interface{}
	for _, m := range input.Summary.AgentMetrics {
		agentMetricsPayload = append(agentMetricsPayload, map[string]interface{}{
			"agent_role":       m.AgentRole,
			"call_count":       m.CallCount,
			"success_count":    m.SuccessCount,
			"failure_count":    m.FailureCount,
			"total_latency_ms": m.TotalLatencyMs,
			"avg_latency_ms":   m.AvgLatencyMs,
			"total_tokens":     m.TotalTokens,
			"tool_call_count":  m.ToolCallCount,
		})
	}

	// Create event
	event := events.NewAgentMetricsSummaryEvent(
		input.TaskID,
		input.WorkflowID,
		input.Summary.TotalCallCount,
		input.Summary.TotalTokens,
		agentMetricsPayload,
	)

	// Store in Redis stream for SSE
	key := fmt.Sprintf("task:%s:events", input.TaskID)
	eventJSON, _ := json.Marshal(event)
	a.redisClient.RPush(ctx, key, string(eventJSON))

	logger.Info("EmitMetricsSummaryActivity completed",
		"total_call_count", input.Summary.TotalCallCount,
		"total_tokens", input.Summary.TotalTokens)

	return nil
}

// Helper methods
func (a *ReActObservabilityActivities) intFromMap(m map[string]string, key string) int {
	v, ok := m[key]
	if !ok {
		return 0
	}
	i, _ := strconv.Atoi(v)
	return i
}

func (a *ReActObservabilityActivities) int64FromMap(m map[string]string, key string) int64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	i, _ := strconv.ParseInt(v, 10, 64)
	return i
}