import { useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import {
  ANNOUNCE_MAX,
  getSettings,
  getSubRules,
  saveSubDPI,
  DEFAULT_SUB_DPI,
  saveHWIDSettings,
  saveSubRules,
  saveSubSettings,
  type HWIDSettings,
  type SubRule,
  type SubDPI,
  type SubSettings,
} from "./api";
import { useAction, useDirtyForm } from "./hooks";
import i18n from "./i18n";
import { notifySuccess } from "./notify";
import { subPathError } from "./validate";
import {
  Card,
  CenterLoader,
  cn,
  SaveBar,
  Select,
  Switch,
  Textarea,
  TextInput,
  ToggleRow,
} from "./ui";
import { SubDPICard } from "./SubDPICard";

const ROUTING_REPO = "https://github.com/hydraponique/roscomvpn-routing";

const EMPTY_SUB: SubSettings = {
  sub_path: "sub",
  sub_base64: true,
  sub_name_in_title: false,
  sub_title: "",
  sub_routing: true,
  sub_routing_happ: "",
  sub_routing_incy: "",
  sub_routing_mihomo: "",
  sub_update_interval: 1,
  sub_announce: "",
  sub_show_configs: true,
  sub_order_mode: "manual",
  sub_hide_offline: false,
};

// Requiring an id is the default: a cap a client can dodge by staying silent is not
// a cap. The panel overwrites this with the saved settings on load; it only matters
// for the moment before they arrive.
const EMPTY_HWID: HWIDSettings = {
  enabled: false,
  require: true,
  fallback_limit: 0,
  ttl_days: 30,
  count_mode: "auto",
};

// Subscription auto-update cadence (hours; "0" = never).
const intervals = () => [
  { value: "0", label: i18n.t("subs.never") },
  ...[1, 6, 12, 24, 48].map((h) => ({
    value: String(h),
    label: i18n.t("subs.hours", { count: h }),
  })),
  { value: "168", label: i18n.t("subs.week") },
];

export function SubscriptionsPanel() {
  const { t } = useTranslation();
  const [loaded, setLoaded] = useState(false);
  const { draft: s, setDraft: setS, isDirty: dirty, load, commit, reset } = useDirtyForm<SubSettings>(EMPTY_SUB);
  const [secret, setSecret] = useState("");
  // The DPI block and the response rules are separate endpoints, but to the operator
  // this is one page: their drafts live here and go out with the page's own Save.
  const [dpi, setDpi] = useState<SubDPI>(DEFAULT_SUB_DPI);
  const [dpiSaved, setDpiSaved] = useState<SubDPI>(DEFAULT_SUB_DPI);
  const [rules, setRules] = useState<SubRule[]>([]);
  const [rulesSaved, setRulesSaved] = useState<SubRule[]>([]);
  const dpiDirty = JSON.stringify(dpi) !== JSON.stringify(dpiSaved);
  const rulesDirty = JSON.stringify(rules) !== JSON.stringify(rulesSaved);

  const {
    draft: h,
    setDraft: setH,
    isDirty: hwidDirty,
    load: loadHwid,
    commit: commitHwid,
    reset: resetHwid,
  } = useDirtyForm<HWIDSettings>(EMPTY_HWID);
  const { busy, run } = useAction();

  useEffect(() => {
    getSettings()
      .then((d) => {
        const init: SubSettings = {
          sub_path: d.sub_path,
          sub_base64: d.sub_base64,
          sub_name_in_title: d.sub_name_in_title,
          sub_title: d.sub_title,
          sub_routing: d.sub_routing,
          sub_routing_happ: d.sub_routing_happ,
          sub_routing_incy: d.sub_routing_incy,
          sub_routing_mihomo: d.sub_routing_mihomo,
          sub_update_interval: d.sub_update_interval,
          sub_announce: d.sub_announce,
          sub_show_configs: d.sub_show_configs,
          sub_order_mode: d.sub_order_mode ?? "manual",
          sub_hide_offline: d.sub_hide_offline ?? false,
        };
        load(init);
        loadHwid(d.hwid ?? EMPTY_HWID);
        setSecret(d.secret_path);
        const dpiInit = { ...DEFAULT_SUB_DPI, ...(d.sub_dpi ?? {}) };
        setDpi(dpiInit);
        setDpiSaved(dpiInit);
      })
      .catch(() => {})
      .finally(() => setLoaded(true));
    getSubRules()
      .then((rs) => {
        setRules(rs);
        setRulesSaved(rs);
      })
      .catch(() => {});
  }, []);

  const patch = (p: Partial<SubSettings>) => setS((cur) => ({ ...cur, ...p }));
  const pathErr = subPathError(s.sub_path, secret);
  // Count runes the way the server does, not UTF-16 code units: an emoji is one
  // character to the client that renders it, and two to String.length.
  const announceLen = [...s.sub_announce.trim()].length;
  const announceErr = announceLen > ANNOUNCE_MAX;

  const patchHwid = (p: Partial<HWIDSettings>) => setH((cur) => ({ ...cur, ...p }));

  // One save button for the page: the two blocks are separate endpoints (device
  // binding doesn't touch the public path or the routing headers), but to the
  // operator this is one settings page and one Save.
  const save = () =>
    run(async () => {
      if (dirty) await saveSubSettings(s);
      if (hwidDirty) await saveHWIDSettings(h);
      if (dpiDirty) {
        await saveSubDPI(dpi);
        setDpiSaved(dpi);
      }
      if (rulesDirty) {
        await saveSubRules(rules);
        setRulesSaved(rules);
      }
      commit();
      commitHwid();
      notifySuccess(t("subs.saved"));
    });

  if (!loaded) return <CenterLoader />;

  return (
    <div className="flex flex-col gap-4 pb-20">
      <Card className="p-4">
        <h3 className="mb-3 font-bold text-ink">{t("subs.format")}</h3>
        <div className="flex flex-col gap-4">
          <div>
            <TextInput
              label={t("subs.path")}
              placeholder="sub"
              value={s.sub_path}
              onChange={(v) =>
                patch({ sub_path: v.replace(/[^A-Za-z0-9_-]/g, "") })
              }
            />
            {pathErr ? (
              <p className="mt-1 text-xs text-danger">{pathErr}</p>
            ) : (
              <p className="mt-1 text-xs text-ink-muted">
                {t("subs.pathHint", { path: s.sub_path || "sub" })}
              </p>
            )}
          </div>
          <ToggleRow
            label={t("subs.base64")}
            hint={t("subs.base64Hint")}
            checked={s.sub_base64}
            onChange={(v) => patch({ sub_base64: v })}
          />
          <TextInput
            label={t("subs.title")}
            placeholder={t("subs.titlePlaceholder")}
            value={s.sub_title}
            onChange={(v) => patch({ sub_title: v })}
          />
          <ToggleRow
            label={t("subs.nameInTitle")}
            hint={t("subs.nameInTitleHint")}
            checked={s.sub_name_in_title}
            onChange={(v) => patch({ sub_name_in_title: v })}
          />
          <Select
            label={t("subs.updateInterval")}
            data={intervals()}
            value={String(s.sub_update_interval)}
            onChange={(v) => patch({ sub_update_interval: Number(v) })}
          />
          <div className="flex flex-col gap-1">
            <Select
              label={t("subs.orderMode.label")}
              data={[
                { value: "manual", label: t("subs.orderMode.manual") },
                { value: "load", label: t("subs.orderMode.load") },
              ]}
              value={s.sub_order_mode}
              onChange={(v) => patch({ sub_order_mode: v })}
            />
            <p className="text-xs text-ink-muted">{t("subs.orderMode.hint")}</p>
          </div>
          <ToggleRow
            label={t("subs.hideOffline")}
            hint={t("subs.hideOfflineHint")}
            checked={s.sub_hide_offline}
            onChange={(v) => patch({ sub_hide_offline: v })}
          />
          <div>
            <Textarea
              label={t("subs.announce")}
              placeholder={t("subs.announcePlaceholder")}
              rows={2}
              value={s.sub_announce}
              onChange={(v) => patch({ sub_announce: v })}
            />
            <p
              className={cn(
                "mt-1 text-xs",
                announceErr ? "text-danger" : "text-ink-muted",
              )}
            >
              {t("subs.announceHint")} {announceLen}/{ANNOUNCE_MAX}
            </p>
          </div>
          <ToggleRow
            label={t("subs.showConfigs")}
            hint={t("subs.showConfigsHint")}
            checked={s.sub_show_configs}
            onChange={(v) => patch({ sub_show_configs: v })}
          />
        </div>
      </Card>

      <Card className="p-4">
        <div className="mb-3 flex items-center justify-between gap-3">
          <div>
            <h3 className="font-bold text-ink">{t("subs.hwid")}</h3>
            <p className="text-xs text-ink-muted">{t("subs.hwidHint")}</p>
          </div>
          <Switch
            checked={h.enabled}
            onChange={(v) => patchHwid({ enabled: v })}
          />
        </div>
        {h.enabled && (
          <div className="flex flex-col gap-4">
            <ToggleRow
              label={t("subs.hwidRequire")}
              hint={t("subs.hwidRequireHint")}
              checked={h.require}
              onChange={(v) => patchHwid({ require: v })}
            />
            <div>
              <Select
                label={t("subs.countMode")}
                // "both" is a stored value from before the handover grace was removed.
                // It now behaves exactly as "auto", so it shows as "auto" rather than as
                // a third choice that does the same thing under a different name.
                value={h.count_mode === "hwid" ? "hwid" : "auto"}
                onChange={(v) => patchHwid({ count_mode: v })}
                data={[
                  { value: "auto", label: t("subs.countModeAuto") },
                  { value: "hwid", label: t("subs.countModeHWID") },
                ]}
              />
              <p className="mt-1 text-xs text-ink-muted">{t("subs.countModeHint")}</p>
            </div>
            <div>
              <TextInput
                label={t("subs.hwidFallback")}
                type="number"
                value={String(h.fallback_limit)}
                onChange={(v) =>
                  patchHwid({ fallback_limit: Math.max(0, Number(v) || 0) })
                }
              />
              <p className="mt-1 text-xs text-ink-muted">
                {t("subs.hwidFallbackHint")}
              </p>
            </div>
            <div>
              <TextInput
                label={t("subs.hwidTTL")}
                type="number"
                value={String(h.ttl_days)}
                onChange={(v) => patchHwid({ ttl_days: Math.max(0, Number(v) || 0) })}
              />
              <p className="mt-1 text-xs text-ink-muted">{t("subs.hwidTTLHint")}</p>
            </div>
          </div>
        )}
      </Card>

      <Card className="p-4">
        <div className="mb-3 flex items-center justify-between gap-3">
          <div>
            <h3 className="font-bold text-ink">{t("subs.routing")}</h3>
            <p className="text-xs text-ink-muted">
              {t("subs.routingHint")}{" "}
              <a
                href={ROUTING_REPO}
                target="_blank"
                rel="noreferrer"
                className="text-accent hover:underline"
              >
                roscomvpn-routing
              </a>
              .
            </p>
          </div>
          <Switch
            checked={s.sub_routing}
            onChange={(v) => patch({ sub_routing: v })}
          />
        </div>
        {s.sub_routing && (
          <div className="flex flex-col gap-3">
            <TextInput
              label={t("subs.happRules")}
              placeholder="https://.../HAPP/DEFAULT.DEEPLINK"
              value={s.sub_routing_happ}
              onChange={(v) => patch({ sub_routing_happ: v })}
            />
            <TextInput
              label={t("subs.incyRules")}
              placeholder="https://.../INCY/DEFAULT.DEEPLINK"
              value={s.sub_routing_incy}
              onChange={(v) => patch({ sub_routing_incy: v })}
            />
            <div>
              <TextInput
                label={t("subs.mihomoRules")}
                placeholder="https://.../MIHOMO/default.yaml"
                value={s.sub_routing_mihomo}
                onChange={(v) => patch({ sub_routing_mihomo: v })}
              />
              <p className="mt-1 text-xs text-ink-muted">
                <Trans
                  i18nKey="subs.mihomoHint"
                  components={{
                    marker: (
                      <code className="rounded bg-gray-100 px-1 font-mono" />
                    ),
                  }}
                />
              </p>
            </div>
          </div>
        )}
      </Card>

      <SubDPICard value={dpi} onChange={setDpi} />
      <SubRulesEditor value={rules} onChange={setRules} />

      <SaveBar
        dirty={dirty || hwidDirty || dpiDirty || rulesDirty}
        busy={busy}
        saveDisabled={!!pathErr || announceErr}
        onSave={save}
        onCancel={() => {
          reset();
          resetHwid();
          setDpi(dpiSaved);
          setRules(rulesSaved);
        }}
      />
    </div>
  );
}

// SubRulesEditor edits the ordered response rules: each matches a request attribute
// (User-Agent or an HWID header) and forces a subscription format or blocks the
// client. Its own endpoint, but not its own Save: the draft belongs to the page, so
// everything on this screen is saved and cancelled together.
function SubRulesEditor({
  value: rules,
  onChange,
}: {
  value: SubRule[];
  onChange: (v: SubRule[]) => void;
}) {
  const { t } = useTranslation();
  const setRules = (fn: (rs: SubRule[]) => SubRule[]) => onChange(fn(rules));

  const patchRule = (i: number, p: Partial<SubRule>) =>
    setRules((rs) => rs.map((r, j) => (j === i ? { ...r, ...p } : r)));
  const addRule = () =>
    setRules((rs) => [
      ...rs,
      { field: "user_agent", op: "contains", value: "", action: "clash", enabled: true },
    ]);
  const removeRule = (i: number) => setRules((rs) => rs.filter((_, j) => j !== i));
  const move = (i: number, d: -1 | 1) =>
    setRules((rs) => {
      const j = i + d;
      if (j < 0 || j >= rs.length) return rs;
      const out = [...rs];
      [out[i], out[j]] = [out[j], out[i]];
      return out;
    });

  // Literal keys (not template strings) so the typed i18n accepts them.
  const fieldOpts: { value: SubRule["field"]; label: string }[] = [
    { value: "user_agent", label: t("subs.ruleField.user_agent") },
    { value: "device_os", label: t("subs.ruleField.device_os") },
    { value: "ver_os", label: t("subs.ruleField.ver_os") },
    { value: "device_model", label: t("subs.ruleField.device_model") },
  ];
  const opOpts: { value: SubRule["op"]; label: string }[] = [
    { value: "contains", label: t("subs.ruleOp.contains") },
    { value: "not_contains", label: t("subs.ruleOp.not_contains") },
    { value: "equals", label: t("subs.ruleOp.equals") },
    { value: "prefix", label: t("subs.ruleOp.prefix") },
    { value: "regex", label: t("subs.ruleOp.regex") },
  ];
  const actionOpts: { value: SubRule["action"]; label: string }[] = [
    { value: "v2ray", label: t("subs.ruleAction.v2ray") },
    { value: "clash", label: t("subs.ruleAction.clash") },
    { value: "singbox", label: t("subs.ruleAction.singbox") },
    { value: "xray-json", label: t("subs.ruleAction.xray-json") },
    { value: "block", label: t("subs.ruleAction.block") },
  ];

  return (
    <Card className="p-4">
      <h3 className="mb-1 font-bold text-ink">{t("subs.rules")}</h3>
      <p className="mb-3 text-xs text-ink-muted">{t("subs.rulesHint")}</p>
      <div className="flex flex-col gap-2">
        {rules.map((r, i) => (
          <div
            key={i}
            className="flex flex-wrap items-center gap-2 rounded-lg border border-gray-200/70 bg-gray-50/60 p-2"
          >
            <Switch checked={r.enabled} onChange={(v) => patchRule(i, { enabled: v })} />
            <Select
              value={r.field}
              onChange={(v) => patchRule(i, { field: v as SubRule["field"] })}
              data={fieldOpts}
            />
            <Select
              value={r.op}
              onChange={(v) => patchRule(i, { op: v as SubRule["op"] })}
              data={opOpts}
            />
            <TextInput
              className="min-w-32 flex-1"
              value={r.value}
              onChange={(v) => patchRule(i, { value: v })}
              placeholder={t("subs.ruleValue")}
            />
            <span className="text-ink-muted">→</span>
            <Select
              value={r.action}
              onChange={(v) => patchRule(i, { action: v as SubRule["action"] })}
              data={actionOpts}
            />
            <button
              type="button"
              className="px-1 text-ink-muted hover:text-ink"
              onClick={() => move(i, -1)}
              title="↑"
            >
              ↑
            </button>
            <button
              type="button"
              className="px-1 text-ink-muted hover:text-ink"
              onClick={() => move(i, 1)}
              title="↓"
            >
              ↓
            </button>
            <button
              type="button"
              className="px-1 text-red-500 hover:text-red-700"
              onClick={() => removeRule(i)}
              title={t("common.delete")}
            >
              ✕
            </button>
          </div>
        ))}
        {rules.length === 0 && (
          <p className="text-sm text-ink-muted">{t("subs.rulesEmpty")}</p>
        )}
      </div>
      <div className="mt-3 flex items-center gap-2">
        <button
          type="button"
          className="rounded-lg border border-gray-300 px-3 py-1.5 text-sm hover:bg-gray-50"
          onClick={addRule}
        >
          + {t("subs.ruleAdd")}
        </button>
      </div>
    </Card>
  );
}
