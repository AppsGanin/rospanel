// Package selftest answers the question a health page can't: not "is Xray
// running?" but "does a client actually get through?". For each enabled protocol
// it spins up a throwaway Xray client — a local SOCKS inbound in front of that
// protocol's outbound, pointed back at our own inbound — and sends real traffic
// through it to the public internet. Success means the credentials, TLS, ALPN and
// transport all line up end-to-end and traffic egresses through this server.
//
// What it does NOT prove: reachability from the outside world. The probe dials the
// inbound over loopback, so the host's own firewall and the provider's edge (the
// usual reason a fresh Hysteria2/UDP port "doesn't work") are bypassed. This is the
// automated form of the `curl -x socks5h://…` check you'd otherwise run by hand —
// it catches config mismatches and dead protocols, not blocked ports.
package selftest

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/net/proxy"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/netinfo"
)

// Result is one protocol's verdict.
type Result struct {
	Proto  string `json:"proto"`  // model.Proto* key
	Label  string `json:"label"`  // human name for the UI
	OK     bool   `json:"ok"`     // traffic made it out and back
	Detail string `json:"detail"` // one-line explanation, always set
	ExitIP string `json:"exit_ip,omitempty"`
}

// perProtoTimeout bounds one protocol's whole attempt — spawn Xray, wait for the
// SOCKS port, dial out, read the response. Generous because a cold QUIC/REALITY
// handshake plus a round-trip to the echo endpoint can be slow on a small VPS.
const perProtoTimeout = 15 * time.Second

// Run tests every enabled protocol for user u and returns one Result each, in a
// stable order. binPath is the Xray binary (shared with the supervisor); an empty
// path yields a single error result, since there's nothing to spawn.
func Run(ctx context.Context, binPath string, set *model.Settings, u model.User) []Result {
	if binPath == "" {
		return []Result{{OK: false, Detail: "бинарник Xray не найден — проверка недоступна"}}
	}

	// The server's own public IP, resolved once, is the baseline for "did traffic go
	// out directly?". Prefer the configured host when it's already an IP (the bare-IP
	// install) to avoid a network call; otherwise ask the network. Empty ⇒ we simply
	// don't annotate the egress, rather than guess.
	serverIP := serverPublicIP(set)

	var results []Result
	for _, p := range enabledProtocols(set) {
		results = append(results, runOne(ctx, binPath, p, set, u, serverIP))
	}
	if len(results) == 0 {
		return []Result{{OK: false, Detail: "нет включённых протоколов для проверки"}}
	}
	return results
}

// serverPublicIP returns the address traffic egresses from when it goes out
// directly, so the probe can tell a direct exit from one routed through a WARP/Opera
// lane or a second interface.
func serverPublicIP(set *model.Settings) string {
	if net.ParseIP(strings.TrimSpace(set.Host)) != nil {
		return strings.TrimSpace(set.Host)
	}
	return netinfo.PublicIP()
}

// protoSpec pairs a protocol key with the client-config builder that dials it.
type protoSpec struct {
	key   string
	build func(*model.Settings, model.User, int) (*clientConfig, error)
}

// enabledProtocols lists the protocols to test, skipping ones turned off in
// settings. Order is fixed so the UI doesn't reshuffle between runs.
func enabledProtocols(set *model.Settings) []protoSpec {
	var specs []protoSpec
	if set.VLESSEnabled {
		specs = append(specs, protoSpec{model.ProtoVLESS, buildVLESS})
	}
	if set.TrojanEnabled {
		specs = append(specs, protoSpec{model.ProtoTrojan, buildTrojan})
	}
	if set.HysteriaEnabled {
		specs = append(specs, protoSpec{model.ProtoHysteria, buildHysteria})
	}
	if set.RealityEnabled {
		specs = append(specs, protoSpec{model.ProtoReality, buildReality})
	}
	return specs
}

func runOne(ctx context.Context, binPath string, spec protoSpec, set *model.Settings, u model.User, serverIP string) Result {
	label := set.ProtoLabel(spec.key)
	res := Result{Proto: spec.key, Label: label}

	socksPort, err := freePort()
	if err != nil {
		res.Detail = "не удалось выделить локальный порт: " + err.Error()
		return res
	}

	cfg, err := spec.build(set, u, socksPort)
	if err != nil {
		res.Detail = "внутренняя ошибка сборки конфига: " + err.Error()
		return res
	}

	pctx, cancel := context.WithTimeout(ctx, perProtoTimeout)
	defer cancel()

	stderr, err := spawnXray(pctx, binPath, cfg)
	if err != nil {
		res.Detail = "не удалось запустить проверочный Xray: " + err.Error()
		return res
	}
	defer stderr.stop()

	if err := waitForPort(pctx, socksPort); err != nil {
		res.Detail = "проверочный Xray не открыл локальный порт: " + firstXrayError(stderr.text())
		return res
	}

	ip, err := probeThrough(pctx, socksPort)
	if err != nil {
		res.Detail = explainFailure(spec.key, err, stderr.text())
		return res
	}

	res.OK = true
	res.ExitIP = ip
	res.Detail = describeExit(ip, serverIP)
	return res
}

// describeExit phrases the success line, noting when traffic left through something
// other than the server's own address. A mismatch isn't a fault — it's exactly what
// a WARP/Opera lane or a multi-IP host looks like — so the wording stays neutral and
// just tells the operator what they're seeing.
func describeExit(exitIP, serverIP string) string {
	switch {
	case exitIP == "":
		return "трафик проходит"
	case serverIP != "" && exitIP != serverIP:
		return "трафик проходит, выход через " + exitIP +
			" — не прямой адрес сервера (полоса WARP/Opera, прокси или второй IP)"
	default:
		return "трафик проходит, прямой выход (" + exitIP + ")"
	}
}

// probeThrough dials the public internet through the local SOCKS proxy and returns
// the egress IP the far end saw. Any of the endpoints succeeding is proof the
// tunnel works; the IP is a bonus for the UI (it reveals which egress — direct,
// WARP, Opera lane — the traffic actually took).
func probeThrough(ctx context.Context, socksPort int) (string, error) {
	dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", socksPort), nil, proxy.Direct)
	if err != nil {
		return "", err
	}
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			},
			DisableKeepAlives: true,
		},
	}

	var lastErr error
	for _, ep := range echoEndpoints {
		ip, err := fetchIP(ctx, client, ep)
		if err == nil {
			return ip, nil
		}
		lastErr = err
	}
	return "", lastErr
}

// echoEndpoints return the caller's public IP as a bare string. Ordered by
// reliability; the probe stops at the first that answers. All are plain, widely
// reachable IP-echo services — if none answer through the tunnel, the tunnel is the
// problem, not the endpoint.
var echoEndpoints = []string{
	"https://api.ipify.org",
	"https://checkip.amazonaws.com",
	"https://icanhazip.com",
}

func fetchIP(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("echo endpoint returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(body))
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("echo endpoint did not return an IP")
	}
	return ip, nil
}

// freePort asks the kernel for an unused loopback TCP port and hands it back. The
// tiny window between closing the listener and Xray binding it is a theoretical
// race; on a panel host nothing else is grabbing ephemeral ports in that instant.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// waitForPort blocks until the SOCKS port accepts a connection or ctx expires —
// Xray needs a moment to parse its config and bind. Polls rather than sleeps a
// fixed time so a fast start returns fast and a slow one still gets its full budget.
func waitForPort(ctx context.Context, port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// explainFailure turns a raw probe error into an operator-facing sentence, folding
// in Xray's own stderr when it points at the real cause (bad auth, TLS mismatch).
func explainFailure(proto string, probeErr error, xrayLog string) string {
	if x := firstXrayError(xrayLog); x != "" {
		return "трафик не проходит: " + x
	}
	// REALITY fails in a way that leaves no keyword-matched error line: the client
	// just gets EOF because the donor handshake never completes (wrong/unreachable
	// dest, or a donor cert too big for this Xray — issue #6402). A bare timeout on
	// REALITY almost always means the dest, so name it instead of the generic hint.
	if proto == model.ProtoReality && isHandshakeFailure(probeErr, xrayLog) {
		return "трафик не проходит: REALITY-хендшейк не завершился — проверьте, что донор (dest) " +
			"доступен, отдаёт TLS 1.3 и его сертификат не слишком большой (issue #6402)"
	}
	if strings.Contains(probeErr.Error(), context.DeadlineExceeded.Error()) ||
		strings.Contains(probeErr.Error(), "timeout") {
		return "трафик не проходит: истекло время ожидания ответа (порт закрыт снаружи, брандмауэр или неверные параметры)"
	}
	return "трафик не проходит: " + probeErr.Error()
}

// isHandshakeFailure reports whether the failure looks like a stalled TLS/REALITY
// handshake — an EOF or a plain timeout with nothing else to go on — rather than a
// clean error Xray already named.
func isHandshakeFailure(probeErr error, xrayLog string) bool {
	e := strings.ToLower(probeErr.Error())
	if strings.Contains(e, "eof") || strings.Contains(strings.ToLower(xrayLog), "eof") {
		return true
	}
	return strings.Contains(e, context.DeadlineExceeded.Error()) || strings.Contains(e, "timeout")
}

// firstXrayError extracts the first line of Xray output that reads like a failure,
// so the UI can show "auth failed" instead of a generic timeout. Empty when the log
// carries nothing actionable.
func firstXrayError(log string) string {
	for _, line := range strings.Split(log, "\n") {
		l := strings.TrimSpace(line)
		low := strings.ToLower(l)
		if strings.Contains(low, "failed") || strings.Contains(low, "rejected") ||
			strings.Contains(low, "invalid") || strings.Contains(low, "error") {
			// Xray prefixes lines with a timestamp; drop it for a cleaner message.
			if i := strings.Index(l, " "); i > 0 && strings.Count(l[:i], "/") == 2 {
				if j := strings.Index(l[i+1:], " "); j > 0 {
					l = strings.TrimSpace(l[i+1+j:])
				}
			}
			return l
		}
	}
	return ""
}

// captured collects a child process's output into a bounded buffer and shuts the
// process down. Bounded so a chatty Xray can't balloon memory during a probe.
type captured struct {
	cancel  context.CancelFunc
	cmd     *exec.Cmd
	buf     *ringBuffer
	cfgPath string
}

func (c *captured) text() string { return c.buf.String() }

func (c *captured) stop() {
	c.cancel()
	_ = c.cmd.Wait() // reap; context cancellation already signalled the kill
	// Remove the config only now: `xray run` reads it asynchronously after Start
	// returns, so deleting it any earlier races the child and it fails to load.
	if c.cfgPath != "" {
		_ = os.Remove(c.cfgPath)
	}
}

// spawnXray writes the client config to a temp .json (Xray picks format by
// extension) and starts `xray run` under ctx, capturing output. The temp file
// outlives Start and is removed in stop(), because Xray opens it asynchronously.
func spawnXray(ctx context.Context, binPath string, cfg *clientConfig) (*captured, error) {
	tmp, err := os.CreateTemp("", "rospanel-selftest-*.json")
	if err != nil {
		return nil, err
	}
	if _, err := tmp.Write(cfg.json); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}

	cctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cctx, binPath, "run", "-c", tmp.Name())
	// Xray prints config-validation errors to STDOUT, not stderr — capturing only
	// stderr loses exactly the message that explains a failed probe. Fold both into
	// one buffer so firstXrayError sees whichever stream the error landed on.
	buf := newRingBuffer(8 * 1024)
	cmd.Stdout = buf
	cmd.Stderr = buf
	if err := cmd.Start(); err != nil {
		cancel()
		os.Remove(tmp.Name())
		return nil, err
	}
	return &captured{cancel: cancel, cmd: cmd, buf: buf, cfgPath: tmp.Name()}, nil
}
