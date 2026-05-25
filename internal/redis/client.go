package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"cribug/internal/events"
	"cribug/internal/types"

	"github.com/redis/go-redis/v9"
)

type Client struct {
	client *redis.Client
}

func New(addr, password string, db int) (*Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	return &Client{client: client}, nil
}

func (c *Client) Close() error {
	return c.client.Close()
}

func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.client.Ping(ctx).Err()
}

func (c *Client) SetTaskStatus(ctx context.Context, task *types.Task) {
	key := fmt.Sprintf("task:%s:status", task.ID)
	fields := map[string]interface{}{
		"task_id":               task.ID,
		"workflow_id":           task.WorkflowID,
		"run_id":                task.RunID.String,
		"session_id":            task.SessionID.String,
		"status":                task.Status,
		"query":                 task.Query,
		"model":                 task.Model,
		"max_total_tokens":      task.MaxTotalTokens,
		"max_completion_tokens": task.MaxCompletionTokens,
		"created_at":            task.CreatedAt.Format(time.RFC3339),
		"updated_at":            task.UpdatedAt.Format(time.RFC3339),
	}

	if _, err := c.client.HSet(ctx, key, fields).Result(); err != nil {
		log.Printf("[WARN] redis: hset task status: %v", err)
		return
	}

	if err := c.client.Expire(ctx, key, 24*time.Hour).Err(); err != nil {
		log.Printf("[WARN] redis: expire task status: %v", err)
	}
}

func (c *Client) GetTaskStatus(ctx context.Context, taskID string) (*types.TaskDetailResponse, error) {
	key := fmt.Sprintf("task:%s:status", taskID)
	result, err := c.client.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("hgetall: %w", err)
	}
	if len(result) == 0 {
		return nil, nil
	}

	createdAt, _ := time.Parse(time.RFC3339, result["created_at"])
	updatedAt, _ := time.Parse(time.RFC3339, result["updated_at"])

	var runID *string
	runIDVal := result["run_id"]
	if runIDVal != "" {
		runID = &runIDVal
	}

	var sessionID *string
	sessionIDVal := result["session_id"]
	if sessionIDVal != "" {
		sessionID = &sessionIDVal
	}

	var taskResult *string
	if resultVal := result["result"]; resultVal != "" {
		taskResult = &resultVal
	}

	return &types.TaskDetailResponse{
		TaskID:              result["task_id"],
		WorkflowID:          result["workflow_id"],
		RunID:               runID,
		SessionID:           sessionID,
		Status:              result["status"],
		Result:              taskResult,
		Model:               result["model"],
		MaxTotalTokens:      parseInt(result["max_total_tokens"]),
		MaxCompletionTokens: parseInt(result["max_completion_tokens"]),
		CreatedAt:           createdAt,
		UpdatedAt:           updatedAt,
	}, nil
}

func (c *Client) AppendTaskEvent(ctx context.Context, taskID string, event events.AgentEvent) {
	key := fmt.Sprintf("task:%s:events", taskID)

	payload, err := json.Marshal(event)
	if err != nil {
		log.Printf("[WARN] redis: marshal event: %v", err)
		return
	}

	fields := map[string]interface{}{
		"event_type": event.EventType,
		"payload":    string(payload),
		"created_at": event.CreatedAt,
	}

	if err := c.client.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		MaxLen: 100,
		Approx: true,
		Values: fields,
	}).Err(); err != nil {
		log.Printf("[WARN] redis: xadd event: %v", err)
		return
	}

	c.client.Expire(ctx, key, 24*time.Hour)
}

func (c *Client) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return c.client.HGetAll(ctx, key).Result()
}

func (c *Client) Keys(ctx context.Context, pattern string) ([]string, error) {
	return c.client.Keys(ctx, pattern).Result()
}

func parseInt(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}

func (c *Client) UpdateTaskRunning(ctx context.Context, taskID, runID string) {
	key := fmt.Sprintf("task:%s:status", taskID)
	fields := map[string]interface{}{
		"run_id":     runID,
		"status":     "running",
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}

	if _, err := c.client.HSet(ctx, key, fields).Result(); err != nil {
		log.Printf("[WARN] redis: update task running: %v", err)
	}
}

func (c *Client) UpdateTaskError(ctx context.Context, taskID, errorType, errorMsg string) {
	key := fmt.Sprintf("task:%s:status", taskID)
	fields := map[string]interface{}{
		"status":     "failed",
		"error_type": errorType,
		"error":      errorMsg,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}

	if _, err := c.client.HSet(ctx, key, fields).Result(); err != nil {
		log.Printf("[WARN] redis: update task error: %v", err)
	}
}

func (c *Client) UpdateTaskCompleted(ctx context.Context, taskID, result string) {
	key := fmt.Sprintf("task:%s:status", taskID)
	fields := map[string]interface{}{
		"status":          "completed",
		"result":          result,
		"result_length":   len(result),
		"updated_at":      time.Now().UTC().Format(time.RFC3339),
	}

	if _, err := c.client.HSet(ctx, key, fields).Result(); err != nil {
		log.Printf("[WARN] redis: update task completed: %v", err)
	}
}

func (c *Client) UpdateTaskBudgetExceeded(ctx context.Context, taskID, reason string) {
	key := fmt.Sprintf("task:%s:status", taskID)
	fields := map[string]interface{}{
		"status":      "budget_exceeded",
		"error_type":  "budget_exceeded",
		"error":       reason,
		"updated_at":  time.Now().UTC().Format(time.RFC3339),
	}

	if _, err := c.client.HSet(ctx, key, fields).Result(); err != nil {
		log.Printf("[WARN] redis: update task budget exceeded: %v", err)
	}
}

type StreamEvent struct {
	ID        string
	EventType string
	Payload   string
	CreatedAt string
}

func (c *Client) ReadTaskEvents(ctx context.Context, taskID string) ([]StreamEvent, error) {
	key := fmt.Sprintf("task:%s:events", taskID)
	results, err := c.client.XRange(ctx, key, "0", "+").Result()
	if err != nil {
		return nil, fmt.Errorf("xrange: %w", err)
	}

	events := make([]StreamEvent, 0, len(results))
	for _, r := range results {
		events = append(events, streamEventFromXRANGE(r))
	}
	return events, nil
}

func (c *Client) ReadTaskEventsBlocking(ctx context.Context, taskID string, lastID string, block time.Duration) ([]StreamEvent, error) {
	key := fmt.Sprintf("task:%s:events", taskID)

	if lastID == "" {
		lastID = "0"
	}

	streams, err := c.client.XRead(ctx, &redis.XReadArgs{
		Streams: []string{key, lastID},
		Block:   block,
	}).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("xread: %w", err)
	}

	events := make([]StreamEvent, 0)
	for _, stream := range streams {
		for _, r := range stream.Messages {
			events = append(events, streamEventFromXMessage(r))
		}
	}
	return events, nil
}

func streamEventFromXRANGE(r redis.XMessage) StreamEvent {
	return StreamEvent{
		ID:        r.ID,
		EventType: getStreamField(r.Values, "event_type"),
		Payload:   getStreamField(r.Values, "payload"),
		CreatedAt: getStreamField(r.Values, "created_at"),
	}
}

func streamEventFromXMessage(r redis.XMessage) StreamEvent {
	return StreamEvent{
		ID:        r.ID,
		EventType: getStreamField(r.Values, "event_type"),
		Payload:   getStreamField(r.Values, "payload"),
		CreatedAt: getStreamField(r.Values, "created_at"),
	}
}

func getStreamField(values map[string]interface{}, field string) string {
	if v, ok := values[field]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

const (
	SessionMaxMessages = 50
	SessionTTL         = 7 * 24 * time.Hour // 7 days
)

func (c *Client) LoadSessionMessages(ctx context.Context, sessionID string) ([]types.LLMMessage, error) {
	if sessionID == "" {
		return nil, nil
	}

	key := fmt.Sprintf("session:%s:messages", sessionID)
	results, err := c.client.LRange(ctx, key, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("lrange session messages: %w", err)
	}

	messages := make([]types.LLMMessage, 0, len(results))
	for _, raw := range results {
		var msg types.LLMMessage
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			log.Printf("[WARN] redis: unmarshal session message: %v", err)
			continue
		}
		messages = append(messages, msg)
	}

	return messages, nil
}

func (c *Client) SaveSessionMessages(ctx context.Context, sessionID string, userMsg, assistantMsg string) error {
	if sessionID == "" {
		return nil
	}

	key := fmt.Sprintf("session:%s:messages", sessionID)

	if userMsg != "" {
		userJSON, err := json.Marshal(types.LLMMessage{Role: "user", Content: userMsg})
		if err != nil {
			return fmt.Errorf("marshal user message: %w", err)
		}
		if err := c.client.RPush(ctx, key, string(userJSON)).Err(); err != nil {
			return fmt.Errorf("rpush user message: %w", err)
		}
	}

	if assistantMsg != "" {
		assistantJSON, err := json.Marshal(types.LLMMessage{Role: "assistant", Content: assistantMsg})
		if err != nil {
			return fmt.Errorf("marshal assistant message: %w", err)
		}
		if err := c.client.RPush(ctx, key, string(assistantJSON)).Err(); err != nil {
			return fmt.Errorf("rpush assistant message: %w", err)
		}
	}

	// Trim to last 50 messages
	if err := c.client.LTrim(ctx, key, -SessionMaxMessages, -1).Err(); err != nil {
		return fmt.Errorf("ltrim session messages: %w", err)
	}

	// Set TTL
	if err := c.client.Expire(ctx, key, SessionTTL).Err(); err != nil {
		return fmt.Errorf("expire session messages: %w", err)
	}

	return nil
}