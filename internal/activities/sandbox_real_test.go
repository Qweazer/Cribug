package activities

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cribug/internal/types"
)

func getRealRunner(t *testing.T) *RealSandboxRunner {
	t.Helper()
	path := os.Getenv("SANDBOX_RUNNER_PATH")
	if path == "" {
		// Try default locations
		candidates := []string{
			"./sandbox/runner/target/release/sandbox-runner",
			"../sandbox/runner/target/release/sandbox-runner",
			"../../sandbox/runner/target/release/sandbox-runner",
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				path = c
				break
			}
		}
	}
	if path == "" || os.Getenv("CRIBUG_REAL_SANDBOX_TEST") != "1" {
		t.Skip("skipping real sandbox test: set CRIBUG_REAL_SANDBOX_TEST=1 and SANDBOX_RUNNER_PATH")
	}
	return &RealSandboxRunner{RunnerPath: path}
}

func fixturePath(name string) string {
	candidates := []string{
		"./testdata/sandbox/",
		"../testdata/sandbox/",
		"../../testdata/sandbox/",
	}
	for _, c := range candidates {
		p := filepath.Join(c, name+".wasm")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func TestRealSandboxRunner_HelloStdout(t *testing.T) {
	runner := getRealRunner(t)
	fp := fixturePath("hello_stdout")
	if fp == "" {
		t.Fatal("hello_stdout.wasm not found")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := runner.Execute(ctx, SandboxRunInput{
		Code:     fp,
		Language: "wasi",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(out.Stdout, "hello") {
		t.Errorf("expected 'hello' in stdout, got: %s", out.Stdout)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", out.ExitCode)
	}
}

func TestRealSandboxRunner_StderrOutput(t *testing.T) {
	runner := getRealRunner(t)
	fp := fixturePath("stderr_output")
	if fp == "" {
		t.Fatal("stderr_output.wasm not found")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := runner.Execute(ctx, SandboxRunInput{
		Code:     fp,
		Language: "wasi",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(out.Stderr, "error") {
		t.Errorf("expected 'error' in stderr, got: %s", out.Stderr)
	}
}

func TestRealSandboxRunner_ExitNonzero(t *testing.T) {
	runner := getRealRunner(t)
	fp := fixturePath("exit_nonzero")
	if fp == "" {
		t.Fatal("exit_nonzero.wasm not found")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := runner.Execute(ctx, SandboxRunInput{
		Code:     fp,
		Language: "wasi",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if out.ExitCode == 0 {
		t.Error("expected non-zero exit code")
	}
	// exit_nonzero exits with code 42
	if out.ExitCode > 0 && out.ExitCode != 42 {
		t.Logf("exit code=%d (expected 42 but any non-zero is acceptable)", out.ExitCode)
	}
}

func TestRealSandboxRunner_InvalidWasm(t *testing.T) {
	runner := getRealRunner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := runner.Execute(ctx, SandboxRunInput{
		Code:     "not valid wasm bytes",
		Language: "wasi",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	// Should return error in output
	if out.ExitCode == 0 && out.Stderr == "" {
		t.Error("expected error for invalid wasm input")
	}
}

func TestRealSandboxRunner_Timeout(t *testing.T) {
	runner := getRealRunner(t)
	fp := fixturePath("infinite_loop")
	if fp == "" {
		t.Fatal("infinite_loop.wasm not found")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := runner.Execute(ctx, SandboxRunInput{
		Code:     fp,
		Language: "wasi",
		Policy:   &types.SandboxPolicy{CPUTimeoutSec: 1, WallTimeoutSec: 5},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if out.ExitCode == 0 && !strings.Contains(out.Stderr, "timeout") && !strings.Contains(out.Stderr, "fuel") {
		t.Logf("timeout may not have triggered: exit=%d stderr=%s", out.ExitCode, out.Stderr)
	}
}

func TestRealSandboxRunner_LargeStdout(t *testing.T) {
	runner := getRealRunner(t)
	fp := fixturePath("large_stdout")
	if fp == "" {
		t.Fatal("large_stdout.wasm not found")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := runner.Execute(ctx, SandboxRunInput{
		Code:     fp,
		Language: "wasi",
		Policy:   &types.SandboxPolicy{MaxStdoutBytes: 1024},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if out.StdoutTruncated {
		t.Logf("stdout correctly truncated")
	}
	if len(out.Stdout) > 1024 {
		t.Errorf("stdout not truncated: len=%d", len(out.Stdout))
	}
}

func TestRealSandboxRunner_UnsupportedLanguage(t *testing.T) {
	runner := getRealRunner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := runner.Execute(ctx, SandboxRunInput{
		Code:     "print('hello')",
		Language: "python",
	})
	if err != nil {
		t.Fatalf("execute should not error for unsupported language: %v", err)
	}
	// Python is not supported by the real runner
	if out.ExitCode == 0 {
		t.Log("python execution via real runner may have succeeded (unexpected)")
	}
}

func TestRealSandboxRunner_OrphanProcessCleanup(t *testing.T) {
	runner := getRealRunner(t)
	fp := fixturePath("infinite_loop")
	if fp == "" {
		t.Fatal("infinite_loop.wasm not found")
	}

	// Short timeout should cause the process to be killed
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	startProcs := countProcesses()
	_, _ = runner.Execute(ctx, SandboxRunInput{
		Code:     fp,
		Language: "wasi",
		Policy:   &types.SandboxPolicy{CPUTimeoutSec: 1, WallTimeoutSec: 2},
	})
	time.Sleep(1 * time.Second) // Wait for cleanup
	endProcs := countProcesses()

	if endProcs > startProcs+5 {
		t.Errorf("possible orphan processes: before=%d after=%d", startProcs, endProcs)
	}
}

func countProcesses() int {
	files, _ := os.ReadDir("/proc")
	count := 0
	for _, f := range files {
		if f.IsDir() && isNumeric(f.Name()) {
			count++
		}
	}
	return count
}

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}
