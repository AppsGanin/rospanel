import { useEffect, useState } from "react";
import {
  cancelPaymentOrder,
  confirmPaymentOrder,
  getPaymentStats,
  listPaymentOrders,
  type PaymentOrder,
  type PaymentStats,
} from "./api";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  CenterLoader,
  cn,
  Modal,
  PasswordInput,
  SettingCard,
} from "./ui";

const PROVIDER_META: Record<
  string,
  { label: string; color: "brand" | "teal" | "gray" }
> = {
  yookassa: { label: "ЮКасса · карта", color: "brand" },
  cryptobot: { label: "CryptoBot · крипта", color: "teal" },
  pal24: { label: "PayPalych · карта/СБП", color: "brand" },
  riopay: { label: "RioPay · карта/СБП", color: "brand" },
  rollypay: { label: "RollyPay · карта/СБП", color: "brand" },
  severpay: { label: "SeverPay · карта/СБП", color: "brand" },
  platega: { label: "Platega · карта/СБП", color: "brand" },
  paypear: { label: "PayPear · карта/СБП", color: "brand" },
  aurapay: { label: "AuraPay · карта/СБП", color: "brand" },
  heleket: { label: "Heleket · крипта", color: "teal" },
  "": { label: "Вручную", color: "gray" },
};

const STATUS_META: Record<
  string,
  { label: string; color: "green" | "gray" | "orange" }
> = {
  paid: { label: "оплачен", color: "green" },
  cancelled: { label: "отменён", color: "gray" },
  pending: { label: "ожидает", color: "orange" },
};

function fmtRub(n: number): string {
  return `${n.toLocaleString("ru-RU")} ₽`;
}

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

function providerMeta(p: string) {
  return PROVIDER_META[p] ?? { label: p, color: "gray" as const };
}

// StatTile is one headline number in the revenue row.
function StatTile({
  label,
  value,
  sub,
  accent,
}: {
  label: string;
  value: string;
  sub?: string;
  accent?: boolean;
}) {
  return (
    <div
      className={cn(
        "rounded-xl border p-4",
        accent ? "border-transparent accent-tint" : "border-gray-200 bg-white",
      )}
    >
      <div className="text-xs font-medium text-ink-muted">{label}</div>
      <div
        className={cn(
          "mt-1 text-2xl font-bold tracking-tight",
          accent ? "text-accent" : "text-ink",
        )}
      >
        {value}
      </div>
      {sub && <div className="mt-0.5 text-xs text-ink-muted">{sub}</div>}
    </div>
  );
}

// PendingRow is an actionable order awaiting payment.
function PendingRow({
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
  const prov = providerMeta(order.provider);
  const auto = order.provider !== "";
  return (
    <li className="flex flex-col gap-2 rounded-xl border border-gray-200 px-3 py-2.5 text-sm">
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
      </div>
    </li>
  );
}

// HistoryRow is a read-only completed order.
function HistoryRow({ order }: { order: PaymentOrder }) {
  const prov = providerMeta(order.provider);
  const st = STATUS_META[order.status] ?? {
    label: order.status,
    color: "gray" as const,
  };
  return (
    <li className="flex items-center justify-between gap-3 rounded-xl border border-gray-200 px-3 py-2.5 text-sm">
      <div className="min-w-0">
        <div className="truncate font-medium text-ink">
          <b>#{order.id}</b> · {order.user_name ?? `user ${order.user_id}`} ·{" "}
          {order.plan_name}
        </div>
        <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-ink-muted">
          <Badge color={prov.color} size="xs">
            {prov.label}
          </Badge>
          <span>
            · {order.status === "paid" ? "оплачен" : "создан"}{" "}
            {fmtDateTime(order.status === "paid" ? order.paid_at : order.created_at)}
          </span>
        </div>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        <span className="font-semibold text-ink">{order.amount_rub} ₽</span>
        <Badge color={st.color}>{st.label}</Badge>
      </div>
    </li>
  );
}

export function PaymentsPage() {
  const [stats, setStats] = useState<PaymentStats | null>(null);
  const [orders, setOrders] = useState<PaymentOrder[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);

  // Password step-up for confirm/cancel.
  const [confirmId, setConfirmId] = useState<number | null>(null);
  const [cancelId, setCancelId] = useState<number | null>(null);
  const [password, setPassword] = useState("");

  const refresh = () =>
    Promise.all([getPaymentStats(), listPaymentOrders()])
      .then(([s, o]) => {
        setStats(s);
        setOrders(o);
      })
      .catch((e) => notifyError(errMessage(e)))
      .finally(() => setLoading(false));

  useEffect(() => {
    refresh();
  }, []);

  const submitConfirm = async () => {
    if (confirmId === null) return;
    setBusy(true);
    try {
      await confirmPaymentOrder(confirmId, password);
      notifySuccess("Оплата подтверждена");
      setConfirmId(null);
      setPassword("");
      await refresh();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const submitCancel = async () => {
    if (cancelId === null) return;
    setBusy(true);
    try {
      await cancelPaymentOrder(cancelId, password);
      notifySuccess("Заказ отменён");
      setCancelId(null);
      setPassword("");
      await refresh();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  if (loading) return <CenterLoader />;
  if (!stats) return null;

  const pending = orders.filter((o) => o.status === "pending");
  const history = orders.filter((o) => o.status !== "pending");

  return (
    <div className="flex flex-col gap-4">
      {/* Revenue headline */}
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatTile
          label="Всего заработано"
          value={fmtRub(stats.total_paid)}
          sub={`${stats.paid_count} оплат`}
          accent
        />
        <StatTile label="За месяц" value={fmtRub(stats.earned_month)} />
        <StatTile label="Сегодня" value={fmtRub(stats.earned_today)} />
        <StatTile
          label="Ожидают оплаты"
          value={String(stats.pending_count)}
          sub={stats.pending_sum ? `на ${fmtRub(stats.pending_sum)}` : "—"}
        />
      </div>

      <SettingCard
        title="По провайдерам"
        description="Подтверждённые оплаты в разрезе способа."
      >
        {stats.by_provider.length === 0 ? (
          <p className="text-sm text-ink-muted">Оплат пока не было.</p>
        ) : (
          <div className="flex flex-col gap-2">
            {stats.by_provider.map((p) => {
              const meta = providerMeta(p.provider);
              const share = stats.total_paid
                ? Math.round((p.sum / stats.total_paid) * 100)
                : 0;
              return (
                <div
                  key={p.provider || "manual"}
                  className="flex items-center justify-between gap-3 rounded-xl border border-gray-200 px-3 py-2.5"
                >
                  <div className="flex items-center gap-2">
                    <Badge color={meta.color}>{meta.label}</Badge>
                    <span className="text-xs text-ink-muted">
                      {p.count} оплат · {share}%
                    </span>
                  </div>
                  <span className="font-semibold text-ink">{fmtRub(p.sum)}</span>
                </div>
              );
            })}
          </div>
        )}
      </SettingCard>

      <SettingCard
        title="Ожидают оплаты"
        action={
          pending.length > 0 ? (
            <Badge color="orange">{pending.length}</Badge>
          ) : undefined
        }
      >
        {pending.length === 0 ? (
          <p className="text-sm text-ink-muted">Нет необработанных заказов.</p>
        ) : (
          <ul className="flex flex-col gap-2">
            {pending.map((o) => (
              <PendingRow
                key={o.id}
                order={o}
                busy={busy}
                onConfirm={setConfirmId}
                onCancel={setCancelId}
              />
            ))}
          </ul>
        )}
      </SettingCard>

      <SettingCard title="История оплат">
        {history.length === 0 ? (
          <p className="text-sm text-ink-muted">История пуста.</p>
        ) : (
          <ul className="flex flex-col gap-2">
            {history.map((o) => (
              <HistoryRow key={o.id} order={o} />
            ))}
          </ul>
        )}
      </SettingCard>

      <Modal
        open={confirmId !== null}
        onClose={() => setConfirmId(null)}
        title="Подтверждение оплаты"
      >
        <p className="mb-3 text-sm text-ink-muted">
          Введите текущий пароль администратора, чтобы подтвердить заказ и
          применить тариф.
        </p>
        <PasswordInput
          label="Текущий пароль"
          value={password}
          onChange={setPassword}
        />
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="subtle" onClick={() => setConfirmId(null)}>
            Отмена
          </Button>
          <Button loading={busy} onClick={() => void submitConfirm()}>
            Подтвердить оплату
          </Button>
        </div>
      </Modal>

      <Modal
        open={cancelId !== null}
        onClose={() => setCancelId(null)}
        title="Отмена заказа"
      >
        <p className="mb-3 text-sm text-ink-muted">
          Введите текущий пароль администратора, чтобы отменить заказ.
        </p>
        <PasswordInput
          label="Текущий пароль"
          value={password}
          onChange={setPassword}
        />
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="subtle" onClick={() => setCancelId(null)}>
            Назад
          </Button>
          <Button loading={busy} color="red" onClick={() => void submitCancel()}>
            Отменить заказ
          </Button>
        </div>
      </Modal>
    </div>
  );
}
