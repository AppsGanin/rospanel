import { useCallback, useEffect, useState } from "react";
import {
  cancelPaymentOrder,
  confirmPaymentOrder,
  deleteTariffPlan,
  getBilling,
  getPayments,
  listPaymentOrders,
  saveBilling,
  savePayments,
  saveTariffPlan,
  type BillingInfo,
  type PaymentOrder,
  type PaymentSettings,
  type TariffPlan,
} from "./api";
import { fmtBytes, gbToBytes } from "./format";
import { useAction } from "./hooks";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  CenterLoader,
  Code,
  Modal,
  PasswordInput,
  SaveBar,
  Select,
  SettingCard,
  Switch,
  TextInput,
  useConfirm,
} from "./ui";

// PaymentIntegrations is a self-contained card for the YooKassa / CryptoBot
// provider settings (independent of the tariff editor below).
function PaymentIntegrations() {
  const [info, setInfo] = useState<PaymentSettings | null>(null);
  const [yooKey, setYooKey] = useState("");
  const [cbToken, setCbToken] = useState("");
  const { isBusy, run } = useAction();

  useEffect(() => {
    getPayments().then(setInfo).catch(() => {});
  }, []);

  const patch = (p: Partial<PaymentSettings>) =>
    setInfo((s) => (s ? { ...s, ...p } : s));

  const save = () =>
    run(
      async () => {
        if (!info) return;
        const next = await savePayments({
          yookassa_enabled: info.yookassa_enabled,
          yookassa_shop_id: info.yookassa_shop_id.trim(),
          yookassa_secret_key: yooKey.trim(),
          yookassa_test: info.yookassa_test,
          cryptobot_enabled: info.cryptobot_enabled,
          cryptobot_token: cbToken.trim(),
          cryptobot_testnet: info.cryptobot_testnet,
        });
        setInfo(next);
        setYooKey("");
        setCbToken("");
        notifySuccess("Настройки оплаты сохранены");
      },
      { key: "pay" },
    );

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

        <div>
          <Button loading={isBusy("pay")} onClick={save}>
            Сохранить
          </Button>
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
  is_free: false,
  payment_url: "",
  sort_order: 0,
  enabled: true,
});

const QUOTA_GB = [
  { value: "0", label: "Без лимита" },
  { value: "5", label: "5 ГБ" },
  { value: "10", label: "10 ГБ" },
  { value: "50", label: "50 ГБ" },
  { value: "100", label: "100 ГБ" },
  { value: "500", label: "500 ГБ" },
];

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
  { value: "7", label: "7 дней" },
  { value: "30", label: "30 дней" },
  { value: "90", label: "90 дней" },
  { value: "365", label: "365 дней" },
];

function gbFromBytes(b: number): string {
  if (!b) return "0";
  const gb = b / (1024 * 1024 * 1024);
  const hit = QUOTA_GB.find((o) => o.value === String(gb));
  return hit ? hit.value : String(gb);
}

function periodLabel(days: number, isFree: boolean): string {
  if (isFree) return "бессрочно";
  if (!days) return "бессрочно";
  return `${days} дн.`;
}

function planSummary(p: TariffPlan): string {
  const parts: string[] = [];
  if (p.is_free) {
    parts.push("бесплатный");
  } else if (p.price_rub > 0) {
    parts.push(`${p.price_rub} ₽ / ${periodLabel(p.period_days, false)}`);
  } else {
    parts.push(periodLabel(p.period_days, false));
  }
  parts.push(p.data_limit ? fmtBytes(p.data_limit) : "∞ трафик");
  parts.push(p.device_limit ? `${p.device_limit} устр.` : "∞ устр.");
  return parts.join(" · ");
}

function PlanForm({
  plan,
  onChange,
}: {
  plan: TariffPlan;
  onChange: (p: TariffPlan) => void;
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
      <label className="flex items-center gap-2 text-sm">
        <Switch
          checked={plan.is_free}
          onChange={(v) =>
            patch({
              is_free: v,
              price_rub: v ? 0 : plan.price_rub,
              period_days: v ? 0 : plan.period_days || 30,
            })
          }
        />
        Бесплатный тариф (лимит трафика сбрасывается каждый месяц)
      </label>
      {!plan.is_free && (
        <div className="grid gap-3 sm:grid-cols-2">
          <TextInput
            label="Цена, ₽"
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
          <TextInput
            label="Ссылка на оплату"
            value={plan.payment_url}
            onChange={(v) => patch({ payment_url: v })}
            placeholder="https://..."
            className="sm:col-span-2"
          />
        </div>
      )}
      <div className="grid gap-3 sm:grid-cols-2">
        <Select
          label="Лимит трафика"
          data={QUOTA_GB}
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
  const [orders, setOrders] = useState<PaymentOrder[]>([]);
  const [editor, setEditor] = useState<TariffPlan | null>(null);
  const [loadErr, setLoadErr] = useState("");
  const [confirmOrderId, setConfirmOrderId] = useState<number | null>(null);
  const [cancelOrderId, setCancelOrderId] = useState<number | null>(null);
  const [confirmPassword, setConfirmPassword] = useState("");
  const { busy, run } = useAction();
  const { confirm, confirmNode } = useConfirm();

  const loadOrders = useCallback(() => {
    listPaymentOrders("pending")
      .then((rows) => setOrders(rows ?? []))
      .catch(() => setOrders([]));
  }, []);

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
        setLoadErr("");
      })
      .catch((e) => setLoadErr(errMessage(e)))
      .finally(() => setLoaded(true));
    loadOrders();
  }, [loadOrders]);

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
      label: p.name + (p.is_free ? " (бесплатный)" : ""),
    }));

  const dirty =
    cfg.enabled !== saved.enabled ||
    cfg.trial_days !== saved.trial_days ||
    cfg.free_plan_id !== saved.free_plan_id ||
    cfg.trial_plan_id !== saved.trial_plan_id ||
    cfg.payment_note !== saved.payment_note;

  const saveSettings = () =>
    run(async () => {
      await saveBilling({
        enabled: cfg.enabled,
        trial_days: cfg.trial_days,
        free_plan_id: cfg.free_plan_id,
        trial_plan_id: cfg.trial_plan_id,
        payment_note: cfg.payment_note,
      });
      setSaved({ ...cfg, plans: safePlans });
      notifySuccess("Настройки тарификации сохранены");
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

  const confirmOrder = (id: number) => {
    setConfirmPassword("");
    setConfirmOrderId(id);
  };

  const submitConfirmOrder = () =>
    run(async () => {
      if (confirmOrderId === null) return;
      if (!confirmPassword) {
        notifyError("Введите текущий пароль администратора");
        return;
      }
      await confirmPaymentOrder(confirmOrderId, confirmPassword);
      setConfirmOrderId(null);
      loadOrders();
      notifySuccess("Оплата подтверждена, тариф применён");
    }).catch((e) => notifyError(errMessage(e)));

  const cancelOrder = (id: number) => {
    setConfirmPassword("");
    setCancelOrderId(id);
  };

  const submitCancelOrder = () =>
    run(async () => {
      if (cancelOrderId === null) return;
      if (!confirmPassword) {
        notifyError("Введите текущий пароль администратора");
        return;
      }
      await cancelPaymentOrder(cancelOrderId, confirmPassword);
      setCancelOrderId(null);
      loadOrders();
      notifySuccess("Заказ отменён");
    }).catch((e) => notifyError(errMessage(e)));

  return (
    <>
      {confirmNode}
      <div className="flex flex-col gap-4">
        <PaymentIntegrations />
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
                      {p.is_free && <Badge color="teal">бесплатный</Badge>}
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
                      onClick={() => setEditor({ ...p })}
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

        <SettingCard title="Тарификация">
          <p className="mb-3 text-sm text-ink-muted">
            При включении существующие пользователи <b>не меняются</b> — у них остаются
            текущие лимиты («тариф вручную»), пока вы не назначите тариф в карточке
            пользователя или через admin-бот. Новые регистрации в user-боте получают
            пробный период, затем бесплатный тариф.
          </p>
          <div className="flex flex-col gap-3">
            <label className="flex items-center justify-between gap-3 text-sm">
              <span>Включить тарификацию</span>
              <Switch
                checked={cfg.enabled}
                onChange={(v) => setCfg({ ...cfg, enabled: v })}
              />
            </label>
            {cfg.enabled && (
              <>
                <TextInput
                  label="Пробный период, дней"
                  type="number"
                  value={String(cfg.trial_days)}
                  onChange={(v) =>
                    setCfg({ ...cfg, trial_days: Math.max(0, Number(v) || 0) })
                  }
                />
                <Select
                  label="Тариф после пробного / при истечении"
                  data={[{ value: "0", label: "— не выбран —" }, ...planOptions]}
                  value={String(cfg.free_plan_id)}
                  onChange={(v) => setCfg({ ...cfg, free_plan_id: Number(v) })}
                />
                <Select
                  label="Пробный тариф (лимиты на время пробы)"
                  data={[{ value: "0", label: "— не выбран —" }, ...planOptions]}
                  value={String(cfg.trial_plan_id)}
                  onChange={(v) => setCfg({ ...cfg, trial_plan_id: Number(v) })}
                />
                <TextInput
                  label="Примечание к оплате"
                  value={cfg.payment_note}
                  onChange={(v) => setCfg({ ...cfg, payment_note: v })}
                  placeholder="Реквизиты, СБП, комментарий для пользователя"
                />
              </>
            )}
          </div>
        </SettingCard>

        <SaveBar
          dirty={dirty}
          busy={busy}
          onSave={saveSettings}
          onCancel={() => setCfg(saved)}
        />

        {cfg.enabled && (
          <SettingCard title="Ожидают оплаты">
            {orders.length === 0 ? (
              <p className="text-sm text-ink-muted">Нет необработанных заказов.</p>
            ) : (
              <ul className="flex flex-col gap-2">
                {orders.map((o) => (
                  <OrderRow
                    key={o.id}
                    order={o}
                    busy={busy}
                    onConfirm={confirmOrder}
                    onCancel={cancelOrder}
                  />
                ))}
              </ul>
            )}
          </SettingCard>
        )}
      </div>

      <Modal
        open={confirmOrderId !== null}
        onClose={() => setConfirmOrderId(null)}
        title="Подтверждение оплаты"
      >
        <p className="mb-3 text-sm text-ink-muted">
          Введите текущий пароль администратора, чтобы подтвердить заказ и применить тариф.
        </p>
        <PasswordInput
          label="Текущий пароль"
          value={confirmPassword}
          onChange={setConfirmPassword}
        />
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="subtle" onClick={() => setConfirmOrderId(null)}>
            Отмена
          </Button>
          <Button loading={busy} onClick={() => void submitConfirmOrder()}>
            Подтвердить оплату
          </Button>
        </div>
      </Modal>

      <Modal
        open={cancelOrderId !== null}
        onClose={() => setCancelOrderId(null)}
        title="Отмена заказа"
      >
        <p className="mb-3 text-sm text-ink-muted">
          Введите текущий пароль администратора, чтобы отменить заказ.
        </p>
        <PasswordInput
          label="Текущий пароль"
          value={confirmPassword}
          onChange={setConfirmPassword}
        />
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="subtle" onClick={() => setCancelOrderId(null)}>
            Назад
          </Button>
          <Button loading={busy} color="red" onClick={() => void submitCancelOrder()}>
            Отменить заказ
          </Button>
        </div>
      </Modal>

      <Modal
        open={!!editor}
        onClose={() => setEditor(null)}
        title={editor?.id ? `Тариф: ${editor.name}` : "Новый тариф"}
        size="md"
      >
        {editor && (
          <div className="flex flex-col gap-4">
            <PlanForm plan={editor} onChange={setEditor} />
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

const PROVIDER_META: Record<
  string,
  { label: string; color: "brand" | "teal" | "gray" }
> = {
  yookassa: { label: "ЮКасса · карта", color: "brand" },
  cryptobot: { label: "CryptoBot · крипта", color: "teal" },
  "": { label: "Вручную", color: "gray" },
};

function fmtDateTime(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString("ru-RU", {
    day: "2-digit",
    month: "2-digit",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function OrderRow({
  order,
  busy,
  onConfirm,
  onCancel,
}: {
  order: PaymentOrder;
  busy: boolean;
  onConfirm: (id: number) => void;
  onCancel: (id: number) => void;
}) {
  const prov = PROVIDER_META[order.provider] ?? {
    label: order.provider,
    color: "gray" as const,
  };
  const auto = order.provider !== "";
  return (
    <li className="flex flex-col gap-2 rounded-lg border border-gray-200 px-3 py-2.5 text-sm">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="font-medium text-ink">
          <b>#{order.id}</b> · {order.user_name ?? `user ${order.user_id}`} ·{" "}
          {order.plan_name} · {order.amount_rub} ₽
        </span>
        <span className="flex gap-2">
          <Button size="sm" onClick={() => onConfirm(order.id)} disabled={busy}>
            Подтвердить
          </Button>
          <Button
            size="sm"
            variant="subtle"
            color="red"
            onClick={() => onCancel(order.id)}
            disabled={busy}
          >
            Отмена
          </Button>
        </span>
      </div>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-ink-muted">
        <Badge color={prov.color} size="xs">
          {prov.label}
        </Badge>
        <span>· создан {fmtDateTime(order.created_at)}</span>
        {auto && <span>· оплата подтвердится автоматически</span>}
        {order.provider_id && (
          <span className="font-mono">· id: {order.provider_id}</span>
        )}
      </div>
    </li>
  );
}
