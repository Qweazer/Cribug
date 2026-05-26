package activities

import (
	"context"
	"database/sql"
	"testing"

	"cribug/internal/db"
	"cribug/internal/types"

	_ "github.com/jackc/pgx/v5/stdlib"
	"go.temporal.io/sdk/testsuite"
)

func TestMockSandboxRunner_Basic(t *testing.T) {
	runner := &MockSandboxRunner{}

	tests := []struct {
		lang     string
		code     string
		exitCode int
	}{
		{"wasi", "(module)", 0},
		{"python", "print('hello')", 0},
		{"python", "raise Exception('fail')", 1},
		{"javascript", "console.log('hi')", 0},
	}

	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			out, err := runner.Execute(context.Background(), SandboxRunInput{
				Code:     tt.code,
				Language: tt.lang,
			})
			if err != nil {
				t.Fatalf("execute failed: %v", err)
			}
			if out.ExitCode != tt.exitCode {
				t.Errorf("exit code = %d, want %d", out.ExitCode, tt.exitCode)
			}
		})
	}
}

func TestMockSandboxRunner_StdoutLimit(t *testing.T) {
	runner := &MockSandboxRunner{}
	policy := &types.SandboxPolicy{MaxStdoutBytes: 10}

	out, err := runner.Execute(context.Background(), SandboxRunInput{
		Code:     "this is a very long output that exceeds the limit",
		Language: "wasi",
		Policy:   policy,
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !out.StdoutTruncated {
		t.Error("expected stdout truncation")
	}
	if len(out.Stdout) > 10 {
		t.Errorf("stdout len = %d, want <= 10", len(out.Stdout))
	}
}

func TestMockSandboxRunner_UnsupportedLanguage(t *testing.T) {
	runner := &MockSandboxRunner{}
	out, err := runner.Execute(context.Background(), SandboxRunInput{
		Code: "code", Language: "ruby",
	})
	if err != nil {
		t.Fatalf("execute should not error: %v", err)
	}
	if out.ExitCode != 1 {
		t.Errorf("unsupported language should return exit 1")
	}
}

// ── Activity Tests (require DB) ─────────────────────────────────

func getSandboxTestDB(t *testing.T) *db.Postgres {
	t.Helper()
	databaseURL := "postgres://admin:admin@localhost:5432/orchestrator?sslmode=disable"
	pg, err := db.New(databaseURL)
	if err != nil {
		t.Skipf("skipping test: cannot connect to DB: %v", err)
	}
	if err := pg.Ping(context.Background()); err != nil {
		pg.Close()
		t.Skipf("skipping test: DB not reachable: %v", err)
	}
	EnsureSandboxTables(pg.Stdlib())
	return pg
}

func cleanupSandboxData(t *testing.T, pg *db.Postgres) {
	t.Helper()
	pg.Stdlib().ExecContext(context.Background(), "DELETE FROM sandbox_audit_logs")
}

func TestExecuteSandboxActivity(t *testing.T) {
	pg := getSandboxTestDB(t)
	defer cleanupSandboxData(t, pg)

	sandbox := NewSandboxActivities(pg, nil, &MockSandboxRunner{})
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(sandbox.ExecuteSandbox)
	env.RegisterActivity(sandbox.AuditSandbox)
	env.RegisterActivity(sandbox.WorkspaceAppendForSandbox)

	t.Run("successful wasi execution", func(t *testing.T) {
		val, err := env.ExecuteActivity(sandbox.ExecuteSandbox, ExecuteSandboxActivityInput{
			Code: "(module)", Language: "wasi", RequestID: "req-1",
			AgentID: "agent-1", WorkflowID: "wf-1",
		})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		var result ExecuteSandboxActivityResult
		val.Get(&result)
		if !result.Result.Success {
			t.Errorf("expected success, got: %s", result.Result.Error)
		}
	})

	t.Run("empty code", func(t *testing.T) {
		val, err := env.ExecuteActivity(sandbox.ExecuteSandbox, ExecuteSandboxActivityInput{
			Code: "", Language: "wasi", RequestID: "req-2",
		})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		var result ExecuteSandboxActivityResult
		val.Get(&result)
		if result.Result.Success {
			t.Error("expected failure for empty code")
		}
	})

	t.Run("non-zero exit code", func(t *testing.T) {
		val, err := env.ExecuteActivity(sandbox.ExecuteSandbox, ExecuteSandboxActivityInput{
			Code: "raise Exception('fail')", Language: "python", RequestID: "req-3",
		})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		var result ExecuteSandboxActivityResult
		val.Get(&result)
		if result.Result.Success {
			t.Error("expected failure for non-zero exit")
		}
		if result.Result.ExitCode != 1 {
			t.Errorf("exit code = %d, want 1", result.Result.ExitCode)
		}
	})
}

func TestAuditSandboxActivity(t *testing.T) {
	pg := getSandboxTestDB(t)
	defer cleanupSandboxData(t, pg)

	sandbox := NewSandboxActivities(pg, nil, nil)
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(sandbox.AuditSandbox)

	t.Run("success audit", func(t *testing.T) {
		val, err := env.ExecuteActivity(sandbox.AuditSandbox, AuditSandboxActivityInput{
			RequestID: "req-1", AgentID: "a1", WorkflowID: "wf-1",
			Language: "wasi", CodeLen: 100, Success: true, ExitCode: 0,
			DurationMs: 50,
		})
		if err != nil {
			t.Fatalf("audit: %v", err)
		}
		var result AuditSandboxActivityResult
		val.Get(&result)
		if result.AuditID == "" {
			t.Error("expected audit ID")
		}
	})

	t.Run("failure audit", func(t *testing.T) {
		val, err := env.ExecuteActivity(sandbox.AuditSandbox, AuditSandboxActivityInput{
			RequestID: "req-2", AgentID: "a1", WorkflowID: "wf-1",
			Language: "python", CodeLen: 50, Success: false, ExitCode: 1,
			Error: "timeout", ErrorType: types.SandboxErrorTypeTimeout, DurationMs: 30000,
		})
		if err != nil {
			t.Fatalf("failure audit: %v", err)
		}
		var result AuditSandboxActivityResult
		val.Get(&result)
		if result.AuditID == "" {
			t.Error("expected audit ID for failure")
		}
	})
}

func TestNewRealSandboxRunner(t *testing.T) {
	runner := NewRealSandboxRunner()
	if runner == nil {
		t.Fatal("NewRealSandboxRunner returned nil")
	}
	if runner.RunnerPath == "" {
		t.Error("runner path should not be empty")
	}
}

func init() {
	if db, err := sql.Open("pgx", "postgres://admin:admin@localhost:5432/orchestrator?sslmode=disable"); err == nil {
		EnsureSandboxTables(db)
		db.Close()
	}
}
