package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"cribug/internal/types"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

type CallRequest struct {
	TraceID             string
	TaskID             string
	SessionID          *string
	Provider           string
	Model              string
	Messages           []types.LLMMessage
	Temperature        float64
	MaxCompletionTokens int
	Metadata           map[string]any
}

type CallResponse struct {
	Content     string      `json:"content"`
	Usage       types.Usage `json:"usage"`
	Model       string      `json:"model"`
	Provider    string      `json:"provider"`
	FinishReason string     `json:"finish_reason"`
	LatencyMS   int64       `json:"latency_ms"`
	Error       *string     `json:"error,omitempty"`
}

func (c *Client) Call(ctx context.Context, req CallRequest) (*CallResponse, error) {
	messages := make([]types.LLMMessage, 0, len(req.Messages))
	messages = append(messages, req.Messages...)

	llmReq := types.LLMRequest{
		TraceID:             req.TraceID,
		TaskID:              req.TaskID,
		SessionID:           req.SessionID,
		Provider:            req.Provider,
		Model:               req.Model,
		Messages:            messages,
		Temperature:         req.Temperature,
		MaxCompletionTokens: req.MaxCompletionTokens,
		Metadata:           req.Metadata,
	}

	jsonBody, err := json.Marshal(llmReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.httpClient.Post(
		c.baseURL+"/chat",
		"application/json",
		bytes.NewBuffer(jsonBody),
	)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm service returned status %d", resp.StatusCode)
	}

	var llmResp CallResponse
	if err := json.NewDecoder(resp.Body).Decode(&llmResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if llmResp.Error != nil && *llmResp.Error != "" {
		return nil, fmt.Errorf("llm error: %s", *llmResp.Error)
	}

	if llmResp.Content == "" {
		return nil, fmt.Errorf("empty content from llm")
	}

	return &llmResp, nil
}
