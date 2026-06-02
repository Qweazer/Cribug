package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"

	cribugllm "cribug/internal/llm"

	"go.temporal.io/sdk/client"
)

const defaultProfilePath = "runtime/llm_active_profile.json"
const defaultCatalogPath = "config/model_providers.yaml"

// LLMConfigHandler handles LLM provider config API endpoints.
type LLMConfigHandler struct {
	temporal client.Client
}

func NewLLMConfigHandler(temporalClient client.Client) *LLMConfigHandler {
	return &LLMConfigHandler{temporal: temporalClient}
}

// ─── GET /api/v1/llm/providers ────────────────────────────────────────

type ProviderItem struct {
	ID               string               `json:"id"`
	DisplayName      string               `json:"display_name"`
	DefaultChatModel string               `json:"default_chat_model"`
	BaseURL          string               `json:"base_url"`
	OpenAICompatible bool                 `json:"openai_compatible"`
	Timeout          int                  `json:"timeout"`
	Models           []cribugllm.ModelInfo `json:"models"`
}

func (h *LLMConfigHandler) ListProviders(w http.ResponseWriter, r *http.Request) {
	catalog, err := cribugllm.LoadCatalog(defaultCatalogPath)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "failed to load provider catalog: "+err.Error(), "catalog_error")
		return
	}

	providers := make([]ProviderItem, 0)
	for _, id := range catalog.ProviderIDs() {
		pi, ok := catalog.ProviderSettings[id]
		if !ok {
			continue
		}
		providers = append(providers, ProviderItem{
			ID:               id,
			DisplayName:      cribugllm.DisplayName(id),
			DefaultChatModel: catalog.DefaultModel(id),
			BaseURL:          pi.BaseURL,
			OpenAICompatible: true,
			Timeout:          pi.Timeout,
			Models:           catalog.ModelsForProvider(id),
		})
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"providers": providers,
	})
}

// ─── PUT /api/v1/llm/config ──────────────────────────────────────────

type SaveLLMConfigRequest struct {
	Provider          string `json:"provider"`
	ChatModel         string `json:"chat_model"`
	EmbeddingModel    string `json:"embedding_model"`
	APIKeyEnv         string `json:"api_key_env"`
	BaseURLOverride   string `json:"base_url_override"`
	AllowMockFallback bool   `json:"allow_mock_fallback"`
	RequireReal       bool   `json:"require_real"`
}

func (h *LLMConfigHandler) SaveConfig(w http.ResponseWriter, r *http.Request) {
	var req SaveLLMConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "validation_error")
		return
	}
	if req.Provider == "" {
		WriteError(w, http.StatusBadRequest, "provider is required", "validation_error")
		return
	}

	catalog, err := cribugllm.LoadCatalog(defaultCatalogPath)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "failed to load catalog", "catalog_error")
		return
	}

	if _, ok := catalog.ProviderSettings[req.Provider]; !ok {
		WriteError(w, http.StatusBadRequest, "unknown provider: "+req.Provider, "validation_error")
		return
	}

	if req.ChatModel == "" {
		req.ChatModel = catalog.DefaultModel(req.Provider)
		if req.ChatModel == "" {
			WriteError(w, http.StatusBadRequest, "no default model for provider "+req.Provider, "validation_error")
			return
		}
	}

	if err := validateProviderModel(req.Provider, req.ChatModel, catalog); err != nil {
		WriteError(w, http.StatusBadRequest, err.Error(), "validation_error")
		return
	}

	profile := &cribugllm.LLMProfile{
		Provider:          req.Provider,
		ChatModel:         req.ChatModel,
		EmbeddingModel:    req.EmbeddingModel,
		BaseURLOverride:   req.BaseURLOverride,
		APIKeyEnv:         req.APIKeyEnv,
		AllowMockFallback: req.AllowMockFallback,
		RequireReal:       req.RequireReal,
	}

	if err := cribugllm.SaveProfile(defaultProfilePath, profile); err != nil {
		WriteError(w, http.StatusInternalServerError, "failed to save profile: "+err.Error(), "db_error")
		return
	}

	log.Printf("[INFO] LLM config saved: provider=%s model=%s require_real=%v", req.Provider, req.ChatModel, req.RequireReal)
	WriteJSON(w, http.StatusOK, map[string]string{"status": "saved", "path": defaultProfilePath})
}

// ─── GET /api/v1/llm/config/effective ─────────────────────────────────

func (h *LLMConfigHandler) GetEffectiveConfig(w http.ResponseWriter, r *http.Request) {
	resolver, err := cribugllm.NewResolver(defaultCatalogPath, defaultProfilePath)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "failed to create resolver: "+err.Error(), "config_error")
		return
	}
	ec := resolver.Resolve()
	WriteJSON(w, http.StatusOK, ec)
}

// ─── POST /api/v1/llm/config/test ─────────────────────────────────────

func (h *LLMConfigHandler) TestConfig(w http.ResponseWriter, r *http.Request) {
	resolver, err := cribugllm.NewResolver(defaultCatalogPath, defaultProfilePath)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "failed to create resolver: "+err.Error(), "config_error")
		return
	}

	ec := resolver.Resolve()
	if ec.ValidationError != "" {
		WriteJSON(w, http.StatusBadRequest, map[string]interface{}{
			"status": "config_invalid", "validation_error": ec.ValidationError, "config": ec,
		})
		return
	}
	if ec.RequireReal && !ec.APIKeyPresent {
		WriteJSON(w, http.StatusBadRequest, map[string]interface{}{
			"status": "api_key_missing", "error": "require_real=true but API key not configured",
		})
		return
	}

	apiKey := resolver.APIKey()
	if apiKey == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]interface{}{
			"status": "api_key_missing", "error": "no API key found",
		})
		return
	}

	llmURL := getEnvDefault("LLM_SERVICE_URL", "http://127.0.0.1:8000")
	reqBody, _ := json.Marshal(map[string]interface{}{
		"trace_id":             "llm-config-test",
		"task_id":              "llm-config-test",
		"provider":             "openai_compatible",
		"model":                ec.ChatModel,
		"messages":             []map[string]string{{"role": "user", "content": "Say hello in one word"}},
		"temperature":          0.5,
		"max_completion_tokens": 20,
	})

	resp, err := http.Post(llmURL+"/chat", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"status": "connection_error", "error": err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(body, &result)

	content, _ := result["content"].(string)
	isMock := containsStr(content, "mock") || containsStr(content, "Safe mock")

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"status":        "ok",
		"provider":      ec.Provider,
		"model":         ec.ChatModel,
		"mock":          isMock,
		"fallback_used": isMock,
		"answer":        content,
		"answer_len":    len(content),
	})
}

func validateProviderModel(provider, model string, catalog *cribugllm.ProviderCatalog) error {
	mc, ok := catalog.ModelCatalog[provider]
	if !ok {
		return nil // no model catalog, skip validation
	}
	if _, valid := mc[model]; !valid {
		// Also check tier lists
		for _, tp := range catalog.ModelTiers.Small.Providers {
			if tp.Provider == provider && tp.Model == model {
				return nil
			}
		}
		for _, tp := range catalog.ModelTiers.Medium.Providers {
			if tp.Provider == provider && tp.Model == model {
				return nil
			}
		}
		for _, tp := range catalog.ModelTiers.Large.Providers {
			if tp.Provider == provider && tp.Model == model {
				return nil
			}
		}
		return &modelError{provider, model}
	}
	return nil
}

type modelError struct{ provider, model string }
func (e *modelError) Error() string {
	return "model " + e.model + " is not a known model for provider " + e.provider
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && searchSubstring(s, sub)
}
func searchSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub { return true }
	}
	return false
}
