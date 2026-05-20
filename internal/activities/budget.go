package activities

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	gocache "github.com/patrickmn/go-cache"

	"go.temporal.io/sdk/activity"
)

// Global token cache with 10 minute TTL
var tokenCache = gocache.New(10*time.Minute, 5*time.Minute)

type BudgetActivities struct {
	llmServiceURL string
	httpClient    *http.Client
}

func NewBudgetActivities(llmServiceURL string) *BudgetActivities {
	return &BudgetActivities{
		llmServiceURL: llmServiceURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type EstimatePromptTokensInput struct {
	Text  string
	Model string
}

type EstimatePromptTokensOutput struct {
	EstimatedPromptTokens int
	Cached               bool
}

// hash256 creates a SHA256 hash of text+model for cache key
func hash256(text, model string) string {
	h := sha256.New()
	h.Write([]byte(text + model))
	return hex.EncodeToString(h.Sum(nil))[:16] // Use first 16 chars for shorter key
}

func (a *BudgetActivities) EstimatePromptTokens(ctx context.Context, input EstimatePromptTokensInput) (*EstimatePromptTokensOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("EstimatePromptTokensActivity started", "text_len", len(input.Text), "model", input.Model)

	// 1. Check cache first
	cacheKey := hash256(input.Text, input.Model)
	if cached, found := tokenCache.Get(cacheKey); found {
		logger.Info("EstimatePromptTokensActivity: cache hit", "key", cacheKey)
		return &EstimatePromptTokensOutput{
			EstimatedPromptTokens: cached.(int),
			Cached:               true,
		}, nil
	}

	// 2. Call /tokenize endpoint
	estimated, err := a.callTokenize(ctx, input.Text, input.Model)
	if err != nil {
		logger.Warn("EstimatePromptTokensActivity: tokenize failed, using fallback", "error", err)
		// Fallback to rough estimate
		estimated = int(math.Ceil(float64(len(input.Text)) / 4.0))
		if estimated < 1 {
			estimated = 1
		}
	}

	// 3. Store in cache
	tokenCache.Set(cacheKey, estimated, gocache.DefaultExpiration)

	logger.Info("EstimatePromptTokensActivity completed", "estimated", estimated, "cached", false)
	return &EstimatePromptTokensOutput{
		EstimatedPromptTokens: estimated,
		Cached:               false,
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
		TokenCount int  `json:"token_count"`
		Encoding  string `json:"encoding,omitempty"`
		Model     string `json:"model,omitempty"`
		Fallback  bool   `json:"fallback,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}

	return result.TokenCount, nil
}

type CheckBudgetInput struct {
	EstimatedPromptTokens int
	MaxTotalTokens        int
	MaxCompletionTokens   int
}

type CheckBudgetOutput struct {
	Allowed               bool
	Reason                string
	AllowedCompletionTokens int
}

func (a *BudgetActivities) CheckBudget(ctx context.Context, input CheckBudgetInput) (*CheckBudgetOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("CheckBudgetActivity started",
		"estimated_prompt_tokens", input.EstimatedPromptTokens,
		"max_total_tokens", input.MaxTotalTokens,
		"max_completion_tokens", input.MaxCompletionTokens)

	// Apply defaults
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