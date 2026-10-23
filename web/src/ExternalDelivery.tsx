import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  type ExtRelayVia,
  type ExtRouting,
  type ExtRule,
  type ExtServer,
  type ExtSubscription,
  type GeoCategories,
  getGeoCategories,
  type NodeView,
  setExternalRelay,
} from "./api";
import { useAction } from "./hooks";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { serverName } from "./NodeStatus";
import { Button, cn, IconButton, IconCheck, IconPlus, IconTrash, Modal, SegmentedControl, Select, TagsInput } from "./ui";

// Where an external subscription goes: handed out as it is, or through our servers.
// Through them, the operator picks the servers (only one when the subscription has
// WireGuard servers: a config is one device) and the lanes on each, then how the
// traffic leaves them — rules sending part of it elsewhere than the partner, where the
// rest goes — and how our server reaches the partner. Only what is switched on on every
// chosen server is offered.

const LANES = ["vless", "reality"] as const;
type Lane = (typeof LANES)[number];

export const relayLaneLabel = (lane: string) => (lane === "reality" ? "REALITY" : "TCP-TLS");

function laneOn(n: NodeView, lane: string): boolean {
  return lane === "reality" ? n.reality_enabled && !!n.reality_public_key : n.vless_enabled;
}

// A way out a chosen server may lack: available when every chosen server has it, else
// missing names those that do not.
type Way = { value: string; label: string; missing: string[] };

export function ExternalDeliveryDialog({
  sub,
  servers,
  nodes,
  onClose,
  onSaved,
}: {
  sub: ExtSubscription;
  servers: ExtServer[];
  nodes: NodeView[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const { busy, run } = useAction();
  const wg = servers.some((s) => s.protocol === "wireguard");
  const onlyWG = servers.length > 0 && servers.every((s) => s.protocol === "wireguard");

  const [relays, setRelays] = useState<ExtRelayVia[]>(sub.relays ?? []);
  const [mode, setMode] = useState<"direct" | "via">(relays.length > 0 || onlyWG ? "via" : "direct");
  const [rules, setRules] = useState<ExtRule[]>(sub.routing?.rules ?? []);
  // The geo databases' categories, for the chips a rule picks from (as the server's
  // routing offers them).
  const [geo, setGeo] = useState<GeoCategories>({ geosite: [], geoip: [], iplist: [] });
  useEffect(() => {
    getGeoCategories()
      .then(setGeo)
      .catch(() => {});
  }, []);
  const domainOpts = useMemo(
    () => [
      ...geo.geosite.map((c) => ({ value: `geosite:${c}`, label: c })),
      ...geo.iplist.map((g) => ({ value: `iplist:${g}`, label: `iplist: ${g}` })),
    ],
    [geo],
  );
  const ipOpts = useMemo(
    () => [
      ...geo.geoip.map((c) => ({ value: `geoip:${c}`, label: c })),
      ...geo.iplist.map((g) => ({ value: `iplist:${g}`, label: `iplist: ${g}` })),
    ],
    [geo],
  );
  const patchRule = (i: number, d: Partial<ExtRule>) => setRules(rules.map((x, j) => (j === i ? { ...x, ...d } : x)));
  const [rest, setRest] = useState(sub.routing?.rest || "partner");
  const [dial, setDial] = useState(sub.routing?.dial || "");

  const chosenIds = [...new Set(relays.map((v) => v.server_id))];
  const chosen = chosenIds.map((id) => nodes.find((n) => n.id === id)).filter((n): n is NodeView => !!n);
  const has = (id: number, lane: string) => relays.some((v) => v.server_id === id && v.lane === lane);

  // Picking a server takes every lane it runs; with WireGuard it is the only one.
  const pickServer = (n: NodeView) => {
    if (chosenIds.includes(n.id)) {
      setRelays(relays.filter((v) => v.server_id !== n.id));
      return;
    }
    const lanes = LANES.filter((l) => laneOn(n, l)).map((lane) => ({ server_id: n.id, lane }));
    setRelays(wg ? lanes : [...relays, ...lanes]);
  };
  const toggleLane = (id: number, lane: Lane) => {
    const next = has(id, lane)
      ? relays.filter((v) => !(v.server_id === id && v.lane === lane))
      : [...(wg ? relays.filter((v) => v.server_id === id) : relays), { server_id: id, lane }];
    setRelays(next);
  };

  // What the chosen servers can send traffic to beyond the partner, direct and block.
  const ways = useMemo((): Way[] => {
    const missing = (ok: (n: NodeView) => boolean) => chosen.filter((n) => !ok(n)).map(serverName);
    const out: Way[] = [
      { value: "warp", label: "WARP", missing: missing((n) => n.warp_enabled && n.warp_registered) },
    ];
    for (const l of chosen[0]?.routing?.lanes ?? []) {
      if (!l.enabled) continue;
      out.push({
        value: l.id,
        label: t("extRoute.lane", { name: l.name || l.id }),
        missing: missing((n) => (n.routing?.lanes ?? []).some((x) => x.id === l.id && x.enabled)),
      });
    }
    return out;
  }, [chosen, t]);

  const fixed = [
    { value: "partner", label: t("extRoute.toPartner") },
    { value: "direct", label: t("extRoute.toDirect") },
    { value: "block", label: t("extRoute.toBlock") },
  ];
  // A target a rule already holds stays listed, marked, so the select says what is set.
  const targetsFor = (current: string) => {
    const list = [...fixed, ...ways.filter((w) => w.missing.length === 0).map((w) => ({ value: w.value, label: w.label }))];
    if (current && !list.some((x) => x.value === current)) {
      list.push({
        value: current,
        label: t("extRoute.unavailable", { name: ways.find((w) => w.value === current)?.label ?? current }),
      });
    }
    return list;
  };

  const save = () =>
    run(async () => {
      const list = mode === "via" ? relays : [];
      const routing: ExtRouting = {
        rules: rules.filter((r) => r.domains.length + r.ips.length > 0),
        rest: rest === "partner" ? "" : rest,
        dial,
      };
      await setExternalRelay(sub.id, list, list.length > 0 ? routing : undefined);
      notifySuccess(t("external.deliverySaved"));
      onSaved();
    }).catch((e) => notifyError(errMessage(e)));

  const canSave = mode === "direct" ? !onlyWG : relays.length > 0;

  return (
    <Modal
      open
      size="lg"
      onClose={onClose}
      title={t("extRoute.title")}
      subtitle={sub.name}
      footer={
        <div className="flex justify-end gap-2">
          <Button variant="light" color="gray" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button onClick={save} loading={busy} disabled={!canSave}>
            {t("common.save")}
          </Button>
        </div>
      }
    >
      <div className="flex flex-col gap-6">
        {!onlyWG && (
        <section className="flex flex-col gap-2">
          <SegmentedControl
            fullWidth
            value={mode}
            onChange={(v) => setMode(v as "direct" | "via")}
            data={[
              { value: "direct", label: t("extRoute.modeDirect") },
              { value: "via", label: t("extRoute.modeVia") },
            ]}
          />
          <p className={cn("text-xs leading-relaxed", mode === "direct" && wg ? "text-warning" : "text-ink-muted")}>
            {mode === "direct"
              ? t(onlyWG ? "extRoute.directWG" : wg ? "extRoute.directSomeWG" : "extRoute.directHint")
              : t("extRoute.viaHint")}
          </p>
        </section>
        )}
        {onlyWG && <p className="text-xs leading-relaxed text-ink-muted">{t("extRoute.viaHintWG")}</p>}

        {mode === "via" && (
          <>
            <section className="flex flex-col gap-2">
              <Heading n={1} title={t("extRoute.servers")} hint={t(wg ? "extRoute.serversHintWG" : "extRoute.serversHint")} />
              <div className="grid gap-2 sm:grid-cols-2">
                {nodes.map((n) => {
                  const on = chosenIds.includes(n.id);
                  return (
                    <div
                      key={n.id}
                      className={cn(
                        "rounded-xl border p-3 transition",
                        on ? "border-brand-600/40 bg-brand-50/60" : "border-gray-200 bg-white hover:border-gray-300",
                      )}
                    >
                      <button type="button" onClick={() => pickServer(n)} className="flex w-full items-center gap-2 text-start">
                        <span
                          className={cn(
                            "flex size-5 shrink-0 items-center justify-center border transition",
                            wg ? "rounded-full" : "rounded-md",
                            on ? "border-brand-600 bg-brand-600 text-white" : "border-gray-300 bg-white",
                          )}
                        >
                          {on && <IconCheck size={13} />}
                        </span>
                        <span className="min-w-0 flex-1 truncate text-sm font-medium text-ink">{serverName(n)}</span>
                      </button>
                      <div className="mt-2.5 flex flex-wrap gap-1.5 ps-7">
                        {LANES.map((lane) => {
                          const runs = laneOn(n, lane);
                          const picked = has(n.id, lane);
                          return (
                            <button
                              key={lane}
                              type="button"
                              disabled={!runs && !picked}
                              onClick={() => toggleLane(n.id, lane)}
                              title={runs ? undefined : t("extRoute.laneOff")}
                              className={cn(
                                "rounded-full border px-2.5 py-0.5 text-xs transition",
                                picked
                                  ? "border-brand-600 bg-brand-600 text-white"
                                  : runs
                                    ? "border-gray-300 text-ink hover:border-brand-600/50"
                                    : "cursor-not-allowed border-dashed border-gray-200 text-ink-muted line-through",
                              )}
                            >
                              {relayLaneLabel(lane)}
                            </button>
                          );
                        })}
                      </div>
                    </div>
                  );
                })}
              </div>
            </section>

            {relays.length > 0 && (
              <>
                <section className="flex flex-col gap-2">
                  <Heading n={2} title={t("extRoute.routing")} hint={t("extRoute.routingHint")} />
                  <div className="flex flex-col gap-2">
                    {rules.map((r, i) => (
                      <div
                        // biome-ignore lint/suspicious/noArrayIndexKey: rows have no identity of their own; they are edited in place
                        key={i}
                        className="rounded-xl border border-gray-200 bg-white"
                      >
                        <div className="flex items-center gap-2 border-b border-gray-100 px-3 py-2">
                          <span className="flex-1 text-xs font-semibold uppercase tracking-wide text-ink-muted">
                            {t("extRoute.ruleN", { n: i + 1 })}
                          </span>
                          <span className="text-xs text-ink-muted">{t("extRoute.sendTo")}</span>
                          <div className="w-52">
                            <Select size="sm" value={r.target} data={targetsFor(r.target)} onChange={(v) => patchRule(i, { target: v })} />
                          </div>
                          <IconButton color="red" title={t("common.delete")} onClick={() => setRules(rules.filter((_, j) => j !== i))}>
                            <IconTrash size={16} />
                          </IconButton>
                        </div>
                        <div className="grid gap-3 p-3 sm:grid-cols-2">
                          <div className="min-w-0">
                            <p className="mb-1 text-xs font-medium text-ink">{t("extRoute.domains")}</p>
                            <TagsInput
                              value={r.domains}
                              onChange={(v) => patchRule(i, { domains: v })}
                              options={domainOpts}
                              placeholder={t("extRoute.domainsPh")}
                            />
                          </div>
                          <div className="min-w-0">
                            <p className="mb-1 text-xs font-medium text-ink">{t("extRoute.ips")}</p>
                            <TagsInput
                              value={r.ips}
                              onChange={(v) => patchRule(i, { ips: v })}
                              options={ipOpts}
                              placeholder={t("extRoute.ipsPh")}
                            />
                          </div>
                        </div>
                      </div>
                    ))}
                    <div className="flex items-center gap-3 rounded-xl border border-brand-600/15 bg-brand-50/40 px-3 py-2.5">
                      <div className="min-w-0 flex-1">
                        <p className="text-sm font-medium text-ink">{t(rules.length ? "extRoute.restOther" : "extRoute.restAll")}</p>
                        <p className="text-xs text-ink-muted">{t(rules.length ? "extRoute.restOtherHint" : "extRoute.restAllHint")}</p>
                      </div>
                      <div className="w-52 shrink-0">
                        <Select size="sm" value={rest} data={targetsFor(rest)} onChange={setRest} />
                      </div>
                    </div>
                  </div>
                  <div>
                    <Button size="xs" variant="light" onClick={() => setRules([...rules, { domains: [], ips: [], target: "direct" }])}>
                      <IconPlus size={14} /> {t("extRoute.addRule")}
                    </Button>
                  </div>
                </section>

                <section className="flex flex-col gap-2">
                  <Heading n={3} title={t("extRoute.dial")} hint={t("extRoute.dialHint")} />
                  <div className="flex flex-col gap-1.5">
                    <Option on={dial === ""} onClick={() => setDial("")} title={t("extRoute.dialDirect")} hint={t("extRoute.dialDirectHint")} />
                    {ways.map((w) => {
                      const why = w.missing.length > 0 ? t("extRoute.notOn", { servers: w.missing.join(", ") }) : "";
                      const hint = why || t(w.value === "warp" ? "extRoute.dialWarpHint" : "extRoute.dialLaneHint");
                      return (
                        <Option
                          key={w.value}
                          on={dial === w.value}
                          disabled={!!why && dial !== w.value}
                          onClick={() => setDial(w.value)}
                          title={t("extRoute.dialVia", { name: w.label })}
                          hint={hint}
                          warn={!!why}
                        />
                      );
                    })}
                  </div>
                </section>
              </>
            )}
          </>
        )}
      </div>
    </Modal>
  );
}

function Heading({ n, title, hint }: { n: number; title: string; hint: string }) {
  return (
    <div className="flex items-start gap-2.5">
      <span className="mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full bg-brand-600/10 text-[11px] font-semibold text-brand-700">
        {n}
      </span>
      <div>
        <p className="text-sm font-semibold text-ink">{title}</p>
        <p className="mt-0.5 text-xs leading-relaxed text-ink-muted">{hint}</p>
      </div>
    </div>
  );
}

function Option({
  on,
  disabled,
  onClick,
  title,
  hint,
  warn,
}: {
  on: boolean;
  disabled?: boolean;
  onClick: () => void;
  title: string;
  hint: string;
  warn?: boolean;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "flex w-full items-start gap-2.5 rounded-xl border px-3 py-2.5 text-start transition",
        on ? "border-brand-600/40 bg-brand-50/60" : "border-gray-200 bg-white hover:border-gray-300",
        disabled && "cursor-not-allowed opacity-60 hover:border-gray-200",
      )}
    >
      <span
        className={cn(
          "mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full border",
          on ? "border-brand-600" : "border-gray-300",
        )}
      >
        {on && <span className="size-2 rounded-full bg-brand-600" />}
      </span>
      <span className="min-w-0">
        <span className="block text-sm text-ink">{title}</span>
        <span className={cn("block text-xs", warn ? "text-warning" : "text-ink-muted")}>{hint}</span>
      </span>
    </button>
  );
}
