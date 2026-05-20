package activities

import (
    "context"
    "encoding/json"
    "fmt"
    "time"

    "github.com/redis/go-redis/v9"
)

// DAGRedisClient handles Redis operations for DAG state tracking
type DAGRedisClient struct {
    client *redis.Client
    ttl    time.Duration
}

// NewDAGRedisClient creates a new DAGRedisClient
func NewDAGRedisClient(redisAddr, redisPass string, redisDB int, ttlSeconds int) *DAGRedisClient {
    client := redis.NewClient(&redis.Options{
        Addr:     redisAddr,
        Password: redisPass,
        DB:       redisDB,
    })
    return &DAGRedisClient{
        client: client,
        ttl:    time.Duration(ttlSeconds) * time.Second,
    }
}

// SetNodeStatus sets the status of a DAG node
func (c *DAGRedisClient) SetNodeStatus(ctx context.Context, taskID, nodeID, status string) error {
    key := fmt.Sprintf("dag:%s:nodes:status", taskID)
    pipe := c.client.Pipeline()
    pipe.HSet(ctx, key, nodeID, status)
    pipe.Expire(ctx, key, c.ttl)
    _, err := pipe.Exec(ctx)
    return err
}

// GetNodeStatus gets the status of a DAG node
func (c *DAGRedisClient) GetNodeStatus(ctx context.Context, taskID, nodeID string) (string, error) {
    key := fmt.Sprintf("dag:%s:nodes:status", taskID)
    return c.client.HGet(ctx, key, nodeID).Result()
}

// SetNodeResult sets the result of a DAG node
func (c *DAGRedisClient) SetNodeResult(ctx context.Context, taskID, nodeID string, result interface{}) error {
    key := fmt.Sprintf("dag:%s:nodes:results", taskID)
    data, err := json.Marshal(result)
    if err != nil {
        return err
    }
    pipe := c.client.Pipeline()
    pipe.HSet(ctx, key, nodeID, string(data))
    pipe.Expire(ctx, key, c.ttl)
    _, err = pipe.Exec(ctx)
    return err
}

// PushReactStep pushes a ReAct step to Redis List (async, non-blocking)
func (c *DAGRedisClient) PushReactStep(ctx context.Context, taskID, nodeID string, step interface{}) error {
    key := fmt.Sprintf("react:%s:%s:steps", taskID, nodeID)
    data, err := json.Marshal(step)
    if err != nil {
        return err
    }
    pipe := c.client.Pipeline()
    pipe.RPush(ctx, key, string(data))
    pipe.Expire(ctx, key, c.ttl)
    _, err = pipe.Exec(ctx)
    return err
}

// GetAllNodeStatuses gets all node statuses for a task
func (c *DAGRedisClient) GetAllNodeStatuses(ctx context.Context, taskID string) (map[string]string, error) {
    key := fmt.Sprintf("dag:%s:nodes:status", taskID)
    return c.client.HGetAll(ctx, key).Result()
}

// GetAllNodeResults gets all node results for a task
func (c *DAGRedisClient) GetAllNodeResults(ctx context.Context, taskID string) (map[string]string, error) {
    key := fmt.Sprintf("dag:%s:nodes:results", taskID)
    return c.client.HGetAll(ctx, key).Result()
}

// Close closes the Redis client
func (c *DAGRedisClient) Close() error {
    return c.client.Close()
}