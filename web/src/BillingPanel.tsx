import { useCallback, useEffect, useState } from "react";
import {
  deleteTariffPlan,
  getBilling,
  getPayments,
  migratePlanUsers,
  saveBilling,
  savePayments,
  saveTariffPlan,
  type BillingInfo,
  type PaymentSettings,
  type TariffPlan,
} from "./api";
import { fmtBytes, gbToBytes, QUOTA_OPTIONS } from "./format";
import { useAction } from "./hooks";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  CenterLoader,
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

// PaymentIntegrations renders the YooKassa / CryptoBot provider settings. It is
// controlled by BillingPanel: edits go through `patch`, and saving happens via the
// shared bottom SaveBar (no local save button).
function PaymentIntegrations({
  info,
  patch,
  yooKey,
  setYooKey,
  cbToken,
  setCbToken,
}: {
  info: PaymentSettings | null;
  patch: (p: Partial<PaymentSettings>) => void;
  yooKey: string;
  setYooKey: (v: string) => void;
  cbToken: string;
  setCbToken: (v: string) => void;
}) {
  if (!info) return null;

  return (
    <SettingCard
      title="Приём платежей"
      description="Автоматическая оплата тарифов в пользовательском боте. Тариф активируется сам после оплаты. Без провайдеров оплата идёт вручную (подтверждает админ)."
    >
      <div className="flex flex-col gap-5">
        {/* YooKassa */}
        <div className="flex flex-col gap-3">
          <div className="flex items-center justify-between">
            <p className="font-semibold text-ink">ЮКасса — карты, ₽</p>
            <Switch
              checked={info.yookassa_enabled}
              onChange={(v) => patch({ yookassa_enabled: v })}
            />
          </div>
          {info.yookassa_enabled && (
            <div className="flex flex-col gap-2">
              <TextInput
                label="shopId"
                value={info.yookassa_shop_id}
                onChange={(v) => patch({ yookassa_shop_id: v })}
              />
              <TextInput
                label={
                  info.yookassa_key_set
                    ? "Секретный ключ (задан — оставьте пустым, чтобы не менять)"
                    : "Секретный ключ"
                }
                value={yooKey}
                onChange={setYooKey}
                placeholder={info.yookassa_key_set ? "••••••••" : "live_… или test_…"}
              />
              <label className="flex items-center gap-2 text-sm text-ink">
                <Switch
                  checked={info.yookassa_test}
                  onChange={(v) => patch({ yookassa_test: v })}
                />
                Тестовый режим (тестовый магазин)
              </label>
              {info.webhook_yookassa && (
                <div>
                  <p className="mb-1 text-xs text-ink-muted">
                    URL для уведомлений в кабинете ЮКассы:
                  </p>
                  <Code block copy>{info.webhook_yookassa}</Code>
                </div>
              )}
            </div>
          )}
        </div>

        {/* CryptoBot */}
        <div className="flex flex-col gap-3 border-t border-gray-200 pt-4">
          <div className="flex items-center justify-between">
            <p className="font-semibold text-ink">CryptoBot — крипта (Telegram)</p>
            <Switch
              checked={info.cryptobot_enabled}
              onChange={(v) => patch({ cryptobot_enabled: v })}
            />
          </div>
          {info.cryptobot_enabled && (
            <div className="flex flex-col gap-2">
              <TextInput
                label={
                  info.cryptobot_token_set
                    ? "API-токен (задан — оставьте пустым, чтобы не менять)"
                    : "API-токен (@CryptoBot → Crypto Pay)"
                }
                value={cbToken}
                onChange={setCbToken}
                placeholder={info.cryptobot_token_set ? "••••••••" : "12345:AA…"}
              />
              <label className="flex items-center gap-2 text-sm text-ink">
                <Switch
                  checked={info.cryptobot_testnet}
                  onChange={(v) => patch({ cryptobot_testnet: v })}
                />
                Тестовый режим (testnet · @CryptoTestnetBot)
              </label>
              {info.webhook_cryptobot && (
                <div>
                  <p className="mb-1 text-xs text-ink-muted">
                    URL для вебхука в настройках Crypto Pay:
                  </p>
                  <Code block copy>{info.webhook_cryptobot}</Code>
                </div>
              )}
            </div>
          )}
        </div>
      </div>
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
  // Payment-provider settings live here too so the whole tab shares one bottom
  // SaveBar (pay = draft, paySaved = server truth; yooKey/cbToken are write-only).
  const [pay, setPay] = useState<PaymentSettings | null>(null);
  const [paySaved, setPaySaved] = useState<PaymentSettings | null>(null);
  const [yooKey, setYooKey] = useState("");
  const [cbToken, setCbToken] = useState("");
  const { busy, run } = useAction();
  const { confirm, confirmNode } = useConfirm();

  const patchPay = (p: Partial<PaymentSettings>) =>
    setPay((s) => (s ? { ...s, ...p } : s));

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
    getPayments()
      .then((p) => {
        setPay(p);
        setPaySaved(p);
      })
      .catch(() => {});
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

  const payDirty =
    !!pay &&
    !!paySaved &&
    (pay.yookassa_enabled !== paySaved.yookassa_enabled ||
      pay.yookassa_shop_id !== paySaved.yookassa_shop_id ||
      pay.yookassa_test !== paySaved.yookassa_test ||
      pay.cryptobot_enabled !== paySaved.cryptobot_enabled ||
      pay.cryptobot_testnet !== paySaved.cryptobot_testnet ||
      yooKey.trim() !== "" ||
      cbToken.trim() !== "");

  const dirty = billingDirty || payDirty;

  const cancel = () => {
    setCfg(saved);
    if (paySaved) setPay(paySaved);
    setYooKey("");
    setCbToken("");
  };

  // saveSettings persists whatever is dirty — the tariff settings and/or the
  // payment-provider settings — behind the single bottom SaveBar.
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
      if (payDirty && pay) {
        const next = await savePayments({
          yookassa_enabled: pay.yookassa_enabled,
          yookassa_shop_id: pay.yookassa_shop_id.trim(),
          yookassa_secret_key: yooKey.trim(),
          yookassa_test: pay.yookassa_test,
          cryptobot_enabled: pay.cryptobot_enabled,
          cryptobot_token: cbToken.trim(),
          cryptobot_testnet: pay.cryptobot_testnet,
        });
        setPay(next);
        setPaySaved(next);
        setYooKey("");
        setCbToken("");
      }
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
          info={pay}
          patch={patchPay}
          yooKey={yooKey}
          setYooKey={setYooKey}
          cbToken={cbToken}
          setCbToken={setCbToken}
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
              hint="Показывается пользователю, когда не подключён автоматический провайдер (ЮКасса/CryptoBot) — и в боте, и на странице подписки. Укажите реквизиты и как подтвердить перевод."
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
