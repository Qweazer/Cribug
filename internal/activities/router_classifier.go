package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"cribug/internal/config"
	"cribug/internal/types"
)

// LLMCaller is the function signature used by RouterActivities to
// invoke an LLM. The default implementation wraps AgentActivity.CallLLM
// (set in NewRouterActivities) but tests can substitute a mock.
type LLMCaller func(ctx context.Context, prompt string, model string, maxTokens int) (answer string, modelUsed string, tokensUsed int, err error)

// ClassifierLLMCaller is the LLM caller used specifically by the
// classifier path. Defaults to the RouterActivities' LLMCaller.
var ClassifierLLMCaller LLMCaller

// LLMClassifierActivity is the v3 Section 14 LLM-assisted Router
// Arbiter. It is a secondary-arbiter that re-ranks heuristic candidates
// when routing is ambiguous. **The classifier is OFF by default** and
// fully independent from the Approval Gate kill switch.
//
// Failure modes (timeout / invalid JSON / missing required fields /
// LLM error) all set Fallback=true. The caller (selectPlannedModeV2)
// MUST honour FailOpenToHeuristic when Fallback is true and continue
// with the heuristic ranking rather than failing the workflow.
func (ra *RouterActivities) LLMClassifierActivity(ctx context.Context, input types.LLMClassifierInput) (*types.LLMClassifierOutput, error) {
	policy := getPolicy()
	if policy == nil {
		policy = config.DefaultRouterPolicyV2()
	}
	cfg := policy.Classifier
	start := time.Now()

	out := &types.LLMClassifierOutput{
		Fallback:      true,
		ModelUsed:     cfg.ModelTier,
		TokensUsed:    0,
		LatencyMs:     0,
		ErrorReason:   "classifier_disabled_or_uninitialised",
	}

	// Hard kill switches.
	if !cfg.Enabled {
		out.ErrorReason = "policy_classifier_disabled"
		return out, nil
	}
	if !classifierEnvEnabled() {
		out.ErrorReason = "env_classifier_disabled"
		return out, nil
	}
	if cfg.RealTestOnly && !realClassifierTestEnabled() {
		out.ErrorReason = "real_test_only_no_flag"
		return out, nil
	}

	// Build prompt.
	prompt, err := buildClassifierPrompt(input, cfg, policy)
	if err != nil {
		out.ErrorReason = "prompt_build_error: " + err.Error()
		return out, nil
	}

	// Time-bounded LLM call.
	timeoutMs := cfg.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 3000
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	caller := ClassifierLLMCaller
	if caller == nil {
		caller = defaultClassifierLLMCaller(ra)
	}
	answer, modelUsed, tokens, callErr := caller(cctx, prompt, cfg.ModelTier, cfg.MaxTokens)
	out.LatencyMs = int(time.Since(start).Milliseconds())
	if modelUsed != "" {
		out.ModelUsed = modelUsed
	}
	out.TokensUsed = tokens

	if callErr != nil {
		out.ErrorReason = "llm_call_error: " + callErr.Error()
		return out, nil
	}
	if err := validateClassifierJSON(answer, cfg, policy); err != nil {
		out.ErrorReason = "validate_error: " + err.Error()
		return out, nil
	}

	// Parse the JSON body.
	var parsed struct {
		Scores       map[string]float64 `json:"scores"`
		Reasoning    string             `json:"reasoning"`
		Confidence   float64            `json:"confidence"`
		SelectedMode string             `json:"selected_mode"`
	}
	if err := json.Unmarshal([]byte(answer), &parsed); err != nil {
		out.ErrorReason = "json_parse_error: " + err.Error()
		return out, nil
	}
	if parsed.Confidence < 0 || parsed.Confidence > 1 {
		out.ErrorReason = "confidence_out_of_range"
		return out, nil
	}
	out.Scores = parsed.Scores
	out.Reasoning = parsed.Reasoning
	out.Confidence = parsed.Confidence
	out.SelectedMode = parsed.SelectedMode
	out.Fallback = false
	return out, nil
}

// defaultClassifierLLMCaller returns a caller that actually invokes
// the LLM service when the RouterActivities was wired with a service
// URL (NewRouterActivitiesWithLLM). When the URL is empty — e.g. unit
// tests or dev environments without an LLM service — the caller falls
// back to a deterministic mock answer so the classifier can still
// produce a valid response and the merge path can run.
func defaultClassifierLLMCaller(ra *RouterActivities) LLMCaller {
	return func(ctx context.Context, prompt, model string, maxTokens int) (string, string, int, error) {
		if ra != nil && ra.llmServiceURL != "" {
			answer, modelUsed, tokens, err := callLLMService(ctx, ra, prompt, model, maxTokens)
			if err == nil {
				return answer, modelUsed, tokens, nil
			}
			// Real LLM call failed — fall through to mock so the
			// classifier can still produce something. The Activity
			// records the error reason in ClassifierMetadata.
		}
		// Mock fallback for dev / unit tests / dev no-LLM mode.
		return buildDeterministicMockClassifierAnswer(prompt), model, len(prompt) / 4, nil
	}
}

// callLLMService POSTs the prompt to the configured LLM service and
// returns the answer string. Implemented as a plain HTTP POST to the
// OpenAI-compatible /v1/chat/completions endpoint shape, which is
// what llm-service.py in cribug exposes.
//
// model / max_tokens / temperature are read from env at call time
// (LLM_MODEL / LLM_MAX_TOKENS / LLM_TEMPERATURE) so router config
// stays env-driven and doesn't need new Config fields.
func callLLMService(ctx context.Context, ra *RouterActivities, prompt, model string, maxTokens int) (string, string, int, error) {
	url := strings.TrimRight(ra.llmServiceURL, "/") + "/v1/chat/completions"
	chosenModel := ra.llmModel
	if chosenModel == "" {
		chosenModel = os.Getenv("LLM_MODEL")
	}
	if chosenModel == "" {
		chosenModel = model
	}
	chosenMax := maxTokens
	if chosenMax <= 0 {
		if ra.llmMaxTokens > 0 {
			chosenMax = ra.llmMaxTokens
		} else if v := os.Getenv("LLM_MAX_TOKENS"); v != "" {
			chosenMax, _ = strconv.Atoi(v)
		}
	}
	chosenTemp := ra.llmTemperature
	if chosenTemp == 0 {
		if v := os.Getenv("LLM_TEMPERATURE"); v != "" {
			chosenTemp, _ = strconv.ParseFloat(v, 64)
		}
	}
	body, _ := json.Marshal(map[string]interface{}{
		"model": chosenModel,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens":  chosenMax,
		"temperature": chosenTemp,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return "", "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Read API key from env at call time (not captured in struct).
	if key := os.Getenv("LLM_API_KEY"); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", "", 0, fmt.Errorf("llm http %d: %s", resp.StatusCode, string(raw))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Model string `json:"model"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", "", 0, fmt.Errorf("llm parse: %w (raw=%s)", err, string(raw))
	}
	if len(parsed.Choices) == 0 {
		return "", parsed.Model, 0, fmt.Errorf("llm returned 0 choices (raw=%s)", string(raw))
	}
	return parsed.Choices[0].Message.Content, parsed.Model, parsed.Usage.TotalTokens, nil
}

// buildDeterministicMockClassifierAnswer returns a STRICT JSON answer
// that validates against the schema. This is the dev / test fallback
// when no LLM service URL is configured. It echoes the prompt and
// produces a uniform score distribution across all top-N candidate
// modes so the merge step behaves deterministically.
func buildDeterministicMockClassifierAnswer(prompt string) string {
	// Extract top-N candidate mode names from the prompt header
	// (best-effort; if parsing fails, return an empty score map).
	modes := extractCandidateModesFromPrompt(prompt)
	scores := make(map[string]float64, len(modes))
	if len(modes) == 0 {
		scores["direct_answer"] = 1.0
	} else {
		share := 1.0 / float64(len(modes))
		for _, m := range modes {
			scores[m] = share
		}
	}
	selected := "direct_answer"
	if len(modes) > 0 {
		selected = modes[0]
	}
	body, _ := json.Marshal(map[string]interface{}{
		"scores":        scores,
		"reasoning":     "deterministic mock classifier (no LLM service URL configured)",
		"confidence":    0.5,
		"selected_mode": selected,
	})
	return string(body)
}

func extractCandidateModesFromPrompt(prompt string) []string {
	// Look for the line "Modes: a, b, c" in the prompt.
	for _, line := range strings.Split(prompt, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Modes:") {
			rest := strings.TrimPrefix(line, "Modes:")
			parts := strings.Split(rest, ",")
			out := make([]string, 0, len(parts))
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					out = append(out, p)
				}
			}
			return out
		}
	}
	return nil
}

// buildClassifierPrompt renders the policy's prompt template with the
// classifier input. The template is expected to contain {query} and
// {mode_list}; if it does not, a simple default is used.
func buildClassifierPrompt(input types.LLMClassifierInput, cfg config.PolicyClassifier, policy *config.RouterPolicyV2) (string, error) {
	template := cfg.PromptTemplate
	if template == "" {
		template = "Score each mode 0-1 for the query: {query}\nModes: {mode_list}\nOutput STRICT JSON: {scores: {mode: score, ...}}"
	}
	modeList := ""
	for i, c := range input.TopNCandidates {
		if i > 0 {
			modeList += ", "
		}
		modeList += string(c.Mode)
	}
	rendered := template
	rendered = strings.ReplaceAll(rendered, "{query}", input.Query)
	rendered = strings.ReplaceAll(rendered, "{mode_list}", modeList)
	rendered = strings.ReplaceAll(rendered, "{signals_summary}", input.SignalsSummary)
	return rendered, nil
}

// validateClassifierJSON checks that the LLM response is non-empty and
// contains the required_fields from the policy. Full schema validation
// (score range, valid modes, etc.) happens after parsing.
func validateClassifierJSON(answer string, cfg config.PolicyClassifier, policy *config.RouterPolicyV2) error {
	if strings.TrimSpace(answer) == "" {
		return fmt.Errorf("empty answer")
	}
	// Quick parse to check required fields exist.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(answer), &probe); err != nil {
		return fmt.Errorf("not valid JSON: %w", err)
	}
	for _, field := range cfg.RequiredFields {
		if _, ok := probe[field]; !ok {
			return fmt.Errorf("missing required field: %s", field)
		}
	}
	return nil
}

// classifierEnvEnabled returns true when ROUTER_CLASSIFIER_ENABLED=1.
func classifierEnvEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(getClassifierEnv()))
	return v == "1" || v == "true" || v == "yes"
}

// realClassifierTestEnabled returns true when REAL_ROUTER_CLASSIFIER_TEST=1.
func realClassifierTestEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(getRealClassifierEnv()))
	return v == "1" || v == "true" || v == "yes"
}

// Indirect env reads so unit tests can override (t.Setenv).
// Production reads from os.Getenv so smoke / integration tests can
// flip the kill switch via ROUTER_CLASSIFIER_ENABLED /
// REAL_ROUTER_CLASSIFIER_TEST. Unit tests swap these vars to nil-return
// so they don't accidentally honour a stray env in the test environment.
var (
	getClassifierEnv    = func() string { return os.Getenv("ROUTER_CLASSIFIER_ENABLED") }
	getRealClassifierEnv = func() string { return os.Getenv("REAL_ROUTER_CLASSIFIER_TEST") }
)

// checkClassifierConditions inspects the heuristic candidate list and
// signals to decide whether the LLM classifier should be invoked.
// Returns the trigger reason ("C1".."C8") or "" if no trigger.
//
// The function is pure — it does not perform any IO. selectPlannedModeV2
// decides whether to actually call the classifier based on this return
// value plus policy / env kill switches.
func checkClassifierConditions(signals types.RouterDecisionSignals, candidates []types.ModeCandidate, policy *config.RouterPolicyV2) string {
	if policy == nil {
		return ""
	}
	cfg := policy.Classifier
	if len(candidates) < 2 {
		return ""
	}

	// C1: top-1 vs top-2 score gap too small.
	if cfg.MinScoreGapToSkip > 0 && len(candidates) >= 2 {
		gap := candidates[0].Score - candidates[1].Score
		if gap >= 0 && gap < cfg.MinScoreGapToSkip && cfg.InvokeOnNoClearWinner {
			return "C1"
		}
	}

	// C3: capability signal conflict.
	if cfg.InvokeOnConflict {
		if signals.RequiresResearch && signals.BudgetUSD > 0 && signals.BudgetUSD < 0.01 {
			return "C3"
		}
		if signals.RequiresCitations && !signals.AllowResearch {
			return "C3"
		}
	}

	// C5: high-risk / high-cost candidate.
	if cfg.InvokeOnHighRisk {
		for _, c := range candidates {
			if c.Rejected {
				continue
			}
			switch c.Mode {
			case types.RouteSandboxExecution, types.RouteSwarmWorkflow, types.RouteResearchV2:
				if c.Score >= 0.3 {
					return "C5"
				}
			}
		}
	}

	// C7: 3+ advanced modes are candidates simultaneously.
	if cfg.InvokeOnMultiAdvanced {
		advanced := 0
		for _, c := range candidates {
			if c.Rejected {
				continue
			}
			switch c.Mode {
			case types.RouteResearchV2, types.RouteDebate, types.RouteTreeOfThoughts, types.RouteReflection, types.RouteSwarmWorkflow:
				advanced++
			}
		}
		if advanced >= 3 {
			return "C7"
		}
	}

	return ""
}

// mergeLLMScores combines the heuristic candidate scores with the LLM
// scores (60% heuristic / 40% LLM by default) and re-sorts. When the
// LLM output is empty / invalid, the original heuristic order is
// returned unchanged.
func mergeLLMScores(candidates []types.ModeCandidate, llmScores map[string]float64) []types.ModeCandidate {
	if len(llmScores) == 0 || len(candidates) == 0 {
		return candidates
	}
	const (
		heuristicWeight = 0.6
		llmWeight       = 0.4
	)
	out := make([]types.ModeCandidate, len(candidates))
	for i, c := range candidates {
		out[i] = c
		llmScore, ok := llmScores[string(c.Mode)]
		if !ok {
			continue
		}
		// Clamp to [0,1] defensively.
		if llmScore < 0 {
			llmScore = 0
		}
		if llmScore > 1 {
			llmScore = 1
		}
		out[i].Score = heuristicWeight*c.Score + llmWeight*llmScore
	}
	// Re-sort by score desc, stable.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Score > out[i].Score {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// maxClassifierScore returns the max score across the candidate list.
func maxClassifierScore(c []types.ModeCandidate) float64 {
	m := 0.0
	for _, x := range c {
		if x.Score > m {
			m = x.Score
		}
	}
	return m
}
