package embeddings

import "time"

// Config holds configuration for the embedding service.
type Config struct {
	BaseURL      string        // Python LLM service URL
	DefaultModel string        // default "text-embedding-3-small"
	ExpectedDim  int           // default 1536
	Timeout      time.Duration // default 30s
	CacheEnabled bool          // default false
	CacheMaxSize int           // max 1000 entries
	MaxRetries   int           // default 2
}

// EmbeddingUsage tracks token usage for an embedding request.
type EmbeddingUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// EmbeddingResult holds the result of an embedding request.
type EmbeddingResult struct {
	Vectors    [][]float64    `json:"vectors"`
	Model      string         `json:"model"`
	Provider   string         `json:"provider"`
	Usage      *EmbeddingUsage `json:"usage"`
	DurationMs int64           `json:"duration_ms"`
	Cached     bool            `json:"cached"`
}
