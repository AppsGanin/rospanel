import { useCallback, useEffect, useState } from "react";
import {
  deleteTariffPlan,
  getBilling,
  getPayments,
  migratePlanUsers,
  saveBilling,
  savePaymentProvider,
  saveTariffPlan,
  type BillingInfo,
  type PaymentProvider,
  type TariffPlan,
} from "./api";
import { fmtBytes, gbToBytes, QUOTA_OPTIONS } from "./format";
import { useAction } from "./hooks";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  CenterLoader,
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
    ? { label: "Выключен", color: "gray" as const }
    : configured
      ? { label: "Подключён", color: "green" as const }
      : { label: "Не настроен", color: "orange" as const };

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
          <p className="truncate text-xs text-ink-muted">{provider.note}</p>
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
                  {f.label}
                  {f.help && (
                    <span className="text-xs text-ink-muted">— {f.help}</span>
                  )}
                </label>
              );
            }
            if (f.kind === "select") {
              const opts = f.options ?? [];
              return (
                <div key={f.key}>
                  <Select
                    label={f.label}
                    data={opts}
                    value={draft.config[f.key] || opts[0]?.value || ""}
                    onChange={(v) => setField(f.key, v)}
                  />
                  {f.help && (
                    <p className="mt-1 text-xs text-ink-muted">{f.help}</p>
                  )}
                </div>
              );
            }
            const isSecret = f.kind === "secret";
            const label =
              isSecret && f.is_set
                ? `${f.label} (задан — оставьте пустым, чтобы не менять)`
                : f.optional
                  ? `${f.label}`
                  : f.label;
            return (
              <div key={f.key}>
                <TextInput
                  label={label}
                  value={draft.config[f.key] ?? ""}
                  onChange={(v) => setField(f.key, v)}
                  placeholder={isSecret && f.is_set ? "••••••••" : f.placeholder}
                />
                {f.help && !isSecret && (
                  <p className="mt-1 text-xs text-ink-muted">{f.help}</p>
                )}
              </div>
            );
          })}

          {provider.webhook_url && (
            <div>
              <p className="mb-1 text-xs text-ink-muted">
                URL для вебхука в кабинете провайдера:
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
  return (
    <SettingCard
      title="Приём платежей"
      description="Автоматическая оплата тарифов в пользовательском боте. Тариф активируется сам после оплаты. Без провайдеров оплата идёт вручную (подтверждает админ)."
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
  price_rub: 0,
  period_days: 30,
  data_limit: 0,
  device_limit: 0,
  sort_order: 0,
  enabled: true,
});


const DEVICES = [
  { value: "0", label: "Без лимита" },
  { value: "1", label: "1" },
  { value: "2", label: "2" },
  { value: "3", label: "3" },
  { value: "5", label: "5" },
  { value: "10", label: "10" },
];

const PERIODS = [
  { value: "0", label: "Бессрочно" },
  { value: "1", label: "1 день" },
  { value: "3", label: "3 дня" },
  { value: "7", label: "7 дней" },
  { value: "14", label: "14 дней" },
  { value: "30", label: "30 дней" },
  { value: "90", label: "90 дней" },
  { value: "180", label: "180 дней" },
  { value: "365", label: "365 дней" },
];

function gbFromBytes(b: number): string {
  if (!b) return "0";
  const gb = b / (1024 * 1024 * 1024);
  const hit = QUOTA_OPTIONS.find((o) => o.value === String(gb));
  return hit ? hit.value : String(gb);
}

function periodLabel(days: number): string {
  if (!days) return "бессрочно";
  return `${days} дн.`;
}

function planSummary(p: TariffPlan): string {
  const parts: string[] = [];
  if (p.price_rub > 0) {
    parts.push(`${p.price_rub} ₽ / ${periodLabel(p.period_days)}`);
  } else {
    parts.push(`бесплатный · ${periodLabel(p.period_days)}`);
  }
  parts.push(p.data_limit ? fmtBytes(p.data_limit) : "∞ трафик");
  parts.push(p.device_limit ? `${p.device_limit} устр.` : "∞ устр.");
  return parts.join(" · ");
}

function PlanForm({
  plan,
  onChange,
  isTrial,
}: {
  plan: TariffPlan;
  onChange: (p: TariffPlan) => void;
  isTrial: boolean;
}) {
  const patch = (p: Partial<TariffPlan>) => onChange({ ...plan, ...p });
  const periodVal = PERIODS.some((o) => o.value === String(plan.period_days))
    ? String(plan.period_days)
    : String(plan.period_days || 0);

  return (
    <div className="flex flex-col gap-3">
      <TextInput
        label="Название"
        value={plan.name}
        onChange={(v) => patch({ name: v })}
        placeholder="Стандарт"
      />
      <TextInput
        label="Код (slug)"
        value={plan.slug}
        onChange={(v) => patch({ slug: v.toLowerCase() })}
        placeholder="standard — пустой = из названия"
      />
      <div className="grid gap-3 sm:grid-cols-2">
        <TextInput
          label="Порядок в списке"
          type="number"
          value={String(plan.sort_order)}
          onChange={(v) => patch({ sort_order: Math.max(0, Number(v) || 0) })}
        />
        <label className="flex items-end gap-2 pb-1 text-sm">
          <Switch checked={plan.enabled} onChange={(v) => patch({ enabled: v })} />
          Активен (виден пользователям)
        </label>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <TextInput
          label="Цена, ₽ (0 = бесплатный)"
          type="number"
          value={String(plan.price_rub)}
          onChange={(v) => patch({ price_rub: Math.max(0, Number(v) || 0) })}
        />
        <Select
          label="Срок действия"
          data={PERIODS}
          value={periodVal}
          onChange={(v) => patch({ period_days: Number(v) })}
        />
      </div>
      <p className="text-xs text-ink-muted">
        {isTrial
          ? "Пробный тариф: доступ истекает через срок действия (при выдаче пробного периода — через «Пробный период, дней»), затем — переход на бесплатный."
          : plan.price_rub <= 0
            ? "Бесплатный тариф: доступ не истекает, лимит трафика сбрасывается каждый срок действия."
            : "Платный тариф: доступ истекает через срок действия, требуется продление."}
      </p>
      <div className="grid gap-3 sm:grid-cols-2">
        <Select
          label="Лимит трафика"
          data={QUOTA_OPTIONS}
          value={gbFromBytes(plan.data_limit)}
          onChange={(v) => patch({ data_limit: gbToBytes(Number(v)) })}
        />
        <Select
          label="Лимит устройств"
          data={DEVICES}
          value={String(plan.device_limit)}
          onChange={(v) => patch({ device_limit: Number(v) })}
        />
      </div>
    </div>
  );
}

export function BillingPanel() {
  const [loaded, setLoaded] = useState(false);
  const [cfg, setCfg] = useState<BillingInfo | null>(null);
  const [saved, setSaved] = useState<BillingInfo | null>(null);
  const [plans, setPlans] = useState<TariffPlan[]>([]);
  const [planUsers, setPlanUsers] = useState<Record<string, number>>({});
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
  }, [seedProviders]);

  const patchProvider = (key: string, d: ProviderDraft) =>
    setPayDrafts((s) => ({ ...s, [key]: d }));

  const reload = useCallback(() => {
    getBilling()
      .then((d) => {
        const nextPlans = d.plans ?? [];
        const nextCfg: BillingInfo = {
          enabled: !!d.enabled,
          trial_days: d.trial_days ?? 0,
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
          trial_days: d.trial_days ?? 0,
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
      <SettingCard title="Тарифы">
        <p className="text-sm text-danger">
          {loadErr || "Не удалось загрузить настройки тарифов."}
        </p>
        <Button className="mt-3" onClick={() => reload()}>
          Повторить
        </Button>
      </SettingCard>
    );
  }

  const safePlans = plans ?? [];
  const planOptions = safePlans
    .filter((p) => p.enabled)
    .map((p) => ({
      value: String(p.id),
      label: p.name + (p.price_rub <= 0 ? " (бесплатный)" : ""),
    }));

  const billingDirty =
    cfg.enabled !== saved.enabled ||
    cfg.trial_days !== saved.trial_days ||
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
          trial_days: cfg.trial_days,
          free_plan_id: cfg.free_plan_id,
          trial_plan_id: cfg.trial_plan_id,
          payment_note: cfg.payment_note,
        });
        setSaved({ ...cfg, plans: safePlans });
        // Tell the top nav to re-read billing_enabled so the "Оплата" menu item
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
      notifySuccess("Настройки сохранены");
    }).catch((e) => notifyError(errMessage(e)));

  const openCreate = () => {
    const maxOrder = safePlans.reduce((m, p) => Math.max(m, p.sort_order), 0);
    setEditor({ ...EMPTY_PLAN(), sort_order: maxOrder + 1 });
  };

  const savePlan = () => {
    if (!editor) return;
    if (!editor.name.trim()) {
      notifyError("Укажите название тарифа");
      return;
    }
    run(async () => {
      const savedPlan = await saveTariffPlan(editor);
      setEditor(null);
      reload();
      notifySuccess(savedPlan.id ? "Тариф сохранён" : "Тариф создан");
    }).catch((e) => notifyError(errMessage(e)));
  };

  const migratePlan = () => {
    if (!editor?.id || !migrateTo) return;
    run(async () => {
      const r = await migratePlanUsers(editor.id, migrateTo);
      setMigrateTo(0);
      reload();
      notifySuccess(`Переведено пользователей: ${r.migrated}`);
    }).catch((e) => notifyError(errMessage(e)));
  };

  const removePlan = async (p: TariffPlan) => {
    const ok = await confirm({
      title: "Удалить тариф?",
      body: `«${p.name}» будет удалён без возможности восстановления.`,
      confirmLabel: "Удалить",
      danger: true,
    });
    if (!ok) return;
    run(async () => {
      await deleteTariffPlan(p.id);
      reload();
      notifySuccess("Тариф удалён");
    }).catch((e) => notifyError(errMessage(e)));
  };


  return (
    <>
      {confirmNode}
      <div className="flex flex-col gap-4">
        <SettingCard
          title="Оплата"
          description="Глобальное включение приёма оплаты. При включении в главном меню появляется раздел «Оплата» со статистикой, ожидающими и историей."
          action={
            <Switch
              checked={cfg.enabled}
              onChange={(v) => setCfg({ ...cfg, enabled: v })}
            />
          }
        >
          <p className="text-sm text-ink-muted">
            Существующие пользователи <b>не меняются</b> — у них остаются текущие
            лимиты («тариф вручную»), пока вы не назначите тариф в карточке
            пользователя или через admin-бот.
          </p>
        </SettingCard>
        <PaymentIntegrations
          providers={providers}
          drafts={payDrafts}
          err={payErr}
          onChange={patchProvider}
        />
        <SettingCard
          title="Тарифные планы"
          description="Создавайте и настраивайте тарифы: лимиты, цена, срок. Бесплатный тариф — для пользователей после пробного периода."
          action={
            <Button size="sm" onClick={openCreate}>
              + Создать
            </Button>
          }
        >
          {safePlans.length === 0 ? (
            <p className="text-sm text-ink-muted">
              Тарифов пока нет. Нажмите «Создать», чтобы добавить первый.
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
                      {!p.enabled && <Badge color="gray">выключен</Badge>}
                      {(planUsers[String(p.id)] ?? 0) > 0 && (
                        <Badge color="gray">
                          {planUsers[String(p.id)]} польз.
                        </Badge>
                      )}
                      {p.price_rub <= 0 && <Badge color="teal">бесплатный</Badge>}
                      {cfg.free_plan_id === p.id && (
                        <Badge color="brand">после пробного</Badge>
                      )}
                      {cfg.trial_plan_id === p.id && (
                        <Badge color="orange">пробный</Badge>
                      )}
                    </div>
                    <p className="mt-0.5 text-xs text-ink-muted">
                      {planSummary(p)}
                      {p.slug ? ` · код: ${p.slug}` : ""}
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
                      Изменить
                    </Button>
                    <Button
                      size="sm"
                      variant="subtle"
                      color="red"
                      onClick={() => removePlan(p)}
                      disabled={busy}
                    >
                      Удалить
                    </Button>
                  </span>
                </li>
              ))}
            </ul>
          )}
        </SettingCard>

        <SettingCard
          title="Тарификация"
          description="Пробный период, тариф по умолчанию и реквизиты для ручной оплаты. Действуют в user-боте и на странице подписки."
        >
          <div className="flex flex-col gap-4">
            <div>
              <TextInput
                label="Пробный период, дней"
                type="number"
                value={String(cfg.trial_days)}
                onChange={(v) =>
                  setCfg({ ...cfg, trial_days: Math.max(0, Number(v) || 0) })
                }
              />
              <p className="mt-1 text-xs text-ink-muted">
                Сколько дней действует пробный тариф после регистрации. 0 —
                без пробного периода.
              </p>
            </div>
            <div>
              <Select
                label="Тариф после пробного / при истечении"
                data={[{ value: "0", label: "— не выбран —" }, ...planOptions]}
                value={String(cfg.free_plan_id)}
                onChange={(v) => setCfg({ ...cfg, free_plan_id: Number(v) })}
              />
              <p className="mt-1 text-xs text-ink-muted">
                На него пользователь переходит, когда закончился пробный или
                платный период, а также при отмене подписки.
              </p>
            </div>
            <div>
              <Select
                label="Пробный тариф (лимиты на время пробы)"
                data={[{ value: "0", label: "— не выбран —" }, ...planOptions]}
                value={String(cfg.trial_plan_id)}
                onChange={(v) => setCfg({ ...cfg, trial_plan_id: Number(v) })}
              />
              <p className="mt-1 text-xs text-ink-muted">
                Лимиты (трафик, устройства), которые действуют во время
                пробного периода.
              </p>
            </div>
            <Textarea
              label="Реквизиты для ручной оплаты"
              value={cfg.payment_note}
              onChange={(v) => setCfg({ ...cfg, payment_note: v })}
              placeholder={
                "Например:\nПеревод на карту 0000 0000 0000 0000\nили СБП по номеру +7 900 000-00-00\nПосле оплаты напишите @admin"
              }
              rows={4}
              hint="Показывается пользователю, когда не подключён автоматический провайдер — и в боте, и на странице подписки. Укажите реквизиты и как подтвердить перевод."
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
        title={editor?.id ? `Тариф: ${editor.name}` : "Новый тариф"}
        size="md"
      >
        {editor && (
          <div className="flex flex-col gap-4">
            <PlanForm
              plan={editor}
              onChange={setEditor}
              isTrial={editor.id > 0 && cfg.trial_plan_id === editor.id}
            />
            {editor.id > 0 && (planUsers[String(editor.id)] ?? 0) > 0 && (
              <div className="accent-tint border-accent rounded-lg border p-3">
                <p className="text-sm font-semibold text-accent">
                  На тарифе {planUsers[String(editor.id)]} польз.
                </p>
                <p className="mt-0.5 text-xs text-ink-muted">
                  Перед отключением или удалением тарифа переведите их на другой —
                  они получат его лимиты и срок.
                </p>
                <Select
                  className="mt-2"
                  label="Перевести на другой тариф"
                  data={[
                    { value: "0", label: "— выберите тариф —" },
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
                  Перевести {planUsers[String(editor.id)]} польз.
                </Button>
              </div>
            )}
            <div className="flex justify-end gap-2">
              <Button variant="subtle" onClick={() => setEditor(null)}>
                Отмена
              </Button>
              <Button onClick={savePlan} loading={busy}>
                {editor.id ? "Сохранить" : "Создать"}
              </Button>
            </div>
          </div>
        )}
      </Modal>
    </>
  );
}
