import { useEffect, useMemo, useState } from "react";
import {
  getGeoCategories,
  getGeoStatus,
  getRouting,
  saveRouting,
  updateGeo,
  type GeoFile,
  type RoutingConfig,
} from "./api";
import { ApplyingModal, useXrayApply } from "./apply";
import { fmtBytes } from "./format";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  Card,
  CenterLoader,
  IconChevron,
  SaveBar,
  SegmentedControl,
  Select,
  Switch,
  TagsInput,
  ToggleRow,
} from "./ui";
import { helperStatus } from "./EgressStatus";

// PROXY_REFRESH are the URL auto-refresh cadence options (minutes; -1 = never).
const PROXY_REFRESH: Opt[] = [
  { value: "30", label: "Каждые 30 минут" },
  { value: "60", label: "Каждый 1 час" },
  { value: "180", label: "Каждые 3 часа" },
  { value: "360", label: "Каждые 6 часов" },
  { value: "720", label: "Каждые 12 часов" },
  { value: "-1", label: "Никогда" },
];

type Opt = { value: string; label: string };

// fmtWhen renders a unix timestamp as a local date+time, or a dash when unset.
const fmtWhen = (unix: number) =>
  unix
    ? new Date(unix * 1000).toLocaleString("ru-RU", {
        dateStyle: "short",
        timeStyle: "short",
      })
    : "—";

const EMPTY: RoutingConfig = {
  block_bittorrent: false,
  block_ads: false,
  block_ips: [],
  block_domains: [],
  warp_domains: [],
  warp_ips: [],
  opera_domains: [],
  opera_ips: [],
  direct_domains: [],
  direct_ips: [],
  routing_order: ["proxy", "warp", "opera", "direct"],
  proxy_urls: [],
  proxy_manual: [],
  proxy_domains: [],
  proxy_ips: [],
  proxy_refresh_minutes: 30,
};

// LANES are the reorderable egress lanes shown in the routing-order card.
const LANES: Record<string, string> = {
  direct: "Напрямую",
  proxy: "Прокси",
  warp: "WARP",
  opera: "Opera VPN",
};

// Opera VPN regions opera-proxy supports.
const OPERA_COUNTRIES = [
  { value: "EU", label: "Европа" },
  { value: "AS", label: "Азия" },
  { value: "AM", label: "Америка" },
];

// KNOWN_LANES is the full egress-lane set in default precedence. Mirrors
// knownLanes in internal/xray/generate.go.
const KNOWN_LANES = ["proxy", "warp", "opera", "direct"];

// normalizeOrder returns a routing order containing every known lane exactly
// once: it keeps the saved precedence and inserts any missing lane (e.g. "opera"
// for configs saved before it existed) just before the catch-all last lane.
function normalizeOrder(order?: string[]): string[] {
  const valid = new Set(KNOWN_LANES);
  const seen = new Set<string>();
  const out: string[] = [];
  for (const l of order ?? []) {
    if (valid.has(l) && !seen.has(l)) {
      seen.add(l);
      out.push(l);
    }
  }
  const missing = KNOWN_LANES.filter((l) => !seen.has(l));
  if (missing.length === 0) return out;
  if (out.length === 0) return missing;
  const last = out[out.length - 1];
  return [...out.slice(0, -1), ...missing, last];
}

export function RoutingPanel() {
  const [cfg, setCfg] = useState<RoutingConfig>(EMPTY);
  const [saved, setSaved] = useState<string>("");
  const [loaded, setLoaded] = useState(false);
  const { applying, apply } = useXrayApply();
  const [warpEnabled, setWarpEnabled] = useState(false);
  const [savedWarp, setSavedWarp] = useState(false);
  const [warpRegistered, setWarpRegistered] = useState(false);
  const [operaEnabled, setOperaEnabled] = useState(false);
  const [savedOpera, setSavedOpera] = useState(false);
  const [operaCountry, setOperaCountry] = useState("EU");
  const [savedOperaCountry, setSavedOperaCountry] = useState("EU");
  const [operaRunning, setOperaRunning] = useState(false);
  const [operaAlive, setOperaAlive] = useState(false);
  const [geosite, setGeosite] = useState<string[]>([]);
  const [geoip, setGeoip] = useState<string[]>([]);
  const [geoStatus, setGeoStatus] = useState<GeoFile[]>([]);
  const [proxyCount, setProxyCount] = useState(0);
  const [proxyMode, setProxyMode] = useState<"urls" | "manual">("urls");

  const loadGeoCategories = () =>
    getGeoCategories()
      .then((g) => {
        setGeosite(g.geosite ?? []);
        setGeoip(g.geoip ?? []);
      })
      .catch(() => {});

  useEffect(() => {
    loadGeoCategories();
    getGeoStatus()
      .then(setGeoStatus)
      .catch(() => {});
  }, []);

  const refreshGeo = () =>
    apply(async () => {
      setGeoStatus(await updateGeo());
      await loadGeoCategories(); // categories may have changed
      notifySuccess("Geo-базы обновлены");
    });

  useEffect(() => {
    getRouting()
      .then((r) => {
        // Go marshals empty slices as null, so coalesce every field to a list.
        const x = r.config ?? ({} as Partial<RoutingConfig>);
        const c: RoutingConfig = {
          block_bittorrent: !!x.block_bittorrent,
          block_ads: !!x.block_ads,
          block_ips: x.block_ips ?? [],
          block_domains: x.block_domains ?? [],
          warp_domains: x.warp_domains ?? [],
          warp_ips: x.warp_ips ?? [],
          opera_domains: x.opera_domains ?? [],
          opera_ips: x.opera_ips ?? [],
          direct_domains: x.direct_domains ?? [],
          direct_ips: x.direct_ips ?? [],
          routing_order: normalizeOrder(x.routing_order),
          proxy_urls: x.proxy_urls ?? [],
          proxy_manual: x.proxy_manual ?? [],
          proxy_domains: x.proxy_domains ?? [],
          proxy_ips: x.proxy_ips ?? [],
          // 0 (absent / pre-feature default) shows as 30; -1 stays "never".
          proxy_refresh_minutes: x.proxy_refresh_minutes || 30,
        };
        setProxyMode(
          (c.proxy_manual.length ?? 0) > 0 && c.proxy_urls.length === 0
            ? "manual"
            : "urls",
        );
        setCfg(c);
        setSaved(JSON.stringify(c));
        setWarpEnabled(r.warp_enabled);
        setSavedWarp(r.warp_enabled);
        setWarpRegistered(r.warp_registered);
        setOperaEnabled(r.opera_enabled);
        setSavedOpera(r.opera_enabled);
        setOperaCountry(r.opera_country || "EU");
        setSavedOperaCountry(r.opera_country || "EU");
        setOperaRunning(r.opera_running);
        setOperaAlive(r.opera_alive);
        setProxyCount(r.proxy_count ?? 0);
      })
      .catch((e) => notifyError(errMessage(e)))
      .finally(() => setLoaded(true));
  }, []);

  // refreshStatus re-fetches just the applied lane-status fields (running/alive/
  // registered/count) without touching the editable config.
  const refreshStatus = () =>
    getRouting()
      .then((r) => {
        setWarpRegistered(r.warp_registered);
        setOperaRunning(r.opera_running);
        setOperaAlive(r.opera_alive);
        setProxyCount(r.proxy_count ?? 0);
      })
      .catch(() => {});

  // Keep the lane status badges live with a 15s poll.
  useEffect(() => {
    const id = setInterval(refreshStatus, 15000);
    return () => clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const set = (patch: Partial<RoutingConfig>) =>
    setCfg((c) => ({ ...c, ...patch }));
  // Only the selected proxy source is persisted; the other list stays in local
  // state so toggling the source back and forth doesn't wipe what was typed, but
  // it's dropped from what we save and compare against.
  const effectiveCfg: RoutingConfig = {
    ...cfg,
    proxy_urls: proxyMode === "urls" ? cfg.proxy_urls : [],
    proxy_manual: proxyMode === "manual" ? cfg.proxy_manual : [],
  };
  const dirty =
    JSON.stringify(effectiveCfg) !== saved ||
    warpEnabled !== savedWarp ||
    operaEnabled !== savedOpera ||
    operaCountry !== savedOperaCountry;

  const moveLane = (i: number, dir: -1 | 1) => {
    const order = [...cfg.routing_order];
    const j = i + dir;
    if (j < 0 || j >= order.length) return;
    [order[i], order[j]] = [order[j], order[i]];
    set({ routing_order: order });
  };

  // Preset option lists from the geo databases. geosite categories feed the
  // domain fields, geoip the IP field. A value already chosen in another
  // category is hidden here so the same rule isn't added twice.
  const geositeOpts = useMemo<Opt[]>(
    () => geosite.map((c) => ({ value: `geosite:${c}`, label: c })),
    [geosite],
  );
  const geoipOpts = useMemo<Opt[]>(
    () => geoip.map((c) => ({ value: `geoip:${c}`, label: c })),
    [geoip],
  );
  const without = (opts: Opt[], used: string[]) => {
    const set = new Set(used);
    return opts.filter((o) => !set.has(o.value));
  };

  // Applied-state status badges per lane (reflect what's actually running, not
  // the pending toggle).
  const warpStatus = !savedWarp
    ? { label: "выключен", color: "gray" as const }
    : warpRegistered
      ? { label: "активен", color: "green" as const }
      : { label: "не зарегистрирован", color: "orange" as const };
  const operaStatus = helperStatus(savedOpera, operaRunning, operaAlive, "");
  const proxyStatus =
    proxyCount > 0
      ? { label: `${proxyCount} живые`, color: "green" as const }
      : { label: "нет прокси", color: "gray" as const };

  const save = () =>
    apply(async () => {
      // Routing rules + WARP/Opera on/off go in one request → one reconcile.
      await saveRouting(effectiveCfg, warpEnabled, operaEnabled, operaCountry);
      setSaved(JSON.stringify(effectiveCfg));
      setSavedWarp(warpEnabled);
      setSavedOpera(operaEnabled);
      setSavedOperaCountry(operaCountry);
      notifySuccess("Маршрутизация сохранена");
    }).then(refreshStatus); // re-fetch lane statuses AFTER Xray has restarted

  if (!loaded) return <CenterLoader />;

  return (
    <div className="flex flex-col gap-4 pb-20">
      {/* Block */}
      <Card className="flex flex-col gap-4 p-4">
        <ToggleRow
          label="Заблокировать рекламу"
          checked={cfg.block_ads}
          onChange={(v) => set({ block_ads: v })}
        />
        <ToggleRow
          label="Заблокировать BitTorrent"
          checked={cfg.block_bittorrent}
          onChange={(v) => set({ block_bittorrent: v })}
        />
        <TagsInput
          label="Заблокированные IP-адреса"
          value={cfg.block_ips}
          onChange={(v) => set({ block_ips: v })}
          options={without(geoipOpts, cfg.warp_ips)}
          placeholder="CIDR или geoip:xx…"
        />
        <TagsInput
          label="Заблокированные домены"
          value={cfg.block_domains}
          onChange={(v) => set({ block_domains: v })}
          options={without(geositeOpts, cfg.warp_domains)}
          placeholder="домен, regexp: или geosite:…"
        />
      </Card>

      {/* Routing order */}
      <Card className="flex flex-col gap-3 p-4">
        <div className="min-w-0">
          <p className="font-bold text-ink">Порядок маршрутизации</p>
          <p className="mt-0.5 text-sm text-ink-muted">
            Правила проверяются сверху вниз (блокировки — всегда первыми).
            Последний пункт — «всё остальное»: туда уходит весь несовпавший
            трафик.
          </p>
        </div>
        <div className="flex flex-col gap-1.5">
          {cfg.routing_order.map((lane, i) => {
            const last = i === cfg.routing_order.length - 1;
            return (
              <div
                key={lane}
                className="flex items-center gap-2 rounded-lg border border-gray-200 bg-gray-50 px-3 py-2"
              >
                <span className="w-5 text-sm font-bold text-ink-muted">
                  {i + 1}
                </span>
                <span className="flex-1 text-sm font-medium text-ink">
                  {LANES[lane] ?? lane}
                  {last && (
                    <span className="ml-2 text-xs font-normal text-ink-muted">
                      · всё остальное
                    </span>
                  )}
                </span>
                <button
                  type="button"
                  disabled={i === 0}
                  onClick={() => moveLane(i, -1)}
                  className="rounded p-1 text-gray-500 hover:bg-gray-200 disabled:opacity-30"
                >
                  <IconChevron className="rotate-180" />
                </button>
                <button
                  type="button"
                  disabled={last}
                  onClick={() => moveLane(i, 1)}
                  className="rounded p-1 text-gray-500 hover:bg-gray-200 disabled:opacity-30"
                >
                  <IconChevron />
                </button>
              </div>
            );
          })}
        </div>
      </Card>

      {/* Direct */}
      <Card className="flex flex-col gap-4 p-4">
        <div className="min-w-0">
          <p className="font-bold text-ink">Напрямую</p>
          <p className="mt-0.5 text-sm text-ink-muted">
            Эти домены/IP идут напрямую с этого сервера.
          </p>
        </div>
        <TagsInput
          label="Домены"
          value={cfg.direct_domains}
          onChange={(v) => set({ direct_domains: v })}
          options={geositeOpts}
          placeholder="домен, regexp: или geosite:…"
        />
        <TagsInput
          label="IP"
          value={cfg.direct_ips}
          onChange={(v) => set({ direct_ips: v })}
          options={geoipOpts}
          placeholder="CIDR или geoip:xx…"
        />
      </Card>

      {/* WARP */}
      <Card className="flex flex-col gap-4 p-4">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <p className="font-bold text-ink">Cloudflare WARP</p>
              <Badge color={warpStatus.color}>{warpStatus.label}</Badge>
            </div>
            <p className="mt-0.5 text-sm text-ink-muted">
              Включите, чтобы работали категории «Правила WARP» ниже.
            </p>
          </div>
          <Switch
            checked={warpEnabled}
            disabled={applying}
            onChange={setWarpEnabled}
          />
        </div>
        <TagsInput
          label="Правила WARP - Домены"
          value={cfg.warp_domains}
          onChange={(v) => set({ warp_domains: v })}
          options={without(geositeOpts, cfg.block_domains)}
          placeholder="домен, regexp: или geosite:…"
        />
        <TagsInput
          label="Правила WARP — IP"
          value={cfg.warp_ips}
          onChange={(v) => set({ warp_ips: v })}
          options={without(geoipOpts, cfg.block_ips)}
          placeholder="CIDR или geoip:xx…"
        />
      </Card>

      {/* Opera VPN */}
      <Card className="flex flex-col gap-4 p-4">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <p className="font-bold text-ink">Opera VPN</p>
              <Badge color={operaStatus.color}>{operaStatus.label}</Badge>
            </div>
            <p className="mt-0.5 text-sm text-ink-muted">
              Бесплатный Opera VPN как отдельный выход. Включите, чтобы работали
              категории «Правила Opera» ниже.
            </p>
          </div>
          <Switch
            checked={operaEnabled}
            disabled={applying}
            onChange={setOperaEnabled}
          />
        </div>
        <Select
          label="Регион"
          data={OPERA_COUNTRIES}
          value={operaCountry}
          onChange={setOperaCountry}
        />
        <TagsInput
          label="Правила Opera — Домены"
          value={cfg.opera_domains}
          onChange={(v) => set({ opera_domains: v })}
          options={without(geositeOpts, cfg.block_domains)}
          placeholder="домен, regexp: или geosite:…"
        />
        <TagsInput
          label="Правила Opera — IP"
          value={cfg.opera_ips}
          onChange={(v) => set({ opera_ips: v })}
          options={without(geoipOpts, cfg.block_ips)}
          placeholder="CIDR или geoip:xx…"
        />
      </Card>

      {/* Proxy pool */}
      <Card className="flex flex-col gap-4 p-4">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <p className="font-bold text-ink">Прокси</p>
            <Badge color={proxyStatus.color}>{proxyStatus.label}</Badge>
          </div>
          <p className="mt-0.5 text-sm text-ink-muted">
            Эти домены/IP идут через прокси.
          </p>
        </div>
        <div>
          <span className="mb-1.5 block text-sm text-ink-muted">
            Источник прокси
          </span>
          <SegmentedControl
            value={proxyMode}
            onChange={(v) => setProxyMode(v as "urls" | "manual")}
            data={[
              { value: "urls", label: "Файлы (URL)" },
              { value: "manual", label: "Вручную" },
            ]}
          />
        </div>
        {proxyMode === "urls" ? (
          <>
            <TagsInput
              label="URL-списки прокси"
              value={cfg.proxy_urls}
              onChange={(v) => set({ proxy_urls: v })}
              placeholder="https://example.com/proxy.txt — добавить и Enter…"
            />
            <Select
              label="Авто-обновление списков"
              data={PROXY_REFRESH}
              value={String(cfg.proxy_refresh_minutes)}
              onChange={(v) => set({ proxy_refresh_minutes: Number(v) })}
            />
          </>
        ) : (
          <TagsInput
            label="Прокси вручную"
            value={cfg.proxy_manual}
            onChange={(v) => set({ proxy_manual: v })}
            placeholder="socks5://ip:port — добавить и Enter…"
          />
        )}
        <TagsInput
          label="Через прокси — Домены"
          value={cfg.proxy_domains}
          onChange={(v) => set({ proxy_domains: v })}
          options={without(geositeOpts, cfg.block_domains)}
          placeholder="домен, regexp: или geosite:…"
        />
        <TagsInput
          label="Через прокси — IP"
          value={cfg.proxy_ips}
          onChange={(v) => set({ proxy_ips: v })}
          options={without(geoipOpts, cfg.block_ips)}
          placeholder="CIDR или geoip:xx…"
        />
      </Card>

      {/* Geo databases */}
      <Card className="flex flex-col gap-3 p-4">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <p className="font-bold text-ink">Geo-базы</p>
            <p className="mt-0.5 text-sm text-ink-muted">
              geosite.dat / geoip.dat — категории доменов и IP для правил выше.
            </p>
          </div>
          <Button
            variant="light"
            size="sm"
            loading={applying}
            onClick={refreshGeo}
          >
            Обновить
          </Button>
        </div>
        <div className="flex flex-col gap-1 text-sm">
          {geoStatus.map((f) => (
            <div
              key={f.name}
              className="flex items-center justify-between gap-2"
            >
              <span className="font-mono text-ink text-xs">{f.name}</span>
              <span className="text-ink-muted text-xs">
                {f.present
                  ? `${fmtBytes(f.size)} · обновлено ${fmtWhen(f.modified_at)}`
                  : "нет файла"}
              </span>
            </div>
          ))}
        </div>
      </Card>

      <SaveBar
        dirty={dirty}
        busy={applying}
        onSave={save}
        onCancel={() => {
          // Restore EVERY field that feeds `dirty`, not just cfg/warp — otherwise
          // Opera/proxy-source edits stay applied and the SaveBar never clears.
          const c = JSON.parse(saved) as RoutingConfig;
          setCfg(c);
          setWarpEnabled(savedWarp);
          setOperaEnabled(savedOpera);
          setOperaCountry(savedOperaCountry);
          setProxyMode(
            (c.proxy_manual.length ?? 0) > 0 && c.proxy_urls.length === 0
              ? "manual"
              : "urls",
          );
        }}
      />
      <ApplyingModal open={applying} />
    </div>
  );
}
