package activities

import (
	"context"
	"fmt"
	"strings"

	cribugllm "cribug/internal/llm"
)

// ReflectionActivities holds dependencies for Reflection Activities.
type ReflectionActivities struct {
	agentActs *AgentActivities // reuse existing LLM call
	llmURL    string
}

// NewReflectionActivities creates a new ReflectionActivities instance.
func NewReflectionActivities(llmServiceURL string) *ReflectionActivities {
	return &ReflectionActivities{
		agentActs: NewAgentActivities(llmServiceURL),
		llmURL:    llmServiceURL,
	}
}

// ─── GenerateInitialDraft ─────────────────────────────────────────────

type GenerateInitialDraftInput struct {
	TaskID              string                 `json:"task_id"`
	WorkflowID          string                 `json:"workflow_id"`
	RunID               string                 `json:"run_id"`
	Query               string                 `json:"query"`
	Context             map[string]interface{} `json:"context,omitempty"`
	MockLLM             bool                   `json:"mock_llm"`
	Model               string                 `json:"model"`
	Temperature         float64                `json:"temperature"`
	MaxCompletionTokens int                    `json:"max_completion_tokens"`
}

type GenerateInitialDraftResult struct {
	DraftRef     string `json:"draft_ref,omitempty"`
	DraftSummary string `json:"draft_summary"`
	DraftText    string `json:"draft_text"`
	TokensUsed   int    `json:"tokens_used"`
	Mode         string `json:"mode,omitempty"`          // "real" | "mock"
	FallbackUsed bool   `json:"fallback_used,omitempty"`
}

func (ra *ReflectionActivities) GenerateInitialDraft(ctx context.Context, input GenerateInitialDraftInput) (*GenerateInitialDraftResult, error) {
	if input.MockLLM {
		mockDraft := fmt.Sprintf("Mock draft for: %s. This is a generated initial response that needs improvement.", truncate(input.Query, 80))
		return &GenerateInitialDraftResult{
			DraftRef:     fmt.Sprintf("reflection:%s:draft:0", input.WorkflowID),
			DraftSummary: truncate(mockDraft, 500),
			DraftText:    mockDraft,
			TokensUsed:   50,
		}, nil
	}

	// Real LLM: reuse AgentActivity
	result, err := ra.agentActs.CallLLM(ctx, AgentActivityInput{
		TaskID:                  input.TaskID,
		WorkflowID:              input.WorkflowID,
		RunID:                   input.RunID,
		Query:                   input.Query,
		Model:                   input.Model,
		Temperature:             input.Temperature,
		MaxCompletionTokens:     input.MaxCompletionTokens,
		AllowedCompletionTokens: input.MaxCompletionTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("generate initial draft: %w", err)
	}

	return &GenerateInitialDraftResult{
		DraftRef:     fmt.Sprintf("reflection:%s:draft:0", input.WorkflowID),
		DraftSummary: truncate(result.Answer, 500),
		DraftText:    result.Answer,
		TokensUsed:   result.Usage.TotalTokens,
		Mode:         "real",
		FallbackUsed: false,
	}, nil
}

// ─── EvaluateDraft ─────────────────────────────────────────────────────

type EvaluateDraftInput struct {
	TaskID     string   `json:"task_id"`
	WorkflowID string   `json:"workflow_id"`
	RunID      string   `json:"run_id"`
	Query      string   `json:"query"`
	DraftText  string   `json:"draft_text"`
	Criteria   []string `json:"criteria"`
	MockLLM    bool     `json:"mock_llm"`
	Model      string   `json:"model"`
}

type EvaluateDraftResult struct {
	Score           float64 `json:"score"`
	CritiqueRef     string  `json:"critique_ref,omitempty"`
	CritiqueSummary string  `json:"critique_summary"`
	CritiqueText    string  `json:"critique_text"`
	TokensUsed      int     `json:"tokens_used"`
	Mode            string  `json:"mode,omitempty"`
	FallbackUsed    bool    `json:"fallback_used,omitempty"`
}

func (ra *ReflectionActivities) EvaluateDraft(ctx context.Context, input EvaluateDraftInput) (*EvaluateDraftResult, error) {
	if input.MockLLM {
		criteriaStr := strings.Join(input.Criteria, ",")
		mockCritique := fmt.Sprintf("Mock critique: the draft needs improvement on %s. It is somewhat brief and could be more detailed.", criteriaStr)
		return &EvaluateDraftResult{
			Score:           0.55, // below threshold, triggers revision
			CritiqueRef:     fmt.Sprintf("reflection:%s:critique:0", input.WorkflowID),
			CritiqueSummary: truncate(mockCritique, 500),
			CritiqueText:    mockCritique,
			TokensUsed:      30,
		}, nil
	}

	// Real LLM: build evaluation prompt
	evalQuery := fmt.Sprintf(
		`Evaluate the following response to the query "%s" on these criteria: %s.
Score each criterion 0.0-1.0 and return a JSON with "score" (average), "critique" (short feedback).
Response to evaluate: %s`,
		input.Query, strings.Join(input.Criteria, ","), input.DraftText,
	)

	result, err := ra.agentActs.CallLLM(ctx, AgentActivityInput{
		TaskID:                  input.TaskID,
		WorkflowID:              input.WorkflowID,
		RunID:                   input.RunID,
		Query:                   evalQuery,
		Model:                   input.Model,
		Temperature:             0.3,
		MaxCompletionTokens:     512,
		AllowedCompletionTokens: 512,
	})
	if err != nil {
		return nil, fmt.Errorf("evaluate draft: %w", err)
	}

	return &EvaluateDraftResult{
		Score:           0.55,
		CritiqueRef:     fmt.Sprintf("reflection:%s:critique:0", input.WorkflowID),
		CritiqueSummary: truncate(result.Answer, 500),
		CritiqueText:    result.Answer,
		TokensUsed:      result.Usage.TotalTokens,
		Mode:            "real",
		FallbackUsed:    false,
	}, nil
}

// ─── ReviseDraft ───────────────────────────────────────────────────────

type ReviseDraftInput struct {
	TaskID              string  `json:"task_id"`
	WorkflowID          string  `json:"workflow_id"`
	RunID               string  `json:"run_id"`
	Query               string  `json:"query"`
	DraftText           string  `json:"draft_text"`
	CritiqueText        string  `json:"critique_text"`
	MockLLM             bool    `json:"mock_llm"`
	Model               string  `json:"model"`
	Temperature         float64 `json:"temperature"`
	MaxCompletionTokens int     `json:"max_completion_tokens"`
}

type ReviseDraftResult struct {
	RevisedRef     string `json:"revised_ref,omitempty"`
	RevisedSummary string `json:"revised_summary"`
	RevisedText    string `json:"revised_text"`
	TokensUsed     int    `json:"tokens_used"`
	Mode           string `json:"mode,omitempty"`
	FallbackUsed   bool   `json:"fallback_used,omitempty"`
}

func (ra *ReflectionActivities) ReviseDraft(ctx context.Context, input ReviseDraftInput) (*ReviseDraftResult, error) {
	if input.MockLLM {
		mockRevised := fmt.Sprintf("Mock revised answer for: %s. This version incorporates the critique feedback and provides a more detailed, clearer response.", truncate(input.Query, 80))
		return &ReviseDraftResult{
			RevisedRef:     fmt.Sprintf("reflection:%s:revised:0", input.WorkflowID),
			RevisedSummary: truncate(mockRevised, 500),
			RevisedText:    mockRevised,
			TokensUsed:     80,
		}, nil
	}

	// Real LLM: build revision prompt
	reviseQuery := fmt.Sprintf(
		`Revise the following response based on the critique.
Query: %s
Critique: %s
Original response: %s
Provide a revised, improved response.`,
		input.Query, input.CritiqueText, input.DraftText,
	)

	result, err := ra.agentActs.CallLLM(ctx, AgentActivityInput{
		TaskID:                  input.TaskID,
		WorkflowID:              input.WorkflowID,
		RunID:                   input.RunID,
		Query:                   reviseQuery,
		Model:                   input.Model,
		Temperature:             input.Temperature,
		MaxCompletionTokens:     input.MaxCompletionTokens,
		AllowedCompletionTokens: input.MaxCompletionTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("revise draft: %w", err)
	}

	return &ReviseDraftResult{
		RevisedRef:     fmt.Sprintf("reflection:%s:revised:0", input.WorkflowID),
		RevisedSummary: truncate(result.Answer, 500),
		RevisedText:    result.Answer,
		TokensUsed:     result.Usage.TotalTokens,
		Mode:           "real",
		FallbackUsed:   false,
	}, nil
}

// ─── AuditReflection ──────────────────────────────────────────────────

type AuditReflectionInput struct {
	WorkflowID  string  `json:"workflow_id"`
	Query       string  `json:"query"`
	FinalScore  float64 `json:"final_score"`
	Iterations  int     `json:"iterations"`
	TotalTokens int     `json:"total_tokens"`
	Status      string  `json:"status"`
}

type AuditReflectionResult struct {
	AuditID string `json:"audit_id"`
}

func (ra *ReflectionActivities) AuditReflection(ctx context.Context, input AuditReflectionInput) (*AuditReflectionResult, error) {
	// Slice 25 MVP: no persistence; placeholder until migration 013 added.
	return &AuditReflectionResult{AuditID: fmt.Sprintf("reflection-audit:%s", input.WorkflowID)}, nil
}

// ─── EmitReflectionEvent ───────────────────────────────────────────────

type EmitReflectionEventInput struct {
	WorkflowID string  `json:"workflow_id"`
	EventType  string  `json:"event_type"` // "REFLECTION_STARTED", "REFLECTION_ITERATION", "REFLECTION_COMPLETED"
	Iteration  int     `json:"iteration"`
	Score      float64 `json:"score"`
}

func (ra *ReflectionActivities) EmitReflectionEvent(ctx context.Context, input EmitReflectionEventInput) error {
	return nil
}

// ─── LLM Config Snapshot (Provider Config Foundation) ────────────────

type LLMConfigSnapshot struct {
	Provider    string `json:"provider"`
	ChatModel   string `json:"chat_model"`
	BaseURL     string `json:"base_url"`
	APIKeyEnv   string `json:"api_key_env"`
	RequireReal bool   `json:"require_real"`
}

type ResolveEffectiveLLMConfigInput struct{}

// ResolveEffectiveLLMConfig reads the active LLM profile + catalog and returns a safe snapshot.
// Does NOT return API key plaintext — only the env var name that holds it.
func (ra *ReflectionActivities) ResolveEffectiveLLMConfig(ctx context.Context, input ResolveEffectiveLLMConfigInput) (*LLMConfigSnapshot, error) {
	catalog, err := cribugllm.LoadCatalog("config/model_providers.yaml")
	if err != nil {
		return nil, fmt.Errorf("load catalog: %w", err)
	}
	profile, err := cribugllm.LoadProfile("runtime/llm_active_profile.json")
	if err != nil {
		return nil, fmt.Errorf("load profile: %w", err)
	}

	ec := (&cribugllm.ConfigResolver{}).WithProfile(profile).WithCatalog(catalog).Resolve()
	if ec.ValidationError != "" {
		return nil, fmt.Errorf("config validation: %s", ec.ValidationError)
	}

	return &LLMConfigSnapshot{
		Provider:    ec.Provider,
		ChatModel:   ec.ChatModel,
		BaseURL:     ec.BaseURL,
		APIKeyEnv:   profile.APIKeyEnv,
		RequireReal: ec.RequireReal,
	}, nil
}
