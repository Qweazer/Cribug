package vectordb

import "time"

type Config struct {
	Host          string       // default "localhost"
	Port          int          // default 6333
	Scheme        string       // default "http"
	Timeout       time.Duration // default 10s
	Collections   Collections
	ExpectedDim   int           // default 1536
	MaxRetries    int           // default 1
	CBMaxFailures int           // circuit breaker, default 3
	CBTimeout     time.Duration // circuit breaker timeout, default 30s
}

type Collections struct {
	TaskEmbeddings string // default "task_embeddings"
	Summaries      string // default "summaries"
	DecompPatterns string // default "decomposition_patterns"
}

type VectorPoint struct {
	ID      string                 `json:"id"`
	Vector  []float64              `json:"vector"`
	Payload map[string]interface{} `json:"payload,omitempty"`
}

type SearchOptions struct {
	TopK      int     // default 5
	Threshold float64 // default 0.7
}

type SearchResult struct {
	ID      string                 `json:"id"`
	Score   float64                `json:"score"`
	Payload map[string]interface{} `json:"payload,omitempty"`
}
