package hooks

import "fmt"

// HookFilter matches a HookEvent against a HookRegistration's filter.
// Supported filter keys (all optional, AND semantics):
//
//	tool_types:   []interface{} of strings — "mcp", "sandbox", "skill"
//	tool_name:    string — exact match
//	tool_id:      string — exact match
//	agent_id:     string — exact match
//	skill_id:     string — exact match
//	error_type:   string — exact match (for on_error hooks)
//	tenant_id:    string — exact match
//
// An empty filter matches everything.
type HookFilter struct{}

func NewHookFilter() *HookFilter {
	return &HookFilter{}
}

// Match returns true if the event matches the registration's filter.
func (f *HookFilter) Match(event HookEvent, registration HookRegistration) bool {
	if registration.Filter == nil || len(registration.Filter) == 0 {
		return true
	}

	for key, expected := range registration.Filter {
		payloadVal := getPayloadValue(event.Payload, key)
		if !matchValue(key, expected, payloadVal, event) {
			return false
		}
	}
	return true
}

func getPayloadValue(payload map[string]interface{}, key string) interface{} {
	if payload == nil {
		return nil
	}
	return payload[key]
}

// knownFilterKeys are the keys used for event payload matching.
// Any key not in this set is handler configuration (e.g., allowed_tools)
// and is skipped during filter matching.
var knownFilterKeys = map[string]bool{
	"tool_type":  true,
	"tool_types": true,
	"tool_name":  true,
	"tool_id":    true,
	"agent_id":   true,
	"skill_id":   true,
	"error_type": true,
	"tenant_id":  true,
}

func matchValue(key string, expected interface{}, actual interface{}, event HookEvent) bool {
	if !knownFilterKeys[key] {
		return true // handler config key, not a filter criterion
	}
	switch key {
	case "tool_types":
		return matchStringSlice(expected, actual)
	case "tool_type", "tool_name", "tool_id", "agent_id", "skill_id", "error_type", "tenant_id":
		return matchString(expected, actual)
	default:
		return matchAnyValue(expected, actual)
	}
}

func matchString(expected interface{}, actual interface{}) bool {
	expectedStr, ok := expected.(string)
	if !ok {
		return false
	}
	actualStr, _ := actual.(string)
	return expectedStr == actualStr
}

func matchStringSlice(expected interface{}, actual interface{}) bool {
	expectedSlice, ok := expected.([]interface{})
	if !ok {
		return false
	}
	actualStr, ok := actual.(string)
	if !ok {
		return false
	}
	for _, item := range expectedSlice {
		if itemStr, ok := item.(string); ok && itemStr == actualStr {
			return true
		}
	}
	return false
}

func matchAnyValue(expected interface{}, actual interface{}) bool {
	return fmt.Sprintf("%v", expected) == fmt.Sprintf("%v", actual)
}
