package awg

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/amnezia-vpn/amneziawg-go/v3/conn"
	"github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun/tuntest"
)

// Everything else in this package checks the TEXT we generate. This checks that
// the protocol engine accepts it and that two ends configured from it actually
// complete a handshake — over a real UDP socket, with the imitation chain, the
// header protection and the spread timers all live.
//
// It exists because the failure it guards against is silent and total: a
// parameter the engine refuses, or one it accepts and then cannot handshake
// under, produces a tunnel that comes up in the log and answers nobody. Reading
// the config file back would not have caught either.
func TestTwoDevicesHandshakeOnGeneratedParameters(t *testing.T) {
	for range 5 { // a fresh draw each time: the profile and every length vary
		p := RandomParams()
		if err := p.Validate(); err != nil {
			t.Fatalf("generated params invalid: %v\n%+v", err, p)
		}
		handshakeOnce(t, p)
	}
}

func handshakeOnce(t *testing.T, p Params) {
	t.Helper()
	srvPriv, srvPub, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	cliPriv, cliPub, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	cliAddr, _ := ClientAddr(7)

	// The server, exactly as the panel configures it.
	srvCfg := Config{PrivateKey: srvPriv, ListenPort: freeUDPPort(t), Params: p,
		Peers: []Peer{{PublicKey: cliPub, Addr: cliAddr, Email: "u7"}}}
	srvIPC, err := srvCfg.UAPI()
	if err != nil {
		t.Fatalf("server uapi: %v", err)
	}
	srv := newDevice(t, "server: ")
	if err := srv.IpcSet(srvIPC); err != nil {
		t.Fatalf("the engine refused the server configuration: %v\n%s", err, srvIPC)
	}
	if err := srv.Up(); err != nil {
		t.Fatal(err)
	}

	// The client, from the very file a user downloads — parsed back out of it, so
	// this covers the renderer too and not just the struct behind it.
	conf := ClientConfig{PrivateKey: cliPriv, Address: cliAddr, Params: p,
		ServerPublicKey: srvPub, Endpoint: fmt.Sprintf("127.0.0.1:%d", srvCfg.ListenPort)}.Render()
	cli := newDevice(t, "client: ")
	cliIPC, err := clientIPC(conf)
	if err != nil {
		t.Fatalf("the rendered config did not read back: %v\n%s", err, conf)
	}
	if err := cli.IpcSet(cliIPC); err != nil {
		t.Fatalf("the engine refused the client configuration: %v\n%s\n--- from ---\n%s", err, cliIPC, conf)
	}
	if err := cli.Up(); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		dump, err := cli.IpcGet()
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(dump, "\n") {
			if hs, ok := strings.CutPrefix(line, "last_handshake_time_sec="); ok && hs != "0" {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no handshake in 15s with:\n%s", conf)
}

func newDevice(t *testing.T, prefix string) *device.Device {
	t.Helper()
	// A channel TUN: the question is whether the two ends agree on the wire, not
	// what rides inside afterwards.
	tun := tuntest.NewChannelTUN()
	d := device.NewDevice(tun.TUN(), conn.NewDefaultBind(), device.NewLogger(device.LogLevelSilent, prefix))
	t.Cleanup(d.Close)
	return d
}

// freeUDPPort borrows a port from the kernel and hands it straight back, so two
// runs of this test in parallel do not land on the same one.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := c.LocalAddr().(*net.UDPAddr).Port
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

// clientIPC turns the rendered config file into the engine's IPC form, the way a
// client app's config parser does.
func clientIPC(conf string) (string, error) {
	var b strings.Builder
	section := ""
	for _, line := range strings.Split(conf, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.Trim(line, "[]"))
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch {
		case section == "interface" && k == "privatekey":
			h, err := keyHex(v)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "private_key=%s\n", h)
		case section == "interface" && k == "headerprotectionkey":
			h, err := keyHex(v)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "header_protection_key=%s\n", h)
		case section == "interface" && k == "randomtrailers":
			fmt.Fprintf(&b, "random_trailers=%v\n", v == "on")
		case section == "interface":
			if ipc, ok := confToIPC[k]; ok {
				fmt.Fprintf(&b, "%s=%s\n", ipc, v)
			}
		case section == "peer" && k == "publickey":
			h, err := keyHex(v)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "public_key=%s\n", h)
		case section == "peer" && k == "endpoint":
			fmt.Fprintf(&b, "endpoint=%s\n", v)
		case section == "peer" && k == "allowedips":
			for _, a := range strings.Split(v, ",") {
				a = strings.TrimSpace(a)
				if _, err := netip.ParsePrefix(a); err == nil {
					fmt.Fprintf(&b, "allowed_ip=%s\n", a)
				}
			}
		case section == "peer" && k == "persistentkeepalive":
			fmt.Fprintf(&b, "persistent_keepalive_interval=%s\n", v)
		}
	}
	return b.String(), nil
}

// The config-file key each engine key is spelled as. Anything not here is not a
// parameter (Address, DNS, MTU are the client's own business).
var confToIPC = map[string]string{
	"jc": "jc", "jmin": "jmin", "jmax": "jmax",
	"s1": "s1", "s2": "s2", "s3": "s3", "s4": "s4",
	"h1": "h1", "h2": "h2", "h3": "h3", "h4": "h4",
	"i1": "i1", "i2": "i2",
	"contentpaddingaddition": "content_padding_addition",
	"rekeyaftertime":         "rekey_after_time",
	"rekeytimeout":           "rekey_timeout",
	"rejectaftertime":        "reject_after_time",
	"keepalivetimeout":       "keepalive_timeout",
	"maxhandshakeattempts":   "max_handshake_attempts",
}

// The decoys have to be on the wire, not merely in the config. This points a
// client at a bare UDP socket and reads what it actually sends before the
// handshake: the profile's packets, in order, at the sizes the protocol they
// imitate would have. A parameter that is carried faithfully all the way to the
// engine and then not emitted would pass every other test in this package.
func TestTheImitationPacketsGoOnTheWire(t *testing.T) {
	for _, im := range imitations() {
		t.Run(im.Name, func(t *testing.T) {
			sock, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer sock.Close()

			p := RandomParams()
			p.I1, p.I2 = im.I1, im.I2
			// Junk packets would land in the same stream and are not what this
			// test is about; the decoys are the first thing sent either way.
			p.Jc = 0
			if err := p.Validate(); err != nil {
				t.Fatal(err)
			}
			cliPriv, _, _ := GenerateKey()
			_, srvPub, _ := GenerateKey()
			addr, _ := ClientAddr(7)
			conf := ClientConfig{PrivateKey: cliPriv, Address: addr, Params: p,
				ServerPublicKey: srvPub, Endpoint: sock.LocalAddr().String()}.Render()
			ipc, err := clientIPC(conf)
			if err != nil {
				t.Fatal(err)
			}
			cli := newDevice(t, "client: ")
			if err := cli.IpcSet(ipc); err != nil {
				t.Fatalf("engine refused: %v\n%s", err, ipc)
			}
			if err := cli.Up(); err != nil {
				t.Fatal(err)
			}

			want := []struct {
				spec string
				size int
			}{{im.I1, chainLen(t, im.I1)}, {im.I2, chainLen(t, im.I2)}}
			buf := make([]byte, 2048)
			for i, w := range want {
				if err := sock.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
					t.Fatal(err)
				}
				n, _, err := sock.ReadFrom(buf)
				if err != nil {
					t.Fatalf("I%d never arrived: %v", i+1, err)
				}
				if n != w.size {
					t.Fatalf("I%d is %d bytes on the wire, want %d (%s)", i+1, n, w.size, w.spec)
				}
				if err := matchesChain(buf[:n], w.spec); err != nil {
					t.Errorf("I%d does not look like %s: %v\n% x", i+1, im.Name, err, buf[:n])
				}
			}
		})
	}
}

// matchesChain checks a datagram against the fixed parts of the chain that made
// it. The random runs are skipped — there is nothing to compare them to, which is
// the point of them — but every literal byte the protocol fixes must be there, in
// place, or the packet does not look like what it claims to.
func matchesChain(pkt []byte, spec string) error {
	at, rest := 0, spec
	for rest != "" {
		end := strings.IndexByte(rest, '>')
		parts := strings.Fields(rest[1:end])
		rest = rest[end+1:]
		switch parts[0] {
		case "b":
			want, err := hex.DecodeString(strings.TrimPrefix(parts[1], "0x"))
			if err != nil {
				return err
			}
			if !bytes.Equal(pkt[at:at+len(want)], want) {
				return fmt.Errorf("at byte %d: have % x, want % x", at, pkt[at:at+len(want)], want)
			}
			at += len(want)
		case "t":
			// A timestamp within a minute of now, which is what makes it a
			// timestamp rather than four more random bytes.
			ts := int64(binary.BigEndian.Uint32(pkt[at : at+4]))
			if d := time.Since(time.Unix(ts, 0)); d < -time.Minute || d > time.Minute {
				return fmt.Errorf("at byte %d: %d is not a current timestamp", at, ts)
			}
			at += 4
		case "rc":
			n, _ := strconv.Atoi(parts[1])
			for _, c := range pkt[at : at+n] {
				if !unicode.IsLetter(rune(c)) {
					return fmt.Errorf("at byte %d: %q is not a letter", at, c)
				}
			}
			at += n
		default:
			n, _ := strconv.Atoi(parts[1])
			at += n
		}
	}
	if at != len(pkt) {
		return fmt.Errorf("chain describes %d bytes, packet is %d", at, len(pkt))
	}
	return nil
}
