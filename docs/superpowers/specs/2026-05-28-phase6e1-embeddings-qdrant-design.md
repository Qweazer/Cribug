# Phase 6E Slice 6E-1: Embeddings + Qdrant Foundation — Design Spec

## 版本说明

Phase 6E-1 是 RAG 管道的基础设施切片，只实现 Embeddings 生成和 Qdrant 向量存储，不涉及文档摄取、chunking、RAG workflow、ReAct 集成。这些由后续 6E-2 实现。

## 1. Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                      Go Orchestrator                           │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐     │
│  │  Activities   │  │embedding.Svc │  │   vectordb       │     │
│  │  EmbedText    │  │ EmbedText()  │  │   .Client        │     │
│  │  UpsertVector │  │ EmbedBatch() │  │   Upsert()       │     │
│  │  SearchVectors│  │ LRU cache    │  │   Search()       │     │
│  └──────┬───────┘  └──────┬───────┘  │   Health()       │    │
│         │                │           └────────┬──────────┘     │
│         ▼                ▼                    │ HTTP (Activity) │
│  ┌─────────────────────────────────────────────▼───────────────│
│  │  All HTTP calls inside Activities, NOT Workflows            │
│  │  Embedding vectors NOT stored in Workflow history           │
│  └─────────────────────────────────────────────────────────────│
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  Python LLM Service         │  Qdrant (Docker)                 │
│  POST /embed                │  Collections:                    │
│  - OpenAI text-embedding-   │  - task_embeddings               │
│    3-small (default)        │  - summaries                     │
│  - Reads OPENAI_API_KEY     │  - decomposition_patterns        │
│  - Returns []float64        │  Vector dim: configured (1536)   │
│  - Timeout / retry          │                                  │
└─────────────────────────────────────────────────────────────────┘
```

**Language boundaries:**
- **Python** = Embedding generation (calls OpenAI from .env)
- **Go** = Orchestration (embedding service wrapper, Qdrant client, activities, LRU cache)
- **Go ↔ Python** = HTTP (inside Activities only)
- **Go ↔ Qdrant** = HTTP REST (inside Activities only)
- **Workflow** = deterministic, no direct IO

**Explicitly NOT in 6E-1:**
- chunking / document ingestion
- documents / document_chunks tables
- CreateDocument / ChunkDocument activities
- RAG workflow
- ReAct / Swarm integration
- Redis embedding cache
- gRPC

## 2. Python Embedding Endpoint

### 2.1 Route

```
POST /embed
```

### 2.2 Request

```json
{
    "input": ["text to embed", "another text"],
    "model": "text-embedding-3-small",
    "provider": "openai"
}
```

- `input`: string or array of strings (batch)
- `model`: optional, default from env `EMBEDDING_MODEL` or `text-embedding-3-small`
- `provider`: optional, default "openai"

### 2.3 Response (success)

```json
{
    "object": "list",
    "data": [
        {
            "object": "embedding",
            "embedding": [0.001, -0.002, ...],
            "index": 0
        }
    ],
    "model": "text-embedding-3-small",
    "usage": {
        "prompt_tokens": 5,
        "total_tokens": 5
    }
}
```

### 2.4 Response (error)

```json
{
    "error": "Missing OPENAI_API_KEY",
    "error_type": "config_missing"
}
```

Error types: `config_missing`, `provider_error`, `timeout`, `rate_limited`, `invalid_input`, `internal_error`

### 2.5 Configuration

Read from `.env` / environment:
- `OPENAI_API_KEY` — required
- `EMBEDDING_MODEL` — default `text-embedding-3-small`
- `EMBEDDING_TIMEOUT_SECONDS` — default 30
- `EMBEDDING_MAX_INPUT_LENGTH` — default 8000 tokens
- `LLM_BASE_URL` — optional custom endpoint

### 2.6 Behaviour

- Accept single string or batch ([]string)
- Call OpenAI `/v1/embeddings` endpoint
- Return embeddings as []float64
- Timeout + retry (max 2)
- **No mock** — if OPENAI_API_KEY missing, return structured error
- **No secret leakage** in logs or response

## 3. Go Embedding Service

### 3.1 Package

```
internal/embeddings/
    service.go      — Service struct, EmbedText, EmbedBatch
    types.go        — Config, EmbeddingResult
    cache.go        — Optional bounded LRU cache
```

### 3.2 Config

```go
type Config struct {
    BaseURL        string        // Python LLM service URL
    DefaultModel   string        // "text-embedding-3-small"
    ExpectedDim    int           // 1536
    Timeout        time.Duration // 30s
    CacheEnabled   bool          // default false
    CacheMaxSize   int           // max entries, default 1000
    MaxRetries     int           // default 2
}
```

### 3.3 Methods

```go
type Service struct { ... }

func NewService(config Config) *Service

// EmbedText embeds a single text, returns []float64
func (s *Service) EmbedText(ctx context.Context, text string, model string) (*EmbeddingResult, error)

// EmbedBatch embeds multiple texts, returns [][]float64
func (s *Service) EmbedBatch(ctx context.Context, texts []string, model string) (*EmbeddingResult, error)
```

### 3.4 EmbeddingResult

```go
type EmbeddingResult struct {
    Vectors    [][]float64
    Model      string
    Provider   string
    Usage      *EmbeddingUsage // input_tokens, total_tokens
    DurationMs int64
    Cached     bool
}

type EmbeddingUsage struct {
    PromptTokens int
    TotalTokens  int
}
```

### 3.5 LRU Cache (optional)

- Cache key: `SHA256(provider + ":" + model + ":" + text)`
- NOT full text as key
- Bounded: max entries configurable, evict LRU
- Cache disabled by default (`CacheEnabled: false`)
- No Redis — in-memory only for 6E-1
- Hit sets `Cached: true` in result

### 3.6 Behaviour

- HTTP POST to `{BaseURL}/embed`
- Dimension validation: response vectors must have len == ExpectedDim
- Timeout from Config
- Retry: 2 retries on transient errors (5xx, timeout)
- Structured error wrapping (config missing, provider error, timeout, dim mismatch)

## 4. Go Qdrant Client

### 4.1 Package

```
internal/vectordb/
    client.go       — Client struct, Upsert, Search, DeleteCollection, Health
    types.go        — Config, VectorPoint, SearchResult
    validation.go   — dimension validation
    circuit.go      — circuit breaker
```

### 4.2 Config

```go
type Config struct {
    Host          string          // "localhost"
    Port          int             // 6333
    Scheme        string          // "http"
    Timeout       time.Duration   // 10s
    Collections   Collections     // pre-configured collection names
    ExpectedDim   int             // 1536
    MaxRetries    int             // default 1
    CBMaxFailures int             // circuit breaker, default 3
    CBTimeout     time.Duration   // circuit breaker open timeout, default 30s
}

type Collections struct {
    TaskEmbeddings string // "task_embeddings" (default)
    Summaries      string // "summaries" (default)
    DecompPatterns string // "decomposition_patterns" (default)
}
```

### 4.3 Methods

```go
type Client struct { ... }

func NewClient(config Config) (*Client, error)

// Health checks Qdrant is reachable, returns version string
func (c *Client) Health(ctx context.Context) (string, error)

// Upsert inserts or updates points in a collection
// If collection doesn't exist and config allows creation, auto-create with ExpectedDim
func (c *Client) Upsert(ctx context.Context, collection string, points []VectorPoint) error

// Search finds nearest neighbors by cosine similarity
func (c *Client) Search(ctx context.Context, collection string, vector []float64, opts SearchOptions) ([]SearchResult, error)

// EnsureCollection creates collection if not exists with configured dimension
func (c *Client) EnsureCollection(ctx context.Context, collection string) error
```

### 4.4 Types

```go
type VectorPoint struct {
    ID       string            // unique ID (UUID)
    Vector   []float64         // embedding vector
    Payload  map[string]any    // metadata (text, source, agent_id, etc.)
}

type SearchOptions struct {
    TopK       int     // default 5
    Threshold  float64 // minimum similarity score, default 0.7
}

type SearchResult struct {
    ID       string
    Score    float64
    Payload  map[string]any
}
```

### 4.5 Behaviour

- HTTP REST client (no gRPC)
- Qdrant REST API: `PUT /collections/{name}`, `PUT /collections/{name}/points`, `POST /collections/{name}/points/search`
- Circuit breaker: open after `CBMaxFailures` consecutive failures, half-open after `CBTimeout`
- Dimension validation on Upsert: reject vectors with wrong dimension
- Auto-ensure collection exists on first Upsert
- Timeout from Config
- HTTP 4xx — return structured error, don't retry
- HTTP 5xx — retry up to MaxRetries
- Qdrant unreachable — circuit breaker opens, fails fast

## 5. Go Activities

### 5.1 Package

```
internal/activities/
    embeddings.go    — EmbedTextActivity, EmbedBatchActivity
    vectordb.go      — UpsertVectorsActivity, SearchVectorsActivity
```

### 5.2 Activities

```go
// EmbedTextActivityInput / EmbedTextActivityOutput
EmbedTextActivity(ctx, input) (output, error)
  → calls embedding.Service.EmbedText()
  → does NOT store vector in workflow history (returns hash + dim + token count)

// UpsertVectorsActivity
UpsertVectorsActivity(ctx, input) (output, error)
  → calls vectordb.Client.Upsert()

// SearchVectorsActivity
SearchVectorsActivity(ctx, input) (output, error)
  → calls vectordb.Client.Search()
  → returns IDs + scores + payloads, NOT full vectors
```

### 5.3 No Workflow in 6E-1

6E-1 only provides activities and services. No RAG workflow is defined yet. Activities are independently testable.

## 6. Configuration

### 6.1 New Env Vars

```bash
# Embeddings
EMBEDDING_MODEL=text-embedding-3-small
EMBEDDING_DIM=1536
EMBEDDING_TIMEOUT_SECONDS=30

# Qdrant
QDRANT_HOST=localhost
QDRANT_PORT=6333
QDRANT_SCHEME=http
QDRANT_TIMEOUT_SECONDS=10

# Embedding Service (Go side)
EMBEDDING_CACHE_ENABLED=false
EMBEDDING_CACHE_MAX_SIZE=1000
```

### 6.2 Config additions in `internal/config/config.go`

```go
// Phase 6E-1: Embeddings + Qdrant
EmbeddingServiceURL   string   // Python LLM service URL (reuse existing)
EmbeddingModel        string   // EMBEDDING_MODEL
EmbeddingDim          int      // EMBEDDING_DIM, default 1536
EmbeddingTimeoutSec   int      // default 30
EmbeddingCacheEnabled bool     // default false
EmbeddingCacheMaxSize int      // default 1000
QdrantHost            string   // QDRANT_HOST
QdrantPort            int      // QDRANT_PORT, default 6333
QdrantScheme          string   // QDRANT_SCHEME, default "http"
QdrantTimeoutSec      int      // default 10
```

## 7. Database

No Postgres migration in 6E-1. Qdrant is self-contained.

Qdrant collections are created on first Upsert (or Health check) with configured dimension.

## 8. File Layout

### New Files (all in `python_llm_service/` and root-level Go)

```
python_llm_service/
└── llm_service/api/
    └── embed.py              — POST /embed endpoint

internal/
├── embeddings/
│   ├── types.go              — Config, EmbeddingResult, EmbeddingUsage
│   ├── service.go            — Service: EmbedText, EmbedBatch
│   ├── cache.go              — Optional bounded LRU cache
│   └── service_test.go
├── vectordb/
│   ├── types.go              — Config, VectorPoint, SearchResult, SearchOptions
│   ├── client.go             — Client: Upsert, Search, Health, EnsureCollection
│   ├── circuit.go            — Circuit breaker
│   ├── validation.go         — Dimension validation
│   └── client_test.go
├── activities/
│   ├── embeddings.go         — EmbedTextActivity, EmbedBatchActivity
│   ├── vectordb.go           — UpsertVectorsActivity, SearchVectorsActivity
│   ├── embeddings_test.go
│   └── vectordb_test.go
└── config/
    └── config.go             — Add embedding/qdrant fields
```

### Modified Files

```
internal/config/config.go     — Add embedding + qdrant config fields
python_llm_service/app.py     — Register /embed route
```

### NOT Modified

```
go/                             — Shannon reference (read-only)
python/llm-service/             — Shannon reference (read-only)
```

## 9. Test Requirements

### 9.1 Python Tests

```
python_llm_service/tests/
    test_embed.py       — Test POST /embed endpoint
```

Tests:
- Single text embedding → returns vector of correct dimension
- Batch embedding → returns array of vectors
- Missing API key → structured error
- Empty input → structured error
- Timeout → structured error

REAL OpenAI API calls only. No mock. If OPENAI_API_KEY missing, tests must clearly skip with message.

### 9.2 Go Tests

```
internal/embeddings/service_test.go   — Test EmbedText, EmbedBatch with mock HTTP
internal/vectordb/client_test.go     — Test Upsert, Search with mock HTTP
internal/activities/embeddings_test.go — Test activities
internal/activities/vectordb_test.go   — Test activities
```

Go unit tests can use mock HTTP servers. Integration tests require running Qdrant and Python service.

### 9.3 Smoke Test

```
scripts/test_phase6e1_smoke.sh
```

Covers:
- Python POST /embed returns 1536d vector
- Qdrant health check
- Qdrant upsert + search round-trip (test collection)
- Dimension mismatch rejection

## 10. Non-Goals (explicitly excluded)

- Document ingestion / chunking → 6E-2
- RAG workflow → 6E-2
- ReAct / Swarm RAG integration → 6E-2
- Redis embedding cache → future
- gRPC Qdrant client → not planned
- Embedding model fine-tuning → not planned
- Custom embedding models → not planned
- Phase 6F Research-Synthesis → not this phase
- Modifying `go/` or `python/llm-service/` → never

## 11. Temporal Deterministic Constraint

6E-1 activities are independently testable. No workflow exists yet, so no deterministic violation risk. When 6E-2 adds RAG workflow:

- Workflow MUST NOT hold full vectors
- Workflow MUST NOT call Qdrant/embedding/Python directly
- Workflow MUST NOT use time.Now/rand/uuid.New/goroutine
- All IO through Activities
