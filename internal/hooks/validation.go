package hooks

import (
	"fmt"
	"strings"
)

// ValidateHookRegistration checks a HookRegistration for correctness.
func ValidateHookRegistration(reg HookRegistration, allowedInternalHandlers []string, allowedHTTPHosts []string) error {
	if strings.TrimSpace(reg.Name) == "" {
		return fmt.Errorf("hook name is required")
	}

	if !reg.HookPoint.IsValid() {
		return fmt.Errorf("invalid hook_point: %s (valid: %v)", reg.HookPoint, AllHookPoints())
	}

	if reg.Blocking && !reg.HookPoint.AllowsBlocking() {
		return fmt.Errorf("blocking hooks are not allowed for hook_point: %s", reg.HookPoint)
	}

	if err := validateHandlerURL(reg.HandlerURL, allowedInternalHandlers, allowedHTTPHosts); err != nil {
		return err
	}

	if reg.Filter != nil {
		// Filter is already a map[string]interface{}, no type assertion needed.
		// The check here is implicit: if it's non-nil, the caller provided it.
		_ = reg.Filter
	}

	return nil
}

func validateHandlerURL(handlerURL string, allowedInternalHandlers []string, allowedHTTPHosts []string) error {
	if strings.TrimSpace(handlerURL) == "" {
		return fmt.Errorf("handler_url is required")
	}

	if strings.HasPrefix(handlerURL, "internal:") {
		handlerName := strings.TrimPrefix(handlerURL, "internal:")
		if handlerName == "" {
			return fmt.Errorf("internal handler name must not be empty")
		}
		if !isAllowedInternalHandler(handlerName, allowedInternalHandlers) {
			return fmt.Errorf("internal handler '%s' is not in the allowed list", handlerName)
		}
		return nil
	}

	if strings.HasPrefix(handlerURL, "http://") || strings.HasPrefix(handlerURL, "https://") {
		host := extractHost(handlerURL)
		if !isAllowedHTTPHost(host, allowedHTTPHosts) {
			return fmt.Errorf("HTTP host '%s' is not in the allowed list", host)
		}
		return nil
	}

	return fmt.Errorf("handler_url must be internal:<name> or http(s):// URL")
}

func isAllowedInternalHandler(name string, allowed []string) bool {
	for _, a := range allowed {
		if a == name {
			return true
		}
	}
	return false
}

func isAllowedHTTPHost(host string, allowed []string) bool {
	for _, a := range allowed {
		if strings.EqualFold(a, host) {
			return true
		}
	}
	return false
}

func extractHost(url string) string {
	s := url
	if idx := strings.Index(s, "://"); idx >= 0 {
		s = s[idx+3:]
	}
	if idx := strings.Index(s, "/"); idx >= 0 {
		s = s[:idx]
	}
	if idx := strings.Index(s, ":"); idx >= 0 {
		s = s[:idx]
	}
	return s
}

// IsInternalHandler returns true if the handler URL is an internal handler.
func IsInternalHandler(handlerURL string) bool {
	return strings.HasPrefix(handlerURL, "internal:")
}

// InternalHandlerName extracts the handler name from an internal handler URL.
func InternalHandlerName(handlerURL string) string {
	return strings.TrimPrefix(handlerURL, "internal:")
}

// IsHTTPHandler returns true if the handler URL is an HTTP handler.
func IsHTTPHandler(handlerURL string) bool {
	return strings.HasPrefix(handlerURL, "http://") || strings.HasPrefix(handlerURL, "https://")
}
