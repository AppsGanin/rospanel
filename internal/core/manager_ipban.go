package core

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/ipblock"
	"github.com/AppsGanin/rospanel/internal/model"
)

// Banning an address by hand, from the addresses a user connects from.
//
// A ban drops everything from the address at the firewall of the master and of every
// node — every port, the panel and SSH included — for everyone behind it, until an
// operator lifts it. It is recorded (ip_bans), re-applied at boot, and handed to the
// nodes with the source policy's blocks. Lifting a ban lifts every ban the panel holds
// on the address, whatever placed it: the operator asking for an address back does not
// care which of the panel's defences is holding it.

// cgnat is the carrier-grade NAT range (RFC 6598). An address in it never reaches a
// public server as a source, so banning one does nothing.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// banCheck refuses the addresses a ban must not touch and returns the address as the
// firewall writes it. adminIP is the address the operator placing the ban is using.
type banCheck func(ip string) (netip.Addr, error)

// banChecker builds the check once for a request: the addresses it compares against
// take a read of the nodes, the admin sessions and this machine's interfaces.
func (m *Manager) banChecker(adminIP string) banCheck {
	servers := m.serverAddresses()
	var admin netip.Addr
	if a, err := parseBanAddr(adminIP); err == nil {
		admin = a
	}
	sessions := map[netip.Addr]bool{}
	if ips, err := m.store.LiveSessionIPs(); err == nil {
		for _, ip := range ips {
			if a, err := parseBanAddr(ip); err == nil {
				sessions[a] = true
			}
		}
	}
	return func(ip string) (netip.Addr, error) {
		addr, err := parseBanAddr(ip)
		if err != nil {
			return netip.Addr{}, invalidCode("err.banBadIP", "{{ip}}: это не IP-адрес", map[string]any{"ip": ip})
		}
		args := map[string]any{"ip": addr.String()}
		switch {
		case !addr.IsGlobalUnicast() || addr.IsPrivate() || cgnat.Contains(addr):
			// Tunnel addresses (a WireGuard or AmneziaWG peer's 10.66.x) land here too:
			// they live inside the tunnel, where the firewall never sees them.
			return netip.Addr{}, invalidCode("err.banNotPublic",
				"{{ip}} — не публичный адрес: с него никто не подключается к серверу напрямую, бан ничего не даст", args)
		case addr == admin:
			return netip.Addr{}, invalidCode("err.banSelf",
				"{{ip}} — ваш текущий адрес: бан отрезал бы вас от панели", args)
		case sessions[addr]:
			return netip.Addr{}, invalidCode("err.banAdmin",
				"{{ip}} — адрес, с которого вошёл администратор панели", args)
		case m.Trusted(addr.String()):
			return netip.Addr{}, invalidCode("err.banTrusted",
				"{{ip}} входит в доверенные сети — уберите его оттуда, чтобы забанить", args)
		case servers[addr]:
			return netip.Addr{}, invalidCode("err.banServer",
				"{{ip}} — адрес одного из серверов панели", args)
		}
		return addr, nil
	}
}

// parseBanAddr reads an address the way the firewall writes it: IPv4-mapped IPv6 as
// IPv4. An address with a zone ("fe80::1%eth0") is refused: nft cannot write one, and a
// zone would make the same address compare unequal to itself.
func parseBanAddr(s string) (netip.Addr, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return netip.Addr{}, err
	}
	if a.Zone() != "" {
		return netip.Addr{}, errors.New("an address with a zone")
	}
	return a.Unmap(), nil
}

// serverAddresses are the addresses of the panel's own servers: this machine's
// interfaces, and its host setting and every node's host — resolved where they are
// names. A ban on one would cut a node off from the panel, or the panel off from
// itself. Each node refuses to drop the panel's address as well (nodeagent), since a
// master behind NAT has an address no interface here carries.
func (m *Manager) serverAddresses() map[netip.Addr]bool {
	out := map[netip.Addr]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if a, err := parseBanAddr(s); err == nil {
			out[a] = true
			return
		}
		for _, a := range m.hosts.lookup(s) {
			out[a] = true
		}
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil {
				out[p.Addr().Unmap()] = true
			}
		}
	}
	if set, err := m.store.GetSettings(); err == nil {
		add(set.Host)
	}
	if nodes, err := m.store.ListNodes(); err == nil {
		for _, n := range nodes {
			add(n.Host)
		}
	}
	return out
}

// CanBanIP reports why ip may not be banned by the operator at adminIP, nil when it
// may.
func (m *Manager) CanBanIP(ip, adminIP string) error {
	_, err := m.banChecker(adminIP)(ip)
	return err
}

// BanChecker returns the check for many addresses at once (a user's address list).
func (m *Manager) BanChecker(adminIP string) func(ip string) error {
	check := m.banChecker(adminIP)
	return func(ip string) error {
		_, err := check(ip)
		return err
	}
}

// BanIP bans an address by hand, everywhere, until it is lifted. userID is whose
// address list it was banned from (0 for none). It returns the address as recorded.
func (m *Manager) BanIP(ip string, userID int64, adminIP string) (string, error) {
	m.banMu.Lock()
	defer m.banMu.Unlock()
	addr, err := m.banChecker(adminIP)(ip)
	if err != nil {
		return "", err
	}
	canon := addr.String()
	if err := m.store.BanIP(model.IPBan{IP: canon, UserID: userID, At: time.Now().Unix()}); err != nil {
		return "", err
	}
	if err := m.ipBan.BlockIP(canon); err != nil {
		logErr("ban: firewall block failed", "ip", canon, "err", err)
	}
	logWarn("ban: address banned", "ip", canon, "user", userID)
	m.notifyNodes() // so the nodes drop it too
	return canon, nil
}

// UnbanIP lifts every ban the panel holds on an address: a manual ban, the source
// policy's block, the brute-force guard's and the scanner block's. It reports whether
// there was any.
//
// Every source is tried even when one fails: an operator letting an address back in
// wants every block that can come off to come off, and hears about the one that did
// not.
func (m *Manager) UnbanIP(ip string) (bool, error) {
	addr, err := parseBanAddr(ip)
	if err != nil {
		return false, invalidCode("err.banBadIP", "{{ip}}: это не IP-адрес", map[string]any{"ip": ip})
	}
	canon := addr.String()
	m.banMu.Lock()
	defer m.banMu.Unlock()
	var errs []error
	manual, err := m.store.UnbanIP(canon)
	if err != nil {
		errs = append(errs, err)
	}
	if err := m.ipBan.UnblockIP(canon); err != nil {
		logErr("ban: firewall unblock failed", "ip", canon, "err", err)
	}
	if manual {
		m.notifyNodes()
	}
	policy, err := m.UnblockIP(canon)
	if err != nil {
		errs = append(errs, err)
	}
	brute := m.guard != nil && m.guard.lift(canon)
	probe := liftFrom(m.probeBlock, canon, "scanner")
	gone := manual || policy || brute || probe
	if gone {
		logInfo("ban: address let back in", "ip", canon)
	}
	return gone, errors.Join(errs...)
}

// liftFrom removes an address from one kernel-only block table and reports whether it
// was there.
func liftFrom(b *ipblock.Blocker, ip, what string) bool {
	ips, err := b.Addresses()
	if err != nil || !slices.Contains(ips, ip) {
		return false
	}
	if err := b.UnblockIP(ip); err != nil {
		logErr("ban: could not lift a block", "table", what, "ip", ip, "err", err)
	}
	return true
}

// Bans lists every address the panel drops at the firewall, whatever put it there,
// newest first. An address held by more than one ban is listed once per ban.
func (m *Manager) Bans() ([]model.Ban, error) {
	var out []model.Ban
	manual, err := m.store.ListIPBans()
	if err != nil {
		return nil, err
	}
	for _, b := range manual {
		out = append(out, model.Ban{IP: b.IP, Source: model.BanManual, UserID: b.UserID, At: b.At})
	}
	policy, err := m.store.ListBlockedIPs(200)
	if err != nil {
		return nil, err
	}
	for _, b := range policy {
		out = append(out, model.Ban{
			IP: b.IP, Source: b.Reason, UserID: b.UserID,
			Country: b.Country, ASN: b.ASN, Org: b.Org, At: b.At, Until: b.Until,
		})
	}
	// The other two live only in this machine's kernel, which knows how long each
	// has left; when each began follows from the length of the ban.
	now := time.Now()
	kernel := func(b *ipblock.Blocker, source string, ttl time.Duration) {
		entries, err := b.Entries()
		if err != nil {
			logErr("ban: could not read a block table", "source", source, "err", err)
			return
		}
		for _, e := range entries {
			until := now.Add(e.Expires)
			out = append(out, model.Ban{IP: e.IP, Source: source, At: until.Add(-ttl).Unix(), Until: until.Unix()})
		}
	}
	if m.guard != nil {
		kernel(m.guard.blocker, model.BanBrute, bruteBanTime)
	}
	kernel(m.probeBlock, model.BanProbe, ipblock.DefaultTTL)

	names := map[int64]string{}
	for i := range out {
		// Where the address is, for the bans that did not record it (the source policy's
		// did): the same in-memory tables the policy decides by.
		if out[i].Country == "" && out[i].ASN == 0 {
			out[i].Country = m.CountryOfIP(out[i].IP)
			out[i].ASN, out[i].Org = m.ASNOfIP(out[i].IP)
		}
		id := out[i].UserID
		if id <= 0 {
			continue
		}
		name, known := names[id]
		if !known {
			if u, err := m.store.GetUser(id); err == nil && u != nil {
				name = u.Name
			}
			names[id] = name
		}
		out[i].UserName = name
	}
	slices.SortStableFunc(out, func(a, b model.Ban) int {
		switch {
		case a.At > b.At:
			return -1
		case a.At < b.At:
			return 1
		}
		return strings.Compare(a.IP, b.IP)
	})
	return out, nil
}

// bannedAddresses is every address a ban holds right now, for marking a user's
// addresses.
func (m *Manager) bannedAddresses() map[string]bool {
	out := map[string]bool{}
	if ips, err := m.store.BannedIPList(); err == nil {
		for _, ip := range ips {
			out[ip] = true
		}
	}
	if ips, err := m.store.BlockedIPList(); err == nil {
		for _, ip := range ips {
			out[ip] = true
		}
	}
	for _, b := range []*ipblock.Blocker{m.probeBlock, m.guardBlocker()} {
		if ips, err := b.Addresses(); err == nil {
			for _, ip := range ips {
				out[ip] = true
			}
		}
	}
	return out
}

func (m *Manager) guardBlocker() *ipblock.Blocker {
	if m.guard == nil {
		return nil
	}
	return m.guard.blocker
}

// ApplyIPBansAtBoot puts the recorded bans back into this machine's firewall: the
// kernel forgets them on a reboot.
func (m *Manager) ApplyIPBansAtBoot() { m.ResyncIPBans() }

// ResyncIPBans makes this machine's firewall hold exactly the recorded bans. Run at
// boot and on a timer: a ban has no timeout to heal a block that did not land — nft
// failing for a moment, a table flushed by hand — so the record is re-applied instead.
func (m *Manager) ResyncIPBans() {
	m.banMu.Lock()
	defer m.banMu.Unlock()
	ips, err := m.store.BannedIPList()
	if err != nil {
		logErr("ban: could not read the ban list", "err", err)
		return
	}
	if err := m.ipBan.Sync(ips); err != nil {
		logErr("ban: could not install the ban list", "err", err)
	}
}

// liftTrustedBans lifts the manual bans inside networks an operator has just trusted.
func (m *Manager) liftTrustedBans(nets model.TrustedNets) {
	m.banMu.Lock()
	defer m.banMu.Unlock()
	ips, err := m.store.BannedIPList()
	if err != nil {
		logErr("trusted: could not read the ban list", "err", err)
		return
	}
	lifted := false
	for _, ip := range ips {
		if !nets.Contains(ip) {
			continue
		}
		if _, err := m.store.UnbanIP(ip); err != nil {
			logErr("trusted: could not lift a ban", "ip", ip, "err", err)
			continue
		}
		if err := m.ipBan.UnblockIP(ip); err != nil {
			logErr("trusted: could not lift a ban", "ip", ip, "err", err)
		}
		lifted = true
	}
	if lifted {
		m.notifyNodes()
	}
}

// hostAddrs resolves host names to addresses, cached for a while: the refusal to ban a
// server's address reads every node's host on each check, and a name that fails to
// resolve must not stall the request.
type hostAddrs struct {
	resolve func(ctx context.Context, host string) ([]netip.Addr, error)
	mu      sync.Mutex
	cache   map[string]hostEntry
}

type hostEntry struct {
	addrs []netip.Addr
	at    time.Time
}

const (
	hostAddrsTTL     = 10 * time.Minute
	hostAddrsTimeout = 2 * time.Second
)

// newHostAddrs returns a resolver; nil resolve uses the system's.
func newHostAddrs(resolve func(ctx context.Context, host string) ([]netip.Addr, error)) *hostAddrs {
	if resolve == nil {
		resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	return &hostAddrs{resolve: resolve, cache: map[string]hostEntry{}}
}

// lookup returns the addresses a host name resolves to, empty when it does not. A nil
// resolver resolves nothing (a test manager).
func (h *hostAddrs) lookup(host string) []netip.Addr {
	if h == nil || host == "" {
		return nil
	}
	h.mu.Lock()
	e, ok := h.cache[host]
	h.mu.Unlock()
	if ok && time.Since(e.at) < hostAddrsTTL {
		return e.addrs
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostAddrsTimeout)
	defer cancel()
	found, err := h.resolve(ctx, host)
	var addrs []netip.Addr
	if err == nil {
		for _, a := range found {
			addrs = append(addrs, a.Unmap().WithZone(""))
		}
	}
	h.mu.Lock()
	h.cache[host] = hostEntry{addrs: addrs, at: time.Now()}
	h.mu.Unlock()
	return addrs
}
