package activities

import (
	"context"
	"log"

	redisclient "cribug/internal/redis"
	"cribug/internal/types"

	"go.temporal.io/sdk/activity"
)

type SessionActivities struct {
	redis *redisclient.Client
}

func NewSessionActivities(redisClient *redisclient.Client) *SessionActivities {
	return &SessionActivities{redis: redisClient}
}

type LoadSessionInput struct {
	TaskID     string
	SessionID  string
}

type LoadSessionOutput struct {
	Messages     []types.LLMMessage
	MessageCount int
}

func (a *SessionActivities) LoadSession(ctx context.Context, input LoadSessionInput) (*LoadSessionOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("LoadSessionActivity started", "task_id", input.TaskID, "session_id", input.SessionID)

	if input.SessionID == "" {
		logger.Info("LoadSessionActivity: empty session_id, returning empty", "task_id", input.TaskID)
		return &LoadSessionOutput{Messages: []types.LLMMessage{}, MessageCount: 0}, nil
	}

	messages, err := a.redis.LoadSessionMessages(ctx, input.SessionID)
	if err != nil {
		logger.Error("LoadSessionActivity failed", "error", err)
		return nil, err
	}

	count := len(messages)
	logger.Info("LoadSessionActivity completed", "task_id", input.TaskID, "session_id", input.SessionID, "message_count", count)
	log.Printf("[INFO] LoadSessionActivity: task=%s session=%s messages=%d", input.TaskID, input.SessionID, count)

	return &LoadSessionOutput{
		Messages:     messages,
		MessageCount: count,
	}, nil
}

type SaveSessionInput struct {
	TaskID           string
	SessionID        string
	UserMessage      string
	AssistantMessage string
}

func (a *SessionActivities) SaveSession(ctx context.Context, input SaveSessionInput) error {
	logger := activity.GetLogger(ctx)
	logger.Info("SaveSessionActivity started", "task_id", input.TaskID, "session_id", input.SessionID)

	if input.SessionID == "" {
		logger.Info("SaveSessionActivity: empty session_id, skipping", "task_id", input.TaskID)
		return nil
	}

	if err := a.redis.SaveSessionMessages(ctx, input.SessionID, input.UserMessage, input.AssistantMessage); err != nil {
		logger.Error("SaveSessionActivity failed", "error", err)
		return err
	}

	logger.Info("SaveSessionActivity completed", "task_id", input.TaskID, "session_id", input.SessionID)
	log.Printf("[INFO] SaveSessionActivity: task=%s session=%s user_len=%d assistant_len=%d",
		input.TaskID, input.SessionID, len(input.UserMessage), len(input.AssistantMessage))

	return nil
}