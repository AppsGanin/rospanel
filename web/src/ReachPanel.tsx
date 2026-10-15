// Reachability from other countries (Globalping): the settings, one server's check,
// and the line of country badges on a server's card.
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  checkNodeReach,
  getNodeReach,
  getReachConfig,
  setReachConfig,
  type NodeView,
  type ReachConfig,
  type ReachCountry,
  type ReachReport,
  type ReachSummary,
} from "./api";
import { fmtStamp } from "./format";
import { locale } from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { serverName } from "./NodeStatus";
import { useCan } from "./role";
import {
  Badge,
  Button,
  CenterLoader,
  cn,
  Modal,
  Mono,
  PasswordInput,
  ReadOnly,
  Select,
  SettingRow,
  TagsInput,
  TextInput,
} from "./ui";

// Countries that filter the internet, offered first; any other ISO code can be typed.
const SUGGESTED = ["RU", "CN", "IR", "BY", "KZ", "UZ", "TM", "TR", "PK", "VN", "AE", "SA", "EG", "MM"];
const EVERY = [0, 30, 60, 180, 360, 720, 1440] as const;

// everyOptions keeps an interval set over the API (any 15–1440) choosable as it is.
function everyOptions(cur: number): number[] {
  const list: number[] = [...EVERY];
  if (!list.includes(cur)) list.push(cur);
  return list.sort((a, b) => a - b);
}
// Globalping's hourly limit without a token and with one.
const LIMIT = 250;
const LIMIT_TOKEN = 500;

export function flag(code: string): string {
  if (!/^[A-Za-z]{2}$/.test(code)) return "";
  return String.fromCodePoint(
    ...[...code.toUpperCase()].map((c) => 0x1f1e6 + c.charCodeAt(0) - 65),
  );
}

export function countryName(code: string): string {
  try {
    return new Intl.DisplayNames([locale()], { type: "region" }).of(code) ?? code;
  } catch {
    return code;
  }
}

// ReachBadges is a server's last check on its card: one badge per country, red when
// that country counts as blocked.
export function ReachBadges({ summary, onClick }: { summary: ReachSummary; onClick: () => void }) {
  const { t } = useTranslation();
  return (
    <button
      type="button"
      onClick={onClick}
      title={`${t("reach.cardAction")} · ${fmtStamp(summary.at)}`}
      className="flex flex-wrap items-center gap-1"
    >
      {summary.countries.map((c) => (
        <Badge
          key={c.code}
          size="xs"
          color={c.answered === 0 ? "gray" : c.blocked ? "red" : "green"}
        >
          <span dir="ltr">
            {flag(c.code)} {c.answered === 0 ? "—" : `${c.ok}/${c.answered}`}
          </span>
        </Badge>
      ))}
    </button>
  );
}

// ReachDialog is one server's check: the last result and a button to run it again.
export function ReachDialog({
  node,
  onClose,
  onChecked,
}: {
  node: NodeView;
  onClose: () => void;
  onChecked: () => void;
}) {
  const { t } = useTranslation();
  const [rep, setRep] = useState<ReachReport | null>(null);
  const [threshold, setThreshold] = useState(50);
  const [checking, setChecking] = useState(false);

  useEffect(() => {
    getNodeReach(node.id)
      .then(setRep)
      .catch((e) => notifyError(errMessage(e)));
    getReachConfig()
      .then((c) => setThreshold(c.threshold))
      .catch(() => {});
  }, [node.id]);

  const check = async () => {
    setChecking(true);
    try {
      setRep(await checkNodeReach(node.id));
      onChecked();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setChecking(false);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      title={t("reach.titleOf", { name: serverName(node) })}
      subtitle={t("reach.about")}
      footer={
        <div className="flex items-center justify-end gap-2">
          {checking && <span className="me-auto text-xs text-ink-muted">{t("reach.checking")}</span>}
          <Button size="sm" onClick={check} loading={checking}>
            {t("reach.check")}
          </Button>
        </div>
      }
    >
      {!rep ? (
        <CenterLoader />
      ) : rep.at === 0 ? (
        <p className="py-6 text-center text-sm text-ink-muted">{t("reach.never")}</p>
      ) : (
        <div className="flex flex-col gap-4">
          <p className="text-xs text-ink-muted">
            {t("reach.checkedAt", { when: fmtStamp(rep.at), host: rep.host, port: rep.port })}
          </p>
          {rep.countries.map((c) => (
            <CountryResult key={c.code} c={c} host={rep.host} threshold={threshold} />
          ))}
        </div>
      )}
    </Modal>
  );
}

function CountryResult({ c, host, threshold }: { c: ReachCountry; host: string; threshold: number }) {
  const { t } = useTranslation();
  const answered = c.ok + c.blocked;
  const pct = answered ? Math.round((c.ok * 100) / answered) : -1;
  const blocked = answered >= 3 && pct < threshold;
  return (
    <section className="rounded-xl border border-gray-200">
      <header className="flex flex-wrap items-center gap-2 border-b border-gray-100 px-3 py-2">
        <span className="text-base leading-none">{flag(c.code)}</span>
        <span className="text-sm font-semibold text-ink">{countryName(c.code)}</span>
        <span className="ms-auto flex items-center gap-2">
          {answered === 0 ? (
            <span className="text-xs text-ink-muted">{t("reach.noAnswer")}</span>
          ) : (
            <>
              <Mono className="text-xs text-ink">{t("reach.ofProbes", { ok: c.ok, n: answered })}</Mono>
              <Badge size="xs" color={blocked ? "red" : "green"}>
                {t(blocked ? "reach.blocked" : "reach.reachable")}
              </Badge>
            </>
          )}
        </span>
      </header>
      <ul className="divide-y divide-gray-100">
        {c.probes.map((p, i) => (
          <li
            // biome-ignore lint/suspicious/noArrayIndexKey: probes have no id, and the list never reorders
            key={i}
            className="flex flex-wrap items-center gap-x-3 gap-y-0.5 px-3 py-1.5 text-xs"
          >
            <span className="min-w-0 flex-1 truncate text-ink">
              {p.city || "—"} <span className="text-ink-muted">· {p.network}</span>
            </span>
            {p.resolved && p.resolved !== host && (
              <Mono className="text-[11px] text-ink-muted" title={t("reach.resolved")}>
                {p.resolved}
              </Mono>
            )}
            <span
              className={cn(
                "shrink-0 font-mono",
                p.state === "ok" && "text-success",
                p.state === "blocked" && "text-danger",
                p.state === "failed" && "text-ink-muted",
              )}
            >
              {p.state === "ok"
                ? t("reach.probeOk", { ms: Math.round(p.rtt_ms ?? 0) })
                : t(p.state === "blocked" ? "reach.probeBlocked" : "reach.probeFailed")}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}

// ReachSettingsDialog sets which countries servers are checked from and how often.
export function ReachSettingsDialog({
  servers,
  onClose,
  onSaved,
}: {
  servers: number;
  onClose: () => void;
  onSaved: (c: ReachConfig) => void;
}) {
  const { t } = useTranslation();
  const canManage = useCan("servers.manage");
  const [cfg, setCfg] = useState<ReachConfig | null>(null);
  const [token, setToken] = useState("");
  const [clearToken, setClearToken] = useState(false);
  const [probes, setProbes] = useState("");
  const [threshold, setThreshold] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    getReachConfig()
      .then((c) => {
        setCfg(c);
        setProbes(String(c.probes));
        setThreshold(String(c.threshold));
      })
      .catch((e) => notifyError(errMessage(e)));
  }, []);

  if (!cfg) {
    return (
      <Modal open onClose={onClose} title={t("reach.title")}>
        <CenterLoader />
      </Modal>
    );
  }

  const hasToken = (cfg.has_token && !clearToken) || token.trim() !== "";
  const limit = hasToken ? LIMIT_TOKEN : LIMIT;
  const perRound = servers * cfg.countries.length * (Number(probes) || 0);
  const perHour = cfg.every > 0 ? Math.ceil((perRound * 60) / cfg.every) : 0;

  const save = async () => {
    setSaving(true);
    try {
      const next = await setReachConfig(
        {
          countries: cfg.countries,
          probes: Number(probes) || 0,
          every: cfg.every,
          threshold: Number(threshold) || 0,
        },
        token.trim() !== "" ? token.trim() : clearToken ? "" : undefined,
      );
      setCfg(next);
      onSaved(next);
      setToken("");
      setClearToken(false);
      notifySuccess(t("common.saved"));
      onClose();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={t("reach.title")}
      subtitle={t("reach.about")}
      footer={
        canManage && (
          <div className="flex justify-end gap-2">
            <Button variant="outline" color="gray" size="sm" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button size="sm" onClick={save} loading={saving}>
              {t("common.save")}
            </Button>
          </div>
        )
      }
    >
      <ReadOnly when={!canManage}>
        <div className="flex flex-col">
          <SettingRow inset>
            <TagsInput
              label={t("reach.countries")}
              hint={t("reach.countriesHint")}
              value={cfg.countries}
              onChange={(v) =>
                setCfg({ ...cfg, countries: v.map((c) => c.trim().toUpperCase()).filter(Boolean) })
              }
              options={SUGGESTED.map((c) => ({ value: c, label: `${flag(c)} ${c} · ${countryName(c)}` }))}
            />
          </SettingRow>
          <SettingRow
            inset
            label={t("reach.probes")}
            field={
              <TextInput value={probes} onChange={setProbes} inputMode="numeric" className="w-24" />
            }
          />
          <SettingRow
            inset
            label={t("reach.every")}
            hint={t("reach.everyHint")}
            field={
              <Select
                value={String(cfg.every)}
                onChange={(v) => setCfg({ ...cfg, every: Number(v) })}
                data={everyOptions(cfg.every).map((m) => ({
                  value: String(m),
                  label: (EVERY as readonly number[]).includes(m)
                    ? t(`reach.every${m as (typeof EVERY)[number]}`)
                    : t("reach.everyN", { n: m }),
                }))}
              />
            }
          />
          <SettingRow
            inset
            label={t("reach.threshold")}
            hint={t("reach.thresholdHint")}
            field={
              <TextInput value={threshold} onChange={setThreshold} inputMode="numeric" className="w-24" />
            }
          />
          <SettingRow inset>
            <PasswordInput
              label={t("reach.token")}
              value={token}
              onChange={setToken}
              placeholder={cfg.has_token && !clearToken ? "••••••••" : undefined}
            />
            <p className="mt-1 text-xs text-ink-muted">
              {cfg.has_token && !clearToken ? t("reach.tokenKept") : t("reach.tokenHint")}
            </p>
            {cfg.has_token && !clearToken && canManage && (
              <Button
                size="xs"
                variant="subtle"
                color="red"
                className="mt-1"
                onClick={() => setClearToken(true)}
              >
                {t("reach.tokenClear")}
              </Button>
            )}
          </SettingRow>
          {perHour > 0 && (
            <p className={cn("pt-2 text-xs", perHour > limit ? "text-danger" : "text-ink-muted")}>
              {t(perHour > limit ? "reach.budgetOver" : "reach.budget", { n: perHour, limit })}
            </p>
          )}
        </div>
      </ReadOnly>
    </Modal>
  );
}
