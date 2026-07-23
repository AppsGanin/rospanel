package core

import (
	"fmt"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/version"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// NodeHealth is the diagnostics for one server, so the Nodes page can show the
// same report next to every card. Node 0 is the panel's own server and reuses the
// full local report (Xray, config, TLS, disk/RAM, geo, connguard, BBR).
//
// A remote node is diagnosed entirely from what it last reported — the panel never
// dials it — so the report is honest about staleness instead of pretending to
// probe: everything is qualified by "как сообщила нода" through the connection
// check, which leads and says how fresh that picture is.
func (m *Manager) NodeHealth(id int64) (*HealthReport, error) {
	if id == model.LocalNodeID {
		rep := m.Health()
		// Drop the fleet summary ("ноды: N онлайн"): it is not a fact about THIS
		// server, and the page it now appears on already lists every node's state one
		// card below. Health() keeps it for /v1/health, where a monitor has no such
		// list — so the status is recomputed here without it.
		kept := rep.Checks[:0]
		for _, c := range rep.Checks {
			if c.Key != "nodes" {
				kept = append(kept, c)
			}
		}
		rep.Checks = kept
		rep.Status = worstStatus(rep.Checks)
		return rep, nil
	}
	n, err := m.store.GetNode(id)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, &ValidationError{Msg: "нода не найдена"}
	}
	now := time.Now().Unix()
	online := n.Online(now)
	checks := []HealthCheck{m.nodeLinkHealth(n, now, online)}
	// Everything below describes the node's last report. When it has never
	// connected there is nothing to describe, so the link check stands alone.
	if n.Joined() {
		checks = append(checks,
			nodeXrayHealth(n),
			m.nodeConfigHealth(n, online),
			nodeCertHealth(n),
		)
		// The machine itself, as the node reported it. An agent older than this
		// feature sends nothing, so the rows are omitted rather than shown as zeros.
		if h, ok := m.NodeHostStats(n.ID); ok {
			checks = append(checks,
				diskHealth(h.DiskUsed, h.DiskTotal),
				memHealth(h.MemUsed, h.MemTotal),
				nodeConnGuardHealth(h),
				nodeBBRHealth(h),
			)
		}
		checks = append(checks,
			m.nodeGeoHealth(n),
			nodeAgentHealth(n),
		)
	}
	return &HealthReport{Status: worstStatus(checks), Checks: checks}, nil
}

// nodeLinkHealth reports the node↔panel link: the one check that says how much the
// rest of the report can be trusted.
func (m *Manager) nodeLinkHealth(n *model.Node, now int64, online bool) HealthCheck {
	const label = "Связь с панелью"
	switch {
	case !n.Enabled:
		return HealthCheck{Key: "link", Label: label, Status: healthInfo,
			Detail: "нода выключена — она не обслуживает пользователей и не входит в подписки"}
	case !n.Joined():
		return HealthCheck{Key: "link", Label: label, Status: healthWarn,
			Detail: "нода ещё ни разу не подключалась",
			Hint:   "Выполните команду установки на сервере ноды («Управление» → «Переустановить»)."}
	case online:
		return HealthCheck{Key: "link", Label: label, Status: healthOK,
			Detail: fmt.Sprintf("на связи · последняя синхронизация %s назад", humanDuration(now-n.LastSeen))}
	default:
		return HealthCheck{Key: "link", Label: label, Status: healthError,
			Detail: fmt.Sprintf("офлайн · молчит уже %s", humanDuration(now-n.LastSeen)),
			Hint:   "Данные ниже — на момент последней синхронизации. Проверьте сервер ноды: systemctl status rospanel-node, journalctl -u rospanel-node."}
	}
}

func nodeXrayHealth(n *model.Node) HealthCheck {
	const label = "Прокси-движок Xray"
	if !n.XrayRunning {
		return HealthCheck{Key: "xray", Label: label, Status: healthError,
			Detail: "процесс не запущен на ноде",
			Hint:   "Откройте логи ноды — вероятна ошибка конфигурации или нехватка ресурсов."}
	}
	ver := n.XrayVersion
	if ver == "" {
		ver = "?"
	}
	if n.XrayVersion != "" && !xray.VersionMatchesPinned(n.XrayVersion) {
		return HealthCheck{Key: "xray", Label: label, Status: healthWarn,
			Detail: fmt.Sprintf("работает · версия %s, панель ожидает %s", ver, xray.PinnedVersion),
			Hint:   "Обновите ноду: «Управление» → «Обновить»."}
	}
	return HealthCheck{Key: "xray", Label: label, Status: healthOK,
		Detail: "работает · версия " + ver}
}

// nodeConfigHealth compares what the node last applied against what the panel
// would push now. A mismatch on a live node means the push is stuck; on an offline
// one it is simply the pending change it will pick up when it returns.
func (m *Manager) nodeConfigHealth(n *model.Node, online bool) HealthCheck {
	const label = "Конфигурация Xray"
	state, err := m.NodeDesiredState(n)
	if err != nil {
		return HealthCheck{Key: "config", Label: label, Status: healthError,
			Detail: "панель не может собрать конфиг для этой ноды: " + err.Error(),
			Hint:   "Проверьте настройки ноды (протоколы, роутинг, DNS)."}
	}
	if state.Hash == n.ConfigHash {
		return HealthCheck{Key: "config", Label: label, Status: healthOK,
			Detail: "актуальна — нода применила последние настройки"}
	}
	if !online {
		return HealthCheck{Key: "config", Label: label, Status: healthInfo,
			Detail: "есть непринятые изменения — нода применит их, когда вернётся на связь"}
	}
	return HealthCheck{Key: "config", Label: label, Status: healthWarn,
		Detail: "нода ещё не применила последние настройки",
		Hint:   "Обычно это занимает несколько секунд. Если висит — смотрите логи ноды: её Xray мог отклонить конфиг."}
}

// nodeCertWarnDays is how close to expiry a node's cert must be before it reads as
// a problem rather than normal renewal churn.
const nodeCertWarnDays = 2

func nodeCertHealth(n *model.Node) HealthCheck {
	const label = "TLS-сертификат"
	if n.CertExpiresAt == 0 && n.CertIssuer == "" && !n.CertSelfSigned {
		return HealthCheck{Key: "tls", Label: label, Status: healthInfo,
			Detail: "нода ещё не сообщила о своём сертификате"}
	}
	daysLeft := int(time.Until(time.Unix(n.CertExpiresAt, 0)).Hours() / 24)
	if n.CertExpiresAt > 0 && time.Now().After(time.Unix(n.CertExpiresAt, 0)) {
		return HealthCheck{Key: "tls", Label: label, Status: healthError,
			Detail: "истёк " + time.Unix(n.CertExpiresAt, 0).Format("02.01.2006"),
			Hint:   "Проверьте домен ноды и доступность ACME с её сервера."}
	}
	if n.CertSelfSigned {
		return HealthCheck{Key: "tls", Label: label, Status: healthWarn,
			Detail: "самоподписанный — ссылки этой ноды работают только с закреплением сертификата",
			Hint:   "Укажите домен ноды во вкладке «Домен» — агент выпустит сертификат через ACME."}
	}
	// A low, fixed floor on purpose: the node reports only the expiry, not the
	// issue date, so we cannot scale the threshold to the cert's lifetime the way
	// tlsHealth does. The master's 14-day floor would warn forever on a node with a
	// ~6-day Let's Encrypt IP cert, which is perfectly healthy and renews itself.
	if daysLeft < nodeCertWarnDays {
		return HealthCheck{Key: "tls", Label: label, Status: healthWarn,
			Detail: fmt.Sprintf("истекает через %d дн. · выдан %s", daysLeft, n.CertIssuer),
			Hint:   "Продление автоматическое; если оно не срабатывает — смотрите логи ноды."}
	}
	return HealthCheck{Key: "tls", Label: label, Status: healthOK,
		Detail: fmt.Sprintf("действителен ещё %d дн. · выдан %s", daysLeft, n.CertIssuer)}
}

// nodeGeoHealth reads the geo status the node reported (the panel can't stat the
// node's disk).
func (m *Manager) nodeGeoHealth(n *model.Node) HealthCheck {
	const label = "Гео-базы (geoip/geosite)"
	files := m.NodeGeoFiles(n.ID)
	if len(files) == 0 {
		return HealthCheck{Key: "geo", Label: label, Status: healthInfo,
			Detail: "нода ещё не сообщила о гео-базах"}
	}
	now := time.Now().Unix()
	var missing []string
	var oldest int64
	for _, f := range files {
		if !f.Present {
			missing = append(missing, f.Name)
			continue
		}
		if age := (now - f.ModifiedAt) / 86400; age > oldest {
			oldest = age
		}
	}
	if len(missing) > 0 {
		return HealthCheck{Key: "geo", Label: label, Status: healthError,
			Detail: "отсутствуют: " + strings.Join(missing, ", "),
			Hint:   "Нажмите «Обновить гео» во вкладке Geo этой ноды."}
	}
	if oldest > 60 {
		return HealthCheck{Key: "geo", Label: label, Status: healthWarn,
			Detail: fmt.Sprintf("устарели (обновлены %d дн. назад) — правила маршрутизации могут быть неточны", oldest),
			Hint:   "Нажмите «Обновить гео» во вкладке Geo этой ноды."}
	}
	return HealthCheck{Key: "geo", Label: label, Status: healthOK,
		Detail: fmt.Sprintf("на месте · обновлены %d дн. назад", oldest)}
}

// nodeConnGuardHealth mirrors the master's flood-guard check. A node always wants
// the guard (its agent installs it on every apply, with no opt-out env), so
// "not active" here means nftables or root was missing — the same silent gap the
// master's check exists to surface.
func nodeConnGuardHealth(h nodeapi.HostStats) HealthCheck {
	const label = "Защита от флуда (лимит соединений с одного IP)"
	if h.ConnGuard {
		return HealthCheck{Key: "connguard", Label: label, Status: healthOK,
			Detail: "правила nftables установлены"}
	}
	return HealthCheck{Key: "connguard", Label: label, Status: healthWarn,
		Detail: "правила не установлены — нода не защищена от флуда соединений",
		Hint:   "На сервере ноды не найден nftables или не хватает прав. Установите пакет nftables и убедитесь, что агент работает от root."}
}

// nodeBBRHealth mirrors bbrHealth: informational, since BBR is a throughput
// optimization and a kernel without it is not a fault.
func nodeBBRHealth(h nodeapi.HostStats) HealthCheck {
	const label = "TCP BBR (ускорение на потерях)"
	if h.BBR {
		return HealthCheck{Key: "bbr", Label: label, Status: healthOK, Detail: "включён"}
	}
	return HealthCheck{Key: "bbr", Label: label, Status: healthInfo,
		Detail: "не активен — ядро ноды без BBR или нет прав",
		Hint:   "Не влияет на работоспособность, только на скорость на нестабильных каналах."}
}

// nodeAgentHealth flags a node running an older build than the panel: the two
// speak one protocol, and a drifting agent is what makes a new panel feature
// silently not work on that server.
func nodeAgentHealth(n *model.Node) HealthCheck {
	const label = "Агент ноды"
	if n.NodeVersion == "" {
		return HealthCheck{Key: "agent", Label: label, Status: healthInfo,
			Detail: "версия неизвестна — нода ещё не сообщила её"}
	}
	if n.NodeVersion != version.Version {
		return HealthCheck{Key: "agent", Label: label, Status: healthWarn,
			Detail: fmt.Sprintf("версия %s, у панели %s", n.NodeVersion, version.Version),
			Hint:   "Обновите ноду: «Управление» → «Обновить»."}
	}
	return HealthCheck{Key: "agent", Label: label, Status: healthOK,
		Detail: "версия " + n.NodeVersion + " — совпадает с панелью"}
}
