package activities

import (
	"context"
	"math"

	"go.temporal.io/sdk/activity"
)

type BudgetActivities struct{}

func NewBudgetActivities() *BudgetActivities {
	return &BudgetActivities{}
}

type EstimatePromptTokensInput struct {
	Text  string
	Model string
}

type EstimatePromptTokensOutput struct {
	EstimatedPromptTokens int
}

func (a *BudgetActivities) EstimatePromptTokens(ctx context.Context, input EstimatePromptTokensInput) (*EstimatePromptTokensOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("EstimatePromptTokensActivity started", "text_len", len(input.Text), "model", input.Model)

	// MVP: ceil(len(text)/4.0)
	estimated := int(math.Ceil(float64(len(input.Text)) / 4.0))
	if estimated < 1 {
		estimated = 1
	}

	logger.Info("EstimatePromptTokensActivity completed", "estimated", estimated)
	return &EstimatePromptTokensOutput{EstimatedPromptTokens: estimated}, nil
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