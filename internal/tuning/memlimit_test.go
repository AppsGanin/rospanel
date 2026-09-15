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
		name  string
		files map[string]string
		want  Memory
	}{
		{"RAM, no cgroup", map[string]string{"/proc/meminfo": meminfo1G}, Memory{Limit: ram / 2, Of: ram, Basis: "RAM"}},
		{"v2 service limit below RAM", map[string]string{
			"/proc/meminfo":     meminfo1G,
			"/proc/self/cgroup": "0::/system.slice/rospanel.service\n",
			"/sys/fs/cgroup/system.slice/rospanel.service/memory.max": "600000000\n",
		}, Memory{Limit: 300000000, Of: 600000000, Basis: "cgroup"}},
		{"v2 unlimited", map[string]string{
			"/proc/meminfo":     meminfo1G,
			"/proc/self/cgroup": "0::/system.slice/rospanel.service\n",
			"/sys/fs/cgroup/system.slice/rospanel.service/memory.max": "max\n",
		}, Memory{Limit: ram / 2, Of: ram, Basis: "RAM"}},
		{"v2 limit on a parent slice", map[string]string{
			"/proc/meminfo":     meminfo1G,
			"/proc/self/cgroup": "0::/system.slice/rospanel.service\n",
			"/sys/fs/cgroup/system.slice/rospanel.service/memory.max": "max\n",
			"/sys/fs/cgroup/system.slice/memory.max":                  "400000000\n",
		}, Memory{Limit: 200000000, Of: 400000000, Basis: "cgroup"}},
		{"container: the limit sits on the root it can see", map[string]string{
			"/proc/meminfo":             meminfo1G,
			"/proc/self/cgroup":         "0::/\n",
			"/sys/fs/cgroup/memory.max": "268435456\n",
		}, Memory{Limit: 134217728, Of: 268435456, Basis: "cgroup"}},
		{"v1 memory controller", map[string]string{
			"/proc/meminfo":     meminfo1G,
			"/proc/self/cgroup": "12:pids:/docker/abc\n4:cpu,memory:/docker/abc\n",
			"/sys/fs/cgroup/memory/docker/abc/memory.limit_in_bytes": "300000000\n",
		}, Memory{Limit: 150000000, Of: 300000000, Basis: "cgroup"}},
		{"v1 unlimited", map[string]string{
			"/proc/meminfo":     meminfo1G,
			"/proc/self/cgroup": "4:memory:/\n",
			"/sys/fs/cgroup/memory/memory.limit_in_bytes": "9223372036854771712\n",
		}, Memory{Limit: ram / 2, Of: ram, Basis: "RAM"}},
		{"cgroup limit above RAM", map[string]string{
			"/proc/meminfo":               meminfo1G,
			"/proc/self/cgroup":           "0::/x\n",
			"/sys/fs/cgroup/x/memory.max": "8000000000\n",
		}, Memory{Limit: ram / 2, Of: ram, Basis: "RAM"}},
		{"cgroup only", map[string]string{
			"/proc/self/cgroup":         "0::/\n",
			"/sys/fs/cgroup/memory.max": "100000000\n",
		}, Memory{Limit: 50000000, Of: 100000000, Basis: "cgroup"}},
		{"nothing readable", map[string]string{}, Memory{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := memoryLimit(files(tc.files), 0.5); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// An operator's GOMEMLIMIT stays in force: the runtime applied it at start and the
// panel does not replace it with its own figure.
func TestSetMemoryLimitKeepsTheEnvironmentsLimit(t *testing.T) {
	t.Setenv("GOMEMLIMIT", "300MiB")
	if got := SetMemoryLimit(0.5); got != (Memory{Basis: "GOMEMLIMIT"}) {
		t.Fatalf("got %+v", got)
	}
}
