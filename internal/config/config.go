package config

import (
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DatabaseURL       string
	RedisAddr         string
	RedisPass         string
	RedisDB           int
	HTTPAddr          string
	TemporalAddress   string
	TemporalTaskQueue string
	LLMServiceURL     string
	EnableDAGWorkflow bool
	EnableMultiAgent  bool
	EnableTools       bool

	// DAG Concurrency (Slice 7)
	EnableDAGConcurrency bool
	DAGTTLSeconds        int
	MaxParallelAgents    int

	// ReAct Reasoning Loop (Slice 8)
	EnableReAct          bool
	ReActMaxIterations   int
	ReActStepsTTLSeconds int

	// MCP Tool Runtime (Phase 6A Slice 17)
	EnableMCP               bool
	MCPDefaultTimeoutSec    int
	MCPResultSizeLimitBytes int

	// Sandbox Runtime (Phase 6B Slice 18)
	EnableSandbox          bool
	SandboxRunnerPath      string
	SandboxDefaultTimeout  int
	SandboxDefaultMemoryMB int

	// Hooks Event System (Phase 6D Slice 20)
	EnableHooks                  bool     // ENABLE_HOOKS — master switch
	HooksBlockingEnabled         bool     // HOOKS_BLOCKING_ENABLED
	HookBlockingFailClosed       bool     // HOOK_BLOCKING_FAIL_CLOSED
	HookHandlerTimeoutSec        int      // HOOK_HANDLER_TIMEOUT_SECONDS
	HookHandlerMaxResultBytes    int      // HOOK_HANDLER_MAX_RESULT_BYTES
	HookAllowedInternalHandlers  []string // HOOK_ALLOWED_INTERNAL_HANDLERS
	HookAllowedHTTPHosts         []string // HOOK_ALLOWED_HTTP_HOSTS

	// Embeddings + Qdrant (Phase 6E-1)
	EmbeddingModel        string // EMBEDDING_MODEL
	EmbeddingDim          int    // EMBEDDING_DIM
	EmbeddingTimeoutSec   int    // EMBEDDING_TIMEOUT_SECONDS
	EmbeddingCacheEnabled bool   // EMBEDDING_CACHE_ENABLED
	EmbeddingCacheMaxSize int    // EMBEDDING_CACHE_MAX_SIZE
	QdrantHost            string // QDRANT_HOST
	QdrantPort            int    // QDRANT_PORT
	QdrantScheme          string // QDRANT_SCHEME
	QdrantTimeoutSec      int    // QDRANT_TIMEOUT_SECONDS
}

func Load() *Config {
	cfg := &Config{
		DatabaseURL:       getEnv("DATABASE_URL", "postgres://admin:admin@localhost:5432/orchestrator?sslmode=disable"),
		RedisAddr:         getEnv("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPass:         getEnv("REDIS_PASSWORD", ""),
		RedisDB:           getEnvAsInt("REDIS_DB", 0),
		HTTPAddr:          getEnv("HTTP_ADDR", ":8080"),
		TemporalAddress:   getEnv("TEMPORAL_ADDRESS", "127.0.0.1:17233"),
		TemporalTaskQueue: getEnv("TEMPORAL_TASK_QUEUE", "orchestrator-task-queue"),
		LLMServiceURL:     getEnv("LLM_SERVICE_URL", "http://127.0.0.1:8000"),
		EnableDAGWorkflow: getEnvAsBool("ENABLE_DAG_WORKFLOW", false),
		EnableMultiAgent:  getEnvAsBool("ENABLE_MULTI_AGENT", false),
		EnableTools:       getEnvAsBool("ENABLE_TOOLS", false),

		// DAG Concurrency (Slice 7)
		EnableDAGConcurrency: getEnvAsBool("ENABLE_DAG_CONCURRENCY", false),
		DAGTTLSeconds:        getEnvAsInt("DAG_TTL_SECONDS", 86400),
		MaxParallelAgents:    getEnvAsInt("MAX_PARALLEL_AGENTS", 5),

		// ReAct Reasoning Loop (Slice 8)
		EnableReAct:          getEnvAsBool("ENABLE_REACT", false),
		ReActMaxIterations:   getEnvAsInt("REACT_MAX_ITERATIONS", 3),
		ReActStepsTTLSeconds: getEnvAsInt("REACT_STEPS_TTL_SECONDS", 86400),

		// MCP Tool Runtime (Phase 6A Slice 17)
		EnableMCP:               getEnvAsBool("ENABLE_MCP", false),
		MCPDefaultTimeoutSec:    getEnvAsInt("MCP_DEFAULT_TIMEOUT_SEC", 30),
		MCPResultSizeLimitBytes: getEnvAsInt("MCP_RESULT_SIZE_LIMIT_BYTES", 65536),

		// Sandbox Runtime (Phase 6B Slice 18)
		EnableSandbox:          getEnvAsBool("ENABLE_SANDBOX", false),
		SandboxRunnerPath:      getEnv("SANDBOX_RUNNER_PATH", "./sandbox/runner/target/release/sandbox-runner"),
		SandboxDefaultTimeout:  getEnvAsInt("SANDBOX_DEFAULT_TIMEOUT_SEC", 30),
		SandboxDefaultMemoryMB: getEnvAsInt("SANDBOX_DEFAULT_MEMORY_MB", 128),

		// Hooks Event System (Phase 6D Slice 20)
		EnableHooks:                  getEnvAsBool("ENABLE_HOOKS", false) || getEnvAsBool("HOOKS_ENABLED", false),
		HooksBlockingEnabled:         getEnvAsBool("HOOKS_BLOCKING_ENABLED", false),
		HookBlockingFailClosed:       getEnvAsBool("HOOK_BLOCKING_FAIL_CLOSED", false),
		HookHandlerTimeoutSec:        getEnvAsInt("HOOK_HANDLER_TIMEOUT_SECONDS", 5),
		HookHandlerMaxResultBytes:    getEnvAsInt("HOOK_HANDLER_MAX_RESULT_BYTES", 65536),
		HookAllowedInternalHandlers:  splitEnv("HOOK_ALLOWED_INTERNAL_HANDLERS", "audit_logger,log_only,permission_check"),
		HookAllowedHTTPHosts:         splitEnv("HOOK_ALLOWED_HTTP_HOSTS", "localhost,127.0.0.1"),

			// Embeddings + Qdrant (Phase 6E-1)
			EmbeddingModel:        getEnv("EMBEDDING_MODEL", "text-embedding-3-small"),
			EmbeddingDim:          getEnvAsInt("EMBEDDING_DIM", 1536),
			EmbeddingTimeoutSec:   getEnvAsInt("EMBEDDING_TIMEOUT_SECONDS", 30),
			EmbeddingCacheEnabled: getEnvAsBool("EMBEDDING_CACHE_ENABLED", false),
			EmbeddingCacheMaxSize: getEnvAsInt("EMBEDDING_CACHE_MAX_SIZE", 1000),
			QdrantHost:            getEnv("QDRANT_HOST", "localhost"),
			QdrantPort:            getEnvAsInt("QDRANT_PORT", 6333),
			QdrantScheme:          getEnv("QDRANT_SCHEME", "http"),
			QdrantTimeoutSec:      getEnvAsInt("QDRANT_TIMEOUT_SECONDS", 10),
	}

	if urlStr := os.Getenv("REDIS_URL"); urlStr != "" {
		if u, err := url.Parse(urlStr); err == nil {
			if u.Host != "" {
				cfg.RedisAddr = u.Host
			}
			if u.Path != "" {
				if db, err := strconv.Atoi(u.Path[1:]); err == nil {
					cfg.RedisDB = db
				}
			}
			if u.User != nil {
				cfg.RedisPass, _ = u.User.Password()
			}
		}
	}

	return cfg
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func splitEnv(key, defaultValue string) []string {
	val := getEnv(key, defaultValue)
	if val == "" {
		return nil
	}
	parts := strings.Split(val, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func getEnvAsBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		return value == "true" || value == "1" || value == "yes"
	}
	return defaultValue
}