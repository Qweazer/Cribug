package db

import (
	"context"
	"testing"

	"cribug/internal/types"

	"github.com/google/uuid"
)

func TestSandboxAudit_InsertAndRead(t *testing.T) {
	pg := getTestDBHelper(t)
	ctx := context.Background()

	// Ensure table exists
	pg.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS sandbox_audit_logs (
		id UUID PRIMARY KEY, request_id VARCHAR(255) NOT NULL,
		agent_id VARCHAR(255) NOT NULL DEFAULT '', workflow_id VARCHAR(255) NOT NULL DEFAULT '',
		language VARCHAR(50) NOT NULL DEFAULT '', code_len INT DEFAULT 0,
		success BOOLEAN NOT NULL DEFAULT false, exit_code INT DEFAULT 0,
		error TEXT DEFAULT '', error_type VARCHAR(100) DEFAULT '',
		duration_ms BIGINT DEFAULT 0, created_at TIMESTAMP DEFAULT NOW())`)
	defer pg.db.ExecContext(ctx, "DELETE FROM sandbox_audit_logs")

	wfID := uuid.New().String()

	// Insert success
	err := pg.CreateSandboxAuditLog(ctx, types.SandboxAuditLog{
		ID: uuid.New().String(), RequestID: "req-1", AgentID: "a1",
		WorkflowID: wfID, Language: "wasi", CodeLen: 100,
		Success: true, ExitCode: 0, DurationMs: 50,
	})
	if err != nil {
		t.Fatalf("insert success audit: %v", err)
	}

	// Insert failure
	err = pg.CreateSandboxAuditLog(ctx, types.SandboxAuditLog{
		ID: uuid.New().String(), RequestID: "req-2", AgentID: "a1",
		WorkflowID: wfID, Language: "python", CodeLen: 50,
		Success: false, ExitCode: 1, Error: "timeout",
		ErrorType: types.SandboxErrorTypeTimeout, DurationMs: 30000,
	})
	if err != nil {
		t.Fatalf("insert failure audit: %v", err)
	}

	logs, err := pg.GetSandboxAuditLogsByWorkflowID(ctx, wfID)
	if err != nil {
		t.Fatalf("get audit logs: %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("expected 2 logs, got %d", len(logs))
	}

	hasSuccess, hasFailure := false, false
	for _, l := range logs {
		if l.Success {
			hasSuccess = true
		}
		if !l.Success && l.ErrorType == types.SandboxErrorTypeTimeout {
			hasFailure = true
		}
	}
	if !hasSuccess {
		t.Error("expected success audit log")
	}
	if !hasFailure {
		t.Error("expected failure audit log")
	}
}
