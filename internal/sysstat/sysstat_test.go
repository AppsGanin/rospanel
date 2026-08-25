package sysstat

import (
	"testing"
	"time"
)

func TestSamplerLifecycle(t *testing.T) {
	dir := t.TempDir()

	s := New(dir)
	if s == nil {
		t.Fatal("New(dir) returned nil")
	}

	// Read initial stats snapshot
	st := s.Read()
	// Stats should have valid types without panic
	_ = st.CPUPercent
	_ = st.MemUsed
	_ = st.MemTotal
	_ = st.DiskUsed
	_ = st.DiskTotal

	// Stop the sampler cleanly
	s.Stop()

	// Calling Stop() a second time should be safe and idempotent
	s.Stop()
}

func TestProcMem(t *testing.T) {
	// ProcMem should return process memory without panic
	mem := ProcMem()
	_ = mem
}

func TestSamplerManualSample(t *testing.T) {
	s := &Sampler{
		diskPath: t.TempDir(),
		stop:     make(chan struct{}),
		lastNetT: time.Now(),
	}

	// Calling sample() manually should not panic
	s.sample()
	s.Stop()
}
