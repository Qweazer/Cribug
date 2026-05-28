package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	hookspkg "cribug/internal/hooks"
	"cribug/internal/db"
	"cribug/internal/events"
	redisclient "cribug/internal/redis"
	"cribug/internal/skillclient"

	"github.com/google/uuid"
)

// SkillActivities handles skill-related Temporal activities
type SkillActivities struct {
	client      *skillclient.Client
	db          *db.Postgres
	redis       *redisclient.Client
	hookRuntime *hookspkg.HookRuntime
}

// SetHookRuntime injects the hook runtime for inline hooks.
func (a *SkillActivities) SetHookRuntime(runtime *hookspkg.HookRuntime) {
	a.hookRuntime = runtime
}

func NewSkillActivities(client *skillclient.Client, database *db.Postgres, redisClient *redisclient.Client) *SkillActivities {
	return &SkillActivities{client: client, db: database, redis: redisClient}
}

// --- Input/Output Types ---

type ListSkillsInput struct{}
type ListSkillsOutput struct{ Skills []string }

type GetSkillInput struct{ SkillName string }
type GetSkillOutput struct{ Metadata *skillclient.SkillMetadata }

type ExecuteSkillInput struct {
	SkillName  string
	Parameters map[string]interface{}
	AgentID    string
	WorkflowID string
	RequestID  string
}

type ExecuteSkillOutput struct {
	RequestID       string
	Success         bool
	Output          interface{}
	Error           string
	ErrorType       string
	ExecutionTimeMs int
	Overflow        bool
	Provider        string
	Model           string
	TokenUsage      map[string]int
}

type AuditSkillInput struct {
	SkillName  string
	AgentID    string
	WorkflowID string
	RequestID  string
	Success    bool
	DurationMs int
	Error      string
	ErrorType  string
	Overflow   bool
	Provider   string
	Model      string
	TokenUsage map[string]int
}

type AuditSkillOutput struct{ AuditID string }

type WorkspaceAppendInput struct {
	TaskID     string
	WorkflowID string
	AgentID    string
	SkillName  string
	RequestID  string
	Result     interface{}
	Success    bool
}

type WorkspaceAppendOutput struct{ ItemID string }

// --- Activities ---

func (a *SkillActivities) ListSkillsActivity(ctx context.Context, input ListSkillsInput) (ListSkillsOutput, error) {
	skills, err := a.client.ListSkills(ctx)
	if err != nil {
		return ListSkillsOutput{}, fmt.Errorf("list skills: %w", err)
	}
	return ListSkillsOutput{Skills: skills}, nil
}

func (a *SkillActivities) GetSkillActivity(ctx context.Context, input GetSkillInput) (GetSkillOutput, error) {
	metadata, err := a.client.GetSkillMetadata(ctx, input.SkillName)
	if err != nil {
		return GetSkillOutput{}, fmt.Errorf("get skill metadata: %w", err)
	}
	return GetSkillOutput{Metadata: metadata}, nil
}

func (a *SkillActivities) ExecuteSkillActivity(ctx context.Context, input ExecuteSkillInput) (ExecuteSkillOutput, error) {
	result, err := a.client.ExecuteSkill(ctx, input.SkillName, input.Parameters)
	if err != nil {
		return ExecuteSkillOutput{
			RequestID: input.RequestID,
			Success:   false,
			Error:     err.Error(),
			ErrorType: "http_error",
		}, nil
	}

	provider := ""
	model := ""
	tokenUsage := map[string]int{}

	if result.Metadata != nil {
		if p, ok := result.Metadata["provider"].(string); ok {
			provider = p
		}
		if m, ok := result.Metadata["model"].(string); ok {
			model = m
		}
		if tu, ok := result.Metadata["token_usage"].(map[string]interface{}); ok {
			for k, v := range tu {
				if vi, ok := v.(float64); ok {
					tokenUsage[k] = int(vi)
				}
			}
		}
	}

	return ExecuteSkillOutput{
		RequestID:       result.RequestID,
		Success:         result.Success,
		Output:          result.Output,
		Error:           result.Error,
		ErrorType:       result.ErrorType,
		ExecutionTimeMs: result.ExecutionTimeMs,
		Overflow:        result.Overflow,
		Provider:        provider,
		Model:           model,
		TokenUsage:      tokenUsage,
	}, nil
}

func (a *SkillActivities) AuditSkillExecutionActivity(ctx context.Context, input AuditSkillInput) (AuditSkillOutput, error) {
	if a.db == nil {
		return AuditSkillOutput{AuditID: fmt.Sprintf("no-db-%s", input.RequestID)}, nil
	}

	auditID, err := a.db.CreateSkillAuditLog(ctx, db.SkillAuditLog{
		SkillName:  input.SkillName,
		AgentID:    input.AgentID,
		WorkflowID: input.WorkflowID,
		RequestID:  input.RequestID,
		Success:    input.Success,
		DurationMs: input.DurationMs,
		Error:      input.Error,
		ErrorType:  input.ErrorType,
		Overflow:   input.Overflow,
		Provider:   input.Provider,
		Model:      input.Model,
		TokenUsage: input.TokenUsage,
	})
	if err != nil {
		return AuditSkillOutput{}, fmt.Errorf("create audit log: %w", err)
	}

	return AuditSkillOutput{AuditID: auditID}, nil
}

func (a *SkillActivities) WorkspaceAppendSkillResultActivity(ctx context.Context, input WorkspaceAppendInput) (WorkspaceAppendOutput, error) {
	itemID := uuid.New().String()

	// Serialize result, truncate oversized payloads
	content, _ := json.Marshal(input.Result)
	const maxContentBytes = 65536
	if len(content) > maxContentBytes {
		content = content[:maxContentBytes]
	}

	// Build workspace event and append to Redis stream
	evt := events.NewAgentEvent("WORKSPACE_SKILL_RESULT", map[string]interface{}{
		"task_id":     input.TaskID,
		"workflow_id": input.WorkflowID,
		"item_id":     itemID,
		"agent_id":    input.AgentID,
		"skill_name":  input.SkillName,
		"request_id":  input.RequestID,
		"success":     input.Success,
		"content":     string(content),
		"timestamp":   "now",
	})

	if a.redis != nil {
		a.redis.AppendTaskEvent(ctx, input.TaskID, evt)
	}

	// Emit on_workspace_append hook (non-blocking, fire-and-forget)
	if a.hookRuntime != nil {
		event := hookspkg.HookEvent{
			EventID:         uuid.New().String(),
			HookPoint:       hookspkg.HookPointOnWorkspaceAppend,
			AgentID:         input.AgentID,
			WorkflowID:      input.WorkflowID,
			TenantID:        "00000000-0000-0000-0000-000000000000",
			Timestamp:       time.Now().UTC(),
			SourceComponent: "workspace",
			HookOrigin:      false,
			RecursionDepth:  0,
			Payload: map[string]interface{}{
				"agent_id":       input.AgentID,
				"task_id":        input.TaskID,
				"item_type":      "skill_result",
				"content_length": len(content),
			},
		}
		a.hookRuntime.EmitAndExecute(ctx, event)
		// on_workspace_append is always non-blocking; ignore decision
	}

	return WorkspaceAppendOutput{ItemID: itemID}, nil
}
