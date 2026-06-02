package llm

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ─── Provider Catalog (from config/model_providers.yaml) ──────────────

type ProviderCatalog struct {
	ModelTiers       ModelTiers               `yaml:"model_tiers"`
	ProviderSettings map[string]ProviderInfo   `yaml:"provider_settings"`
	ModelCatalog     map[string]ModelCatalog   `yaml:"model_catalog"`
	Selection        SelectionStrategy         `yaml:"selection_strategy"`
}

type ModelTiers struct {
	Small  TierProviders `yaml:"small"`
	Medium TierProviders `yaml:"medium"`
	Large  TierProviders `yaml:"large"`
}

type TierProviders struct {
	Providers []TierProvider `yaml:"providers"`
}

type TierProvider struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	Priority int    `yaml:"priority"`
}

type ProviderInfo struct {
	BaseURL  string `yaml:"base_url"`
	Timeout  int    `yaml:"timeout"`
	MaxRetry int    `yaml:"max_retries"`
	Region   string `yaml:"region,omitempty"`
}

type ModelCatalog map[string]ModelInfo

type ModelInfo struct {
	ModelID           string `yaml:"model_id"`
	Tier              string `yaml:"tier"`
	ContextWindow     int    `yaml:"context_window"`
	MaxTokens         int    `yaml:"max_tokens"`
	SupportsFunctions bool   `yaml:"supports_functions"`
	SupportsStreaming bool   `yaml:"supports_streaming"`
}

type SelectionStrategy struct {
	Mode       string `yaml:"mode"`
	Fallback   bool   `yaml:"fallback_enabled"`
	MaxRetries int    `yaml:"max_retries"`
	TimeoutSec int    `yaml:"timeout_seconds"`
}

// ─── Active LLM Profile (runtime/llm_active_profile.json) ──────────────

type LLMProfile struct {
	Provider          string `json:"provider"`
	ChatModel         string `json:"chat_model"`
	EmbeddingModel    string `json:"embedding_model"`
	BaseURLOverride   string `json:"base_url_override"`
	APIKeyEnv         string `json:"api_key_env"`
	AllowMockFallback bool   `json:"allow_mock_fallback"`
	RequireReal       bool   `json:"require_real"`
}

// ─── Effective Config ──────────────────────────────────────────────────

type EffectiveConfig struct {
	Provider        string `json:"provider"`
	ChatModel       string `json:"chat_model"`
	BaseURL         string `json:"base_url"`
	APIKeyPresent   bool   `json:"api_key_present"`
	AllowMock       bool   `json:"allow_mock_fallback"`
	RequireReal     bool   `json:"require_real"`
	OpenAICompat    bool   `json:"openai_compatible"`
	Timeout         int    `json:"timeout"`
	SourceCatalog   string `json:"source_catalog"`
	SourceProfile   string `json:"source_profile"`
	ValidationError string `json:"validation_error,omitempty"`
}

// ─── Resolver ──────────────────────────────────────────────────────────

type ConfigResolver struct {
	catalog *ProviderCatalog
	profile *LLMProfile
}

// NewResolver creates a ConfigResolver from file paths.
func NewResolver(catalogPath, profilePath string) (*ConfigResolver, error) {
	catalog, err := LoadCatalog(catalogPath)
	if err != nil {
		return nil, fmt.Errorf("load catalog: %w", err)
	}
	profile, err := LoadProfile(profilePath)
	if err != nil {
		// Profile not found is OK — use empty profile
		profile = nil
	}
	return &ConfigResolver{catalog: catalog, profile: profile}, nil
}

func (r *ConfigResolver) Profile() *LLMProfile       { return r.profile }
func (r *ConfigResolver) Catalog() *ProviderCatalog   { return r.catalog }
func (r *ConfigResolver) WithProfile(p *LLMProfile) *ConfigResolver       { r.profile = p; return r }
func (r *ConfigResolver) WithCatalog(c *ProviderCatalog) *ConfigResolver   { r.catalog = c; return r }

// Resolve merges profile + catalog into an effective config.
func (r *ConfigResolver) Resolve() EffectiveConfig {
	ec := EffectiveConfig{
		SourceCatalog: "config/model_providers.yaml",
		SourceProfile: "runtime/llm_active_profile.json",
	}

	if r.profile == nil {
		ec.ValidationError = "no active LLM profile configured"
		return ec
	}
	if r.catalog == nil {
		ec.ValidationError = "provider catalog not loaded"
		return ec
	}

	ec.Provider = r.profile.Provider
	ec.AllowMock = r.profile.AllowMockFallback
	ec.RequireReal = r.profile.RequireReal

	pi, hasProvider := r.catalog.ProviderSettings[ec.Provider]
	if !hasProvider {
		ec.ValidationError = fmt.Sprintf("provider %q not found in catalog", ec.Provider)
		return ec
	}

	ec.BaseURL = pi.BaseURL
	ec.Timeout = pi.Timeout
	if ec.Timeout <= 0 {
		ec.Timeout = 60
	}
	if r.profile.BaseURLOverride != "" {
		ec.BaseURL = r.profile.BaseURLOverride
	}

	// Model: user selection > catalog default
	ec.ChatModel = r.profile.ChatModel
	if ec.ChatModel == "" {
		ec.ChatModel = defaultModelForProvider(r.catalog, ec.Provider)
	}
	if ec.ChatModel == "" {
		ec.ValidationError = fmt.Sprintf("no chat model configured for provider %q", ec.Provider)
		return ec
	}

	// Validate model exists in catalog for this provider
	if mc, ok := r.catalog.ModelCatalog[ec.Provider]; ok {
		if _, valid := mc[ec.ChatModel]; !valid {
			// Allow if the model is from the tier list (not just model_catalog)
			found := false
			for _, tp := range r.catalog.ModelTiers.Small.Providers {
				if tp.Provider == ec.Provider && tp.Model == ec.ChatModel {
					found = true; break
				}
			}
			if !found {
				for _, tp := range r.catalog.ModelTiers.Medium.Providers {
					if tp.Provider == ec.Provider && tp.Model == ec.ChatModel {
						found = true; break
					}
				}
			}
			if !found {
				for _, tp := range r.catalog.ModelTiers.Large.Providers {
					if tp.Provider == ec.Provider && tp.Model == ec.ChatModel {
						found = true; break
					}
				}
			}
			if !found {
				ec.ValidationError = fmt.Sprintf("model %q is not in catalog for provider %q", ec.ChatModel, ec.Provider)
				return ec
			}
		}
	}

	// Cross-provider model validation: prevent gpt-4o-mini on MiniMax, etc.
	if err := validateModelProvider(ec.Provider, ec.ChatModel); err != nil {
		ec.ValidationError = err.Error()
		return ec
	}

	// API key
	apiKey := ""
	if r.profile.APIKeyEnv != "" {
		apiKey = os.Getenv(r.profile.APIKeyEnv)
	}
	if apiKey == "" {
		apiKey = os.Getenv("LLM_API_KEY")
	}
	ec.APIKeyPresent = apiKey != ""

	if ec.RequireReal && !ec.APIKeyPresent {
		ec.ValidationError = fmt.Sprintf("require_real=true but API key (env %q) is missing", r.profile.APIKeyEnv)
	}

	ec.OpenAICompat = true
	return ec
}

// APIKey returns the resolved API key.
func (r *ConfigResolver) APIKey() string {
	if r.profile == nil {
		return os.Getenv("LLM_API_KEY")
	}
	key := ""
	if r.profile.APIKeyEnv != "" {
		key = os.Getenv(r.profile.APIKeyEnv)
	}
	if key == "" {
		key = os.Getenv("LLM_API_KEY")
	}
	if key == "" {
		key = os.Getenv("OPENAI_API_KEY")
	}
	return key
}

func validateModelProvider(provider, model string) error {
	lowerModel := strings.ToLower(model)
	lowerProvider := strings.ToLower(provider)

	// OpenAI models (gpt-*) only valid with openai provider
	if strings.HasPrefix(lowerModel, "gpt-") && lowerProvider != "openai" {
		return fmt.Errorf("model %q is an OpenAI model but provider is %q; use provider=openai or select a %s-compatible model", model, provider, provider)
	}

	// Anthropic models (claude-*) only valid with anthropic
	if strings.HasPrefix(lowerModel, "claude-") && lowerProvider != "anthropic" {
		return fmt.Errorf("model %q is an Anthropic model but provider is %q", model, provider)
	}

	return nil
}

func defaultModelForProvider(catalog *ProviderCatalog, provider string) string {
	for _, tp := range catalog.ModelTiers.Medium.Providers {
		if tp.Provider == provider {
			return tp.Model
		}
	}
	for _, tp := range catalog.ModelTiers.Small.Providers {
		if tp.Provider == provider {
			return tp.Model
		}
	}
	return ""
}

// ─── Catalog helpers ──────────────────────────────────────────────────

func (c *ProviderCatalog) ProviderIDs() []string {
	seen := map[string]bool{}
	for _, tier := range []TierProviders{c.ModelTiers.Small, c.ModelTiers.Medium, c.ModelTiers.Large} {
		for _, tp := range tier.Providers {
			seen[tp.Provider] = true
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return ids
}

func (c *ProviderCatalog) ModelsForProvider(provider string) []ModelInfo {
	mc, ok := c.ModelCatalog[provider]
	if !ok {
		return nil
	}
	models := make([]ModelInfo, 0, len(mc))
	for name, info := range mc {
		if info.ModelID == "" {
			info.ModelID = name
		}
		models = append(models, info)
	}
	return models
}

func (c *ProviderCatalog) DefaultModel(provider string) string {
	return defaultModelForProvider(c, provider)
}

func DisplayName(id string) string {
	switch strings.ToLower(id) {
	case "openai": return "OpenAI"
	case "anthropic": return "Anthropic"
	case "google": return "Google"
	case "deepseek": return "DeepSeek"
	case "qwen": return "Qwen"
	case "minimax": return "MiniMax"
	case "xai": return "xAI"
	case "meta": return "Meta"
	case "zai": return "Z.AI"
	case "kimi": return "Kimi"
	case "ollama": return "Ollama"
	default: return id
	}
}

// ─── File I/O ─────────────────────────────────────────────────────────

func LoadCatalog(path string) (*ProviderCatalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read catalog: %w", err)
	}
	var c ProviderCatalog
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}
	return &c, nil
}

func LoadProfile(path string) (*LLMProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read profile: %w", err)
	}
	var p LLMProfile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse profile: %w", err)
	}
	return &p, nil
}

func SaveProfile(path string, p *LLMProfile) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal profile: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}
