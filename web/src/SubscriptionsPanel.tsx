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
  saveSubTemplates,
  getSubTemplates,
  type SubTemplates,
  saveSubSettings,
  type HWIDSettings,
  type SubRule,
  type SubDPI,
  type SubApp,
  type SubSettings,
  type AppLinks,
} from "./api";
import { useAction, useDirtyForm } from "./hooks";
import i18n from "./i18n";
import { notifySuccess } from "./notify";
import { subPathError } from "./validate";
import {
  CenterLoader,
  cn,
  IconButton,
  IconChevron,
  IconPlus,
  IconTrash,
  Panel,
  SaveBar,
  Select,
  SettingRow,
  Switch,
  Textarea,
  TextInput,
  ToggleRow,
} from "./ui";
import { SubDPICard } from "./SubDPICard";
import { SubRulesEditor } from "./SubRulesEditor";
import { SubTemplatesCard } from "./SubTemplatesCard";

// The wizard's platforms, in its order; the OS names read the same in every language.
const WIZARD_PLATFORMS: [string, string][] = [
  ["ios", "iPhone · iPad"],
  ["android", "Android"],
  ["windows", "Windows"],
  ["macos", "macOS"],
  ["linux", "Linux"],
  ["tv", "TV"],
];

const ROUTING_REPO = "https://github.com/hydraponique/roscomvpn-routing";

const EMPTY_TPL: SubTemplates = { clash: "", singbox: "", xray: "" };

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
  sub_show_clash: true,
  sub_happ_crypt: false,
  sub_order_mode: "manual",
  sub_hide_offline: false,
  sub_self_rotate: 0,
  sub_page_enabled: true,
  sub_page_url: "",
  sub_moved_to: "",
  sub_apps: [],
  sub_wizard_apps: {},
};

// Requiring an id is the default: a cap a client can dodge by staying silent is not
// a cap. The panel overwrites this with the saved settings on load; it only matters
// for the moment before they arrive.
const EMPTY_HWID: HWIDSettings = {
  enabled: false,
  require: true,
  fallback_limit: 0,
  ip_fallback_limit: 0,
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
  const [tpl, setTpl] = useState<SubTemplates>(EMPTY_TPL);
  const [tplSaved, setTplSaved] = useState<SubTemplates>(EMPTY_TPL);
  const dpiDirty = JSON.stringify(dpi) !== JSON.stringify(dpiSaved);
  const rulesDirty = JSON.stringify(rules) !== JSON.stringify(rulesSaved);
  const tplDirty = JSON.stringify(tpl) !== JSON.stringify(tplSaved);

  const {
    draft: h,
    setDraft: setH,
    isDirty: hwidDirty,
    load: loadHwid,
    commit: commitHwid,
    reset: resetHwid,
  } = useDirtyForm<HWIDSettings>(EMPTY_HWID);
  const { busy, run } = useAction();

  // biome-ignore lint/correctness/useExhaustiveDependencies: runs once on mount; the loader is redefined every render, so listing it would refetch in a loop
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
          sub_show_clash: d.sub_show_clash ?? true,
          sub_happ_crypt: d.sub_happ_crypt ?? false,
          sub_order_mode: d.sub_order_mode ?? "manual",
          sub_hide_offline: d.sub_hide_offline ?? false,
          sub_self_rotate: d.sub_self_rotate ?? 0,
          sub_page_enabled: d.sub_page_enabled ?? true,
          sub_page_url: d.sub_page_url ?? "",
          sub_moved_to: d.sub_moved_to ?? "",
          sub_apps: d.sub_apps ?? [],
          sub_wizard_apps: d.sub_wizard_apps ?? {},
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
    getSubTemplates()
      .then((v) => {
        setTpl(v);
        setTplSaved(v);
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
  const hwidLocks = h.enabled && h.require;

  // One save button for the page: the two blocks are separate endpoints (device
  // binding doesn't touch the public path or the routing headers), but to the
  // operator this is one settings page and one Save.
  const save = () =>
    run(async () => {
      // A client of the operator's own gets its key on save; the list is read back so
      // the next save keeps that key instead of making the client anew.
      let reloaded = false;
      if (dirty) {
        await saveSubSettings(s);
        if (s.sub_apps.some((a) => !a.key)) {
          const d = await getSettings();
          load({ ...s, sub_apps: d.sub_apps ?? [], sub_wizard_apps: d.sub_wizard_apps ?? {} });
          reloaded = true;
        }
      }
      if (hwidDirty) await saveHWIDSettings(h);
      if (dpiDirty) {
        await saveSubDPI(dpi);
        setDpiSaved(dpi);
      }
      if (rulesDirty) {
        await saveSubRules(rules);
        setRulesSaved(rules);
      }
      if (tplDirty) {
        await saveSubTemplates(tpl);
        setTplSaved(tpl);
      }
      if (!reloaded) commit();
      commitHwid();
      notifySuccess(t("subs.saved"));
    });

  if (!loaded) return <CenterLoader />;

  return (
    <div className="flex flex-1 flex-col gap-3.5">
      {/* What the subscription link is and what comes back from it. */}
      <Panel title={t("subs.format")}>
        <ToggleRow
          label={t("subs.pageEnabled")}
          hint={t("subs.pageEnabledHint")}
          checked={s.sub_page_enabled}
          onChange={(v) => patch({ sub_page_enabled: v })}
        />
        <SettingRow
          label={t("subs.path")}
          hint={
            pathErr ? (
              <span className="text-danger">{pathErr}</span>
            ) : (
              t("subs.pathHint", { path: s.sub_path || "sub" })
            )
          }
          field={
            <TextInput
              placeholder="sub"
              value={s.sub_path}
              mono
              onChange={(v) => patch({ sub_path: v.replace(/[^A-Za-z0-9_-]/g, "") })}
            />
          }
        />
        <ToggleRow
          label={t("subs.base64")}
          hint={t("subs.base64Hint")}
          checked={s.sub_base64}
          onChange={(v) => patch({ sub_base64: v })}
        />
        <SettingRow
          label={t("subs.title")}
          field={
            <TextInput
              placeholder={t("subs.titlePlaceholder")}
              value={s.sub_title}
              onChange={(v) => patch({ sub_title: v })}
            />
          }
        />
        <ToggleRow
          label={t("subs.nameInTitle")}
          hint={t("subs.nameInTitleHint")}
          checked={s.sub_name_in_title}
          onChange={(v) => patch({ sub_name_in_title: v })}
        />
        <SettingRow
          label={t("subs.updateInterval")}
          field={
            <Select
              data={intervals()}
              value={String(s.sub_update_interval)}
              onChange={(v) => patch({ sub_update_interval: Number(v) })}
            />
          }
        />
        <SettingRow
          label={t("subs.orderMode.label")}
          hint={t("subs.orderMode.hint")}
          field={
            <Select
              data={[
                { value: "manual", label: t("subs.orderMode.manual") },
                { value: "load", label: t("subs.orderMode.load") },
                { value: "random", label: t("subs.orderMode.random") },
              ]}
              value={s.sub_order_mode}
              onChange={(v) => patch({ sub_order_mode: v })}
            />
          }
        />
        <ToggleRow
          label={t("subs.hideOffline")}
          hint={t("subs.hideOfflineHint")}
          checked={s.sub_hide_offline}
          onChange={(v) => patch({ sub_hide_offline: v })}
        />
        <SettingRow
          label={t("subs.selfRotate.label")}
          hint={t("subs.selfRotate.hint")}
          field={
            <Select
              data={[
                { value: "0", label: t("subs.selfRotate.off") },
                { value: "1", label: t("subs.selfRotate.hour") },
                { value: "24", label: t("subs.selfRotate.day") },
                { value: "168", label: t("subs.selfRotate.week") },
                { value: "720", label: t("subs.selfRotate.month") },
              ]}
              value={String(s.sub_self_rotate)}
              onChange={(v) => patch({ sub_self_rotate: Number(v) })}
            />
          }
        />

        <SettingRow
          label={t("subs.announce")}
          hint={
            <span className={cn(announceErr && "text-danger")}>
              {t("subs.announceHint")} {announceLen}/{ANNOUNCE_MAX}
            </span>
          }
        >
          <Textarea
            placeholder={t("subs.announcePlaceholder")}
            rows={2}
            value={s.sub_announce}
            onChange={(v) => patch({ sub_announce: v })}
          />
        </SettingRow>
        <SettingRow
          label={t("subs.movedTo")}
          hint={t("subs.movedToHint")}
          wideField
          field={
            <TextInput
              placeholder="https://new.example.com/sub/{token}"
              value={s.sub_moved_to}
              mono
              onChange={(v) => patch({ sub_moved_to: v })}
            />
          }
        />
        <SettingRow
          label={t("subs.pageURL")}
          hint={t("subs.pageURLHint")}
          wideField
          field={
            <TextInput
              placeholder="https://example.com/cabinet?sub={token}"
              value={s.sub_page_url}
              mono
              onChange={(v) => patch({ sub_page_url: v })}
            />
          }
        />
        {/* A required HWID hides both whatever they say: a raw link would bypass the
            device cap, and the browser's download carries no device id, so it is refused.
            Shown off and locked rather than as switches that seem to do nothing; the
            stored choice comes back once the requirement is lifted. */}
        <ToggleRow
          label={t("subs.showConfigs")}
          hint={hwidLocks ? t("subs.showConfigsLocked") : t("subs.showConfigsHint")}
          checked={s.sub_show_configs && !hwidLocks}
          disabled={hwidLocks}
          onChange={(v) => patch({ sub_show_configs: v })}
        />
        <ToggleRow
          label={t("subs.showClash")}
          hint={hwidLocks ? t("subs.showClashLocked") : t("subs.showClashHint")}
          checked={s.sub_show_clash && !hwidLocks}
          disabled={hwidLocks}
          onChange={(v) => patch({ sub_show_clash: v })}
        />
        <ToggleRow
          label={t("subs.happCrypt")}
          hint={t("subs.happCryptHint")}
          checked={s.sub_happ_crypt}
          onChange={(v) => patch({ sub_happ_crypt: v })}
        />
      </Panel>

      <Panel
        title={t("subs.apps")}
        aside={
          <IconButton
            variant="filled"
            color="brand"
            title={t("subs.appAdd")}
            onClick={() =>
              patch({
                sub_apps: [
                  { key: "", label: "", on: true, custom: true, platforms: "", link: "" },
                  ...s.sub_apps,
                ],
              })
            }
          >
            <IconPlus />
          </IconButton>
        }
      >
        <SettingRow hint={t("subs.appsHint")} />
        <SubAppsList
          apps={s.sub_apps}
          onChange={(sub_apps) =>
            // Removing a client the wizard uses hands those platforms back to Happ.
            patch({
              sub_apps,
              sub_wizard_apps: Object.fromEntries(
                Object.entries(s.sub_wizard_apps).map(([p, k]) => [p, sub_apps.some((a) => a.key === k) ? k : "happ"]),
              ),
            })
          }
        />
        <SettingRow label={t("subs.wizardApp")} hint={t("subs.wizardAppHint")} />
        {WIZARD_PLATFORMS.map(([key, name]) => (
          <SettingRow
            key={key}
            label={name === "TV" ? t("subs.wizardTV") : name}
            field={
              <Select
                // A client gets its key on save, so a new one is offered after that.
                data={s.sub_apps.filter((a) => a.key).map((a) => ({ value: a.key, label: a.label }))}
                value={s.sub_wizard_apps[key] ?? "happ"}
                onChange={(v) => patch({ sub_wizard_apps: { ...s.sub_wizard_apps, [key]: v } })}
              />
            }
          />
        ))}
      </Panel>

      {/* Device binding: off is one row, on is five. */}
      <Panel
        title={t("subs.hwid")}
        aside={
          <Switch
            checked={h.enabled}
            onChange={(v) => patchHwid({ enabled: v })}
          />
        }
      >
        <SettingRow hint={t("subs.hwidHint")} />
        <SettingRow
          label={t("subs.ipFallback")}
          hint={t("subs.ipFallbackHint")}
          field={
            <TextInput
              type="number"
              value={String(h.ip_fallback_limit ?? 0)}
              onChange={(v) =>
                patchHwid({ ip_fallback_limit: Math.min(100, Math.max(0, Number(v) || 0)) })
              }
            />
          }
        />
        {h.enabled && (
          <>
            <ToggleRow
              label={t("subs.hwidRequire")}
              hint={t("subs.hwidRequireHint")}
              checked={h.require}
              onChange={(v) => patchHwid({ require: v })}
            />
            <SettingRow
              label={t("subs.countMode")}
              hint={t("subs.countModeHint")}
              field={
                <Select
                  // "both" is a stored value from before the handover grace was
                  // removed. It behaves exactly as "auto", so it shows as "auto"
                  // rather than as a third choice that does the same thing.
                  value={h.count_mode === "hwid" ? "hwid" : "auto"}
                  onChange={(v) => patchHwid({ count_mode: v })}
                  data={[
                    { value: "auto", label: t("subs.countModeAuto") },
                    { value: "hwid", label: t("subs.countModeHWID") },
                  ]}
                />
              }
            />
            <SettingRow
              label={t("subs.hwidFallback")}
              hint={t("subs.hwidFallbackHint")}
              field={
                <TextInput
                  type="number"
                  value={String(h.fallback_limit)}
                  onChange={(v) =>
                    patchHwid({ fallback_limit: Math.max(0, Number(v) || 0) })
                  }
                />
              }
            />
            <SettingRow
              label={t("subs.hwidTTL")}
              hint={t("subs.hwidTTLHint")}
              field={
                <TextInput
                  type="number"
                  value={String(h.ttl_days)}
                  onChange={(v) =>
                    patchHwid({ ttl_days: Math.max(0, Number(v) || 0) })
                  }
                />
              }
            />
          </>
        )}
      </Panel>

      {/* Routing rule sets handed to the clients that understand them. */}
      <Panel
        title={t("subs.routing")}
        aside={
          <Switch
            checked={s.sub_routing}
            onChange={(v) => patch({ sub_routing: v })}
          />
        }
      >
        <SettingRow
          hint={
            <>
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
            </>
          }
        />
        {s.sub_routing && (
          <>
            <SettingRow
              label={t("subs.happRules")}
              wideField
              field={
                <TextInput
                  placeholder="https://.../HAPP/DEFAULT.DEEPLINK"
                  value={s.sub_routing_happ}
                  onChange={(v) => patch({ sub_routing_happ: v })}
                />
              }
            />
            <SettingRow
              label={t("subs.incyRules")}
              wideField
              field={
                <TextInput
                  placeholder="https://.../INCY/DEFAULT.DEEPLINK"
                  value={s.sub_routing_incy}
                  onChange={(v) => patch({ sub_routing_incy: v })}
                />
              }
            />
            <SettingRow
              label={t("subs.mihomoRules")}
              hint={
                <Trans
                  i18nKey="subs.mihomoHint"
                  components={{
                    marker: <code className="rounded bg-gray-100 px-1 font-mono" />,
                  }}
                />
              }
              wideField
              field={
                <TextInput
                  placeholder="https://.../MIHOMO/default.yaml"
                  value={s.sub_routing_mihomo}
                  onChange={(v) => patch({ sub_routing_mihomo: v })}
                />
              }
            />
          </>
        )}
      </Panel>

      <SubDPICard value={dpi} onChange={setDpi} />

      <SubTemplatesCard value={tpl} onChange={setTpl} />
      <SubRulesEditor value={rules} onChange={setRules} />

      <SaveBar
        dirty={dirty || hwidDirty || dpiDirty || rulesDirty || tplDirty}
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


// SubAppsList orders the page's "open in app" buttons and switches each on or off.
// A row opens to the client's download buttons per platform — and for one of the
// operator's own, its name, platforms and import link.
function SubAppsList({ apps, onChange }: { apps: SubApp[]; onChange: (a: SubApp[]) => void }) {
  const { t } = useTranslation();
  // Rows the operator opened or closed, by key; a new client (no key yet) starts open.
  const [toggled, setToggled] = useState<Set<string>>(new Set());
  const move = (i: number, d: -1 | 1) => {
    const next = [...apps];
    [next[i], next[i + d]] = [next[i + d], next[i]];
    onChange(next);
  };
  const edit = (i: number, p: Partial<SubApp>) => onChange(apps.map((x, j) => (j === i ? { ...x, ...p } : x)));
  return (
    <>
      {apps.map((a, i) => {
        const id = a.key || `new-${i}`;
        const open = toggled.has(id) !== !a.key;
        const count = Object.values(a.links ?? {}).reduce((n, ls) => n + ls.filter((l) => l.url.trim()).length, 0);
        return (
          <div key={id} className={cn("border-t border-gray-100", !a.on && "opacity-60")}>
            <div className="flex items-center gap-3 px-3.5 py-2">
              <Switch checked={a.on} onChange={(v) => edit(i, { on: v })} />
              <button
                type="button"
                className="flex min-w-0 flex-1 items-center gap-2 text-start"
                aria-expanded={open}
                onClick={() =>
                  setToggled((cur) => {
                    const next = new Set(cur);
                    if (next.has(id)) next.delete(id);
                    else next.add(id);
                    return next;
                  })
                }
              >
                <span className="truncate text-[13px] font-medium text-ink">{a.label || t("subs.appNew")}</span>
                <span className="shrink-0 text-xs text-ink-muted">{t("subs.wizardLinkCount", { count })}</span>
                <IconChevron className={cn("shrink-0 text-ink-muted transition", open && "rotate-180")} />
              </button>
              <IconButton title={t("subs.ruleUp")} disabled={i === 0} onClick={() => move(i, -1)}>
                <IconChevron className="rotate-180" />
              </IconButton>
              <IconButton title={t("subs.ruleDown")} disabled={i === apps.length - 1} onClick={() => move(i, 1)}>
                <IconChevron />
              </IconButton>
              {a.custom && (
                <IconButton color="red" title={t("common.delete")} onClick={() => onChange(apps.filter((_, j) => j !== i))}>
                  <IconTrash />
                </IconButton>
              )}
            </div>
            {open && (
              <div className="flex flex-col gap-3 bg-gray-50/60 px-3.5 py-3">
                {a.custom && (
                  <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                    <TextInput
                      ariaLabel={t("subs.appName")}
                      placeholder={t("subs.appName")}
                      value={a.label}
                      onChange={(v) => edit(i, { label: v })}
                    />
                    <TextInput
                      ariaLabel={t("subs.appPlatforms")}
                      placeholder={t("subs.appPlatforms")}
                      value={a.platforms ?? ""}
                      onChange={(v) => edit(i, { platforms: v })}
                    />
                    <TextInput
                      className="sm:col-span-2"
                      ariaLabel={t("subs.appLink")}
                      placeholder="myapp://import/{url}"
                      mono
                      value={a.link ?? ""}
                      onChange={(v) => edit(i, { link: v })}
                    />
                  </div>
                )}
                <div className="text-xs text-ink-muted">{t("subs.appLinksHint")}</div>
                {WIZARD_PLATFORMS.map(([key, name]) => (
                  <PlatformLinks
                    key={key}
                    name={name === "TV" ? t("subs.wizardTV") : name}
                    links={a.links?.[key] ?? []}
                    onChange={(ls) => edit(i, { links: { ...(a.links ?? {}), [key]: ls } as AppLinks })}
                  />
                ))}
              </div>
            )}
          </div>
        );
      })}
    </>
  );
}

// PlatformLinks edits one platform's download buttons: a label and an address each.
function PlatformLinks({
  name,
  links,
  onChange,
}: {
  name: string;
  links: { label: string; url: string }[];
  onChange: (ls: { label: string; url: string }[]) => void;
}) {
  const { t } = useTranslation();
  const edit = (i: number, p: Partial<{ label: string; url: string }>) =>
    onChange(links.map((l, j) => (j === i ? { ...l, ...p } : l)));
  return (
    <div>
      <div className="mb-1 flex items-center justify-between gap-3">
        <span className="text-xs font-semibold text-ink">{name}</span>
        <IconButton
          title={t("subs.wizardLinkAdd")}
          disabled={links.length >= 5}
          onClick={() => onChange([...links, { label: "", url: "" }])}
        >
          <IconPlus />
        </IconButton>
      </div>
      <div className="flex flex-col gap-2">
        {links.map((l, i) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: rows have no id of their own and are edited in place
          <div key={i} className="grid grid-cols-[minmax(0,1fr)_auto] gap-2 sm:grid-cols-[180px_minmax(0,1fr)_auto]">
            <TextInput
              className="col-span-2 sm:col-span-1"
              ariaLabel={t("subs.wizardLinkLabel")}
              placeholder={t("subs.wizardLinkLabel")}
              value={l.label}
              onChange={(v) => edit(i, { label: v })}
            />
            <TextInput ariaLabel={name} placeholder="https://" mono value={l.url} onChange={(v) => edit(i, { url: v })} />
            <IconButton color="red" title={t("common.delete")} onClick={() => onChange(links.filter((_, j) => j !== i))}>
              <IconTrash />
            </IconButton>
          </div>
        ))}
      </div>
    </div>
  );
}
