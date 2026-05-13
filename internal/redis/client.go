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

	return &types.TaskDetailResponse{
		TaskID:             result["task_id"],
		WorkflowID:         result["workflow_id"],
		RunID:              runID,
		SessionID:          sessionID,
		Status:             result["status"],
		Model:              result["model"],
		MaxTotalTokens:      parseInt(result["max_total_tokens"]),
		MaxCompletionTokens: parseInt(result["max_completion_tokens"]),
		CreatedAt:          createdAt,
		UpdatedAt:          updatedAt,
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