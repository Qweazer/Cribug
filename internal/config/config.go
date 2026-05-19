package config

import (
	"net/url"
	"os"
	"strconv"
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

func getEnvAsBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		return value == "true" || value == "1" || value == "yes"
	}
	return defaultValue
}