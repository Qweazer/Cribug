package activities

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	redisclient "cribug/internal/redis"

	"go.temporal.io/sdk/activity"
)

type BudgetActivities struct {
	llmServiceURL string
	httpClient    *http.Client
	tokenCache    *TwoLevelTokenCache
}

func NewBudgetActivities(llmServiceURL string, redisClient *redisclient.Client) *BudgetActivities {
	return &BudgetActivities{
		llmServiceURL: llmServiceURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		tokenCache: NewTwoLevelTokenCache(redisClient, 10000, 5*time.Minute, 1*time.Hour),
	}
}

type EstimatePromptTokensInput struct {
	Text  string
	Model string
}

type EstimatePromptTokensOutput struct {
	EstimatedPromptTokens int
	Cached                bool
	Source                string // local_lru, redis, python_service, fallback
}

func (a *BudgetActivities) EstimatePromptTokens(ctx context.Context, input EstimatePromptTokensInput) (*EstimatePromptTokensOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("EstimatePromptTokensActivity started", "text_len", len(input.Text), "model", input.Model)

	// 1. Check two-level cache
	if a.tokenCache != nil {
		result := a.tokenCache.Get(ctx, input.Model, input.Text)
		if result.Cached {
			logger.Info("EstimatePromptTokensActivity: cache hit", "source", result.Source, "count", result.Count)
			return &EstimatePromptTokensOutput{
				EstimatedPromptTokens: result.Count,
				Cached:                true,
				Source:                result.Source,
			}, nil
		}
	}

	// 2. Call /tokenize endpoint
	estimated, err := a.callTokenize(ctx, input.Text, input.Model)
	if err != nil {
		logger.Warn("EstimatePromptTokensActivity: tokenize failed, using fallback", "error", err)
		estimated = int(math.Ceil(float64(len(input.Text)) / 4.0))
		if estimated < 1 {
			estimated = 1
		}
		if a.tokenCache != nil {
			a.tokenCache.RecordFallback(ctx)
		}
		// Store in cache even for fallback
		if a.tokenCache != nil {
			a.tokenCache.Set(ctx, input.Model, input.Text, estimated)
		}
		return &EstimatePromptTokensOutput{
			EstimatedPromptTokens: estimated,
			Cached:                false,
			Source:                "fallback",
		}, nil
	}

	// 3. Store in two-level cache
	if a.tokenCache != nil {
		a.tokenCache.RecordPythonCall(ctx)
		a.tokenCache.Set(ctx, input.Model, input.Text, estimated)
	}

	logger.Info("EstimatePromptTokensActivity completed", "estimated", estimated, "source", "python_service")
	return &EstimatePromptTokensOutput{
		EstimatedPromptTokens: estimated,
		Cached:                false,
		Source:                "python_service",
	}, nil
}

// callTokenize calls the Python /tokenize endpoint
func (a *BudgetActivities) callTokenize(ctx context.Context, text, model string) (int, error) {
	reqBody := map[string]string{
		"text":  text,
		"model": model,
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return 0, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := a.httpClient.Post(
		a.llmServiceURL+"/tokenize",
		"application/json",
		bytes.NewBuffer(jsonBody),
	)
	if err != nil {
		return 0, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("tokenize returned status %d", resp.StatusCode)
	}

	var result struct {
		TokenCount int    `json:"token_count"`
		Encoding   string `json:"encoding,omitempty"`
		Model      string `json:"model,omitempty"`
		Fallback   bool   `json:"fallback,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}

	return result.TokenCount, nil
}

// GetCacheStats returns the current lru:stats from Redis.
func (a *BudgetActivities) GetCacheStats(ctx context.Context) map[string]string {
	if a.tokenCache == nil {
		return nil
	}
	return a.tokenCache.GetStats(ctx)
}

type CheckBudgetInput struct {
	EstimatedPromptTokens int
	MaxTotalTokens        int
	MaxCompletionTokens   int
}

type CheckBudgetOutput struct {
	Allowed                 bool
	Reason                  string
	AllowedCompletionTokens int
}

func (a *BudgetActivities) CheckBudget(ctx context.Context, input CheckBudgetInput) (*CheckBudgetOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("CheckBudgetActivity started",
		"estimated_prompt_tokens", input.EstimatedPromptTokens,
		"max_total_tokens", input.MaxTotalTokens,
		"max_completion_tokens", input.MaxCompletionTokens)

	maxTotal := input.MaxTotalTokens
	maxCompletion := input.MaxCompletionTokens

	if maxTotal <= 0 {
		maxTotal = 8000
	}
	if maxCompletion <= 0 {
		maxCompletion = 1024
	}

	output := &CheckBudgetOutput{}

	if input.EstimatedPromptTokens > maxTotal {
		output.Allowed = false
		output.Reason = "estimated_prompt_tokens exceeds max_total_tokens"
		output.AllowedCompletionTokens = 0
		logger.Info("CheckBudgetActivity: budget exceeded", "reason", output.Reason)
	} else {
		output.Allowed = true
		allowed := maxTotal - input.EstimatedPromptTokens
		if allowed > maxCompletion {
			allowed = maxCompletion
		}
		output.AllowedCompletionTokens = allowed
		output.Reason = ""
		logger.Info("CheckBudgetActivity: budget ok", "allowed_completion_tokens", allowed)
	}

	return output, nil
}

// CheckSynthesizerBudget checks budget for synthesizer LLM call
func (a *BudgetActivities) CheckSynthesizerBudget(ctx context.Context, input CheckBudgetInput) (*CheckBudgetOutput, error) {
	return a.CheckBudget(ctx, input)
}
