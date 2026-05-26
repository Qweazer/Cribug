package types

import (
	"testing"
)

func TestSanitizeEnvString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"api key", "API_KEY=sk-abc123", "API_KEY=***"},
		{"token", "AUTH_TOKEN=xyz", "AUTH_TOKEN=***"},
		{"secret", "DB_SECRET=mysecret", "DB_SECRET=***"},
		{"password", "DB_PASSWORD=hunter2", "DB_PASSWORD=***"},
		{"credential", "CREDENTIAL=abc", "CREDENTIAL=***"},
		{"non-sensitive debug", "LOG_LEVEL=debug", "LOG_LEVEL=debug"},
		{"non-sensitive url", "ENDPOINT_URL=https://example.com", "ENDPOINT_URL=https://example.com"},
		{"no equal sign", "plain_string", "plain_string"},
		{"empty", "", ""},
		{"case insensitive", "api_key_lower=secret", "api_key_lower=***"},
		{"passwd variant", "DB_PASSWD=secret", "DB_PASSWD=***"},
		{"auth variant", "AUTH_HEADER=Bearer xyz", "AUTH_HEADER=***"},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/"+tt.input, func(t *testing.T) {
			result := SanitizeEnvString(tt.input)
			if result != tt.expected {
				t.Errorf("SanitizeEnvString(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsSensitiveEnvKey(t *testing.T) {
	tests := []struct {
		key      string
		sensitive bool
	}{
		{"API_KEY", true},
		{"SECRET_TOKEN", true},
		{"DB_PASSWORD", true},
		{"AUTH_TOKEN", true},
		{"CREDENTIAL", true},
		{"api_key", true},
		{"db_secret", true},
		{"LOG_LEVEL", false},
		{"MODEL_NAME", false},
		{"TIMEOUT", false},
		{"ENDPOINT_URL", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			result := IsSensitiveEnvKey(tt.key)
			if result != tt.sensitive {
				t.Errorf("IsSensitiveEnvKey(%q) = %v, want %v", tt.key, result, tt.sensitive)
			}
		})
	}
}

func TestSanitizeEnv(t *testing.T) {
	srv := &MCPServer{
		EnvRaw: []string{
			"API_KEY=super-secret-123",
			"DB_PASSWORD=hunter2",
			"LOG_LEVEL=debug",
			"LOG_LEVEL=info",
		},
	}
	srv.SanitizeEnv()

	if len(srv.Env) != 4 {
		t.Fatalf("expected 4 env entries, got %d", len(srv.Env))
	}
	if srv.Env[0] != "API_KEY=***" {
		t.Errorf("Env[0] = %q, want 'API_KEY=***'", srv.Env[0])
	}
	if srv.Env[1] != "DB_PASSWORD=***" {
		t.Errorf("Env[1] = %q, want 'DB_PASSWORD=***'", srv.Env[1])
	}
	if srv.Env[2] != "LOG_LEVEL=debug" {
		t.Errorf("Env[2] = %q, want 'LOG_LEVEL=debug'", srv.Env[2])
	}
	if srv.Env[3] != "LOG_LEVEL=info" {
		t.Errorf("Env[3] = %q, want 'LOG_LEVEL=info'", srv.Env[3])
	}
}

func TestMCPToolCallInput_EffectiveTimeout(t *testing.T) {
	tests := []struct {
		timeout  int
		expected int
	}{
		{0, DefaultMCPTimeoutSec},
		{-1, DefaultMCPTimeoutSec},
		{30, 30},
		{10, 10},
		{300, 300},
		{301, DefaultMCPTimeoutSec},
		{999, DefaultMCPTimeoutSec},
	}

	for _, tt := range tests {
		in := &MCPToolCallInput{Timeout: tt.timeout}
		result := in.EffectiveTimeout()
		if result != tt.expected {
			t.Errorf("EffectiveTimeout(%d) = %d, want %d", tt.timeout, result, tt.expected)
		}
	}
}

func TestCleanAuditPayload(t *testing.T) {
	payload := []byte(`{"api_key": "secret123", "log_level": "debug", "nested": {"db_password": "hunter2"}}`)
	result := CleanAuditPayload(payload)
	resultStr := string(result)
	if containsPlaintext(resultStr, "secret123") {
		t.Error("audit payload contains plaintext API key")
	}
	if containsPlaintext(resultStr, "hunter2") {
		t.Error("audit payload contains plaintext nested password")
	}
	if !containsPlaintext(resultStr, "debug") {
		t.Error("audit payload should preserve non-sensitive values")
	}
}

func containsPlaintext(s, needle string) bool {
	for i := 0; i <= len(s)-len(needle); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestMCPError_Types(t *testing.T) {
	errTypes := []string{
		MCPErrorTypeServerNotFound, MCPErrorTypeServerUnavailable,
		MCPErrorTypeToolNotFound, MCPErrorTypePermissionDenied,
		MCPErrorTypeSchemaValidation, MCPErrorTypeTimeout,
		MCPErrorTypeResultOverflow, MCPErrorTypeInvalidInput,
		MCPErrorTypeRegistrationFailed, MCPErrorTypeDiscoveryFailed,
		MCPErrorTypeCallFailed, MCPErrorTypeAuditFailed,
	}
	for _, et := range errTypes {
		err := NewMCPError(et, "test message")
		if err.Type != et {
			t.Errorf("NewMCPError type = %q, want %q", err.Type, et)
		}
		if err.Message != "test message" {
			t.Errorf("NewMCPError message = %q, want %q", err.Message, "test message")
		}
		if err.Error() != "test message" {
			t.Errorf("NewMCPError.Error() = %q, want %q", err.Error(), "test message")
		}
	}
}

func TestMCPServer_Defaults(t *testing.T) {
	srv := MCPServer{Name: "test-server"}
	if srv.Status != "" {
		// Status should be set explicitly, not defaulted here
	}
	if srv.ID != "" {
		t.Error("ID should be empty before creation")
	}
}
