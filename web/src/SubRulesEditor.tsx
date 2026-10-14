import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { arrow } from "./i18n";
import {
  previewSubRules,
  SUB_PROTOS,
  type SubProto,
  type SubRule,
  type SubRuleHeaders,
  type SubRulePreview,
} from "./api";
import {
  Badge,
  cn,
  IconButton,
  IconCheck,
  IconChevron,
  IconClose,
  IconPlus,
  IconTrash,
  MICRO,
  Panel,
  SegmentedControl,
  Select,
  SettingRow,
  Switch,
  TextInput,
} from "./ui";

// The clients a subscription meets, for the preview: every app the subscription page
// offers to open, then the rest the panel tells apart — with the User-Agent each sends
// (a recent version; edit the field for another).
const SAMPLES: { name: string; ua: string }[] = [
  { name: "Happ", ua: "Happ/3.8.1" },
  { name: "INCY", ua: "INCY/2.6.5/iOS" },
  { name: "v2RayTun", ua: "v2RayTun/5.24.76" },
  { name: "Hiddify", ua: "HiddifyNext/4.1.1 (android) like ClashMeta v2ray sing-box" },
  { name: "Karing", ua: "Karing/1.2.25.2701 android/14" },
  { name: "sing-box (Android)", ua: "SFA (sing-box 1.13.0; language en_US)" },
  { name: "sing-box (iOS)", ua: "SFI (sing-box 1.13.0; language en_US)" },
  { name: "Clash Meta / mihomo", ua: "clash.meta/v1.19.32" },
  { name: "V2Box", ua: "V2Box/10.1.9" },
  { name: "v2rayNG", ua: "v2rayNG/1.10.21" },
  { name: "NekoBox", ua: "NekoBox/Android/1.4.0 (Prefer ClashMeta Format)" },
  { name: "Streisand", ua: "Streisand/1.6.76" },
  { name: "Shadowrocket", ua: "Shadowrocket/2070 CFNetwork/1410.0.3 Darwin/22.6.0" },
  { name: "v2rayN", ua: "v2rayN/7.15.0" },
  { name: "Throne", ua: "Throne/1.3.0" },
  { name: "Exclave", ua: "Exclave/0.17.33" },
  { name: "husi", ua: "husi/0.15.5 (sing-box 1.13.4)" },
  { name: "NyameBox", ua: "nekobox/5.0.0 (Prefer ClashMeta Format)" },
  { name: "Clash Verge Rev", ua: "clash-verge/v2.4.2" },
  { name: "FlClash", ua: "FlClash/v0.8.99 clash-verge Platform/android" },
  { name: "FlClashX", ua: "FlClash X/v0.4.1 core/v1.19.32 Platform/android" },
  { name: "ClashMetaForAndroid", ua: "ClashMetaForAndroid/2.11.33" },
  { name: "Clash Party", ua: "mihomo.party/v1.7.6 (clash.meta)" },
  { name: "Koala Clash", ua: "koala-clash/v1.5.0" },
  { name: "ClashMi", ua: "ClashMi/1.0.18 platform/android ClashMeta/1.19.32; mihomo/1.19.32" },
  { name: "Stash", ua: "Stash/3.6.1 Clash/1.11.0" },
  { name: "Loon", ua: "Loon/898 CFNetwork/1568.100.1 Darwin/24.0.0" },
  { name: "sing-box (macOS)", ua: "SFM (sing-box 1.13.0; language en_US)" },
  { name: "Sparkle", ua: "clash.meta/v1.19.32" },
  { name: "SlothClash", ua: "clash.meta/v1.19.32 SlothClash/0.9.4" },
  { name: "ClashFest", ua: "mihomo/1.19.32 ClashFest/1.2.0" },
  { name: "Clash Nyanpasu", ua: "clash-nyanpasu/v2.0.0" },
  { name: "Surge", ua: "Surge iOS/3044" },
  { name: "Quantumult X", ua: "Quantumult%20X/1.5.2" },
  { name: "Egern", ua: "Egern/2.19.0" },
];

// SubRulesEditor edits the ordered response rules: each matches a request attribute
// (User-Agent or an HWID header) and changes what the subscription answers with — the
// format, which protocols it carries, or the decoy. Its own endpoint, but not its own
// Save: the draft belongs to the page, so everything on this screen is saved and
// cancelled together.
export function SubRulesEditor({
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
      { field: "user_agent", op: "contains", value: "", action: "auto", enabled: true },
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

  return (
    <Panel
      title={t("subs.rules")}
      aside={
        <IconButton variant="filled" color="brand" title={t("subs.ruleAdd")} onClick={addRule}>
          <IconPlus />
        </IconButton>
      }
    >
      <SettingRow hint={t("subs.rulesHint")} />
      <RulePreview rules={rules} />
      {rules.length === 0 ? (
        <SettingRow hint={t("subs.rulesEmpty")} />
      ) : (
        <div className="flex flex-col gap-3 border-t border-gray-100 p-3.5">
          {rules.map((r, i) => (
            <RuleCard
              // biome-ignore lint/suspicious/noArrayIndexKey: the rule list is the wire payload — it carries no id, and a rule's position IS its precedence, which is what every mutator here addresses
              key={i}
              n={i + 1}
              rule={r}
              first={i === 0}
              last={i === rules.length - 1}
              onPatch={(p) => patchRule(i, p)}
              onMove={(d) => move(i, d)}
              onRemove={() => removeRule(i)}
            />
          ))}
        </div>
      )}
    </Panel>
  );
}

// RuleCard is one rule as the operator reads it: "If <field> <op> <value>", then what
// the subscription becomes — a format, and the protocols it carries.
function RuleCard({
  n,
  rule: r,
  first,
  last,
  onPatch,
  onMove,
  onRemove,
}: {
  n: number;
  rule: SubRule;
  first: boolean;
  last: boolean;
  onPatch: (p: Partial<SubRule>) => void;
  onMove: (d: -1 | 1) => void;
  onRemove: () => void;
}) {
  const { t } = useTranslation();
  const protos = r.protocols ?? {};
  const blocked = r.action === "block";

  const setProto = (k: SubProto, v: string) => {
    const next = { ...protos };
    if (v === "auto") delete next[k];
    else next[k] = v as "on" | "off";
    onPatch({ protocols: Object.keys(next).length ? next : undefined });
  };

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
    { value: "auto", label: t("subs.ruleFormatShort.auto") },
    { value: "v2ray", label: t("subs.ruleFormatShort.v2ray") },
    { value: "clash", label: t("subs.ruleAction.clash") },
    { value: "singbox", label: t("subs.ruleAction.singbox") },
    { value: "xray-json", label: t("subs.ruleAction.xray-json") },
    { value: "block", label: t("subs.ruleFormatShort.block") },
  ];
  const fieldLabel = fieldOpts.find((o) => o.value === r.field)?.label ?? r.field;
  const opLabel = opOpts.find((o) => o.value === r.op)?.label ?? r.op;
  const actionLabel = actionOpts.find((o) => o.value === r.action)?.label ?? r.action;

  return (
    <div
      className={cn(
        "@container overflow-hidden rounded-xl border border-gray-200 bg-white transition",
        !r.enabled && "opacity-60",
      )}
    >
      <div className="flex items-center gap-3 bg-gray-50/70 px-3.5 py-2.5">
        <Switch checked={r.enabled} onChange={(v) => onPatch({ enabled: v })} />
        <div className="min-w-0 flex-1">
          <div className="text-[13px] font-semibold text-ink">{t("subs.ruleN", { n })}</div>
          {/* The rule as a sentence, so a folded-up list still reads. */}
          <div className="truncate text-xs text-ink-muted">
            {fieldLabel} {opLabel} <bdi>«{r.value || "…"}»</bdi> {arrow()} <bdi>{actionLabel}</bdi>
          </div>
        </div>
        <div className="flex shrink-0 items-center">
          <IconButton title={t("subs.ruleUp")} disabled={first} onClick={() => onMove(-1)}>
            <IconChevron className="rotate-180" />
          </IconButton>
          <IconButton title={t("subs.ruleDown")} disabled={last} onClick={() => onMove(1)}>
            <IconChevron />
          </IconButton>
          <IconButton color="red" title={t("common.delete")} onClick={onRemove}>
            <IconTrash />
          </IconButton>
        </div>
      </div>

      <div className="flex flex-col gap-4 px-3.5 py-3.5">
        <Line label={t("subs.ruleIf")}>
          <div className="grid w-full grid-cols-2 gap-2 @3xl:grid-cols-[170px_170px_minmax(0,1fr)]">
            <Select
              value={r.field}
              onChange={(v) => onPatch({ field: v as SubRule["field"] })}
              data={fieldOpts}
            />
            <Select
              value={r.op}
              onChange={(v) => onPatch({ op: v as SubRule["op"] })}
              data={opOpts}
            />
            <div className="col-span-2 @3xl:col-span-1">
              <TextInput
                value={r.value}
                onChange={(v) => onPatch({ value: v })}
                placeholder={t("subs.ruleValue")}
                mono
              />
            </div>
          </div>
        </Line>

        <Line label={t("subs.ruleFormat")}>
          {/* Six formats fit side by side on a wide card; a narrow one takes a list. */}
          <div className="hidden @2xl:block">
            <SegmentedControl
              value={r.action}
              onChange={(v) => onPatch({ action: v as SubRule["action"] })}
              data={actionOpts}
            />
          </div>
          <div className="w-full @2xl:hidden">
            <Select
              value={r.action}
              onChange={(v) => onPatch({ action: v as SubRule["action"] })}
              data={actionOpts}
            />
          </div>
        </Line>

        <Line label={t("subs.ruleProtocols")}>
          {blocked ? (
            <p className="pt-2 text-[13px] text-ink-muted">{t("subs.ruleBlockNote")}</p>
          ) : (
            <div className="flex w-full flex-col gap-2 pt-1">
              {/* One chip per protocol; a tap moves it on: auto → give → hide → auto. */}
              <div className="flex flex-wrap gap-1.5">
                {SUB_PROTOS.map((k) => {
                  const v = protos[k] ?? "auto";
                  const nextState = v === "auto" ? "on" : v === "on" ? "off" : "auto";
                  return (
                    <button
                      key={k}
                      type="button"
                      onClick={() => setProto(k, nextState)}
                      title={t(`subs.ruleProtoState.${v}`)}
                      className={cn(
                        "inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-[13px] transition",
                        v === "auto" &&
                          "border-gray-200 text-ink hover:border-brand-600/40",
                        v === "on" && "success-tint border-transparent",
                        v === "off" && "danger-tint border-transparent",
                      )}
                    >
                      <ProtoMark state={v} />
                      <span className={cn(v === "off" && "line-through")}>
                        {t(`subs.ruleProto.${k}`)}
                      </span>
                    </button>
                  );
                })}
              </div>
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-ink-muted">
                {(["auto", "on", "off"] as const).map((v) => (
                  <span key={v} className="inline-flex items-center gap-1">
                    <ProtoMark state={v} />
                    {t(`subs.ruleProtoLegend.${v}`)}
                  </span>
                ))}
                <span>· {t("subs.ruleProtoTap")}</span>
              </div>
            </div>
          )}
        </Line>
      </div>
    </div>
  );
}

// ProtoMark is a protocol chip's state at a glance: a dot for auto, a tick for give,
// a cross for hide.
// Always the same box, so a chip keeps its width whatever its state.
function ProtoMark({ state }: { state: "auto" | "on" | "off" }) {
  return (
    <span className="inline-flex size-[13px] shrink-0 items-center justify-center">
      {state === "on" ? (
        <IconCheck size={13} />
      ) : state === "off" ? (
        <IconClose size={13} />
      ) : (
        <span className="size-1.5 rounded-full bg-gray-400" />
      )}
    </span>
  );
}

// Line is a labelled row of a rule card: the label in a fixed column on a wide card,
// above the controls on a narrow one — by the card's width (it is a @container), as
// the settings page sits beside a sidebar and the window's width says little.
function Line({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5 @2xl:flex-row @2xl:items-start @2xl:gap-3">
      <span className={cn(MICRO, "shrink-0 @2xl:w-24 @2xl:pt-2.5")}>{label}</span>
      <div className="flex min-w-0 flex-1">{children}</div>
    </div>
  );
}

// RulePreview answers "what does this client get" against the rules on screen, saved
// or not: the rule that fires, the format, and each protocol given or not.
function RulePreview({ rules }: { rules: SubRule[] }) {
  const { t } = useTranslation();
  const [ua, setUA] = useState("");
  // The HWID headers, for rules on the device rather than the app; folded away until
  // a rule tests one.
  const [hw, setHW] = useState<SubRuleHeaders>({});
  const [hwOpen, setHWOpen] = useState(false);
  const [res, setRes] = useState<SubRulePreview | null>(null);
  const key = JSON.stringify(rules);
  const hwKey = JSON.stringify(hw);
  const testsDevice = rules.some((r) => r.field !== "user_agent");

  useEffect(() => {
    if (!ua.trim()) {
      setRes(null);
      return;
    }
    let live = true;
    const id = setTimeout(() => {
      previewSubRules(JSON.parse(key) as SubRule[], ua, JSON.parse(hwKey) as SubRuleHeaders)
        .then((r) => live && setRes(r))
        .catch(() => live && setRes(null));
    }, 250);
    return () => {
      live = false;
      clearTimeout(id);
    };
  }, [ua, key, hwKey]);

  const fmt = res?.format;
  const fmtLabel =
    fmt === "v2ray" || fmt === "clash" || fmt === "singbox" || fmt === "xray-json"
      ? t(`subs.ruleAction.${fmt}`)
      : fmt;

  return (
    <div className="flex flex-col gap-2 border-t border-gray-100 px-3.5 py-3">
      <span className={MICRO}>{t("subs.ruleTest.title")}</span>
      <TextInput
        nav
        value={ua}
        onChange={setUA}
        placeholder={t("subs.ruleTest.placeholder")}
        mono
      />
      {(hwOpen || testsDevice) && (
        <div className="grid gap-2 sm:grid-cols-3">
          <TextInput
            nav
            value={hw.device_os ?? ""}
            onChange={(v) => setHW((h) => ({ ...h, device_os: v }))}
            placeholder={t("subs.ruleField.device_os")}
          />
          <TextInput
            nav
            value={hw.ver_os ?? ""}
            onChange={(v) => setHW((h) => ({ ...h, ver_os: v }))}
            placeholder={t("subs.ruleField.ver_os")}
          />
          <TextInput
            nav
            value={hw.device_model ?? ""}
            onChange={(v) => setHW((h) => ({ ...h, device_model: v }))}
            placeholder={t("subs.ruleField.device_model")}
          />
        </div>
      )}
      {!hwOpen && !testsDevice && (
        <button
          type="button"
          className="self-start text-xs font-medium text-accent hover:underline"
          onClick={() => setHWOpen(true)}
        >
          {t("subs.ruleTest.device")}
        </button>
      )}
      <div className="flex flex-wrap gap-1.5">
        {SAMPLES.map((s) => (
          <button
            key={s.name}
            type="button"
            title={s.ua}
            onClick={() => setUA(s.ua)}
            className={cn(
              "rounded-full border px-2.5 py-0.5 text-xs transition",
              ua === s.ua
                ? "border-brand-600 bg-brand-600 text-onbrand"
                : "border-gray-200 text-ink-muted hover:border-brand-600/40 hover:text-ink",
            )}
          >
            {s.name}
          </button>
        ))}
      </div>
      {res && (
        <div className="flex flex-col gap-2 rounded-lg bg-gray-50 p-2.5">
          <div className="flex flex-wrap items-center gap-2 text-[13px]">
            <span className="text-ink-muted">
              {res.rule >= 0
                ? t("subs.ruleTest.matched", { n: res.rule + 1 })
                : t("subs.ruleTest.none")}
            </span>
            {res.blocked ? (
              <Badge color="red" size="xs">
                {t("subs.ruleTest.blocked")}
              </Badge>
            ) : (
              <Badge color="brand" size="xs">
                {t("subs.ruleFormatIs", { fmt: fmtLabel })}
              </Badge>
            )}
          </div>
          {!res.blocked && res.protocols && (
            <div className="flex flex-wrap gap-1.5">
              {SUB_PROTOS.map((k) => (
                <Badge key={k} color={res.protocols?.[k] ? "green" : "gray"} size="xs">
                  {res.protocols?.[k] ? "✓ " : "✕ "}
                  {t(`subs.ruleProto.${k}`)}
                </Badge>
              ))}
            </div>
          )}
          <p className="text-[11px] leading-relaxed text-ink-muted">{t("subs.ruleTest.hint")}</p>
        </div>
      )}
    </div>
  );
}
