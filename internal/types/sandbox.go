package types

import "time"

// ── Sandbox Error Types ─────────────────────────────────────────

const (
	SandboxErrorTypePermissionDenied  = "sandbox_permission_denied"
	SandboxErrorTypeTimeout           = "sandbox_timeout"
	SandboxErrorTypeStdoutLimit       = "sandbox_stdout_limit"
	SandboxErrorTypeStderrLimit       = "sandbox_stderr_limit"
	SandboxErrorTypeResultSizeLimit   = "sandbox_result_size_limit"
	SandboxErrorTypeNonZeroExit       = "sandbox_nonzero_exit"
	SandboxErrorTypeExecutionFailed   = "sandbox_execution_failed"
	SandboxErrorTypeInvalidInput      = "sandbox_invalid_input"
	SandboxErrorTypeRunnerUnavailable = "sandbox_runner_unavailable"
	SandboxErrorTypeNetworkBlocked    = "sandbox_network_blocked"
	SandboxErrorTypeMemoryExceeded    = "sandbox_memory_exceeded"
)

// ── Default Limits ──────────────────────────────────────────────

const (
	DefaultSandboxCPUTimeoutSec    = 10
	DefaultSandboxWallTimeoutSec   = 30
	DefaultSandboxMemoryMB         = 128
	DefaultSandboxStdoutMaxBytes   = 64 * 1024  // 64KB
	DefaultSandboxStderrMaxBytes   = 64 * 1024  // 64KB
	DefaultSandboxResultMaxBytes   = 256 * 1024 // 256KB
)

// ── Sandbox Policy ──────────────────────────────────────────────

type SandboxPolicy struct {
	AllowNetwork   bool `json:"allow_network"`    // default: false
	AllowFilesystem bool `json:"allow_filesystem"` // default: false (only temp workspace)
	CPUTimeoutSec  int  `json:"cpu_timeout_sec"`  // default: 10
	WallTimeoutSec int  `json:"wall_timeout_sec"` // default: 30
	MemoryMB       int  `json:"memory_mb"`        // default: 128
	MaxStdoutBytes int  `json:"max_stdout_bytes"` // default: 64KB
	MaxStderrBytes int  `json:"max_stderr_bytes"` // default: 64KB
	MaxResultBytes  int  `json:"max_result_bytes"` // default: 256KB
}

func (p *SandboxPolicy) EffectiveCPUTimeout() int {
	if p == nil || p.CPUTimeoutSec <= 0 || p.CPUTimeoutSec > 60 {
		return DefaultSandboxCPUTimeoutSec
	}
	return p.CPUTimeoutSec
}

func (p *SandboxPolicy) EffectiveWallTimeout() int {
	if p == nil || p.WallTimeoutSec <= 0 || p.WallTimeoutSec > 120 {
		return DefaultSandboxWallTimeoutSec
	}
	return p.WallTimeoutSec
}

func (p *SandboxPolicy) EffectiveMemoryMB() int {
	if p == nil || p.MemoryMB <= 0 || p.MemoryMB > 1024 {
		return DefaultSandboxMemoryMB
	}
	return p.MemoryMB
}

func (p *SandboxPolicy) EffectiveMaxStdout() int {
	if p == nil || p.MaxStdoutBytes <= 0 {
		return DefaultSandboxStdoutMaxBytes
	}
	return p.MaxStdoutBytes
}

func (p *SandboxPolicy) EffectiveMaxStderr() int {
	if p == nil || p.MaxStderrBytes <= 0 {
		return DefaultSandboxStderrMaxBytes
	}
	return p.MaxStderrBytes
}

// ── Sandbox Execution ──────────────────────────────────────────

type SandboxExecutionInput struct {
	Code       string         `json:"code"`
	Language   string         `json:"language"`   // "wasm" | "wasi" | "python" | "javascript"
	Stdin      string         `json:"stdin,omitempty"`
	Policy     *SandboxPolicy `json:"policy,omitempty"`
	RequestID  string         `json:"request_id"`
	AgentID    string         `json:"agent_id"`
	WorkflowID string         `json:"workflow_id"`
	RunID      string         `json:"run_id"`
	TaskID     string         `json:"task_id"`
}

type SandboxExecutionResult struct {
	RequestID   string `json:"request_id"`
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
	ExitCode    int    `json:"exit_code"`
	Success     bool   `json:"success"`
	Error       string `json:"error,omitempty"`
	ErrorType   string `json:"error_type,omitempty"`
	DurationMs  int64  `json:"duration_ms"`
	CPUTimeMs   int64  `json:"cpu_time_ms,omitempty"`
	MemoryKB    int64  `json:"memory_kb,omitempty"`
	StdoutOverflow bool `json:"stdout_overflow"`
	StderrOverflow bool `json:"stderr_overflow"`
	NetworkBlocked bool `json:"network_blocked,omitempty"`
}

// ── Sandbox Audit ───────────────────────────────────────────────

type SandboxAuditLog struct {
	ID         string    `json:"id"`
	RequestID  string    `json:"request_id"`
	AgentID    string    `json:"agent_id"`
	WorkflowID string    `json:"workflow_id"`
	Language   string    `json:"language"`
	CodeLen    int       `json:"code_len"`
	Success    bool      `json:"success"`
	ExitCode   int       `json:"exit_code"`
	Error      string    `json:"error,omitempty"`
	ErrorType  string    `json:"error_type,omitempty"`
	DurationMs int64     `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}
