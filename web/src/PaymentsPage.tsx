import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { currentLang, td } from "./i18n";
import {
  cancelPaymentOrder,
  confirmPaymentOrder,
  getPaymentStats,
  listPaymentOrders,
  type PaymentOrder,
  type PaymentStats,
} from "./api";
import { useShowMore, useViewMode } from "./hooks";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  CenterLoader,
  cn,
  Modal,
  PasswordInput,
  SettingCard,
  ShowMore,
  TableShell,
  TD,
  THead,
  TR,
  ViewSwitch,
} from "./ui";

const PROVIDER_META: Record<
  string,
  { label: string; color: "brand" | "teal" | "gray" }
> = {
  yookassa: { label: "yookassa", color: "brand" },
  cryptobot: { label: "cryptobot", color: "teal" },
  pal24: { label: "pal24", color: "brand" },
  riopay: { label: "riopay", color: "brand" },
  rollypay: { label: "rollypay", color: "brand" },
  severpay: { label: "severpay", color: "brand" },
  platega: { label: "platega", color: "brand" },
  paypear: { label: "paypear", color: "brand" },
  aurapay: { label: "aurapay", color: "brand" },
  heleket: { label: "heleket", color: "teal" },
  "": { label: "manual", color: "gray" },
};

const STATUS_META: Record<
  string,
  { label: string; color: "green" | "gray" | "orange" }
> = {
  paid: { label: "paid", color: "green" },
  cancelled: { label: "cancelled", color: "gray" },
  pending: { label: "pending", color: "orange" },
};

function fmtRub(n: number): string {
  return `${n.toLocaleString(currentLang())} ₽`;
}

function fmtDateTime(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString(currentLang(), {
    day: "2-digit",
    month: "2-digit",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

// The label is a dictionary key, resolved at call time so the badges follow the
// panel's language rather than whichever one was active at import.
function providerMeta(p: string) {
  const m = PROVIDER_META[p];
  return m
    ? { label: td(`pay.provider.${m.label}`), color: m.color }
    : { label: p, color: "gray" as const };
}

function statusMeta(status: string) {
  const m = STATUS_META[status];
  return m
    ? { label: td(`pay.status.${m.label}`), color: m.color }
    : { label: status, color: "gray" as const };
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



export function PaymentsPage() {
  const { t } = useTranslation();
  const [pendingView, setPendingView] = useViewMode("payments.pending");
  const [historyView, setHistoryView] = useViewMode("payments.history");
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
      notifySuccess(t("pay.confirmed"));
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
      notifySuccess(t("pay.orderCancelled"));
      setCancelId(null);
      setPassword("");
      await refresh();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  // Derived (and chunked) above the early returns: hooks may not sit behind them.
  // The server hands over the last 100 orders, which is a long scroll on a page
  // whose useful part — the pending queue — is at the top.
  const pending = orders.filter((o) => o.status === "pending");
  const history = orders.filter((o) => o.status !== "pending");
  const pendingPage = useShowMore(pending);
  const historyPage = useShowMore(history);

  if (loading) return <CenterLoader />;
  if (!stats) return null;

  return (
    <div className="flex flex-col gap-4">
      {/* Revenue headline */}
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatTile
          label={t("pay.totalEarned")}
          value={fmtRub(stats.total_paid)}
          sub={t("pay.nPayments", { count: stats.paid_count })}
          accent
        />
        <StatTile label={t("pay.thisMonth")} value={fmtRub(stats.earned_month)} />
        <StatTile label={t("pay.today")} value={fmtRub(stats.earned_today)} />
        <StatTile
          label={t("pay.awaiting")}
          value={String(stats.pending_count)}
          sub={stats.pending_sum ? t("pay.forSum", { sum: fmtRub(stats.pending_sum) }) : "—"}
        />
      </div>

      <SettingCard
        title={t("pay.byProvider")}
        description={t("pay.byProviderHint")}
      >
        {stats.by_provider.length === 0 ? (
          <p className="text-sm text-ink-muted">{t("pay.noPayments")}</p>
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
                      {t("pay.nPayments", { count: p.count })} · {share}%
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
        title={t("pay.awaiting")}
        action={
          <span className="flex items-center gap-2">
            {pending.length > 0 && <Badge color="orange">{pending.length}</Badge>}
            <ViewSwitch
              value={pendingView}
              onChange={setPendingView}
              tableLabel={t("usersPanel.viewTable")}
              cardsLabel={t("usersPanel.viewCards")}
            />
          </span>
        }
      >
        {pending.length === 0 ? (
          <p className="text-sm text-ink-muted">{t("pay.noPending")}</p>
        ) : pendingView === "table" ? (
          <PendingTable
            orders={pendingPage.shown}
            busy={busy}
            onConfirm={setConfirmId}
            onCancel={setCancelId}
          />
        ) : (
          <ul className="flex flex-col gap-2">
            {pendingPage.shown.map((o) => (
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
        <ShowMore
          rest={pendingPage.rest}
          onClick={pendingPage.showMore}
          className="mt-2"
        />
      </SettingCard>

      <SettingCard
        title={t("pay.history")}
        action={
          <ViewSwitch
            value={historyView}
            onChange={setHistoryView}
            tableLabel={t("usersPanel.viewTable")}
            cardsLabel={t("usersPanel.viewCards")}
          />
        }
      >
        {history.length === 0 ? (
          <p className="text-sm text-ink-muted">{t("pay.historyEmpty")}</p>
        ) : historyView === "table" ? (
          <HistoryTable orders={historyPage.shown} />
        ) : (
          <ul className="flex flex-col gap-2">
            {historyPage.shown.map((o) => (
              <HistoryRow key={o.id} order={o} />
            ))}
          </ul>
        )}
        <ShowMore
          rest={historyPage.rest}
          onClick={historyPage.showMore}
          className="mt-2"
        />
      </SettingCard>

      <Modal
        open={confirmId !== null}
        onClose={() => setConfirmId(null)}
        title={t("pay.confirmTitle")}
      >
        <p className="mb-3 text-sm text-ink-muted">
          {t("pay.confirmBody")}
        </p>
        <PasswordInput
          label={t("creds.currentPassword")}
          value={password}
          onChange={setPassword}
        />
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="subtle" onClick={() => setConfirmId(null)}>
            {t("common.cancel")}
          </Button>
          <Button loading={busy} onClick={() => void submitConfirm()}>
            {t("pay.confirmPayment")}
          </Button>
        </div>
      </Modal>

      <Modal
        open={cancelId !== null}
        onClose={() => setCancelId(null)}
        title={t("pay.cancelTitle")}
      >
        <p className="mb-3 text-sm text-ink-muted">
          {t("pay.cancelBody")}
        </p>
        <PasswordInput
          label={t("creds.currentPassword")}
          value={password}
          onChange={setPassword}
        />
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="subtle" onClick={() => setCancelId(null)}>
            {t("common.back")}
          </Button>
          <Button loading={busy} color="red" onClick={() => void submitCancel()}>
            {t("pay.cancelOrder")}
          </Button>
        </div>
      </Modal>
    </div>
  );
}

// orderWho is the account an order belongs to, by name when the row still has one.
function orderWho(o: PaymentOrder): string {
  return o.user_name ?? `user ${o.user_id}`;
}

// PendingTable lists orders awaiting payment. Every row carries the same six facts and
// the same two decisions, which is exactly what a table is for — the card version spent a
// line per order on punctuation.
function PendingTable({
  orders,
  busy,
  onConfirm,
  onCancel,
}: {
  orders: PaymentOrder[];
  busy: boolean;
  onConfirm: (id: number) => void;
  onCancel: (id: number) => void;
}) {
  const { t } = useTranslation();
  return (
    <TableShell>
      <THead
        cols={[
          { label: t("pay.colOrder") },
          { label: t("pay.colUser") },
          { label: t("pay.colPlan"), className: "hidden sm:table-cell" },
          { label: t("pay.colAmount") },
          { label: t("pay.colMethod"), className: "hidden md:table-cell" },
          { label: t("pay.colCreated"), className: "hidden lg:table-cell" },
          { srOnly: t("pay.colActions") },
        ]}
      />
      <tbody>
        {orders.map((o) => {
          const prov = providerMeta(o.provider);
          return (
            <TR key={o.id}>
              <TD className="whitespace-nowrap font-medium text-ink">#{o.id}</TD>
              <TD className="max-w-[12rem] truncate">{orderWho(o)}</TD>
              <TD className="hidden max-w-[12rem] truncate sm:table-cell">{o.plan_name}</TD>
              <TD className="whitespace-nowrap font-semibold text-ink">{o.amount_rub} ₽</TD>
              <TD className="hidden whitespace-nowrap md:table-cell">
                <Badge color={prov.color} size="xs">
                  {prov.label}
                </Badge>
                {/* An order made through a provider confirms itself when the money
                    lands; the buttons are for the ones that cannot. */}
                {o.provider !== "" && (
                  <span className="ml-1 text-xs text-ink-muted">{t("pay.autoConfirm")}</span>
                )}
              </TD>
              <TD className="hidden whitespace-nowrap text-ink-muted lg:table-cell">
                {fmtDateTime(o.created_at)}
              </TD>
              <TD>
                <div className="flex justify-end gap-2 whitespace-nowrap">
                  <Button size="sm" onClick={() => onConfirm(o.id)} disabled={busy}>
                    {t("common.confirm")}
                  </Button>
                  <Button
                    size="sm"
                    variant="subtle"
                    color="red"
                    onClick={() => onCancel(o.id)}
                    disabled={busy}
                  >
                    {t("common.cancel")}
                  </Button>
                </div>
              </TD>
            </TR>
          );
        })}
      </tbody>
    </TableShell>
  );
}

// HistoryTable is the settled orders, read-only.
function HistoryTable({ orders }: { orders: PaymentOrder[] }) {
  const { t } = useTranslation();
  return (
    <TableShell>
      <THead
        cols={[
          { label: t("pay.colOrder") },
          { label: t("pay.colUser") },
          { label: t("pay.colPlan"), className: "hidden sm:table-cell" },
          { label: t("pay.colAmount") },
          { label: t("pay.colMethod"), className: "hidden md:table-cell" },
          { label: t("pay.colStatus") },
          { label: t("pay.colWhen"), className: "hidden lg:table-cell" },
        ]}
      />
      <tbody>
        {orders.map((o) => {
          const prov = providerMeta(o.provider);
          const st = statusMeta(o.status);
          const paid = o.status === "paid";
          return (
            <TR key={o.id}>
              <TD className="whitespace-nowrap font-medium text-ink">#{o.id}</TD>
              <TD className="max-w-[12rem] truncate">{orderWho(o)}</TD>
              <TD className="hidden max-w-[12rem] truncate sm:table-cell">{o.plan_name}</TD>
              <TD className="whitespace-nowrap font-semibold text-ink">{o.amount_rub} ₽</TD>
              <TD className="hidden whitespace-nowrap md:table-cell">
                <Badge color={prov.color} size="xs">
                  {prov.label}
                </Badge>
              </TD>
              <TD className="whitespace-nowrap">
                <Badge color={st.color}>{st.label}</Badge>
              </TD>
              <TD className="hidden whitespace-nowrap text-ink-muted lg:table-cell">
                {/* A settled order is dated by when it settled; one that never did keeps
                    the date it was raised. */}
                {fmtDateTime(paid ? o.paid_at : o.created_at)}
              </TD>
            </TR>
          );
        })}
      </tbody>
    </TableShell>
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
  const { t } = useTranslation();
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
            {t("common.confirm")}
          </Button>
          <Button
            size="sm"
            variant="subtle"
            color="red"
            onClick={() => onCancel(order.id)}
            disabled={busy}
          >
            {t("common.cancel")}
          </Button>
        </span>
      </div>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-ink-muted">
        <Badge color={prov.color} size="xs">
          {prov.label}
        </Badge>
        <span>· {t("pay.createdAt", { when: fmtDateTime(order.created_at) })}</span>
        {auto && <span>· {t("pay.autoConfirm")}</span>}
      </div>
    </li>
  );
}

// HistoryRow is a read-only completed order.
function HistoryRow({ order }: { order: PaymentOrder }) {
  const { t } = useTranslation();
  const prov = providerMeta(order.provider);
  const st = statusMeta(order.status);
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
            · {t(order.status === "paid" ? "pay.paidWord" : "pay.createdWord")}{" "}
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
