import { useCallback, useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import {
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
import { useAction } from "./hooks";
import { currentLang } from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Button,
  CenterLoader,
  cn,
  Drawer,
  EmptyState,
  IconButton,
  IconPencil,
  IconPlus,
  IconTrash,
  MICRO,
  Mono,
  Panel,
  ReadOnly,
  SaveBar,
  Select,
  SettingRow,
  Switch,
  useConfirm,
  useWideBox,
} from "./ui";
import { useCan } from "./role";
import {
  PaymentIntegrations,
  type ProviderDraft,
  draftFromProvider,
  providerDirty,
} from "./PaymentIntegrations";
import { EMPTY_PLAN, PlanForm, planSummary } from "./PlanForm";

// The plan roster's columns.
const PLAN_TPL =
  "minmax(0,1.6fr) minmax(0,.8fr) minmax(0,1.8fr) minmax(0,.5fr) 76px";
const PLAN_TPL_NARROW = "minmax(0,1fr) auto";
const PLANS_WIDE_MIN = 620;

export function BillingPanel() {
  const { t } = useTranslation();
  // Plans and the tariff settings are billing.*; the payment providers and their
  // keys are a permission of their own.
  const canManage = useCan("billing.manage");
  const canPayments = useCan("payments.manage");
  const [loaded, setLoaded] = useState(false);
  const [cfg, setCfg] = useState<BillingInfo | null>(null);
  const [saved, setSaved] = useState<BillingInfo | null>(null);
  const [plans, setPlans] = useState<TariffPlan[]>([]);
  const [plansRef, widePlans] = useWideBox(PLANS_WIDE_MIN);
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
    if (canPayments) {
      getPayments()
        .then((d) => seedProviders(d.providers ?? []))
        .catch((e) => setPayErr(errMessage(e)));
    }
    listGroups()
      .then(setGroups)
      .catch(() => {});
  }, [seedProviders, canPayments]);

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
          manual: !!d.manual,
          manual_label: d.manual_label ?? "",
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
          manual: !!d.manual,
          manual_label: d.manual_label ?? "",
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
      <Panel
        title={t("bill.plans")}
        aside={
          <Button size="xs" variant="light" color="gray" onClick={() => reload()}>
            {t("common.retry")}
          </Button>
        }
      >
        <SettingRow
          hint={<span className="text-danger">{loadErr || t("bill.loadFailed")}</span>}
        />
      </Panel>
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
    cfg.payment_note !== saved.payment_note ||
    cfg.manual !== saved.manual ||
    cfg.manual_label !== saved.manual_label;

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
          manual: cfg.manual,
          manual_label: cfg.manual_label,
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
      <div className="flex flex-1 flex-col gap-3.5">
        <ReadOnly when={!canManage}>
        <Panel
          title={t("settings.tabBilling")}
          aside={
            <Switch
              checked={cfg.enabled}
              onChange={(v) => setCfg({ ...cfg, enabled: v })}
            />
          }
        >
          <SettingRow
            hint={
              <>
                {t("bill.globalHint")}{" "}
                <Trans i18nKey="bill.existingUsers" components={{ b: <b /> }} />
              </>
            }
          />
        </Panel>
        </ReadOnly>
        <PaymentIntegrations
          manualReadOnly={!canManage}
          showProviders={canPayments}
          providers={providers}
          drafts={payDrafts}
          err={payErr}
          onChange={patchProvider}
          manual={cfg.manual}
          label={cfg.manual_label}
          note={cfg.payment_note}
          onManual={(v) => setCfg({ ...cfg, manual: v })}
          onLabel={(v) => setCfg({ ...cfg, manual_label: v })}
          onNote={(v) => setCfg({ ...cfg, payment_note: v })}
        />
        <ReadOnly when={!canManage}>
        <Panel
          title={t("bill.plansTitle")}
          aside={
            <IconButton
              variant="filled"
              color="brand"
              title={t("bill.newPlan")}
              onClick={openCreate}
            >
              <IconPlus />
            </IconButton>
          }
        >
          <SettingRow hint={t("bill.plansHint")} />
          {safePlans.length === 0 ? (
            <EmptyState title={t("bill.noPlans")} />
          ) : (
            <div ref={plansRef}>
              {widePlans && (
                <div
                  className={cn(MICRO, "grid items-center gap-3 border-t border-gray-100 px-3.5 py-2")}
                  style={{ gridTemplateColumns: PLAN_TPL }}
                >
                  <span className="truncate">{t("bill.colPlan")}</span>
                  <span className="truncate">{t("bill.colPrice")}</span>
                  <span className="truncate">{t("bill.colLimits")}</span>
                  <span className="truncate">{t("bill.colUsers")}</span>
                  <span />
                </div>
              )}
              {safePlans.map((p) => {
                const users = planUsers[String(p.id)] ?? 0
                const marks = [
                  !p.enabled ? t("conn.off") : "",
                  cfg.free_plan_id === p.id ? t("bill.afterTrial") : "",
                  cfg.trial_plan_id === p.id ? t("bill.trial") : "",
                  ...groups
                    .filter((g) => (p.group_ids ?? []).includes(g.id))
                    .map((g) => g.name),
                ].filter(Boolean)
                const actions = (
                  <span className="flex justify-end gap-0.5">
                    <IconButton
                      title={t("common.edit")}
                      nav
                      onClick={() => {
                        setEditor({ ...p });
                        setMigrateTo(0);
                      }}
                    >
                      <IconPencil size={16} />
                    </IconButton>
                    <IconButton
                      color="red"
                      title={t("common.delete")}
                      disabled={busy}
                      onClick={() => removePlan(p)}
                    >
                      <IconTrash size={16} />
                    </IconButton>
                  </span>
                )
                return (
                  <div
                    key={p.id}
                    className={cn(
                      "grid items-center gap-x-3 gap-y-0.5 border-t border-gray-100 px-3.5 py-[7px]",
                      !p.enabled && "opacity-60",
                    )}
                    style={{ gridTemplateColumns: widePlans ? PLAN_TPL : PLAN_TPL_NARROW }}
                  >
                    <span className="flex min-w-0 items-center gap-2">
                      <span className="truncate text-[13px] font-medium text-ink">
                        {p.name}
                      </span>
                      {p.slug && (
                        <Mono className="shrink-0 text-[11px] text-ink-muted">
                          {p.slug}
                        </Mono>
                      )}
                    </span>
                    {widePlans ? (
                      <>
                        <Mono className="truncate text-xs text-ink">
                          {p.price_rub > 0
                            ? `${p.price_rub.toLocaleString(currentLang())} ₽`
                            : t("bill.free")}
                        </Mono>
                        <span className="truncate text-xs text-ink-muted" title={planSummary(p)}>
                          {planSummary(p)}
                        </span>
                        <Mono className="truncate text-xs text-ink-muted">
                          {users || "—"}
                        </Mono>
                        {actions}
                      </>
                    ) : (
                      <>
                        {actions}
                        <span className="col-span-2 truncate text-[11px] text-ink-muted">
                          {p.price_rub > 0
                            ? `${p.price_rub.toLocaleString(currentLang())} ₽`
                            : t("bill.free")}{" "}
                          · {planSummary(p)}
                          {users ? ` · ${t("bill.nUsers", { count: users })}` : ""}
                        </span>
                      </>
                    )}
                    {marks.length > 0 && widePlans && (
                      <span className="col-start-1 truncate text-[11px] text-accent">
                        {marks.join(" · ")}
                      </span>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </Panel>

        <Panel title={t("bill.pricing")}>
          <SettingRow hint={t("bill.pricingHint")} />
          <SettingRow
            label={t("bill.freePlan")}
            hint={t("bill.freePlanHint")}
            field={
              <Select
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
            }
          />
          <SettingRow
            label={t("bill.trialPlan")}
            hint={t("bill.trialPlanHint")}
            field={
              <Select
                data={[
                  { value: "0", label: t("bill.notSelected") },
                  ...planOptions.filter((o) => o.value !== String(cfg.free_plan_id)),
                ]}
                value={String(cfg.trial_plan_id)}
                onChange={(v) => setCfg({ ...cfg, trial_plan_id: Number(v) })}
              />
            }
          />
        </Panel>
        </ReadOnly>

        <SaveBar
          dirty={dirty}
          busy={busy}
          onSave={saveSettings}
          onCancel={cancel}
        />

      </div>

      {/* A tariff is a form of a dozen fields plus a migration block — a side drawer
          holds it at full height, with Save pinned where it can always be reached. */}
      <Drawer
        open={!!editor}
        onClose={() => setEditor(null)}
        title={editor?.id ? t("bill.planOf", { name: editor.name }) : t("bill.newPlan")}
        footer={
          editor ? (
            <div className="flex justify-end gap-2">
              <Button
                variant="light"
                color="gray"
                size="sm"
                onClick={() => setEditor(null)}
              >
                {t("common.cancel")}
              </Button>
              <Button size="sm" onClick={savePlan} loading={busy}>
                {t(editor.id ? "common.save" : "common.create")}
              </Button>
            </div>
          ) : undefined
        }
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
                <p className="mt-0.5 text-xs text-ink-muted">{t("bill.migrateHint")}</p>
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
                  size="sm"
                  onClick={migratePlan}
                  disabled={!migrateTo || busy}
                  loading={busy}
                >
                  {t("bill.migrateN", { count: planUsers[String(editor.id)] })}
                </Button>
              </div>
            )}
          </div>
        )}
      </Drawer>
    </>
  );
}
