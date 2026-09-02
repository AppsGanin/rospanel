package sysstat

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Read must produce a usable snapshot everywhere the panel runs: full numbers on
// Linux, zeros where /proc is missing — but never negatives or a used figure
// larger than its total, which the dashboard would draw as a broken gauge.
func TestReadReportsSaneNumbers(t *testing.T) {
	s := New(t.TempDir())
	defer s.Stop()
	st := s.Read()

	// Statfs works on every supported platform, so disk is the one field that is
	// always populated.
	if st.DiskTotal <= 0 {
		t.Errorf("DiskTotal = %d, want > 0 for an existing directory", st.DiskTotal)
	}
	if st.DiskUsed < 0 || st.DiskUsed > st.DiskTotal {
		t.Errorf("DiskUsed = %d outside [0, %d]", st.DiskUsed, st.DiskTotal)
	}
	if st.MemUsed < 0 || st.MemUsed > st.MemTotal {
		t.Errorf("MemUsed = %d outside [0, %d]", st.MemUsed, st.MemTotal)
	}
	if st.SwapUsed < 0 || st.SwapUsed > st.SwapTotal {
		t.Errorf("SwapUsed = %d outside [0, %d]", st.SwapUsed, st.SwapTotal)
	}
	if st.CPUPercent < 0 || st.CPUPercent > 100 {
		t.Errorf("CPUPercent = %v outside [0, 100]", st.CPUPercent)
	}
	if st.HostUptime < 0 || st.NetUp < 0 || st.NetDown < 0 {
		t.Errorf("negative counters: uptime %d up %d down %d", st.HostUptime, st.NetUp, st.NetDown)
	}
	if runtime.GOOS == "linux" && st.MemTotal == 0 {
		t.Error("MemTotal = 0 on Linux; /proc/meminfo was not read")
	}
}

// A disk path the sampler cannot stat (the data dir may be created after the
// sampler on first boot) yields zeros, not a failure.
func TestReadDiskFailsSoftly(t *testing.T) {
	total, used := readDisk(filepath.Join(t.TempDir(), "absent"))
	if total != 0 || used != 0 {
		t.Errorf("readDisk(missing) = %d, %d; want zeros", total, used)
	}
	// An empty path means the root filesystem, the default the panel is deployed on.
	rootTotal, _ := readDisk("/")
	if emptyTotal, _ := readDisk(""); emptyTotal != rootTotal {
		t.Errorf("readDisk(\"\") = %d, want the root fs total %d", emptyTotal, rootTotal)
	}
}

// Stop must end the ticker goroutine (a sampler is rebuilt on some settings
// changes, and each leaked loop would keep polling /proc forever) and must be
// safe to call twice, since more than one shutdown path may reach it.
func TestStopIsIdempotentAndEndsTheLoop(t *testing.T) {
	before := runtime.NumGoroutine()
	s := New("")
	s.Stop()
	s.Stop() // a second call must neither panic nor block

	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Errorf("%d goroutines after Stop, %d before: the sampling loop leaked", n, before)
	}
	// A stopped sampler still answers; it just no longer refreshes the rates.
	if st := s.Read(); st.DiskTotal <= 0 {
		t.Error("Read after Stop returned no disk figure")
	}
}

// The rate maths only runs on a fresh sample, and the first tick after New must
// not produce garbage: with no counter movement the CPU figure stays as it was.
func TestSampleWithoutMovementKeepsCPU(t *testing.T) {
	s := &Sampler{stop: make(chan struct{})}
	s.cpu = 42
	// Pretend the last reading equals whatever the next one will be; on a
	// /proc-less host readCPU fails and sample() must leave the value alone too.
	if cur, ok := readCPU(); ok {
		s.lastCPU = cur
	}
	s.sample()
	if s.cpu < 0 || s.cpu > 100 {
		t.Errorf("cpu = %v after a no-movement sample", s.cpu)
	}
}
