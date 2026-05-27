package activities

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"cribug/internal/db"
	"cribug/internal/events"
	redisclient "cribug/internal/redis"
	"cribug/internal/types"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
)

// ── Sandbox Runner Interface ────────────────────────────────────

type SandboxRunner interface {
	Execute(ctx context.Context, input SandboxRunInput) (*SandboxRunOutput, error)
}

type SandboxRunInput struct {
	Code      string
	Language  string
	Stdin     string
	Policy    *types.SandboxPolicy
	RequestID string
}

type SandboxRunOutput struct {
	Stdout      string
	Stderr      string
	ExitCode    int
	DurationMs  int64
	CPUTimeMs   int64
	MemoryKB    int64
	NetworkBlocked bool
	StdoutTruncated bool
	StderrTruncated bool
}

// ── Mock Sandbox Runner ─────────────────────────────────────────

type MockSandboxRunner struct{}

func (m *MockSandboxRunner) Execute(ctx context.Context, input SandboxRunInput) (*SandboxRunOutput, error) {
	start := time.Now()

	var stdout, stderr string
	var exitCode int

	switch input.Language {
	case "wasi", "wasm":
		stdout = fmt.Sprintf("mock WASI execution: %s", truncateForMock(input.Code, 100))
		exitCode = 0
	case "python":
		stdout = executeMockPython(input.Code)
		exitCode = 0
		if strings.Contains(input.Code, "raise") || strings.Contains(input.Code, "exit(1)") {
			exitCode = 1
			stderr = "mock Python error"
		}
	case "javascript":
		stdout = fmt.Sprintf("mock JS execution: %s", truncateForMock(input.Code, 100))
		exitCode = 0
	default:
		stderr = fmt.Sprintf("unsupported language: %s", input.Language)
		exitCode = 1
	}

	// Apply stdout/stderr limits
	stdoutTrunc := false
	stderrTrunc := false
	maxStdout := types.DefaultSandboxStdoutMaxBytes
	maxStderr := types.DefaultSandboxStderrMaxBytes
	if input.Policy != nil {
		maxStdout = input.Policy.EffectiveMaxStdout()
		maxStderr = input.Policy.EffectiveMaxStderr()
	}
	if len(stdout) > maxStdout {
		stdout = stdout[:maxStdout]
		stdoutTrunc = true
	}
	if len(stderr) > maxStderr {
		stderr = stderr[:maxStderr]
		stderrTrunc = true
	}

	return &SandboxRunOutput{
		Stdout:          stdout,
		Stderr:          stderr,
		ExitCode:        exitCode,
		DurationMs:      time.Since(start).Milliseconds(),
		NetworkBlocked:  true,
		StdoutTruncated: stdoutTrunc,
		StderrTruncated: stderrTrunc,
	}, nil
}

func executeMockPython(code string) string {
	if strings.Contains(code, "print") {
		// Extract what's printed
		return "mock python output"
	}
	if strings.Contains(code, "def ") {
		return "mock python function defined"
	}
	return "mock python executed"
}

func truncateForMock(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen-3] + "..."
	}
	return s
}

func truncateStr(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}

// ── Real Sandbox Runner (calls Rust sandbox binary) ─────────────

type RealSandboxRunner struct {
	RunnerPath string
}

func NewRealSandboxRunner() *RealSandboxRunner {
	path := os.Getenv("SANDBOX_RUNNER_PATH")
	if path == "" {
		path = "./sandbox/runner/target/release/sandbox-runner"
	}
	return &RealSandboxRunner{RunnerPath: path}
}

func (r *RealSandboxRunner) Execute(ctx context.Context, input SandboxRunInput) (*SandboxRunOutput, error) {
	start := time.Now()

	// RealSandboxRunner only supports WASI wasm. Other languages must be rejected.
	lang := strings.ToLower(strings.TrimSpace(input.Language))
	if lang != "wasi" && lang != "wasm" {
		return &SandboxRunOutput{
			Stdout: "",
			Stderr: fmt.Sprintf("unsupported language '%s': RealSandboxRunner only supports 'wasi'/'wasm'. Use MockSandboxRunner for testing other languages.", input.Language),
			ExitCode:        -1,
			DurationMs:      time.Since(start).Milliseconds(),
			NetworkBlocked:  true,
		}, nil
	}

	timeout := types.DefaultSandboxWallTimeoutSec
	memoryMB := types.DefaultSandboxMemoryMB
	if input.Policy != nil {
		timeout = input.Policy.EffectiveWallTimeout()
		memoryMB = input.Policy.EffectiveMemoryMB()
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	wasmBytes := []byte(input.Code)
	if strings.HasSuffix(strings.TrimSpace(input.Code), ".wasm") {
		var readErr error
		wasmBytes, readErr = os.ReadFile(strings.TrimSpace(input.Code))
		if readErr != nil {
			return nil, fmt.Errorf("read wasm fixture: %w", readErr)
		}
	}

	cmd := exec.CommandContext(ctx, r.RunnerPath,
		"--memory-mb", fmt.Sprintf("%d", memoryMB),
		"--timeout-sec", fmt.Sprintf("%d", timeout),
	)
	cmd.Stdin = strings.NewReader(string(wasmBytes))

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	cmdRunErr := cmd.Run()
	duration := time.Since(start).Milliseconds()

	// Parse JSON output from Rust sandbox runner
	type runnerOutput struct {
		Success        bool   `json:"success"`
		ExitCode       int    `json:"exit_code"`
		Stdout         string `json:"stdout"`
		Stderr         string `json:"stderr"`
		DurationMs     int64  `json:"duration_ms"`
		Error          string `json:"error"`
		ErrorType      string `json:"error_type"`
		StdoutTrunc    bool   `json:"stdout_truncated"`
		StderrTrunc    bool   `json:"stderr_truncated"`
	}

	var runnerOut runnerOutput
	if err := json.Unmarshal(stdout.Bytes(), &runnerOut); err != nil {
		// Couldn't parse JSON — use raw output as stderr
		return &SandboxRunOutput{
			ExitCode:   -1,
			DurationMs: duration,
			Stderr:     fmt.Sprintf("failed to parse runner output: %v; raw: %s", err, truncateStr(stdout.String(), 500)),
			NetworkBlocked: true,
		}, nil
	}

	// Check for sandbox timeout or execution error
	if cmdRunErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return &SandboxRunOutput{
				ExitCode:   -1,
				DurationMs: duration,
				Stderr:     "sandbox timeout",
				NetworkBlocked: true,
			}, nil
		}
		// Runner exited non-zero or was killed
	}

	// If the Rust runner reported an error
	if !runnerOut.Success && runnerOut.ExitCode != 0 {
		return &SandboxRunOutput{
			Stdout:          runnerOut.Stdout,
			Stderr:          runnerOut.Stderr,
			ExitCode:        runnerOut.ExitCode,
			DurationMs:      duration,
			NetworkBlocked:  true,
			StdoutTruncated: runnerOut.StdoutTrunc,
			StderrTruncated: runnerOut.StderrTrunc,
		}, nil
	}

	// Apply Go-level limits
	stdoutStr := runnerOut.Stdout
	stderrStr := runnerOut.Stderr
	maxStdout := types.DefaultSandboxStdoutMaxBytes
	maxStderr := types.DefaultSandboxStderrMaxBytes
	if input.Policy != nil {
		maxStdout = input.Policy.EffectiveMaxStdout()
		maxStderr = input.Policy.EffectiveMaxStderr()
	}

	stdoutTrunc := runnerOut.StdoutTrunc
	stderrTrunc := runnerOut.StderrTrunc
	if len(stdoutStr) > maxStdout {
		stdoutStr = stdoutStr[:maxStdout]
		stdoutTrunc = true
	}
	if len(stderrStr) > maxStderr {
		stderrStr = stderrStr[:maxStderr]
		stderrTrunc = true
	}

	return &SandboxRunOutput{
		Stdout:          stdoutStr,
		Stderr:          stderrStr,
		ExitCode:        0,
		DurationMs:      duration,
		NetworkBlocked:  true,
		StdoutTruncated: stdoutTrunc,
		StderrTruncated: stderrTrunc,
	}, nil
}

// ── Sandbox Activities ──────────────────────────────────────────

type SandboxActivities struct {
	db     *db.Postgres
	redis  *redisclient.Client
	runner SandboxRunner
}

func NewSandboxActivities(database *db.Postgres, redisClient *redisclient.Client, runner SandboxRunner) *SandboxActivities {
	if runner == nil {
		runner = &MockSandboxRunner{}
	}
	return &SandboxActivities{db: database, redis: redisClient, runner: runner}
}

// ── Activity Input/Output Types ─────────────────────────────────

type ExecuteSandboxActivityInput struct {
	Code       string               `json:"code"`
	Language   string               `json:"language"`
	Stdin      string               `json:"stdin,omitempty"`
	Policy     *types.SandboxPolicy `json:"policy,omitempty"`
	RequestID  string               `json:"request_id"`
	AgentID    string               `json:"agent_id"`
	WorkflowID string               `json:"workflow_id"`
	RunID      string               `json:"run_id"`
	TaskID     string               `json:"task_id"`
}

type ExecuteSandboxActivityResult struct {
	Result *types.SandboxExecutionResult `json:"result"`
}

type AuditSandboxActivityInput struct {
	RequestID  string `json:"request_id"`
	AgentID    string `json:"agent_id"`
	WorkflowID string `json:"workflow_id"`
	Language   string `json:"language"`
	CodeLen    int    `json:"code_len"`
	Success    bool   `json:"success"`
	ExitCode   int    `json:"exit_code"`
	Error      string `json:"error,omitempty"`
	ErrorType  string `json:"error_type,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

type AuditSandboxActivityResult struct {
	AuditID string `json:"audit_id"`
}

// ── ExecuteSandboxActivity ──────────────────────────────────────

func (a *SandboxActivities) ExecuteSandbox(ctx context.Context, input ExecuteSandboxActivityInput) (*ExecuteSandboxActivityResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("ExecuteSandboxActivity", "language", input.Language, "request_id", input.RequestID)

	if strings.TrimSpace(input.Code) == "" {
		return &ExecuteSandboxActivityResult{
			Result: &types.SandboxExecutionResult{
				RequestID: input.RequestID, Success: false,
				Error: "code is required", ErrorType: types.SandboxErrorTypeInvalidInput,
			},
		}, nil
	}

	if input.Language == "" {
		input.Language = "wasi"
	}

	output, err := a.runner.Execute(ctx, SandboxRunInput{
		Code:      input.Code,
		Language:  input.Language,
		Stdin:     input.Stdin,
		Policy:    input.Policy,
		RequestID: input.RequestID,
	})

	if err != nil {
		return &ExecuteSandboxActivityResult{
			Result: &types.SandboxExecutionResult{
				RequestID: input.RequestID, Success: false,
				Error:     err.Error(),
				ErrorType: types.SandboxErrorTypeExecutionFailed,
			},
		}, nil
	}

	result := &types.SandboxExecutionResult{
		RequestID:       input.RequestID,
		Stdout:          output.Stdout,
		Stderr:          output.Stderr,
		ExitCode:        output.ExitCode,
		Success:         output.ExitCode == 0,
		DurationMs:      output.DurationMs,
		CPUTimeMs:       output.CPUTimeMs,
		MemoryKB:        output.MemoryKB,
		StdoutOverflow:  output.StdoutTruncated,
		StderrOverflow:  output.StderrTruncated,
		NetworkBlocked:  output.NetworkBlocked,
	}

	if output.ExitCode != 0 {
		result.Error = fmt.Sprintf("exit code %d: %s", output.ExitCode, output.Stderr)
		result.ErrorType = types.SandboxErrorTypeNonZeroExit
	}

	logger.Info("ExecuteSandboxActivity completed",
		"exit_code", output.ExitCode, "duration_ms", output.DurationMs)
	return &ExecuteSandboxActivityResult{Result: result}, nil
}

// ── AuditSandboxActivity ────────────────────────────────────────

func (a *SandboxActivities) AuditSandbox(ctx context.Context, input AuditSandboxActivityInput) (*AuditSandboxActivityResult, error) {
	logger := activity.GetLogger(ctx)

	audit := types.SandboxAuditLog{
		ID:         uuid.New().String(),
		RequestID:  input.RequestID,
		AgentID:    input.AgentID,
		WorkflowID: input.WorkflowID,
		Language:   input.Language,
		CodeLen:    input.CodeLen,
		Success:    input.Success,
		ExitCode:   input.ExitCode,
		Error:      input.Error,
		ErrorType:  input.ErrorType,
		DurationMs: input.DurationMs,
		CreatedAt:  time.Now().UTC(),
	}

	if err := a.db.CreateSandboxAuditLog(ctx, audit); err != nil {
		logger.Error("Failed to create sandbox audit log", "error", err)
		return nil, fmt.Errorf("create sandbox audit log: %w", err)
	}

	logger.Info("AuditSandboxActivity completed", "audit_id", audit.ID)
	return &AuditSandboxActivityResult{AuditID: audit.ID}, nil
}

// ── Workspace Append Activity ───────────────────────────────────

type SandboxWorkspaceAppendInput struct {
	TaskID   string `json:"task_id"`
	AgentID  string `json:"agent_id"`
	Role     string `json:"role"`
	Title    string `json:"title"`
	Content  string `json:"content"`
}

type SandboxWorkspaceAppendResult struct {
	ItemID string `json:"item_id"`
}

func (a *SandboxActivities) WorkspaceAppendForSandbox(ctx context.Context, input SandboxWorkspaceAppendInput) (*SandboxWorkspaceAppendResult, error) {
	logger := activity.GetLogger(ctx)
	itemID := uuid.New().String()

	evt := events.NewWorkspaceItemCreatedEvent(input.TaskID, "", itemID, input.AgentID, input.Role, "sandbox_output", 0)
	if a.redis != nil {
		a.redis.AppendTaskEvent(ctx, input.TaskID, evt)
	}

	logger.Info("Sandbox WorkspaceAppend completed", "item_id", itemID)
	return &SandboxWorkspaceAppendResult{ItemID: itemID}, nil
}

// ── Ensure Sandbox Schema ──────────────────────────────────────

func EnsureSandboxTables(dbConn *sql.DB) error {
	ddl := `CREATE TABLE IF NOT EXISTS sandbox_audit_logs (
		id UUID PRIMARY KEY,
		request_id VARCHAR(255) NOT NULL,
		agent_id VARCHAR(255) NOT NULL DEFAULT '',
		workflow_id VARCHAR(255) NOT NULL DEFAULT '',
		language VARCHAR(50) NOT NULL DEFAULT '',
		code_len INT DEFAULT 0,
		success BOOLEAN NOT NULL DEFAULT false,
		exit_code INT DEFAULT 0,
		error TEXT DEFAULT '',
		error_type VARCHAR(100) DEFAULT '',
		duration_ms BIGINT DEFAULT 0,
		created_at TIMESTAMP DEFAULT NOW()
	)`
	_, err := dbConn.ExecContext(context.Background(), ddl)
	if err != nil {
		return fmt.Errorf("create sandbox_audit_logs: %w", err)
	}

	idxDDL := `CREATE INDEX IF NOT EXISTS idx_sandbox_audit_workflow
		ON sandbox_audit_logs(workflow_id)`
	_, err = dbConn.ExecContext(context.Background(), idxDDL)
	return err
}
