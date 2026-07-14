package nodeagent

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AppsGanin/rospanel/internal/connguard"
	"github.com/AppsGanin/rospanel/internal/decoy"
	"github.com/AppsGanin/rospanel/internal/geo"
	"github.com/AppsGanin/rospanel/internal/hop"
	"github.com/AppsGanin/rospanel/internal/logbuf"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/opera"
	"github.com/AppsGanin/rospanel/internal/proxyproto"
	"github.com/AppsGanin/rospanel/internal/tlsmgr"
	"github.com/AppsGanin/rospanel/internal/tlsutil"
	"github.com/AppsGanin/rospanel/internal/tuning"
	"github.com/AppsGanin/rospanel/internal/updater"
	"github.com/AppsGanin/rospanel/internal/version"
	"github.com/AppsGanin/rospanel/internal/xray"
)

const (
	// syncTimeout bounds one long-poll: the panel holds ≤45s, so 90s leaves ample
	// headroom for the round trip before we consider the request stuck.
	syncTimeout = 90 * time.Second
	// backoffMin/Max bound the reconnect backoff when the panel is unreachable.
	backoffMin = 2 * time.Second
	backoffMax = 60 * time.Second
	// revokedPoll is the slow cadence a revoked node keeps checking in at, in case
	// it is re-enabled.
	revokedPoll = 60 * time.Second
	// statsInterval is how often the agent samples Xray traffic counters.
	statsInterval = 60 * time.Second
	// certRetryFast is how often the agent retries ACME while it has no CA cert yet
	// (still on the self-signed fallback); certRenewSlow is the cadence once a real
	// cert is in place (renewal is driven off the cert's own lifetime inside tlsmgr).
	certRetryFast = 3 * time.Minute
	certRenewSlow = 6 * time.Hour
)

// Agent is the running node: it owns the local Xray supervisor and the decoy
// server, holds the long-poll to the panel, and reports traffic back.
type Agent struct {
	dataDir  string
	ident    *Identity
	client   *http.Client
	sup      *xray.Supervisor
	certPath string
	keyPath  string
	acmeDir  string
	geoDir   string
	certMu   sync.Mutex // serializes cert-file writes (applyState vs certLoop)

	state   *persistState
	stateMu sync.Mutex

	// Opera VPN egress helper (opera-proxy). Off unless the panel enables it for this
	// node. operaCountry/operaPort track the last-applied config so a repeated apply
	// with the same settings doesn't needlessly restart the helper.
	operaSup     *opera.Supervisor
	operaDir     string
	operaMu      sync.Mutex
	operaOn      bool
	operaCountry string
	operaPort    int

	// decoy server on the loopback fallback dest. The listener stays up for the
	// agent's life; decoyHandler is swapped when the template changes.
	decoySrv     *http.Server
	decoyHandler atomic.Pointer[http.Handler]
	decoyTmpl    string
	decoyMu      sync.Mutex

	// Traffic accounting. Deltas accumulate into `pending`; when a sync goes out and
	// nothing is in flight, `pending` is promoted to `inflight` with a fresh id.
	// `inflight` is resent verbatim (same id) until acked, so a lost response never
	// double-counts (the panel dedups by id) and never loses new traffic (it keeps
	// piling into `pending`). See buildSyncRequest / ackReport.
	statsMu      sync.Mutex
	lastCounters map[string]xray.Traffic         // last raw Xray counter per user email
	pending      map[int64]*nodeapi.TrafficDelta // accumulated, not yet sent
	inflight     map[int64]*nodeapi.TrafficDelta // sent, awaiting ack
	inflightID   int64                           // report id of inflight (0 = none)
	reportSeq    int64                           // monotonic report-id source

	// Connection samples for fleet-wide device counting: distinct (email, ip) pairs
	// tapped from Xray's access log since the last sync. Snapshotted-and-cleared on
	// each sync; a lost send just re-samples on the next connection (the panel upserts
	// idempotently), so no ack/inflight machinery is needed.
	connMu sync.Mutex
	conns  map[string]nodeapi.ConnSample

	logsWanted atomic.Bool // panel asked for the log tail on the next sync
}

// Run loads the node identity and runs the agent until the context is cancelled
// (SIGTERM). It is the body of `rospanel node run`.
func Run(ctx context.Context, dataDir string) error {
	ident, err := LoadIdentity(dataDir)
	if err != nil {
		return err
	}
	a, err := newAgent(dataDir, ident)
	if err != nil {
		return err
	}
	slog.Info("node agent: starting", "node_id", ident.NodeID, "panel", ident.PanelURL, "version", version.Version)

	// Best-effort host tuning (same as the panel).
	if state, _ := tuning.EnsureBBR(); state == tuning.BBREnabled {
		slog.Info("node: BBR enabled")
	}
	// Re-apply the last known-good config on boot so the node serves immediately,
	// even before the first successful sync (or if the panel is down).
	if a.state.LastConfig != nil {
		if err := a.applyState(a.state.LastConfig); err != nil {
			slog.Warn("node: re-applying saved config failed", "err", err)
		}
	}

	go a.statsLoop(ctx)
	go a.certLoop(ctx) // retry ACME + reload Xray when the real cert lands
	go a.geoLoop(ctx)  // auto-refresh geo databases on the panel-pushed cadence
	a.syncLoop(ctx)
	a.shutdown()
	return nil
}

// geoLoop auto-refreshes the node's geo databases on the cadence the panel pushes
// (meta.GeoRefreshHours; 0 ⇒ off), then reloads Xray so routing rules pick up the
// new data. Sleeps first — the agent already ensured geo exists at apply time.
func (a *Agent) geoLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
		meta, ok := a.currentMeta()
		if !ok || meta.GeoRefreshHours <= 0 {
			continue
		}
		if !geoStale(a.geoDir, time.Duration(meta.GeoRefreshHours)*time.Hour) {
			continue
		}
		if err := geo.Refresh(a.geoDir); err != nil {
			slog.Warn("node: geo auto-refresh failed", "err", err)
			continue
		}
		slog.Info("node: geo auto-refreshed", "cadence_hours", meta.GeoRefreshHours)
		if err := a.sup.Restart(); err != nil {
			slog.Warn("node: xray restart after geo refresh failed", "err", err)
		}
	}
}

// geoStale reports whether any geo database in dir is missing or older than maxAge.
func geoStale(dir string, maxAge time.Duration) bool {
	cutoff := time.Now().Add(-maxAge).Unix()
	for _, f := range geo.Status(dir) {
		if !f.Present || f.ModifiedAt < cutoff {
			return true
		}
	}
	return false
}

func newAgent(dataDir string, ident *Identity) (*Agent, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	bin := resolveNodeXrayBin(filepath.Join(dataDir, "bin"))
	sup := xray.NewSupervisor(bin, filepath.Join(dataDir, "xray", "config.json"), filepath.Join(dataDir, "geo"))
	client := &http.Client{Timeout: syncTimeout}
	if ident.Insecure {
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // opt-in via --insecure
	}
	operaDir := filepath.Join(dataDir, "opera")
	a := &Agent{
		dataDir:      dataDir,
		ident:        ident,
		client:       client,
		sup:          sup,
		certPath:     filepath.Join(dataDir, "certs", "cert.pem"),
		keyPath:      filepath.Join(dataDir, "certs", "key.pem"),
		acmeDir:      filepath.Join(dataDir, "acme"),
		geoDir:       filepath.Join(dataDir, "geo"),
		operaSup:     opera.New(filepath.Join(operaDir, "opera-proxy")),
		operaDir:     operaDir,
		state:        loadState(dataDir),
		lastCounters: map[string]xray.Traffic{},
		pending:      map[int64]*nodeapi.TrafficDelta{},
		inflight:     map[int64]*nodeapi.TrafficDelta{},
		conns:        map[string]nodeapi.ConnSample{},
	}
	// Tap Xray's access log so the panel can count this node's devices (mirrors the
	// master's sup.SetOnAccess(RecordAccess)).
	a.sup.SetOnAccess(a.recordConn)
	return a, nil
}

// recordConn buffers one access-log connection (a "uN" email + source IP) for the
// next sync. Deduped per (email, ip); the buffer is bounded so a flood can't grow it
// without limit.
func (a *Agent) recordConn(email, ip string) {
	if !strings.HasPrefix(email, "u") || ip == "" {
		return
	}
	a.connMu.Lock()
	if len(a.conns) < 8192 {
		a.conns[email+"\x00"+ip] = nodeapi.ConnSample{Email: email, IP: ip}
	}
	a.connMu.Unlock()
}

// takeConns snapshots and clears the buffered connection samples.
func (a *Agent) takeConns() []nodeapi.ConnSample {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	if len(a.conns) == 0 {
		return nil
	}
	out := make([]nodeapi.ConnSample, 0, len(a.conns))
	for _, c := range a.conns {
		out = append(out, c)
	}
	a.conns = map[string]nodeapi.ConnSample{}
	return out
}

// syncLoop holds the long-poll to the panel, applying pushed config and handling
// revocation. Backs off when the panel is unreachable; keeps serving throughout.
func (a *Agent) syncLoop(ctx context.Context) {
	backoff := backoffMin
	applyBackoff := backoffMin
	for {
		if ctx.Err() != nil {
			return
		}
		resp, err := a.syncOnce(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("node: sync failed (keeping last config)", "err", err)
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, backoffMax)
			continue
		}
		backoff = backoffMin

		if resp.Revoked {
			slog.Warn("node: revoked by panel — stopping Xray, will keep checking in")
			a.sup.Stop()
			if !sleepCtx(ctx, revokedPoll) {
				return
			}
			continue
		}
		if resp.PanelURL != "" && resp.PanelURL != a.ident.PanelURL {
			if validPanelURL(resp.PanelURL) {
				slog.Info("node: panel address changed", "new", resp.PanelURL)
				a.ident.PanelURL = resp.PanelURL
				_ = a.ident.Save(a.dataDir)
			} else {
				slog.Warn("node: ignoring malformed panel_url broadcast", "url", resp.PanelURL)
			}
		}
		// Ack BEFORE a possible self-update exit: the panel already ingested this
		// batch (that's what AckReport means), so clearing it here avoids re-sending
		// it and losing nothing if the process restarts for an update.
		a.ackReport(resp.AckReport)
		if resp.WantLogs {
			a.logsWanted.Store(true) // include the log tail in the next sync request
		}
		if resp.RefreshGeo {
			if err := geo.Refresh(a.geoDir); err != nil {
				slog.Warn("node: geo refresh (on request) failed", "err", err)
			} else if err := a.sup.Restart(); err != nil {
				slog.Warn("node: xray restart after geo refresh failed", "err", err)
			}
		}
		if resp.Update {
			if a.selfUpdate() {
				return // binary swapped; exit so systemd restarts the new one
			}
		}
		if resp.Changed && resp.State != nil {
			if err := a.applyState(resp.State); err != nil {
				// Don't persist a config we couldn't apply. The panel keeps returning
				// Changed=true immediately (our hash still differs), so back off here —
				// otherwise a config this node's Xray can't parse (e.g. version skew)
				// spins geo/ACME/`xray -test` every few seconds on both sides forever.
				slog.Error("node: applying pushed config failed — backing off", "err", err, "backoff", applyBackoff)
				if !sleepCtx(ctx, applyBackoff) {
					return
				}
				applyBackoff = min(applyBackoff*2, backoffMax)
				continue
			}
			applyBackoff = backoffMin
			a.setLastConfig(resp.State)
			slog.Info("node: applied new config", "hash", short(resp.State.Hash))
		}
		// Immediately loop for the next long-poll (the panel holds it if nothing changed).
	}
}

// syncOnce sends one long-poll with the current applied hash + pending traffic.
func (a *Agent) syncOnce(ctx context.Context) (*nodeapi.SyncResponse, error) {
	req := a.buildSyncRequest()
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.ident.syncURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.ident.Token)

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// A decoy/HTML response (wrong path or an unreachable/revoked-by-deletion
		// panel) isn't valid JSON → treated as "keep serving" by the caller's error
		// path. That is the intended behavior: only an explicit Revoked stops us.
		return nil, fmt.Errorf("panel returned HTTP %d", resp.StatusCode)
	}
	var out nodeapi.SyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode sync response: %w", err)
	}
	return &out, nil
}

// buildSyncRequest snapshots the current applied hash, cert fingerprint and the
// pending traffic deltas into a sync request, assigning a fresh report id.
func (a *Agent) buildSyncRequest() nodeapi.SyncRequest {
	a.stateMu.Lock()
	hash := ""
	if a.state.LastConfig != nil {
		hash = a.state.LastConfig.Hash
	}
	a.stateMu.Unlock()

	sha, selfSigned, certIssuer, certExpiresAt := a.certStatus()

	a.statsMu.Lock()
	// Nothing in flight and new traffic waiting → promote it to a fresh batch. An
	// unacked in-flight batch is resent unchanged (same id) instead.
	if len(a.inflight) == 0 && len(a.pending) > 0 {
		a.reportSeq++
		a.inflightID = a.reportSeq
		a.inflight = a.pending
		a.pending = map[int64]*nodeapi.TrafficDelta{}
	}
	var traffic []nodeapi.TrafficDelta
	for _, d := range a.inflight {
		traffic = append(traffic, *d)
	}
	rid := a.inflightID
	a.statsMu.Unlock()

	var logs []string
	if a.logsWanted.Swap(false) {
		logs = a.logTail()
	}

	return nodeapi.SyncRequest{
		ConfigHash:     hash,
		NodeVersion:    version.Version,
		XrayVersion:    a.sup.Version(),
		XrayRunning:    a.sup.Running(),
		CertSHA256:     sha,
		CertSelfSigned: selfSigned,
		CertIssuer:     certIssuer,
		CertExpiresAt:  certExpiresAt,
		ReportID:       rid,
		Traffic:        traffic,
		Conns:          a.takeConns(),
		Logs:           logs,
	}
}

// logTail returns the node's recent log lines: the agent's own log ring (its slog
// output is teed into logbuf.Default by main) plus the Xray process log tail.
func (a *Agent) logTail() []string {
	out := append([]string(nil), logbuf.Default.Tail()...)
	if xl := a.sup.LogTail(); len(xl) > 0 {
		out = append(out, "--- Xray ---")
		out = append(out, xl...)
	}
	return out
}

// certStatus returns the current cert's SHA-256 fingerprint and whether it is
// self-signed (Issuer == Subject), so the panel can pin links correctly.
func (a *Agent) certStatus() (sha string, selfSigned bool, issuer string, expiresAt int64) {
	sha, err := tlsutil.CertPinSHA256(a.certPath)
	if err != nil {
		return "", true, "", 0 // no cert yet → treat as untrusted
	}
	info, err := tlsutil.ReadCertInfo(a.certPath)
	if err != nil {
		return sha, true, "", 0
	}
	selfSigned = info.Issuer == "" || info.Issuer == info.Subject
	if !selfSigned {
		issuer = info.Issuer
	}
	return sha, selfSigned, issuer, info.NotAfter.Unix()
}

// applyState brings the node's host + Xray in line with the desired state: obtain
// a cert for its host, set up port-hopping / connection guard / geo, refresh the
// decoy, then apply the Xray config (cert-path sentinels substituted).
func (a *Agent) applyState(st *nodeapi.NodeState) error {
	m := st.Meta

	// Geo databases first — routing rules may reference geosite/geoip.
	if err := geo.Ensure(a.geoDir); err != nil {
		slog.Warn("node: geo databases", "err", err)
	}

	// Obtain the TLS cert for this node's host. Non-fatal: a self-signed fallback is
	// written so Xray still comes up, the panel pins it via the fingerprint we report,
	// and certLoop keeps retrying ACME and swaps in the real cert once it succeeds.
	a.ensureCert(m)

	// Port-hopping for Hysteria2 (best-effort; no-op off Linux / without nft).
	if m.HysteriaEnabled {
		if err := hop.Ensure(m.HopStart, m.HopEnd, m.HysteriaPort); err != nil {
			slog.Warn("node: port-hopping setup failed", "err", err)
		}
	}
	// Per-IP connection guard on the public TCP ports.
	if len(m.ConnGuardPorts) > 0 {
		if err := connguard.Ensure(m.ConnGuardPorts, connguard.DefaultLimits()); err != nil {
			slog.Warn("node: connection guard setup failed", "err", err)
		}
	}
	// Decoy server on the loopback fallback dest (the config's VLESS fallback points
	// here for non-VPN traffic).
	if err := a.ensureDecoy(m.LoopbackDest, m.DecoyTemplate); err != nil {
		slog.Warn("node: decoy server", "err", err)
	}

	// Opera VPN egress helper: bring it up/down to match the desired state. The
	// generated config's "opera" outbound already points at 127.0.0.1:OperaPort.
	a.syncOpera(m.OperaEnabled, m.OperaCountry, m.OperaPort)

	// Substitute the cert-path sentinels with the node's absolute paths and apply.
	if err := a.sup.ApplyRaw(substituteCertPaths(st.XrayConfig, a.certPath, a.keyPath)); err != nil {
		return fmt.Errorf("apply xray config: %w", err)
	}
	return nil
}

// ensureCert obtains (or renews) the node's TLS cert for the given host meta,
// falling back to self-signed if ACME is unavailable. Serialized so applyState and
// certLoop don't write the cert files concurrently.
func (a *Agent) ensureCert(m nodeapi.NodeMeta) {
	a.certMu.Lock()
	defer a.certMu.Unlock()
	settings := &model.Settings{
		Host:           m.Host,
		SNI:            m.SNI,
		ACMEEmail:      m.ACMEEmail,
		ACMEProvider:   m.ACMEProvider,
		ZeroSSLEABKID:  m.ZeroSSLEABKID,
		ZeroSSLEABHMAC: m.ZeroSSLEABHMAC,
	}
	if err := tlsmgr.Ensure(settings, a.certPath, a.keyPath, a.acmeDir, false); err != nil {
		slog.Warn("node: TLS not ready yet (self-signed for now)", "err", err)
	}
}

// certLoop keeps the node's TLS cert current: it retries ACME (fast while the node
// is still on a self-signed fallback, slowly once a CA cert is in place), and when
// the cert actually changes — self-signed → real ACME cert, or a renewal — it
// reloads Xray so the new cert is served immediately. The next sync auto-reports
// the fresh fingerprint, so the panel drops the pin from this node's links once
// it's CA-trusted. Mirrors the panel's own tlsLoop.
func (a *Agent) certLoop(ctx context.Context) {
	for {
		meta, ok := a.currentMeta()
		if !ok {
			// No config yet → nothing to get a cert for. Check back soon.
			if !sleepCtx(ctx, certRetryFast) {
				return
			}
			continue
		}
		beforeSHA, _, _, _ := a.certStatus()
		a.ensureCert(meta)
		afterSHA, selfSigned, _, _ := a.certStatus()
		if afterSHA != "" && afterSHA != beforeSHA {
			slog.Info("node: TLS cert changed — reloading Xray", "self_signed", selfSigned)
			if err := a.sup.Restart(); err != nil {
				slog.Warn("node: reload after cert change failed", "err", err)
			}
		}
		wait := certRenewSlow
		if selfSigned || afterSHA == "" {
			wait = certRetryFast // keep trying to get a real cert
		}
		if !sleepCtx(ctx, wait) {
			return
		}
	}
}

// currentMeta returns the host meta of the last applied config (for the cert loop),
// or ok=false when nothing has been applied yet.
func (a *Agent) currentMeta() (nodeapi.NodeMeta, bool) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	if a.state.LastConfig == nil {
		return nodeapi.NodeMeta{}, false
	}
	return a.state.LastConfig.Meta, true
}

// substituteCertPaths replaces the panel's cert-path sentinels in a generated Xray
// config with the node's own absolute cert/key paths.
func substituteCertPaths(raw []byte, certPath, keyPath string) []byte {
	out := bytes.ReplaceAll(raw, []byte(nodeapi.CertPathSentinel), []byte(certPath))
	return bytes.ReplaceAll(out, []byte(nodeapi.KeyPathSentinel), []byte(keyPath))
}

// ensureDecoy starts the loopback decoy HTTP server (once) and updates the served
// template. The listener stays up across template changes — only the handler is
// swapped atomically — so the masquerade is never briefly down and there's no
// same-port relisten race. The listener is wrapped with proxyproto so Xray's
// fallback PROXY header (xver=1) is stripped before the decoy sees the request.
func (a *Agent) ensureDecoy(dest, template string) error {
	if dest == "" {
		dest = "127.0.0.1:8080"
	}
	h, err := decoy.New(template) // validate the template before touching the server
	if err != nil {
		return err
	}
	a.decoyMu.Lock()
	defer a.decoyMu.Unlock()
	var hh http.Handler = h
	a.decoyHandler.Store(&hh) // swap the live handler (nil-safe until first set)
	a.decoyTmpl = template
	if a.decoySrv != nil {
		return nil // already listening; the handler swap above is enough
	}
	ln, err := net.Listen("tcp", dest)
	if err != nil {
		return err
	}
	// Serve HTTP/2 cleartext too: Xray's :443 VLESS inbound offers ALPN h2, so a
	// browser hitting the node negotiates HTTP/2 and Xray forwards it (prior-knowledge
	// h2c) over the plaintext fallback to this decoy. Without UnencryptedHTTP2 the
	// decoy can't parse the h2 frames → the browser gets ERR_HTTP2_PROTOCOL_ERROR and
	// no masquerade. Mirrors the panel's own server (cmd/rospanel/service.go).
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	// The server dispatches to whatever handler is currently stored, so a later
	// template change is a pointer swap, not a listener restart.
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if hp := a.decoyHandler.Load(); hp != nil {
				(*hp).ServeHTTP(w, r)
			}
		}),
		Protocols:         protocols,
		ReadHeaderTimeout: 10 * time.Second,
	}
	a.decoySrv = srv
	go func() {
		_ = srv.Serve(&proxyproto.Listener{Listener: ln})
	}()
	return nil
}

func (a *Agent) shutdown() {
	a.sup.Stop()
	a.operaSup.Stop()
	a.decoyMu.Lock()
	if a.decoySrv != nil {
		_ = a.decoySrv.Close()
	}
	a.decoyMu.Unlock()
}

// syncOpera reconciles the opera-proxy helper to the desired state, restarting it
// only when the enable flag / region / port actually changed — so a repeated apply
// (every config push) doesn't churn the helper and drop the "opera" lane. On the
// first enable the binary download + start run inline (one-time); the "opera" lane's
// Xray balancer falls back to direct until the helper is up. Failures are logged and
// swallowed — Opera egress is best-effort, never fatal to applying the config.
func (a *Agent) syncOpera(enabled bool, country string, port int) {
	a.operaMu.Lock()
	defer a.operaMu.Unlock()
	if !enabled {
		if a.operaOn {
			slog.Info("node: disabling Opera egress")
			a.operaSup.Stop()
			a.operaOn, a.operaCountry, a.operaPort = false, "", 0
		}
		return
	}
	if a.operaOn && a.operaCountry == country && a.operaPort == port {
		return // already running with these settings — don't restart
	}
	if _, err := opera.EnsureBinary(a.operaDir); err != nil {
		slog.Warn("node: opera-proxy download failed", "err", err)
		return
	}
	if err := a.operaSup.Start(country, port); err != nil {
		slog.Warn("node: opera-proxy start failed", "err", err)
		return
	}
	a.operaOn, a.operaCountry, a.operaPort = true, country, port
	slog.Info("node: Opera egress started", "country", country, "port", port)
}

// selfUpdate downloads + verifies the latest release and swaps the node binary,
// then stops Xray and returns true so Run exits — systemd (Restart=always) starts
// the new binary, which re-applies the saved config. Returns false (and keeps
// running the current version) if there's nothing newer or the update fails.
func (a *Agent) selfUpdate() bool {
	repo := updater.Repo
	if r := strings.TrimSpace(os.Getenv("ROSPANEL_REPO")); r != "" {
		repo = r
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rel, err := updater.Latest(ctx, repo)
	if err != nil {
		slog.Warn("node self-update: check failed", "err", err)
		return false
	}
	if !updater.IsNewer(rel.Version, version.Version) {
		slog.Info("node self-update: already on the latest version", "version", version.Version)
		return false
	}
	slog.Info("node self-update: installing", "from", version.Version, "to", rel.Version)
	if err := updater.Apply(ctx, rel, nil); err != nil {
		slog.Error("node self-update: failed", "err", err)
		return false
	}
	slog.Info("node self-update: binary updated — restarting", "version", rel.Version)
	a.sup.Stop()
	return true
}

// resolveNodeXrayBin finds or downloads the Xray binary, like the panel's resolver
// but non-fatal: on failure the Supervisor runs in config-only mode (writes config
// but doesn't start Xray) and the next sync/geo cycle can retry.
func resolveNodeXrayBin(downloadDir string) string {
	if p, err := exec.LookPath(env("XRAY_BIN", "xray")); err == nil {
		return p
	}
	slog.Info("node: downloading pinned Xray release", "version", xray.PinnedVersion)
	p, err := xray.EnsureBinary(downloadDir)
	if err != nil {
		slog.Error("node: Xray binary unavailable — config will be written but not started", "err", err)
		return ""
	}
	return p
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// validPanelURL guards against switching to a malformed/unsafe broadcast address:
// it must be an absolute https URL with a host (the panel always sits behind TLS).
func validPanelURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && u.Scheme == "https" && u.Host != ""
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
