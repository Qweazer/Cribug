package skillclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client calls Cribug Python Skills API
type Client struct {
	baseURL    string
	httpClient *http.Client
	timeout    time.Duration
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		timeout:    30 * time.Second,
	}
}

func WithTimeout(timeout time.Duration) func(*Client) {
	return func(c *Client) {
		c.timeout = timeout
		c.httpClient.Timeout = timeout
	}
}

type ExecuteRequest struct {
	Parameters     map[string]interface{} `json:"parameters"`
	SessionContext map[string]interface{} `json:"session_context,omitempty"`
	RequestID      string                 `json:"request_id,omitempty"`
	AgentID        string                 `json:"agent_id,omitempty"`
	WorkflowID     string                 `json:"workflow_id,omitempty"`
}

type ExecuteResponse struct {
	ToolName        string                 `json:"tool_name"`
	RequestID       string                 `json:"request_id"`
	Success         bool                   `json:"success"`
	Output          interface{}            `json:"output,omitempty"`
	Error           string                 `json:"error,omitempty"`
	ErrorType       string                 `json:"error_type,omitempty"`
	ExecutionTimeMs int                    `json:"execution_time_ms,omitempty"`
	Overflow        bool                   `json:"overflow"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
}

type SkillMetadata struct {
	Name            string                 `json:"name"`
	Version         string                 `json:"version"`
	Description     string                 `json:"description"`
	Category        string                 `json:"category"`
	ExecutionMode   string                 `json:"execution_mode"`
	RequiresSandbox bool                   `json:"requires_sandbox"`
	RequiresLLM     bool                   `json:"requires_llm"`
	RiskLevel       string                 `json:"risk_level"`
	SideEffects     bool                   `json:"side_effects"`
	Parameters      map[string]interface{} `json:"parameters"`
}

func (c *Client) ListSkills(ctx context.Context) ([]string, error) {
	url := fmt.Sprintf("%s/tools", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var skills []string
	if err := json.NewDecoder(resp.Body).Decode(&skills); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return skills, nil
}

func (c *Client) GetSkillMetadata(ctx context.Context, skillName string) (*SkillMetadata, error) {
	url := fmt.Sprintf("%s/tools/%s", c.baseURL, skillName)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("skill not found: %s", skillName)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var metadata SkillMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &metadata, nil
}

func (c *Client) ExecuteSkill(ctx context.Context, skillName string, parameters map[string]interface{}) (*ExecuteResponse, error) {
	url := fmt.Sprintf("%s/tools/%s/execute", c.baseURL, skillName)

	reqBody := ExecuteRequest{Parameters: parameters}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	var result ExecuteResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		result.Success = false
		result.Error = "skill not found"
		result.ErrorType = "tool_not_found"
		return &result, nil
	}
	if resp.StatusCode >= 400 {
		result.Success = false
		result.Error = fmt.Sprintf("HTTP error: %d", resp.StatusCode)
		result.ErrorType = "http_error"
		return &result, nil
	}

	return &result, nil
}
