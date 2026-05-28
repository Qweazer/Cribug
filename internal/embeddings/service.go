package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultBaseURL      = "http://localhost:8000"
	defaultModel        = "text-embedding-3-small"
	defaultExpectedDim  = 1536
	defaultTimeout      = 30 * time.Second
	defaultMaxRetries   = 2
	defaultCacheMaxSize = 1000
)

// embedRequest is the JSON body sent to the Python embedding service.
type embedRequest struct {
	Texts []string `json:"texts"`
	Model string   `json:"model"`
}

// embedResponse is the JSON response from the Python embedding service.
type embedResponse struct {
	Vectors  [][]float64    `json:"vectors"`
	Model    string         `json:"model"`
	Provider string         `json:"provider"`
	Usage    *EmbeddingUsage `json:"usage,omitempty"`
}

// Service provides text embedding via a remote Python LLM service.
type Service struct {
	config Config
	client *http.Client
	cache  *lruCache
}

// NewService creates a new embedding Service with the given config.
// Config fields with zero values are set to sensible defaults.
func NewService(config Config) *Service {
	if config.BaseURL == "" {
		config.BaseURL = defaultBaseURL
	}
	if config.DefaultModel == "" {
		config.DefaultModel = defaultModel
	}
	if config.ExpectedDim <= 0 {
		config.ExpectedDim = defaultExpectedDim
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultTimeout
	}
	if config.MaxRetries <= 0 {
		config.MaxRetries = defaultMaxRetries
	}
	if config.CacheMaxSize <= 0 {
		config.CacheMaxSize = defaultCacheMaxSize
	}

	s := &Service{
		config: config,
		client: &http.Client{
			Timeout: config.Timeout,
		},
	}

	if config.CacheEnabled {
		s.cache = newLRUCache(config.CacheMaxSize)
	}

	return s
}

// EmbedText embeds a single text string and returns the result.
// If caching is enabled, the cache is checked before making the request.
func (s *Service) EmbedText(ctx context.Context, text string, model string) (*EmbeddingResult, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("embeddings: empty text")
	}

	if model == "" {
		model = s.config.DefaultModel
	}

	// Check cache for single-text requests
	if s.cache != nil {
		key := cacheKey(s.config.BaseURL, model, text)
		if cached := s.cache.Get(key); cached != nil {
			// Return a copy to avoid mutating the cache entry
			cp := *cached
			cp.Cached = true
			return &cp, nil
		}
	}

	result, err := s.embedWithRetry(ctx, []string{text}, model)
	if err != nil {
		return nil, err
	}

	// Unwrap single-text result: extract first vector
	if len(result.Vectors) > 0 {
		result.Vectors = [][]float64{result.Vectors[0]}
	}

	// Store in cache (with Cached=false, the default)
	if s.cache != nil {
		key := cacheKey(s.config.BaseURL, model, text)
		result.Cached = false
		s.cache.Set(key, result)
	}

	return result, nil
}

// EmbedBatch embeds multiple text strings in a single request.
func (s *Service) EmbedBatch(ctx context.Context, texts []string, model string) (*EmbeddingResult, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("embeddings: empty texts slice")
	}
	for i, t := range texts {
		if strings.TrimSpace(t) == "" {
			return nil, fmt.Errorf("embeddings: empty text at index %d", i)
		}
	}

	if model == "" {
		model = s.config.DefaultModel
	}

	return s.embedWithRetry(ctx, texts, model)
}

// embedWithRetry performs the embedding request with retry logic.
func (s *Service) embedWithRetry(ctx context.Context, texts []string, model string) (*EmbeddingResult, error) {
	var lastErr error

	for attempt := 0; attempt <= s.config.MaxRetries; attempt++ {
		if attempt > 0 {
			// Simple backoff: 100ms * 2^(attempt-1)
			backoff := time.Duration(100*(1<<(attempt-1))) * time.Millisecond
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		result, err := s.doEmbed(ctx, texts, model)
		if err == nil {
			return result, nil
		}

		lastErr = err

		// Only retry on transient errors
		if !isTransientError(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("embeddings: request failed after %d retries: %w", s.config.MaxRetries, lastErr)
}

// doEmbed performs a single HTTP request to the embedding service.
func (s *Service) doEmbed(ctx context.Context, texts []string, model string) (*EmbeddingResult, error) {
	reqBody := embedRequest{
		Texts: texts,
		Model: model,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("embeddings: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.config.BaseURL+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embeddings: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embeddings: http request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("embeddings: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embeddings: server returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var embedResp embedResponse
	if err := json.Unmarshal(bodyBytes, &embedResp); err != nil {
		return nil, fmt.Errorf("embeddings: unmarshal response: %w", err)
	}

	// Validate dimensions
	for i, vec := range embedResp.Vectors {
		if len(vec) != s.config.ExpectedDim {
			return nil, fmt.Errorf("embeddings: vector %d has dimension %d, expected %d", i, len(vec), s.config.ExpectedDim)
		}
	}

	durationMs := time.Since(start).Milliseconds()

	result := &EmbeddingResult{
		Vectors:    embedResp.Vectors,
		Model:      embedResp.Model,
		Provider:   embedResp.Provider,
		Usage:      embedResp.Usage,
		DurationMs: durationMs,
	}

	return result, nil
}

// isTransientError returns true if the error is a transient error that can be retried.
func isTransientError(err error) bool {
	errStr := err.Error()
	// HTTP 5xx errors
	if strings.Contains(errStr, "status 5") || strings.Contains(errStr, "500") || strings.Contains(errStr, "502") || strings.Contains(errStr, "503") || strings.Contains(errStr, "504") {
		return true
	}
	// Note: Timeout errors from http.Client produce context.DeadlineExceeded errors
	// which are propagated through the call chain.
	return false
}
