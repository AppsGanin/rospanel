//go:build linux

package awg

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/amnezia-vpn/amneziawg-go/v3/conn"
	"github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun"
)

// nftTable is the panel's own table for the tunnel's NAT and forwarding — its
// own so it can be recreated wholesale without touching anyone else's rules.
const nftTable = "rospanel_awg"

// linuxDevice is the tunnel on a real server: a TUN handed to amneziawg-go, the
// interface addressed and raised with iproute2, forwarding switched on and the
// subnet masqueraded out through nftables.
type linuxDevice struct {
	mu      sync.Mutex
	dev     *device.Device
	tun     tun.Device
	port    int
	privKey string
	lastErr string
}

// New returns the tunnel driver for this platform.
func New() Device { return &linuxDevice{} }

func (d *linuxDevice) Apply(cfg Config) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	err := d.apply(cfg)
	if err != nil {
		d.lastErr = err.Error()
	} else {
		d.lastErr = ""
	}
	return err
}

func (d *linuxDevice) apply(cfg Config) error {
	uapi, err := cfg.UAPI()
	if err != nil {
		return err
	}
	// A new key or port means a new socket and a new identity: tear down and start
	// over rather than trying to change them under a live device.
	if d.dev != nil && (d.port != cfg.ListenPort || d.privKey != cfg.PrivateKey) {
		d.closeLocked()
	}
	if d.dev == nil {
		mtu := cfg.MTU
		if mtu <= 0 {
			mtu = DefaultMTU
		}
		t, err := tun.CreateTUN(Iface, mtu)
		if err != nil {
			return fmt.Errorf("awg: create tun %s: %w (the service needs CAP_NET_ADMIN and /dev/net/tun)", Iface, err)
		}
		logger := device.NewLogger(device.LogLevelError, "awg: ")
		dev := device.NewDevice(t, conn.NewDefaultBind(), logger)
		if err := dev.IpcSet(uapi); err != nil {
			dev.Close()
			return fmt.Errorf("awg: configure device: %w", err)
		}
		if err := dev.Up(); err != nil {
			dev.Close()
			return fmt.Errorf("awg: bring device up: %w", err)
		}
		if err := configureInterface(); err != nil {
			dev.Close()
			return err
		}
		d.dev, d.tun, d.port, d.privKey = dev, t, cfg.ListenPort, cfg.PrivateKey
		slog.Info("awg: tunnel up", "iface", Iface, "port", cfg.ListenPort, "peers", len(cfg.Peers))
		return nil
	}
	if err := d.dev.IpcSet(uapi); err != nil {
		return fmt.Errorf("awg: update peers: %w", err)
	}
	return nil
}

// configureInterface addresses and raises the TUN, turns forwarding on and
// installs the NAT table. Each step is idempotent, so a restart of the panel
// over a still-configured interface is fine.
func configureInterface() error {
	addr := fmt.Sprintf("%s/%d", ServerAddr, Subnet.Bits())
	if out, err := exec.Command("ip", "addr", "replace", addr, "dev", Iface).CombinedOutput(); err != nil {
		return fmt.Errorf("awg: ip addr replace: %w: %s", err, bytes.TrimSpace(out))
	}
	if out, err := exec.Command("ip", "link", "set", "up", "dev", Iface).CombinedOutput(); err != nil {
		return fmt.Errorf("awg: ip link up: %w: %s", err, bytes.TrimSpace(out))
	}
	if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1\n"), 0o644); err != nil {
		slog.Warn("awg: could not enable ip_forward", "err", err)
	}
	if _, err := exec.LookPath("nft"); err != nil {
		slog.Warn("awg: nft not found; clients will connect but not reach the internet (install nftables)")
		return nil
	}
	_ = exec.Command("nft", "delete", "table", "inet", nftTable).Run()
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(Ruleset())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("awg: nft load: %w: %s", err, bytes.TrimSpace(out))
	}
	allowIptablesForward()
	return nil
}

// iptablesForward lets tunnel traffic through iptables' FORWARD chain. Our table's
// accept does not outvote another table's drop on the same hook — a packet must pass
// every base chain — and Docker and ufw both set FORWARD to DROP, so on such a host
// clients handshake but reach nothing.
var iptablesForward = [][]string{
	{"-i", Iface, "-j", "ACCEPT"},
	{"-o", Iface, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"},
}

// allowIptablesForward puts iptablesForward at the head of FORWARD when that chain
// is managed — a DROP policy or any rule in it; a bare ACCEPT chain needs nothing.
func allowIptablesForward() {
	if _, err := exec.LookPath("iptables"); err != nil {
		return
	}
	out, err := exec.Command("iptables", "-S", "FORWARD").Output()
	if err != nil || strings.TrimSpace(string(out)) == "-P FORWARD ACCEPT" {
		return
	}
	for _, r := range iptablesForward {
		if exec.Command("iptables", append([]string{"-C", "FORWARD"}, r...)...).Run() == nil {
			continue
		}
		if out, err := exec.Command("iptables", append([]string{"-I", "FORWARD", "1"}, r...)...).CombinedOutput(); err != nil {
			slog.Warn("awg: could not open iptables FORWARD for the tunnel", "err", err, "out", string(bytes.TrimSpace(out)))
		}
	}
}

// dropIptablesForward removes what allowIptablesForward added.
func dropIptablesForward() {
	if _, err := exec.LookPath("iptables"); err != nil {
		return
	}
	for _, r := range iptablesForward {
		del := append([]string{"-D", "FORWARD"}, r...)
		for i := 0; i < 8 && exec.Command("iptables", del...).Run() == nil; i++ {
			// A duplicate can be left by an earlier run that died mid-way.
		}
	}
}

// Ruleset is the NAT/forward table: masquerade the tunnel subnet on its way out
// of any other interface, let tunnel traffic through the forward chain both ways.
func Ruleset() string {
	return fmt.Sprintf(`table inet %[1]s {
	chain postrouting {
		type nat hook postrouting priority srcnat; policy accept;
		ip saddr %[2]s oifname != "%[3]s" masquerade
	}
	chain forward {
		type filter hook forward priority filter; policy accept;
		iifname "%[3]s" accept
		oifname "%[3]s" ct state established,related accept
	}
}
`, nftTable, Subnet, Iface)
}

func (d *linuxDevice) Stats() (map[string]PeerStat, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dev == nil {
		return nil, nil
	}
	dump, err := d.dev.IpcGet()
	if err != nil {
		return nil, err
	}
	return ParseStats(dump), nil
}

func (d *linuxDevice) Running() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dev != nil
}

func (d *linuxDevice) LastError() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastErr
}

func (d *linuxDevice) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closeLocked()
}

func (d *linuxDevice) closeLocked() {
	if d.dev == nil {
		return
	}
	d.dev.Close() // closes the TUN too
	d.dev, d.tun = nil, nil
	_ = exec.Command("nft", "delete", "table", "inet", nftTable).Run()
	dropIptablesForward()
	slog.Info("awg: tunnel down", "iface", Iface)
}
