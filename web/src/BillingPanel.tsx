import { useCallback, useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import {
  MAX_DEVICE_LIMIT,
  deleteTariffPlan,
  getBilling,
  getPayments,
  listGroups,
  migratePlanUsers,
  saveBilling,
  savePaymentProvider,
  saveTariffPlan,
  type BillingInfo,
  type Group,
  type PaymentProvider,
  type TariffPlan,
} from "./api";
import { fmtBytes, fmtSpeed, gbToBytes, quotaOptions, resetPeriods, speedLimitOptions } from "./format";
import { useAction } from "./hooks";
import i18n, { td } from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  CustomizableSelect,
  Badge,
  Button,
  CenterLoader,
  Checkbox,
  cn,
  Code,
  Modal,
  SaveBar,
  Select,
  SettingCard,
  Switch,
  Textarea,
  TextInput,
  useConfirm,
} from "./ui";

// ProviderDraft is one provider's editable state (mirrors PaymentField kinds:
// secrets/text as strings, bools as "1"/"").
type ProviderDraft = { enabled: boolean; config: Record<string, string> };

// draftFromProvider seeds a provider's editable form from the server's view: field
// values for text/bool, and empty strings for secrets (which are write-only — the
// server only tells us whether one is set, never its value).
function draftFromProvider(p: PaymentProvider): ProviderDraft {
  const config: Record<string, string> = {};
  for (const f of p.fields) {
    if (f.kind === "secret") config[f.key] = "";
    else if (f.kind === "bool") config[f.key] = f.value === true ? "1" : "";
    else config[f.key] = typeof f.value === "string" ? f.value : "";
  }
  return { enabled: p.enabled, config };
}

// providerDirty reports whether a draft differs from the server's saved view.
function providerDirty(p: PaymentProvider, draft: ProviderDraft): boolean {
  if (draft.enabled !== p.enabled) return true;
  return p.fields.some((f) => {
    if (f.kind === "secret") return draft.config[f.key] !== "";
    if (f.kind === "bool")
      return draft.config[f.key] !== (f.value === true ? "1" : "");
    return draft.config[f.key] !== (typeof f.value === "string" ? f.value : "");
  });
}

// ProviderCard is one provider's settings form, rendered entirely from the schema
// the server sends. It's controlled — edits bubble up via onChange and are saved by
// the page's shared bottom SaveBar, not here.
function ProviderCard({
  provider,
  draft,
  onChange,
}: {
  provider: PaymentProvider;
  draft: ProviderDraft;
  onChange: (d: ProviderDraft) => void;
}) {
  const { t } = useTranslation();
  const setField = (key: string, value: string) =>
    onChange({ ...draft, config: { ...draft.config, [key]: value } });

  // A provider is "configured" when every required field has a value: text fields
  // non-empty, secrets either already stored or being entered now.
  const configured = provider.fields.every((f) => {
    if (f.optional || f.kind === "bool") return true;
    if (f.kind === "secret") return f.is_set || draft.config[f.key] !== "";
    return (draft.config[f.key] ?? "") !== "";
  });

  const status = !draft.enabled
    ? { label: i18n.t("bill.provOff"), color: "gray" as const }
    : configured
      ? { label: i18n.t("bill.provOn"), color: "green" as const }
      : { label: i18n.t("bill.provUnset"), color: "orange" as const };

  return (
    <div
      className={cn(
        "overflow-hidden rounded-2xl border transition-colors",
        draft.enabled ? "border-gray-200 bg-white" : "border-gray-200 bg-gray-50/50",
      )}
    >
      {/* Header: monogram, name + note, status, toggle. */}
      <div className="flex items-center gap-3 p-3.5">
        <div
          className={cn(
            "flex h-10 w-10 shrink-0 items-center justify-center rounded-xl text-base font-bold",
            draft.enabled ? "accent-tint text-accent" : "bg-gray-100 text-ink-muted",
          )}
        >
          {provider.label.charAt(0).toUpperCase()}
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-semibold text-ink">{provider.label}</span>
            <Badge color={status.color} size="xs">
              {status.label}
            </Badge>
          </div>
          <p className="truncate text-xs text-ink-muted">{td(provider.note)}</p>
        </div>
        <Switch
          checked={draft.enabled}
          onChange={(v) => onChange({ ...draft, enabled: v })}
        />
      </div>

      {/* Credentials form, revealed when the provider is on. */}
      {draft.enabled && (
        <div className="flex flex-col gap-3 border-t border-gray-100 px-3.5 py-3.5">
          {provider.fields.map((f) => {
            if (f.kind === "bool") {
              return (
                <label
                  key={f.key}
                  className="flex items-center gap-2 text-sm text-ink"
                >
                  <Switch
                    checked={draft.config[f.key] === "1"}
                    onChange={(v) => setField(f.key, v ? "1" : "")}
                  />
                  {td(f.label)}
                  {f.help && (
                    <span className="text-xs text-ink-muted">— {td(f.help)}</span>
                  )}
                </label>
              );
            }
            if (f.kind === "select") {
              const opts = f.options ?? [];
              return (
                <div key={f.key}>
                  <Select
                    label={td(f.label)}
                    data={opts.map((o) => ({ ...o, label: td(o.label) }))}
                    value={draft.config[f.key] || opts[0]?.value || ""}
                    onChange={(v) => setField(f.key, v)}
                  />
                  {f.help && (
                    <p className="mt-1 text-xs text-ink-muted">{td(f.help)}</p>
                  )}
                </div>
              );
            }
            const isSecret = f.kind === "secret";
            // Every operator-visible string a provider describes itself with — label,
            // note, help, placeholder — is a dictionary key. td() falls back to the
            // string itself, so a brand name in the same field renders unchanged.
            const name = td(f.label);
            const label = isSecret && f.is_set ? t("bill.fieldSet", { label: name }) : name;
            return (
              <div key={f.key}>
                <TextInput
                  label={label}
                  value={draft.config[f.key] ?? ""}
                  onChange={(v) => setField(f.key, v)}
                  placeholder={isSecret && f.is_set ? "••••••••" : td(f.placeholder ?? "")}
                />
                {f.help && !isSecret && (
                  <p className="mt-1 text-xs text-ink-muted">{td(f.help)}</p>
                )}
              </div>
            );
          })}

          {provider.webhook_url && (
            <div>
              <p className="mb-1 text-xs text-ink-muted">
                {t("bill.webhookUrl")}
              </p>
              <Code block copy>
                {provider.webhook_url}
              </Code>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// PaymentIntegrations lists every payment provider the panel knows about and its
// settings form. It's controlled by BillingPanel so edits ride the page's single
// bottom SaveBar. Providers, fields and validation all come from the server, so a
// newly added provider shows up here with no frontend change.
function PaymentIntegrations({
  providers,
  drafts,
  err,
  onChange,
}: {
  providers: PaymentProvider[] | null;
  drafts: Record<string, ProviderDraft>;
  err: string;
  onChange: (key: string, d: ProviderDraft) => void;
}) {
  const { t } = useTranslation();
  return (
    <SettingCard
      title={t("bill.acceptTitle")}
      description={t("bill.acceptDescription")}
    >
      {err ? (
        <p className="text-sm text-danger">{err}</p>
      ) : !providers ? (
        <CenterLoader />
      ) : (
        <div className="flex flex-col gap-3">
          {providers.map((p) => (
            <ProviderCard
              key={p.key}
              provider={p}
              draft={drafts[p.key] ?? draftFromProvider(p)}
              onChange={(d) => onChange(p.key, d)}
            />
          ))}
        </div>
      )}
    </SettingCard>
  );
}

const EMPTY_PLAN = (): TariffPlan => ({
  id: 0,
  slug: "",
  name: "",
  price_rub: 100, // a new plan is paid; free is a designation, not a price of 0
  period_days: 30,
  data_limit: 0,
  device_limit: 0,
  speed_limit: 0,
  reset_period: "",
  sort_order: 0,
  enabled: true,
  group_ids: [],
});


// devices() are the plan presets; the editor also takes any other number up to
// MAX_DEVICE_LIMIT (see CustomizableSelect).
const devices = () => [
  { value: "0", label: i18n.t("common.unlimited") },
  { value: "1", label: "1" },
  { value: "2", label: "2" },
  { value: "3", label: "3" },
  { value: "5", label: "5" },
  { value: "10", label: "10" },
];

const periods = () => [
  { value: "0", label: i18n.t("bill.unlimitedTerm") },
  ...[1, 3, 7, 14, 30, 90, 180, 365].map((d) => ({
    value: String(d),
    label: i18n.t("bc.days", { count: d }),
  })),
];

function gbFromBytes(b: number): string {
  if (!b) return "0";
  const gb = b / (1024 * 1024 * 1024);
  const hit = quotaOptions().find((o) => o.value === String(gb));
  return hit ? hit.value : String(gb);
}

function periodLabel(days: number): string {
  if (!days) return i18n.t("common.never");
  return i18n.t("bill.nDays", { count: days });
}

function planSummary(p: TariffPlan): string {
  const parts: string[] = [];
  if (p.price_rub > 0) {
    parts.push(`${p.price_rub} ₽ / ${periodLabel(p.period_days)}`);
  } else {
    parts.push(`${i18n.t("bill.free")} · ${periodLabel(p.period_days)}`);
  }
  parts.push(p.data_limit ? fmtBytes(p.data_limit) : i18n.t("bill.infTraffic"));
  parts.push(
    p.device_limit
      ? i18n.t("bill.nDevices", { count: p.device_limit })
      : i18n.t("bill.infDevices"),
  );
  // Only when the plan promises one: "unlimited speed" is the norm and would just
  // make every summary longer.
  if (p.speed_limit > 0) parts.push(fmtSpeed(p.speed_limit));
  // Same rule: the derived cycle is the norm, only an explicit one is news.
  if (p.reset_period && p.data_limit) {
    parts.push(i18n.t("bill.resetSummary", { period: resetLabel(p.reset_period) }));
  }
  return parts.join(" · ");
}

// resetLabel renders a plan's explicit cycle with the same words the user card
// uses for the same value.
function resetLabel(period: string): string {
  const hit = resetPeriods().find((o) => o.value === period);
  return hit ? hit.label.toLowerCase() : period;
}

// planResetOptions are the cycles a plan may carry. The first entry is the derived
// default and reads differently for a free plan (refill every duration) and a paid
// one (the quota covers the whole period) — the server decides which, this only
// tells the operator what leaving it blank means.
const planResetOptions = (free: boolean) => [
  { value: "", label: i18n.t(free ? "bill.resetAutoFree" : "bill.resetAutoPaid") },
  ...resetPeriods().filter((o) => o.value !== "none"),
];

function PlanForm({
  plan,
  onChange,
  isTrial,
  isFree,
  groups,
}: {
  plan: TariffPlan;
  onChange: (p: TariffPlan) => void;
  isTrial: boolean;
  isFree: boolean;
  groups: Group[];
}) {
  const { t } = useTranslation();
  const patch = (p: Partial<TariffPlan>) => onChange({ ...plan, ...p });
  const selected = new Set(plan.group_ids ?? []);
  // A plan is free because it is designated free/trial in the pricing card — never
  // because someone typed 0 here. The server enforces both halves of that.
  const designated = isFree || isTrial;
  const periodVal = periods().some((o) => o.value === String(plan.period_days))
    ? String(plan.period_days)
    : String(plan.period_days || 0);

  return (
    <div className="flex flex-col gap-3">
      <TextInput
        label={t("groups.name")}
        value={plan.name}
        onChange={(v) => patch({ name: v })}
        placeholder={t("bill.namePlaceholder")}
      />
      <TextInput
        label={t("bill.slug")}
        value={plan.slug}
        onChange={(v) => patch({ slug: v.toLowerCase() })}
        placeholder={t("bill.slugPlaceholder")}
      />
      {/* Order, visibility and price are all about being offered for sale, which a
          designated free/trial plan never is — it is assigned automatically and is
          filtered out of every user-facing list server-side. Showing the fields just
          invited setting a price nobody charges or hiding a plan that is not shown
          anyway. */}
      {!designated && (
        <div className="grid gap-3 sm:grid-cols-2">
          <TextInput
            label={t("bill.order")}
            type="number"
            value={String(plan.sort_order)}
            onChange={(v) => patch({ sort_order: Math.max(0, Number(v) || 0) })}
          />
          <label className="flex items-end gap-2 pb-1 text-sm">
            <Switch checked={plan.enabled} onChange={(v) => patch({ enabled: v })} />
            {t("bill.activeVisible")}
          </label>
        </div>
      )}
      <div className={designated ? "grid gap-3" : "grid gap-3 sm:grid-cols-2"}>
        {!designated && (
          <TextInput
            label={t("bill.price")}
            type="number"
            value={String(plan.price_rub)}
            onChange={(v) => patch({ price_rub: Math.max(1, Number(v) || 1) })}
          />
        )}
        <Select
          label={t("bill.term")}
          data={periods()}
          value={periodVal}
          onChange={(v) => patch({ period_days: Number(v) })}
        />
      </div>
      <p className="text-xs text-ink-muted">
        {isTrial
          ? t("bill.trialHint")
          : isFree
            ? t("bill.freeHint")
            : t("bill.paidHint")}
      </p>
      <div className="grid gap-3 sm:grid-cols-2">
        <Select
          label={t("usersPanel.trafficLimit")}
          data={quotaOptions()}
          value={gbFromBytes(plan.data_limit)}
          onChange={(v) => patch({ data_limit: gbToBytes(Number(v)) })}
        />
        {/* The presets cover the plans people actually sell; "other" takes any
            number up to the panel's ceiling, the same as the user card. */}
        <CustomizableSelect
          label={t("userDetail.deviceLimit")}
          data={devices()}
          value={String(plan.device_limit)}
          max={MAX_DEVICE_LIMIT}
          format={(n) => t("bill.nDevices", { count: n })}
          onChange={(v) => patch({ device_limit: Number(v) })}
        />
        <Select
          label={t("userDetail.speedLimit")}
          data={speedLimitOptions()}
          value={String(plan.speed_limit)}
          onChange={(v) => patch({ speed_limit: Number(v) })}
        />
        {/* A cycle needs a quota to refill; without one the choice is meaningless
            and the server ignores it, so the control says so instead of pretending. */}
        <Select
          label={t("bill.resetPeriod")}
          data={planResetOptions(isFree)}
          value={plan.data_limit ? plan.reset_period : ""}
          disabled={!plan.data_limit}
          onChange={(v) => patch({ reset_period: v })}
        />
      </div>
      <p className="text-xs text-ink-muted">{t("bill.resetHint")}</p>
      {/* Access groups: the plan decides WHICH connections its users may reach, not
          only how much traffic. Ticking nothing keeps the plan silent about access —
          the historical behaviour, and what every existing plan has. */}
      <div className="flex flex-col gap-2 border-t border-gray-100 pt-3">
        <div className="flex items-center justify-between">
          <span className="text-sm font-medium text-ink">{t("bill.planGroups")}</span>
          {selected.size > 0 && (
            <Badge color="gray">{t("groups.nSelected", { count: selected.size })}</Badge>
          )}
        </div>
        {groups.length === 0 ? (
          <p className="text-xs text-ink-muted">{t("bill.planGroupsNone")}</p>
        ) : (
          <div className="flex max-h-44 flex-col gap-1.5 overflow-y-auto rounded-lg border border-gray-200/80 bg-white/50 p-2">
            {groups.map((g) => (
              <Checkbox
                key={g.id}
                checked={selected.has(g.id)}
                onChange={(c) => {
                  const next = new Set(selected);
                  if (c) next.add(g.id);
                  else next.delete(g.id);
                  patch({ group_ids: [...next] });
                }}
                label={g.name}
                hint={t("groups.nConnections", { count: g.grants?.length ?? 0 })}
              />
            ))}
          </div>
        )}
        <p className="text-xs text-ink-muted">{t("bill.planGroupsHint")}</p>
      </div>
    </div>
  );
}

export function BillingPanel() {
  const { t } = useTranslation();
  const [loaded, setLoaded] = useState(false);
  const [cfg, setCfg] = useState<BillingInfo | null>(null);
  const [saved, setSaved] = useState<BillingInfo | null>(null);
  const [plans, setPlans] = useState<TariffPlan[]>([]);
  const [planUsers, setPlanUsers] = useState<Record<string, number>>({});
  // Access groups a plan can grant. Best-effort: if the list can't be read the editor
  // just says there are none to pick, which is also the honest state for most installs.
  const [groups, setGroups] = useState<Group[]>([]);
  const [editor, setEditor] = useState<TariffPlan | null>(null);
  const [migrateTo, setMigrateTo] = useState(0);
  const [loadErr, setLoadErr] = useState("");
  // Payment providers: `providers` is the server's saved view; `payDrafts` the
  // per-provider edits. Both the tariff settings and the provider edits ride the one
  // shared bottom SaveBar (saveSettings persists whatever is dirty).
  const [providers, setProviders] = useState<PaymentProvider[] | null>(null);
  const [payDrafts, setPayDrafts] = useState<Record<string, ProviderDraft>>({});
  const [payErr, setPayErr] = useState("");
  const { busy, run } = useAction();
  const { confirm, confirmNode } = useConfirm();

  // seedProviders replaces the server view and resets all drafts to match it.
  const seedProviders = useCallback((list: PaymentProvider[]) => {
    setProviders(list);
    setPayDrafts(
      Object.fromEntries(list.map((p) => [p.key, draftFromProvider(p)])),
    );
  }, []);

  useEffect(() => {
    getPayments()
      .then((d) => seedProviders(d.providers ?? []))
      .catch((e) => setPayErr(errMessage(e)));
    listGroups()
      .then(setGroups)
      .catch(() => {});
  }, [seedProviders]);

  const patchProvider = (key: string, d: ProviderDraft) =>
    setPayDrafts((s) => ({ ...s, [key]: d }));

  const reload = useCallback(() => {
    getBilling()
      .then((d) => {
        const nextPlans = d.plans ?? [];
        const nextCfg: BillingInfo = {
          enabled: !!d.enabled,
          free_plan_id: d.free_plan_id ?? 0,
          trial_plan_id: d.trial_plan_id ?? 0,
          payment_note: d.payment_note ?? "",
          plans: nextPlans,
        };
        setCfg(nextCfg);
        setSaved(nextCfg);
        setPlans(nextPlans);
        setPlanUsers(d.plan_users ?? {});
        setLoadErr("");
      })
      .catch((e) => setLoadErr(errMessage(e)));
  }, []);

  useEffect(() => {
    getBilling()
      .then((d) => {
        const nextPlans = d.plans ?? [];
        const nextCfg: BillingInfo = {
          enabled: !!d.enabled,
          free_plan_id: d.free_plan_id ?? 0,
          trial_plan_id: d.trial_plan_id ?? 0,
          payment_note: d.payment_note ?? "",
          plans: nextPlans,
        };
        setCfg(nextCfg);
        setSaved(nextCfg);
        setPlans(nextPlans);
        setPlanUsers(d.plan_users ?? {});
        setLoadErr("");
      })
      .catch((e) => setLoadErr(errMessage(e)))
      .finally(() => setLoaded(true));
  }, []);

  if (!loaded) return <CenterLoader />;

  if (loadErr || !cfg || !saved) {
    return (
      <SettingCard title={t("bill.plans")}>
        <p className="text-sm text-danger">
          {loadErr || t("bill.loadFailed")}
        </p>
        <Button className="mt-3" onClick={() => reload()}>
          {t("common.retry")}
        </Button>
      </SettingCard>
    );
  }

  const safePlans = plans ?? [];
  const planOptions = safePlans
    .filter((p) => p.enabled)
    .map((p) => ({
      value: String(p.id),
      label: p.name,
    }));

  const billingDirty =
    cfg.enabled !== saved.enabled ||
    cfg.free_plan_id !== saved.free_plan_id ||
    cfg.trial_plan_id !== saved.trial_plan_id ||
    cfg.payment_note !== saved.payment_note;

  // Which providers have unsaved edits (skip any whose server view we don't have).
  const dirtyProviders = (providers ?? []).filter(
    (p) => payDrafts[p.key] && providerDirty(p, payDrafts[p.key]),
  );

  const dirty = billingDirty || dirtyProviders.length > 0;

  const cancel = () => {
    setCfg(saved);
    if (providers) seedProviders(providers);
  };

  // saveSettings persists whatever is dirty behind the single bottom SaveBar: the
  // tariff settings and every changed provider (each provider is its own API call;
  // the last response carries the refreshed provider list).
  const saveSettings = () =>
    run(async () => {
      if (billingDirty) {
        await saveBilling({
          enabled: cfg.enabled,
          free_plan_id: cfg.free_plan_id,
          trial_plan_id: cfg.trial_plan_id,
          payment_note: cfg.payment_note,
        });
        setSaved({ ...cfg, plans: safePlans });
        // Tell the top nav to re-read billing_enabled so the payments menu item
        // appears/disappears immediately (no page reload needed).
        window.dispatchEvent(new Event("rospanel:billing-changed"));
      }
      let latest: PaymentProvider[] | null = null;
      for (const p of dirtyProviders) {
        const draft = payDrafts[p.key];
        const { providers: list } = await savePaymentProvider({
          key: p.key,
          enabled: draft.enabled,
          config: draft.config,
        });
        latest = list;
      }
      if (latest) seedProviders(latest);
      notifySuccess(t("general.saved"));
    }).catch((e) => notifyError(errMessage(e)));

  const openCreate = () => {
    const maxOrder = safePlans.reduce((m, p) => Math.max(m, p.sort_order), 0);
    setEditor({ ...EMPTY_PLAN(), sort_order: maxOrder + 1 });
  };

  const savePlan = () => {
    if (!editor) return;
    if (!editor.name.trim()) {
      notifyError(t("bill.needName"));
      return;
    }
    run(async () => {
      const savedPlan = await saveTariffPlan(editor);
      setEditor(null);
      reload();
      notifySuccess(t(savedPlan.id ? "bill.planSaved" : "bill.planCreated"));
    }).catch((e) => notifyError(errMessage(e)));
  };

  const migratePlan = () => {
    if (!editor?.id || !migrateTo) return;
    run(async () => {
      const r = await migratePlanUsers(editor.id, migrateTo);
      setMigrateTo(0);
      reload();
      notifySuccess(t("bill.migrated", { count: r.migrated }));
    }).catch((e) => notifyError(errMessage(e)));
  };

  const removePlan = async (p: TariffPlan) => {
    const ok = await confirm({
      title: t("bill.deleteTitle"),
      body: t("bill.deleteBody", { name: p.name }),
      confirmLabel: t("common.delete"),
      danger: true,
    });
    if (!ok) return;
    run(async () => {
      await deleteTariffPlan(p.id);
      reload();
      notifySuccess(t("bill.planDeleted"));
    }).catch((e) => notifyError(errMessage(e)));
  };


  return (
    <>
      {confirmNode}
      <div className="flex flex-col gap-4">
        <SettingCard
          title={t("settings.tabBilling")}
          description={t("bill.globalHint")}
          action={
            <Switch
              checked={cfg.enabled}
              onChange={(v) => setCfg({ ...cfg, enabled: v })}
            />
          }
        >
          <p className="text-sm text-ink-muted">
            <Trans i18nKey="bill.existingUsers" components={{ b: <b /> }} />
          </p>
        </SettingCard>
        <PaymentIntegrations
          providers={providers}
          drafts={payDrafts}
          err={payErr}
          onChange={patchProvider}
        />
        <SettingCard
          title={t("bill.plansTitle")}
          description={t("bill.plansHint")}
          action={
            <Button size="sm" onClick={openCreate}>
              {t("common.create")}
            </Button>
          }
        >
          {safePlans.length === 0 ? (
            <p className="text-sm text-ink-muted">
              {t("bill.noPlans")}
            </p>
          ) : (
            <ul className="flex flex-col gap-2">
              {safePlans.map((p) => (
                <li
                  key={p.id}
                  className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-gray-200 px-3 py-2.5"
                >
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-semibold text-ink">{p.name}</span>
                      {!p.enabled && <Badge color="gray">{t("conn.off")}</Badge>}
                      {(planUsers[String(p.id)] ?? 0) > 0 && (
                        <Badge color="gray">
                          {t("bill.nUsers", { count: planUsers[String(p.id)] })}
                        </Badge>
                      )}
                      {p.price_rub <= 0 && <Badge color="teal">{t("bill.free")}</Badge>}
                      {cfg.free_plan_id === p.id && (
                        <Badge color="brand">{t("bill.afterTrial")}</Badge>
                      )}
                      {cfg.trial_plan_id === p.id && (
                        <Badge color="orange">{t("bill.trial")}</Badge>
                      )}
                      {/* The groups the plan hands out — the difference between two
                          plans is often only this, so it belongs in the list. */}
                      {groups
                        .filter((g) => (p.group_ids ?? []).includes(g.id))
                        .map((g) => (
                          <Badge key={g.id} color="brand">
                            {g.name}
                          </Badge>
                        ))}
                    </div>
                    <p className="mt-0.5 text-xs text-ink-muted">
                      {planSummary(p)}
                      {p.slug ? ` · ${t("bill.codeIs", { slug: p.slug })}` : ""}
                    </p>
                  </div>
                  <span className="flex shrink-0 gap-2">
                    <Button
                      size="sm"
                      variant="light"
                      onClick={() => {
                        setEditor({ ...p });
                        setMigrateTo(0);
                      }}
                    >
                      {t("common.edit")}
                    </Button>
                    <Button
                      size="sm"
                      variant="subtle"
                      color="red"
                      onClick={() => removePlan(p)}
                      disabled={busy}
                    >
                      {t("common.delete")}
                    </Button>
                  </span>
                </li>
              ))}
            </ul>
          )}
        </SettingCard>

        <SettingCard
          title={t("bill.pricing")}
          description={t("bill.pricingHint")}
        >
          <div className="flex flex-col gap-4">
            <div>
              <Select
                label={t("bill.freePlan")}
                data={[
                  { value: "0", label: t("bill.notSelected") },
                  // One plan cannot hold both roles: the trial has to expire into
                  // something, and it cannot expire into itself. The server refuses
                  // it too — this just keeps the choice off the menu.
                  ...planOptions.filter((o) => o.value !== String(cfg.trial_plan_id)),
                ]}
                value={String(cfg.free_plan_id)}
                onChange={(v) => setCfg({ ...cfg, free_plan_id: Number(v) })}
              />
              <p className="mt-1 text-xs text-ink-muted">
                {t("bill.freePlanHint")}
              </p>
            </div>
            <div>
              <Select
                label={t("bill.trialPlan")}
                data={[
                  { value: "0", label: t("bill.notSelected") },
                  ...planOptions.filter((o) => o.value !== String(cfg.free_plan_id)),
                ]}
                value={String(cfg.trial_plan_id)}
                onChange={(v) => setCfg({ ...cfg, trial_plan_id: Number(v) })}
              />
              <p className="mt-1 text-xs text-ink-muted">
                {t("bill.trialPlanHint")}
              </p>
            </div>
            <Textarea
              label={t("bill.manualDetails")}
              value={cfg.payment_note}
              onChange={(v) => setCfg({ ...cfg, payment_note: v })}
              placeholder={
                t("bill.manualPlaceholder")
              }
              rows={4}
              hint={t("bill.manualHint")}
            />
          </div>
        </SettingCard>

        <SaveBar
          dirty={dirty}
          busy={busy}
          onSave={saveSettings}
          onCancel={cancel}
        />

      </div>

      <Modal
        open={!!editor}
        onClose={() => setEditor(null)}
        title={editor?.id ? t("bill.planOf", { name: editor.name }) : t("bill.newPlan")}
        size="md"
      >
        {editor && (
          <div className="flex flex-col gap-4">
            <PlanForm
              plan={editor}
              onChange={setEditor}
              isTrial={editor.id > 0 && cfg.trial_plan_id === editor.id}
              isFree={editor.id > 0 && cfg.free_plan_id === editor.id}
              groups={groups}
            />
            {editor.id > 0 && (planUsers[String(editor.id)] ?? 0) > 0 && (
              <div className="accent-tint border-accent rounded-lg border p-3">
                <p className="text-sm font-semibold text-accent">
                  {t("bill.onPlanN", { count: planUsers[String(editor.id)] })}
                </p>
                <p className="mt-0.5 text-xs text-ink-muted">
                  {t("bill.migrateHint")}
                </p>
                <Select
                  className="mt-2"
                  label={t("bill.migrateTo")}
                  data={[
                    { value: "0", label: t("bill.pickPlan") },
                    ...safePlans
                      .filter((p) => p.id !== editor.id)
                      .map((p) => ({ value: String(p.id), label: p.name })),
                  ]}
                  value={String(migrateTo)}
                  onChange={(v) => setMigrateTo(Number(v))}
                />
                <Button
                  className="mt-2"
                  onClick={migratePlan}
                  disabled={!migrateTo || busy}
                  loading={busy}
                >
                  {t("bill.migrateN", { count: planUsers[String(editor.id)] })}
                </Button>
              </div>
            )}
            <div className="flex justify-end gap-2">
              <Button variant="subtle" onClick={() => setEditor(null)}>
                {t("common.cancel")}
              </Button>
              <Button onClick={savePlan} loading={busy}>
                {t(editor.id ? "common.save" : "common.create")}
              </Button>
            </div>
          </div>
        )}
      </Modal>
    </>
  );
}
