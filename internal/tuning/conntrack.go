package tuning

import (
	"os"
	"os/exec"
	"strconv"
)

const (
	ctMaxPath         = "/proc/sys/net/netfilter/nf_conntrack_max"
	ctEstablishedPath = "/proc/sys/net/netfilter/nf_conntrack_tcp_timeout_established"
	ctHashsizePath    = "/sys/module/nf_conntrack/parameters/hashsize"

	// The kernel's own default for an established TCP entry: five days. An entry
	// whose client vanished without a FIN — a phone that lost signal — holds its slot
	// that long.
	ctEstablishedKernel = 432000
	ctEstablishedWant   = 86400

	// ctPerMiB is conntrack slots per MiB of RAM; an entry is about 320 bytes, so a
	// full table costs about 2% of memory.
	ctPerMiB = 64
	ctMin    = 65536
	ctMax    = 1 << 20
)

// Conntrack is what EnsureConntrack found and did, for logging.
type Conntrack struct {
	Max, WasMax      int
	Established, Was int
	Changed          bool
}

// ConntrackTarget is the table size for a host with ramMiB of memory.
func ConntrackTarget(ramMiB int) int {
	return min(max(ramMiB*ctPerMiB, ctMin), ctMax)
}

// EnsureConntrack sizes the connection-tracking table for a proxy. The kernel sizes it
// from memory, which on a 1 GB box is 8192 entries: a few hundred users fill that,
// and a full table drops every new connection — the node's own to the panel and to
// Xray's API among them — while the connections already open carry on. Only raises:
// a larger size or a shorter timeout someone set is theirs. Applied on every start, not
// in sysctl.d: at boot the module is not loaded yet and those keys do not exist.
// Best-effort.
func EnsureConntrack(ramMiB int) (Conntrack, error) {
	// Our nftables rules load the module anyway; loading it first lets the size be set
	// before the table fills.
	_ = exec.Command("modprobe", "nf_conntrack").Run()
	var c Conntrack
	cur, err := readInt(ctMaxPath)
	if err != nil {
		return c, err
	}
	c.Max, c.WasMax = cur, cur
	want := ConntrackTarget(ramMiB)
	if cur < want {
		// A bigger table on the same buckets only makes the chains longer.
		if hs, err := readInt(ctHashsizePath); err == nil && hs < want/4 {
			_ = os.WriteFile(ctHashsizePath, []byte(strconv.Itoa(want/4)), 0o644)
		}
		if err := os.WriteFile(ctMaxPath, []byte(strconv.Itoa(want)), 0o644); err != nil {
			return c, err
		}
		c.Max, c.Changed = want, true
	}
	if est, err := readInt(ctEstablishedPath); err == nil {
		c.Established, c.Was = est, est
		if est == ctEstablishedKernel {
			if os.WriteFile(ctEstablishedPath, []byte(strconv.Itoa(ctEstablishedWant)), 0o644) == nil {
				c.Established, c.Changed = ctEstablishedWant, true
			}
		}
	}
	return c, nil
}

func readInt(p string) (int, error) {
	s, err := readTrim(p)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(s)
}

// RAMMiB is the host's memory in MiB, 0 when unreadable.
func RAMMiB() int { return int(memTotal(os.ReadFile) >> 20) }
