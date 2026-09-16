package tuning

import (
	"os"
	"path"
	"runtime/debug"
	"strconv"
	"strings"
)

// Memory is what SetMemoryLimit decided: the soft limit it gave the Go runtime, the
// memory that limit is a share of, what was set aside for what runs beside it, and
// where the figure came from.
type Memory struct {
	Limit    int64  // bytes; 0 ⇒ the runtime was left alone
	Of       int64  // bytes the process can use
	Reserved int64  // bytes of Of left to what runs beside this process
	Basis    string // "RAM", "cgroup", or "GOMEMLIMIT" when the environment already set one
}

// SetMemoryLimit gives the Go runtime a soft memory limit of share × the memory this
// process can use once reserve is set aside for what runs beside it: the machine's
// RAM, or its cgroup's limit when that is lower.
//
// reserve is there because the panel and the node both run Xray, which is Go too and
// is given a soft limit of its own (xray.MemoryLimit). The two were set in different
// files and nobody added them up: on a 1 GB box half the RAM for the panel, a quarter
// of it for Xray and what each runtime holds above its own limit came to the whole
// machine. With 50,000 users and five nodes the panel was OOM-killed every time a user
// was created — the moment five node configs are built at once.
//
// Without one the collector lets the heap grow to twice what is live before it runs.
// On a 1 GB box that is the difference between working and being OOM-killed: in a
// load test with 50,000 users the panel's heap reached 280 MB, the collector's next
// target was 471 MB, and the process was killed at 520 MB before it got there. With
// a limit the same load ran at 350–410 MB. The collector runs earlier as the heap
// nears the limit. It is soft: past it the runtime keeps allocating rather than
// failing, so a heap that genuinely needs more is slowed down by collection, never
// refused.
//
// A GOMEMLIMIT in the environment is the operator's own choice and is left in force.
func SetMemoryLimit(share float64, reserve int64) Memory {
	if os.Getenv("GOMEMLIMIT") != "" {
		return Memory{Basis: "GOMEMLIMIT"}
	}
	m := memoryLimit(os.ReadFile, share, reserve)
	if m.Limit > 0 {
		debug.SetMemoryLimit(m.Limit)
	}
	return m
}

func memoryLimit(read func(string) ([]byte, error), share float64, reserve int64) Memory {
	ram := memTotal(read)
	of, basis := ram, "RAM"
	if lim := cgroupLimit(read); lim > 0 && (ram <= 0 || lim < ram) {
		of, basis = lim, "cgroup"
	}
	if of <= 0 || share <= 0 {
		return Memory{}
	}
	usable := of - reserve
	// A box so small that what runs beside this process would claim most of it. A
	// quarter of the machine is then the most that can honestly be steered under;
	// below that the limit stops describing anything and only makes the collector run.
	if floor := of / 4; usable < floor {
		usable, reserve = floor, of-floor
	}
	return Memory{Limit: int64(float64(usable) * share), Of: of, Reserved: reserve, Basis: basis}
}

// memTotal is MemTotal from /proc/meminfo in bytes, 0 when unreadable.
func memTotal(read func(string) ([]byte, error)) int64 {
	data, err := read("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "MemTotal:" {
			kb, err := strconv.ParseInt(f[1], 10, 64)
			if err != nil {
				return 0
			}
			return kb * 1024
		}
	}
	return 0
}

// cgroupLimit is the lowest memory limit on this process's cgroup or any cgroup above
// it, in bytes; 0 when there is none. A limit on a parent binds as surely as one on the
// process's own group — a systemd slice, or a container whose group is the root of
// what the process can see.
func cgroupLimit(read func(string) ([]byte, error)) int64 {
	data, err := read("/proc/self/cgroup")
	if err != nil {
		return 0
	}
	var lowest int64
	note := func(file string) {
		b, err := read(file)
		if err != nil {
			return
		}
		v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		// "max" (v2) does not parse; v1 spells "unlimited" as a page-rounded 2^63-1.
		if err != nil || v <= 0 || v >= 1<<62 {
			return
		}
		if lowest == 0 || v < lowest {
			lowest = v
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		// hierarchy-id:controllers:path — v2 has one line with no controllers.
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 || !strings.HasPrefix(parts[2], "/") {
			continue
		}
		var root, file string
		switch {
		case parts[0] == "0" && parts[1] == "":
			root, file = "/sys/fs/cgroup", "memory.max"
		case hasController(parts[1], "memory"):
			root, file = "/sys/fs/cgroup/memory", "memory.limit_in_bytes"
		default:
			continue
		}
		for p := path.Clean(parts[2]); ; p = path.Dir(p) {
			note(path.Join(root, p, file))
			if p == "/" {
				break
			}
		}
	}
	return lowest
}

func hasController(list, name string) bool {
	for _, c := range strings.Split(list, ",") {
		if c == name {
			return true
		}
	}
	return false
}
