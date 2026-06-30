package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/backup"
	"github.com/AppsGanin/rospanel/internal/connguard"
	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/datasec"
	"github.com/AppsGanin/rospanel/internal/geo"
	"github.com/AppsGanin/rospanel/internal/hop"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/netinfo"
	"github.com/AppsGanin/rospanel/internal/proxyproto"
	"github.com/AppsGanin/rospanel/internal/server"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/telegram"
	"github.com/AppsGanin/rospanel/internal/tlsmgr"
	"github.com/AppsGanin/rospanel/internal/tuning"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// runServer is the default (no-subcommand) path: boot the store, obtain a cert,
// generate the Xray config, start the background loops and serve the admin API +
// masquerade/subscription surface until a termination signal arrives.
func runServer(dataDir string) {
	adminAddr := env("ROSPANEL_ADMIN_ADDR", "127.0.0.1:8080")
	xrayBin := resolveXrayBin(env("XRAY_BIN", "xray"), filepath.Join(dataDir, "bin"))

	dbPath := filepath.Join(dataDir, "rospanel.db")
	certPath := filepath.Join(dataDir, "certs", "cert.pem")
	keyPath := filepath.Join(dataDir, "certs", "key.pem")
	acmeDir := filepath.Join(dataDir, "acme")
	geoDir := filepath.Join(dataDir, "geo")
	xrayConfig := filepath.Join(dataDir, "xray", "config.json")

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	// Tune Argon2id cost to this host before any password is hashed/verified, so a
	// small VPS isn't OOM-killed by concurrent logins (per-attempt memory is also
	// bounded by an internal concurrency cap in the auth package).
	auth.Configure()

	// Apply a staged restore (if any) before opening the DB, so the restored
	// database isn't clobbered by a stale WAL from the pre-restart process. A
	// failure mid-restore leaves the data dir half-applied (e.g. new DB + old
	// certs/secret), so abort rather than boot a broken/wrong-identity panel —
	// systemd restarts us and ApplyPending resumes from the still-staged entries.
	if applied, err := backup.ApplyPending(dataDir); err != nil {
		log.Fatalf("restore: applying staged backup failed — refusing to start on half-restored data: %v", err)
	} else if applied {
		log.Print("restore: staged backup applied")
	}

	// After any staged restore has put the real secrets.key in place, load it —
	// doing this before ApplyPending would pin a freshly-generated key in memory
	// that the restore then overwrites on disk, breaking decryption.
	if err := datasec.Init(dataDir); err != nil {
		log.Fatalf("secrets key: %v", err)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.ReencryptSensitiveFields(); err != nil {
		log.Printf("[WARN] reencrypt secrets: %v", err)
	}

	// Obtain the ACME cert before Xray opens :443. Non-fatal: if ACME isn't
	// reachable yet, a self-signed fallback is written so Xray still comes up, and
	// tlsLoop keeps retrying ACME and swaps in the real cert once it succeeds.
	if err := bootstrapTLS(st, certPath, keyPath, acmeDir); err != nil {
		log.Printf("TLS not ready yet: %v — will keep retrying; Xray starts once a cert is issued", err)
	}
	secret, err := bootstrapPanel(st)
	if err != nil {
		log.Fatalf("bootstrap panel: %v", err)
	}

	set, err := st.GetSettings()
	if err != nil {
		log.Fatalf("load settings: %v", err)
	}

	// Best-effort host-NAT port hopping for Hysteria2 (no-op off Linux).
	if err := hop.Ensure(set.HopStart, set.HopEnd, set.HysteriaPort); err != nil {
		log.Printf("port-hopping setup failed (Hysteria2 hopping disabled): %v", err)
	}

	// Best-effort host-level per-IP connection guard on the public TCP ports: caps
	// concurrent connections and the new-connection rate per source IP so a single
	// client can't flood the proxy with TLS handshakes (a CPU/connection-exhaustion
	// DoS the per-user quota/device model never sees, since it happens before auth).
	// Tunable / disable-able at runtime via ROSPANEL_CONNLIMIT* (no redeploy needed
	// if a busy CGNAT egress trips the defaults).
	if strings.EqualFold(env("ROSPANEL_CONNLIMIT", "on"), "off") {
		log.Printf("connguard: disabled via ROSPANEL_CONNLIMIT=off")
		_ = connguard.Ensure(nil, connguard.DefaultLimits()) // tear down any stale table
	} else {
		ports := []int{set.VLESSPort}
		if set.RealityEnabled {
			ports = append(ports, set.RealityPort)
		}
		lim := connguard.DefaultLimits()
		lim.MaxConnPerIP = envInt("ROSPANEL_CONNLIMIT_MAX", lim.MaxConnPerIP)
		lim.NewConnRate = envInt("ROSPANEL_CONNLIMIT_RATE", lim.NewConnRate)
		if err := connguard.Ensure(ports, lim); err != nil {
			log.Printf("connguard setup failed (per-IP connection limits not active): %v", err)
		}
	}

	// Best-effort: enable TCP BBR congestion control (better throughput on
	// lossy/long-haul links). No-op if already on or unavailable.
	switch state, err := tuning.EnsureBBR(); state {
	case tuning.BBRAlready:
		log.Printf("bbr: already enabled")
	case tuning.BBREnabled:
		log.Printf("bbr: enabled (net.core.default_qdisc=fq, tcp_congestion_control=bbr)")
	default:
		log.Printf("bbr: not enabled (unavailable on this kernel or insufficient perms): %v", err)
	}

	// Geo databases for geosite/geoip routing rules. Done synchronously before the
	// first reconcile so the builtin geoip rule resolves and Xray can start.
	if err := geo.Ensure(geoDir); err != nil {
		log.Printf("geo: %v", err)
	}

	sup := xray.NewSupervisor(xrayBin, xrayConfig, geoDir)
	mgr := core.New(st, sup, xray.Options{PanelDest: panelDest(adminAddr)},
		core.TLSPaths{CertPath: certPath, KeyPath: keyPath, ACMEDir: acmeDir},
		filepath.Join(dataDir, "opera"))
	sup.SetOnAccess(mgr.RecordAccess) // track online status + connection IPs
	mgr.StartSysstat(dataDir)         // host metrics for the dashboard

	// Load the proxy pool synchronously before the first reconcile so Xray starts
	// once with the proxies already in place — instead of starting empty and being
	// restarted moments later when the background fetch lands.
	mgr.SeedProxies()

	if err := mgr.Reconcile(); err != nil {
		log.Printf("initial reconcile failed (panel still starting): %v", err)
	}

	// Daily TLS check: renews ACME certs near expiry and reloads Xray on change.
	go tlsLoop(mgr)
	// Periodic traffic accounting + quota/expiry enforcement.
	go statsPollLoop(mgr)
	// Payment polling fallback: reconciles pending provider orders in case a webhook
	// was missed. Idles cheaply when there are no pending orders.
	go paymentPollLoop(mgr)
	// Telegram admin bot: view/add/remove users + scheduled backups. It idles until
	// enabled with a token in Settings → Telegram, re-reading config each cycle.
	go telegram.New(mgr, st, dataDir).Run(context.Background())
	// Telegram user bot: public self-service for VPN clients (registration,
	// subscription, stats). Idles until enabled with its own token in Settings.
	go telegram.NewUser(mgr, st).Run(context.Background())

	handler, err := server.New(mgr, secret, set.DecoyTemplate, dataDir)
	if err != nil {
		log.Fatalf("build router: %v", err)
	}
	// Serve HTTP/2 cleartext too: Xray's VLESS inbound offers ALPN h2, so non-VPN
	// traffic arrives as HTTP/2 over the plaintext fallback. h2c lets the loopback
	// panel speak both HTTP/1.1 and HTTP/2.
	httpSrv := &http.Server{
		Handler:           h2c.NewHandler(handler, &http2.Server{}),
		ReadHeaderTimeout: 10 * time.Second,  // slowloris: bound how long request headers may dribble in
		IdleTimeout:       120 * time.Second, // reap idle keep-alive connections so they can't be held open indefinitely
		// ReadTimeout / WriteTimeout are intentionally unset: a server-wide read
		// deadline would tear down the long-lived SSE streams (panel_stream), and a
		// write deadline would cut off SSE and the backup download. Slow request
		// bodies are instead bounded per-handler — decodeJSON sets a short read
		// deadline, the backup-upload handlers a longer one.
	}

	// The panel sits behind Xray, which forwards the decrypted request over
	// loopback with a PROXY-protocol header (xver=1) — the proxyproto listener
	// recovers the real client IP from it (else everything reads as 127.0.0.1).
	ln, err := net.Listen("tcp", adminAddr)
	if err != nil {
		log.Fatalf("listen %s: %v", adminAddr, err)
	}
	ln = &proxyproto.Listener{Listener: ln}

	go func() {
		log.Printf("admin API listening on %s", adminAddr)
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Print("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	sup.Stop()
}

// bootstrapTLS configures host/SNI and resolves a cert via ACME, falling back to
// a self-signed cert if ACME is unavailable so Xray can still open :443.
func bootstrapTLS(st *store.Store, certPath, keyPath, acmeDir string) error {
	set, err := st.GetSettings()
	if err != nil {
		return err
	}
	// A real host set in the panel (setup wizard → Settings → Домен) always wins.
	// When none is set yet — or a previous boot persisted the loopback fallback —
	// resolve one for an unattended first boot: an explicit ROSPANEL_HOST (domain
	// or IP) takes priority so an operator can pin a domain; otherwise auto-detect
	// the server's public IP. Loopback is the last resort (ACME can't issue for it,
	// so the panel then stays on its loopback API until a real host is configured).
	host := core.NormalizeACMEHost(set.Host)
	if host == "" || host == "127.0.0.1" {
		host = firstNonEmpty(strings.TrimSpace(os.Getenv("ROSPANEL_HOST")), netinfo.PublicIP(), "127.0.0.1")
	}
	host = core.NormalizeACMEHost(host)
	// SNI always equals the ACME target (host) so the cert, link address and SNI
	// all match. ACME (model.TLSModeACME) is the only mode.
	if err := st.SetTLS(host, host, model.TLSModeACME, certPath, keyPath); err != nil {
		return err
	}
	// Optionally seed the ACME contact email from the environment on first boot,
	// so a domain install can be fully non-interactive.
	if set.ACMEEmail == "" {
		if email := strings.TrimSpace(os.Getenv("ROSPANEL_ACME_EMAIL")); email != "" {
			if err := st.SetTLSMode(model.TLSModeACME, host, host, email); err != nil {
				return err
			}
		}
	}
	set, err = st.GetSettings()
	if err != nil {
		return err
	}
	return tlsmgr.Ensure(set, certPath, keyPath, acmeDir, false)
}

// safeTick runs fn, recovering from panics so a single bad iteration can't kill
// a long-lived background loop (after which it would silently stop forever).
func safeTick(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("%s: panic recovered: %v", name, r)
		}
	}()
	fn()
}

// statsPollLoop accounts per-user traffic and enforces quotas every minute.
func statsPollLoop(mgr *core.Manager) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for range t.C {
		safeTick("stats poll", func() {
			if err := mgr.PollStats(); err != nil {
				// Expected when Xray isn't running (e.g. local dev) — keep quiet-ish.
				log.Printf("stats poll: %v", err)
			}
		})
	}
}

// paymentPollLoop reconciles pending provider orders (webhook fallback) every 25s.
func paymentPollLoop(mgr *core.Manager) {
	t := time.NewTicker(25 * time.Second)
	defer t.Stop()
	for range t.C {
		safeTick("payment poll", mgr.PollPendingPayments)
	}
}

// tlsLoop obtains the cert if missing and renews it before expiry, reloading
// Xray whenever the cert changes. It retries quickly while there's no usable
// cert (e.g. ACME wasn't reachable at boot) and settles into a slow renew
// cadence once one is in place.
func tlsLoop(mgr *core.Manager) {
	for {
		safeTick("tls", func() {
			changed, err := mgr.RenewTLSIfNeeded()
			if err != nil {
				log.Printf("tls: %v", err)
			}
			if changed {
				log.Print("tls: certificate updated — reloading Xray")
				if err := mgr.Reconcile(); err != nil {
					log.Printf("tls reconcile: %v", err)
				}
			}
		})
		if mgr.HasValidCert() {
			time.Sleep(6 * time.Hour)
		} else {
			time.Sleep(3 * time.Minute) // keep trying to get the first cert
		}
	}
}

// bootstrapPanel ensures a secret panel path and at least one admin exist. On
// first run it generates both and prints the credentials + panel URL once.
func bootstrapPanel(st *store.Store) (string, error) {
	set, err := st.GetSettings()
	if err != nil {
		return "", err
	}
	secret := set.PanelSecretPath
	if secret == "" {
		// Predictable default path; the first-run wizard prompts the operator to
		// rotate it to a random one.
		secret = "rospanel"
		if err := st.SetSecretPath(secret); err != nil {
			return "", err
		}
	}

	if set.WSPath == "" {
		ws, err := auth.RandomWSPath()
		if err != nil {
			return "", err
		}
		if err := st.SetWSPath(ws); err != nil {
			return "", err
		}
	}

	// REALITY needs an X25519 keypair + shortId + gRPC service name; generate them
	// once so the inbound and share links are ready when the operator enables it.
	if set.RealityPrivateKey == "" {
		priv, pub, err := auth.GenerateRealityKeys()
		if err != nil {
			return "", err
		}
		shortIDs, err := auth.RandomShortIDs()
		if err != nil {
			return "", err
		}
		svc, err := auth.RandomServiceName()
		if err != nil {
			return "", err
		}
		if err := st.SetRealityKeys(priv, pub, shortIDs, svc); err != nil {
			return "", err
		}
	}

	n, err := st.CountAdmins()
	if err != nil {
		return "", err
	}
	if n == 0 {
		// Predictable default login; the first-run wizard forces a password change.
		const password = "admin"
		hash, err := auth.HashPassword(password)
		if err != nil {
			return "", err
		}
		if _, err := st.CreateAdmin("admin", hash); err != nil {
			return "", err
		}
		// Gate the whole authed API on a password change: until the default password
		// is replaced, requireAuth lets through only the password-change/restore
		// endpoints. This makes the change server-enforced, not just a wizard prompt
		// the SPA happens to show, so a leaked secret path before first setup can't be
		// driven with admin/admin.
		if err := st.SetMustChangePassword(true); err != nil {
			return "", err
		}
		printFirstRunBanner(set.Host, secret, "admin", password)
	}
	return secret, nil
}

func printFirstRunBanner(host, secret, username, password string) {
	bar := strings.Repeat("=", 64)
	log.Printf("\n%s\n FIRST-RUN CREDENTIALS (shown only once — save them now)\n%s\n"+
		" Panel path : /%s/\n"+
		" If you have a domain: https://%s/%s/\n"+
		" Username   : %s\n"+
		" Password   : %s\n%s",
		bar, bar, secret, host, secret, username, password, bar)
}

// panelDest converts the admin listen address into the loopback dest Xray uses
// for the VLESS default fallback (":8080" → "127.0.0.1:8080").
func panelDest(adminAddr string) string {
	host, port, err := net.SplitHostPort(adminAddr)
	if err != nil {
		return adminAddr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// resolveXrayBin returns a usable Xray binary path. If bin isn't found on PATH or
// as an existing file, it auto-downloads the pinned release into downloadDir so a
// bare box works without a separate install step. Xray is required: if no binary
// can be resolved or fetched, the process exits rather than running without it.
func resolveXrayBin(bin, downloadDir string) string {
	if p, err := exec.LookPath(bin); err == nil {
		return p
	}
	if fi, err := os.Stat(bin); err == nil && !fi.IsDir() {
		return bin
	}
	log.Printf("xray: %q not found — fetching pinned release %s", bin, xray.PinnedVersion)
	p, err := xray.EnsureBinary(downloadDir)
	if err != nil {
		log.Fatalf("xray: required binary not found and auto-install failed: %v "+
			"(connect to the internet, install Xray, or set XRAY_BIN)", err)
	}
	log.Printf("xray: using auto-installed binary %s", p)
	return p
}
