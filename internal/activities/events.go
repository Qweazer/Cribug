package activities

import (
	"context"
	"log"

	"cribug/internal/events"
	redisclient "cribug/internal/redis"
)

type EmitEventInput struct {
	TaskID string
	Event  events.AgentEvent
}

type EmitEventActivity struct {
	redis *redisclient.Client
}

func NewEmitEventActivity(redisClient *redisclient.Client) *EmitEventActivity {
	return &EmitEventActivity{redis: redisClient}
}

func (a *EmitEventActivity) Execute(ctx context.Context, input EmitEventInput) error {
	a.redis.AppendTaskEvent(ctx, input.TaskID, input.Event)
	log.Printf("[INFO] EmitEventActivity: task=%s event=%s", input.TaskID, input.Event.EventType)
	return nil
}