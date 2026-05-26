package types

import "testing"

func TestSandboxPolicy_Defaults(t *testing.T) {
	var p *SandboxPolicy
	if p.EffectiveCPUTimeout() != DefaultSandboxCPUTimeoutSec {
		t.Error("nil policy: default CPU timeout")
	}
	if p.EffectiveWallTimeout() != DefaultSandboxWallTimeoutSec {
		t.Error("nil policy: default wall timeout")
	}
	if p.EffectiveMemoryMB() != DefaultSandboxMemoryMB {
		t.Error("nil policy: default memory")
	}
}

func TestSandboxPolicy_Bounds(t *testing.T) {
	p := &SandboxPolicy{CPUTimeoutSec: 120, WallTimeoutSec: 200, MemoryMB: 2048}
	if p.EffectiveCPUTimeout() != DefaultSandboxCPUTimeoutSec {
		t.Errorf("CPU timeout over 60 should return default, got %d", p.EffectiveCPUTimeout())
	}
	if p.EffectiveWallTimeout() != DefaultSandboxWallTimeoutSec {
		t.Errorf("wall timeout over 120 should return default, got %d", p.EffectiveWallTimeout())
	}
	if p.EffectiveMemoryMB() != DefaultSandboxMemoryMB {
		t.Errorf("memory over 1024 should return default, got %d", p.EffectiveMemoryMB())
	}
}

func TestSandboxPolicy_Valid(t *testing.T) {
	p := &SandboxPolicy{CPUTimeoutSec: 5, WallTimeoutSec: 15, MemoryMB: 64}
	if p.EffectiveCPUTimeout() != 5 {
		t.Error("valid CPU timeout")
	}
	if p.EffectiveWallTimeout() != 15 {
		t.Error("valid wall timeout")
	}
	if p.EffectiveMemoryMB() != 64 {
		t.Error("valid memory")
	}
}

func TestSandboxPolicy_StdoutStderrLimits(t *testing.T) {
	p := &SandboxPolicy{MaxStdoutBytes: 1024, MaxStderrBytes: 512}
	if p.EffectiveMaxStdout() != 1024 {
		t.Error("stdout limit")
	}
	if p.EffectiveMaxStderr() != 512 {
		t.Error("stderr limit")
	}

	var nilP *SandboxPolicy
	if nilP.EffectiveMaxStdout() != DefaultSandboxStdoutMaxBytes {
		t.Error("nil policy stdout default")
	}
}
