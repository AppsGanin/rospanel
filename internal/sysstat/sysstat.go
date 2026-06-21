// Package sysstat samples host metrics (CPU, memory, swap, disk, network) from
// /proc on Linux. On platforms without /proc (e.g. local dev on macOS) the
// readers fail softly and return zeros — the panel still runs.
package sysstat

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Stats is a point-in-time host snapshot. Byte fields are absolute; Net* are
// per-second rates.
type Stats struct {
	CPUPercent float64 `json:"cpu_percent"`
	MemUsed    int64   `json:"mem_used"`
	MemTotal   int64   `json:"mem_total"`
	SwapUsed   int64   `json:"swap_used"`
	SwapTotal  int64   `json:"swap_total"`
	DiskUsed   int64   `json:"disk_used"`
	DiskTotal  int64   `json:"disk_total"`
	HostUptime int64   `json:"host_uptime"` // seconds
	NetUp      int64   `json:"net_up"`      // bytes/sec
	NetDown    int64   `json:"net_down"`    // bytes/sec
}

type cpuTimes struct{ total, idle uint64 }
type netCounters struct{ rx, tx uint64 }

// Sampler maintains rolling CPU% and network rates via a background ticker.
type Sampler struct {
	diskPath string

	mu       sync.Mutex
	cpu      float64
	netUp    int64
	netDown  int64
	lastCPU  cpuTimes
	lastNet  netCounters
	lastNetT time.Time
}

// New starts a sampler that refreshes CPU/network rates every 2s.
func New(diskPath string) *Sampler {
	s := &Sampler{diskPath: diskPath}
	s.lastCPU, _ = readCPU()
	s.lastNet, _ = readNet()
	s.lastNetT = time.Now()
	go s.loop()
	return s
}

func (s *Sampler) loop() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for range t.C {
		s.sample()
	}
}

func (s *Sampler) sample() {
	if cur, ok := readCPU(); ok {
		s.mu.Lock()
		dTot := float64(cur.total - s.lastCPU.total)
		dIdle := float64(cur.idle - s.lastCPU.idle)
		if dTot > 0 {
			s.cpu = (1 - dIdle/dTot) * 100
		}
		s.lastCPU = cur
		s.mu.Unlock()
	}
	if cur, ok := readNet(); ok {
		now := time.Now()
		s.mu.Lock()
		dt := now.Sub(s.lastNetT).Seconds()
		if dt > 0 {
			s.netDown = int64(float64(cur.rx-s.lastNet.rx) / dt)
			s.netUp = int64(float64(cur.tx-s.lastNet.tx) / dt)
		}
		s.lastNet = cur
		s.lastNetT = now
		s.mu.Unlock()
	}
}

// Read returns the current snapshot (cheap; on-demand for mem/disk/uptime).
func (s *Sampler) Read() Stats {
	var st Stats
	s.mu.Lock()
	st.CPUPercent = s.cpu
	st.NetUp = s.netUp
	st.NetDown = s.netDown
	s.mu.Unlock()

	memTotal, memAvail, swapTotal, swapFree := readMem()
	st.MemTotal = memTotal
	st.MemUsed = memTotal - memAvail
	st.SwapTotal = swapTotal
	st.SwapUsed = swapTotal - swapFree
	st.HostUptime = readUptime()
	st.DiskTotal, st.DiskUsed = readDisk(s.diskPath)
	return st
}

func readCPU() (cpuTimes, bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuTimes{}, false
	}
	line := strings.SplitN(string(data), "\n", 2)[0]
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return cpuTimes{}, false
	}
	var total, idle uint64
	for i := 1; i < len(f); i++ {
		v, err := strconv.ParseUint(f[i], 10, 64)
		if err != nil {
			continue
		}
		total += v
		if i == 4 || i == 5 { // idle + iowait
			idle += v
		}
	}
	return cpuTimes{total: total, idle: idle}, true
}

func readNet() (netCounters, bool) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return netCounters{}, false
	}
	var c netCounters
	for _, line := range strings.Split(string(data), "\n") {
		idx := strings.IndexByte(line, ':')
		if idx < 0 {
			continue
		}
		name := strings.TrimSpace(line[:idx])
		if name == "lo" {
			continue
		}
		f := strings.Fields(line[idx+1:])
		if len(f) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(f[0], 10, 64)
		tx, _ := strconv.ParseUint(f[8], 10, 64)
		c.rx += rx
		c.tx += tx
	}
	return c, true
}

// readMem returns total, available, swapTotal, swapFree in bytes.
func readMem() (total, avail, swapTotal, swapFree int64) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseInt(f[1], 10, 64)
		v *= 1024 // kB → bytes
		switch f[0] {
		case "MemTotal:":
			total = v
		case "MemAvailable:":
			avail = v
		case "SwapTotal:":
			swapTotal = v
		case "SwapFree:":
			swapFree = v
		}
	}
	return total, avail, swapTotal, swapFree
}

// ProcMem returns the panel process's resident set size (RSS) in bytes, read
// from /proc/self/status. 0 if unavailable.
func ProcMem() int64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				v, _ := strconv.ParseInt(f[1], 10, 64)
				return v * 1024 // kB → bytes
			}
		}
	}
	return 0
}

func readUptime() int64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(data))
	if len(f) == 0 {
		return 0
	}
	secs, _ := strconv.ParseFloat(f[0], 64)
	return int64(secs)
}

func readDisk(path string) (total, used int64) {
	if path == "" {
		path = "/"
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	bs := uint64(st.Bsize)
	total = int64(st.Blocks * bs)
	used = int64((st.Blocks - st.Bfree) * bs)
	return total, used
}
