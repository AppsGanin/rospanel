package tuning

import (
	"io/fs"
	"testing"
)

func files(m map[string]string) func(string) ([]byte, error) {
	return func(name string) ([]byte, error) {
		if v, ok := m[name]; ok {
			return []byte(v), nil
		}
		return nil, fs.ErrNotExist
	}
}

const meminfo1G = "MemTotal:         984064 kB\nMemFree:          100000 kB\nMemAvailable:     500000 kB\n"

func TestMemoryLimit(t *testing.T) {
	const ram = 984064 * 1024
	for _, tc := range []struct {
		name    string
		files   map[string]string
		reserve int64
		want    Memory
	}{
		{name: "RAM, no cgroup", files: map[string]string{"/proc/meminfo": meminfo1G}, want: Memory{Limit: ram / 2, Of: ram, Basis: "RAM"}},
		{name: "v2 service limit below RAM", files: map[string]string{
			"/proc/meminfo":     meminfo1G,
			"/proc/self/cgroup": "0::/system.slice/rospanel.service\n",
			"/sys/fs/cgroup/system.slice/rospanel.service/memory.max": "600000000\n",
		}, want: Memory{Limit: 300000000, Of: 600000000, Basis: "cgroup"}},
		{name: "v2 unlimited", files: map[string]string{
			"/proc/meminfo":     meminfo1G,
			"/proc/self/cgroup": "0::/system.slice/rospanel.service\n",
			"/sys/fs/cgroup/system.slice/rospanel.service/memory.max": "max\n",
		}, want: Memory{Limit: ram / 2, Of: ram, Basis: "RAM"}},
		{name: "v2 limit on a parent slice", files: map[string]string{
			"/proc/meminfo":     meminfo1G,
			"/proc/self/cgroup": "0::/system.slice/rospanel.service\n",
			"/sys/fs/cgroup/system.slice/rospanel.service/memory.max": "max\n",
			"/sys/fs/cgroup/system.slice/memory.max":                  "400000000\n",
		}, want: Memory{Limit: 200000000, Of: 400000000, Basis: "cgroup"}},
		{name: "container: the limit sits on the root it can see", files: map[string]string{
			"/proc/meminfo":             meminfo1G,
			"/proc/self/cgroup":         "0::/\n",
			"/sys/fs/cgroup/memory.max": "268435456\n",
		}, want: Memory{Limit: 134217728, Of: 268435456, Basis: "cgroup"}},
		{name: "v1 memory controller", files: map[string]string{
			"/proc/meminfo":     meminfo1G,
			"/proc/self/cgroup": "12:pids:/docker/abc\n4:cpu,memory:/docker/abc\n",
			"/sys/fs/cgroup/memory/docker/abc/memory.limit_in_bytes": "300000000\n",
		}, want: Memory{Limit: 150000000, Of: 300000000, Basis: "cgroup"}},
		{name: "v1 unlimited", files: map[string]string{
			"/proc/meminfo":     meminfo1G,
			"/proc/self/cgroup": "4:memory:/\n",
			"/sys/fs/cgroup/memory/memory.limit_in_bytes": "9223372036854771712\n",
		}, want: Memory{Limit: ram / 2, Of: ram, Basis: "RAM"}},
		{name: "cgroup limit above RAM", files: map[string]string{
			"/proc/meminfo":               meminfo1G,
			"/proc/self/cgroup":           "0::/x\n",
			"/sys/fs/cgroup/x/memory.max": "8000000000\n",
		}, want: Memory{Limit: ram / 2, Of: ram, Basis: "RAM"}},
		{name: "cgroup only", files: map[string]string{
			"/proc/self/cgroup":         "0::/\n",
			"/sys/fs/cgroup/memory.max": "100000000\n",
		}, want: Memory{Limit: 50000000, Of: 100000000, Basis: "cgroup"}},
		{name: "nothing readable", files: map[string]string{}, want: Memory{}},
		// What runs beside this process comes off the top: the share is of the rest.
		{name: "Xray's ceiling set aside", files: map[string]string{"/proc/meminfo": meminfo1G}, reserve: 256 << 20,
			want: Memory{Limit: (ram - 256<<20) / 2, Of: ram, Reserved: 256 << 20, Basis: "RAM"}},
		{name: "a cgroup with room for both", files: map[string]string{
			"/proc/meminfo":             meminfo1G,
			"/proc/self/cgroup":         "0::/\n",
			"/sys/fs/cgroup/memory.max": "800000000\n",
		}, reserve: 256 << 20, want: Memory{Limit: (800000000 - 256<<20) / 2, Of: 800000000, Reserved: 256 << 20, Basis: "cgroup"}},
		// A box where the reserve would leave almost nothing: a quarter of it is the
		// most that can be steered under, and the rest is what the reserve really got.
		{name: "a box smaller than the reserve", files: map[string]string{
			"/proc/meminfo":             meminfo1G,
			"/proc/self/cgroup":         "0::/\n",
			"/sys/fs/cgroup/memory.max": "200000000\n",
		}, reserve: 256 << 20, want: Memory{Limit: 200000000 / 4 / 2, Of: 200000000, Reserved: 200000000 - 200000000/4, Basis: "cgroup"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := memoryLimit(files(tc.files), 0.5, tc.reserve); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// An operator's GOMEMLIMIT stays in force: the runtime applied it at start and the
// panel does not replace it with its own figure.
func TestSetMemoryLimitKeepsTheEnvironmentsLimit(t *testing.T) {
	t.Setenv("GOMEMLIMIT", "300MiB")
	if got := SetMemoryLimit(0.5, 1<<20); got != (Memory{Basis: "GOMEMLIMIT"}) {
		t.Fatalf("got %+v", got)
	}
}
