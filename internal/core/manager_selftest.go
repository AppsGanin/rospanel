package core

import (
	"context"
	"time"

	"github.com/AppsGanin/rospanel/internal/selftest"
)

// SelfTest connects to each enabled protocol as a real client — through a throwaway
// Xray the panel spawns — and reports whether traffic actually flows end-to-end. It
// answers the question the health page can't: "if I hand a user a link right now,
// does it work?" See the selftest package for what this does and does not prove.
//
// It tests against a real working user's credentials (the first one currently in the
// config), because that's what actually gets handed out — a synthetic user could
// pass while every real link is broken by, say, a bad flow or SNI.
func (m *Manager) SelfTest(ctx context.Context) ([]selftest.Result, error) {
	set, err := m.store.GetSettings()
	if err != nil {
		return nil, err
	}
	// Populate the same per-request TLS hints the subscription path fills in (see
	// server.applyTLSHints): on a self-signed / not-yet-CA-trusted cert, real links
	// carry the cert pin (pcs → pinnedPeerCertSha256) so clients trust it. Without
	// this the probe would do full verification and fail the TLS handshake on a
	// self-signed fallback — reporting a working server as broken, exactly on the
	// fresh/IP installs where the self-test matters most.
	if !m.HasValidCert() {
		set.TLSInsecure = true
		set.TLSPinSHA256 = m.CertPinSHA256()
	}
	if !m.sup.Running() {
		return []selftest.Result{{OK: false,
			Detail: "Xray не запущен — сначала устраните ошибку в разделе диагностики"}}, nil
	}

	users, err := m.store.WorkingUsers(time.Now().Unix())
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return []selftest.Result{{OK: false,
			Detail: "нет ни одного активного пользователя — добавьте пользователя, чтобы было чем проверить"}}, nil
	}

	return selftest.Run(ctx, m.sup.BinPath(), set, users[0]), nil
}
