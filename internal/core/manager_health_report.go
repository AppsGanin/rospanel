package core

import (
	"fmt"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/connguard"
	"github.com/AppsGanin/rospanel/internal/geo"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/tlsutil"
	"github.com/AppsGanin/rospanel/internal/tuning"
)

// Health check severities. worstStatus ranks error > warn > ok; info is advisory
// and never worsens the overall verdict.
const (
	healthOK    = "ok"
	healthWarn  = "warn"
	healthError = "error"
	healthInfo  = "info"
)

// HealthCheck is one diagnostic line shown on the Health page.
type HealthCheck struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Status string `json:"status"` // ok | warn | error | info
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"` // shown when the check isn't ok
}

// HealthReport aggregates the per-component checks plus the worst overall status.
type HealthReport struct {
	Status string        `json:"status"`
	Checks []HealthCheck `json:"checks"`
}

// Health gathers the panel's self-diagnostics: the Xray process, last config
// apply, TLS certificate, disk/RAM headroom, geo databases, and any enabled
// helper egress lane. Every signal is read from memory/disk — no extra network
// calls — so the page is cheap to poll.
func (m *Manager) Health() *HealthReport {
	set, _ := m.store.GetSettings()
	checks := []HealthCheck{m.xrayHealth(), m.configHealth(set), m.tlsHealth()}

	if m.sys != nil {
		s := m.sys.Read()
		checks = append(checks, diskHealth(s.DiskUsed, s.DiskTotal), memHealth(s.MemUsed, s.MemTotal))
	}
	checks = append(checks, m.geoHealth())

	checks = append(checks, m.connGuardHealth(), bbrHealth())

	if set != nil && set.OperaEnabled {
		if m.OperaHealthy() {
			checks = append(checks, HealthCheck{Key: "opera", Label: "Выход Opera VPN", Status: healthOK, Detail: "канал активен"})
		} else {
			checks = append(checks, HealthCheck{Key: "opera", Label: "Выход Opera VPN", Status: healthWarn,
				Detail: "недоступен — трафик идёт напрямую (фолбэк)", Hint: "Проверьте, что opera-proxy запущен и есть доступ в сеть."})
		}
	}
	return &HealthReport{Status: worstStatus(checks), Checks: checks}
}

func (m *Manager) xrayHealth() HealthCheck {
	if !m.sup.Running() {
		return HealthCheck{Key: "xray", Label: "Прокси-движок Xray", Status: healthError,
			Detail: "процесс не запущен", Hint: "Откройте логи Xray — вероятна ошибка конфигурации или нехватка ресурсов."}
	}
	ver := m.sup.Version()
	if ver == "" {
		ver = "?"
	}
	return HealthCheck{Key: "xray", Label: "Прокси-движок Xray", Status: healthOK,
		Detail: fmt.Sprintf("работает · версия %s · аптайм %s", ver, humanDuration(m.sup.UptimeSeconds()))}
}

func (m *Manager) configHealth(set *model.Settings) HealthCheck {
	if set != nil && strings.TrimSpace(set.LastConfigError) != "" {
		return HealthCheck{Key: "config", Label: "Конфигурация Xray", Status: healthError,
			Detail: set.LastConfigError, Hint: "Последнее применение конфига не удалось — проверьте настройки протоколов/роутинга."}
	}
	var rev int64
	if set != nil {
		rev = set.ConfigRevision
	}
	return HealthCheck{Key: "config", Label: "Конфигурация Xray", Status: healthOK,
		Detail: fmt.Sprintf("применена без ошибок (ревизия %d)", rev)}
}

func (m *Manager) tlsHealth() HealthCheck {
	const label = "TLS-сертификат"
	info, err := tlsutil.ReadCertInfo(m.tls.CertPath)
	if err != nil || info == nil {
		return HealthCheck{Key: "tls", Label: label, Status: healthError,
			Detail: "сертификат не найден или нечитаем", Hint: "Выпустите сертификат в разделе «Настройки → TLS»."}
	}
	if !time.Now().Before(info.NotAfter) {
		return HealthCheck{Key: "tls", Label: label, Status: healthError,
			Detail: "истёк " + info.NotAfter.Format("02.01.2006"), Hint: "Перевыпустите сертификат."}
	}
	if info.Issuer == "" || info.Issuer == info.Subject { // self-signed fallback
		return HealthCheck{Key: "tls", Label: label, Status: healthWarn,
			Detail: "самоподписанный — часть клиентов не подключится",
			Hint:   "Укажите домен и выпустите сертификат через ACME (Let's Encrypt / ZeroSSL)."}
	}
	// Renewal runs in the last third of the cert's lifetime, so the "expiring
	// soon" floor must scale with that lifetime. A Let's Encrypt IP cert lives
	// only ~6 days and is perfectly healthy at 5 days left; a 90-day domain cert
	// at 5 days left means renewal is failing. Without scaling, every IP install
	// would sit in a permanent false warning.
	lifeDays := int(info.NotAfter.Sub(info.NotBefore).Hours() / 24)
	note := ""
	if lifeDays > 0 && lifeDays <= 10 {
		note = " · короткоживущий сертификат (IP), продление автоматическое"
	}
	if info.DaysLeft < certWarnThreshold(lifeDays) {
		return HealthCheck{Key: "tls", Label: label, Status: healthWarn,
			Detail: fmt.Sprintf("истекает через %d дн.%s · выдан %s", info.DaysLeft, note, info.Issuer),
			Hint:   "Продление автоматическое; если оно не срабатывает — проверьте доступность ACME и логи."}
	}
	return HealthCheck{Key: "tls", Label: label, Status: healthOK,
		Detail: fmt.Sprintf("действителен ещё %d дн.%s · выдан %s", info.DaysLeft, note, info.Issuer)}
}

// certWarnThreshold is the "days left" floor below which a certificate is flagged
// as expiring soon. It scales with the cert's own lifetime — renewal happens in
// the last third of life — so a short-lived (~6-day LE IP) cert isn't perpetually
// warned, while a 90-day domain cert still warns at 14 days. Never below 1.
func certWarnThreshold(lifeDays int) int {
	t := 14
	if lifeDays > 0 && lifeDays/3 < t {
		t = lifeDays / 3
	}
	if t < 1 {
		t = 1
	}
	return t
}

// SetConnGuardWanted records whether the operator left the per-IP connection guard
// enabled (ROSPANEL_CONNLIMIT != off). Called once at boot.
func (m *Manager) SetConnGuardWanted(v bool) { m.connGuardWanted.Store(v) }

// connGuardHealth reports whether the per-IP connection limits are actually in
// force. connguard.Ensure degrades to a no-op when nft is missing or the panel
// isn't root, and only logs — so an operator who believes they're protected can be
// running with no guard at all and never know. That silent gap is the whole reason
// this check exists.
func (m *Manager) connGuardHealth() HealthCheck {
	const label = "Защита от флуда (лимит соединений с одного IP)"
	if !m.connGuardWanted.Load() {
		return HealthCheck{Key: "connguard", Label: label, Status: healthInfo,
			Detail: "выключена оператором (ROSPANEL_CONNLIMIT=off)"}
	}
	if connguard.Active() {
		return HealthCheck{Key: "connguard", Label: label, Status: healthOK,
			Detail: "правила nftables установлены"}
	}
	return HealthCheck{Key: "connguard", Label: label, Status: healthWarn,
		Detail: "включена, но правила не установлены — сервер не защищён от флуда соединений",
		Hint:   "Не найден nftables или не хватает прав. Установите пакет nftables и убедитесь, что панель работает от root."}
}

// bbrHealth reports the congestion-control algorithm. Informational, not a warning:
// BBR is a throughput optimization, and plenty of healthy kernels (and every non-
// Linux dev box) simply don't offer it — flagging that as a problem would be noise.
func bbrHealth() HealthCheck {
	const label = "TCP BBR (ускорение на потерях)"
	if tuning.Active() {
		return HealthCheck{Key: "bbr", Label: label, Status: healthOK, Detail: "включён"}
	}
	return HealthCheck{Key: "bbr", Label: label, Status: healthInfo,
		Detail: "не активен — ядро без BBR или нет прав",
		Hint:   "Не влияет на работоспособность, только на скорость на нестабильных каналах."}
}

func (m *Manager) geoHealth() HealthCheck {
	const label = "Гео-базы (geoip/geosite)"
	files := geo.Status(m.sup.AssetDir())
	var missing []string
	var oldest int64
	now := time.Now().Unix()
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
			Detail: "отсутствуют: " + strings.Join(missing, ", "), Hint: "Обновите гео-базы в разделе «Настройки → Роутинг»."}
	}
	if oldest > 60 {
		return HealthCheck{Key: "geo", Label: label, Status: healthWarn,
			Detail: fmt.Sprintf("устарели (обновлены %d дн. назад) — правила маршрутизации могут быть неточны", oldest),
			Hint:   "Обновите гео-базы в разделе «Настройки → Роутинг».",
		}
	}
	return HealthCheck{Key: "geo", Label: label, Status: healthOK,
		Detail: fmt.Sprintf("на месте · обновлены %d дн. назад", oldest)}
}

func diskHealth(used, total int64) HealthCheck {
	const label = "Диск"
	if total <= 0 {
		return HealthCheck{Key: "disk", Label: label, Status: healthInfo, Detail: "нет данных"}
	}
	freePct := float64(total-used) / float64(total) * 100
	detail := fmt.Sprintf("занято %s из %s · свободно %.0f%%", humanBytes(used), humanBytes(total), freePct)
	switch {
	case freePct < 5:
		return HealthCheck{Key: "disk", Label: label, Status: healthError, Detail: detail,
			Hint: "Освободите место — при заполнении диска БД (WAL) и Xray могут отказать."}
	case freePct < 15:
		return HealthCheck{Key: "disk", Label: label, Status: healthWarn, Detail: detail,
			Hint: "Места мало — удалите старые бэкапы и подрежьте логи."}
	default:
		return HealthCheck{Key: "disk", Label: label, Status: healthOK, Detail: detail}
	}
}

func memHealth(used, total int64) HealthCheck {
	const label = "Оперативная память"
	if total <= 0 {
		return HealthCheck{Key: "mem", Label: label, Status: healthInfo, Detail: "нет данных"}
	}
	usedPct := float64(used) / float64(total) * 100
	detail := fmt.Sprintf("занято %s из %s · %.0f%%", humanBytes(used), humanBytes(total), usedPct)
	if usedPct > 92 {
		return HealthCheck{Key: "mem", Label: label, Status: healthWarn, Detail: detail,
			Hint: "Памяти почти нет — на боксах с 1 ГБ это близко к норме, но следите за OOM-перезапусками Xray."}
	}
	return HealthCheck{Key: "mem", Label: label, Status: healthOK, Detail: detail}
}

// worstStatus returns the most severe status among the checks (error > warn > ok),
// ignoring purely informational rows. Empty → ok.
func worstStatus(checks []HealthCheck) string {
	rank := map[string]int{healthOK: 0, healthWarn: 1, healthError: 2}
	worst := healthOK
	for _, c := range checks {
		if rank[c.Status] > rank[worst] {
			worst = c.Status
		}
	}
	return worst
}

// humanBytes renders a byte count as a compact КБ/МБ/ГБ/ТБ string.
func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d Б", b)
	}
	units := []string{"КБ", "МБ", "ГБ", "ТБ", "ПБ"}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit && exp < len(units)-1; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(b)/float64(div), units[exp])
}

// humanDuration renders a second count as a coarse "Nд Nч" / "Nч Nм" / "Nм" string.
func humanDuration(sec int64) string {
	if sec <= 0 {
		return "—"
	}
	d := time.Duration(sec) * time.Second
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dд %dч", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dч %dм", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dм", int(d.Minutes()))
	}
}
