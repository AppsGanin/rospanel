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
import { useShowMore } from "./hooks";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { useStepUpDialog } from "./stepup";
import {
  Badge,
  Button,
  cn,
  EmptyState,
  KpiTile,
  MICRO,
  Mono,
  Panel,
  ShowMore,
  Skeleton,
  Skeletons,
  useWideBox,
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

// The history's columns, one template for the header and every row. Narrow, the row
// folds onto two lines rather than shrinking six columns of prose to a word each.
// Status and time are their own columns: sharing one cell put the badge under the
// "когда" heading and the time under nothing at all.
const TPL =
  "minmax(0,.8fr) minmax(0,1.1fr) minmax(0,1.4fr) minmax(0,.8fr) minmax(0,1fr) minmax(0,.8fr) minmax(0,1fr)";
const TPL_NARROW = "minmax(0,1fr) auto";
const WIDE_MIN = 620;

// orderWho is the account an order belongs to, by name when the row still has one.
function orderWho(o: PaymentOrder): string {
  return o.user_name ?? `user ${o.user_id}`;
}


export function PaymentsPage() {
  const { t } = useTranslation();
  const [stats, setStats] = useState<PaymentStats | null>(null);
  const [orders, setOrders] = useState<PaymentOrder[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [boxRef, wide] = useWideBox(WIDE_MIN);

  const { ask, stepUpNode } = useStepUpDialog();

  const refresh = () =>
    Promise.all([getPaymentStats(), listPaymentOrders()])
      .then(([s, o]) => {
        setStats(s);
        setOrders(o);
      })
      .catch((e) => notifyError(errMessage(e)))
      .finally(() => setLoading(false));

  // biome-ignore lint/correctness/useExhaustiveDependencies: runs once on mount; the loader is redefined every render, so listing it would refetch in a loop
  useEffect(() => {
    refresh();
  }, []);

  const creditOrder = async (o: PaymentOrder) => {
    const creds = await ask({
      title: t("pay.confirmTitle"),
      body: `${orderWho(o)} · ${o.plan_name ?? ""} · ${fmtRub(o.amount_rub)}`,
      confirmLabel: t("pay.confirmPayment"),
    });
    if (!creds) return;
    setBusy(true);
    try {
      await confirmPaymentOrder(o.id, creds.password);
      notifySuccess(t("pay.confirmed"));
      await refresh();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const cancelOrder = async (o: PaymentOrder) => {
    const creds = await ask({
      title: t("pay.cancelTitle"),
      body: `${orderWho(o)} · ${o.plan_name ?? ""} · ${fmtRub(o.amount_rub)}`,
      confirmLabel: t("pay.cancelOrder"),
      danger: true,
    });
    if (!creds) return;
    setBusy(true);
    try {
      await cancelPaymentOrder(o.id, creds.password);
      notifySuccess(t("pay.orderCancelled"));
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
  const pendingPage = useShowMore(pending, { first: 8, step: 20 });
  const historyPage = useShowMore(orders);

  if (loading)
    return (
      <div className="flex flex-col gap-3.5">
        <div className="grid gap-3.5 sm:grid-cols-2 lg:grid-cols-4">
          <Skeletons n={4} className="h-[86px] rounded-xl" />
        </div>
        <Skeleton className="h-64 rounded-xl" />
      </div>
    );
  if (!stats) return null;

  const avg = stats.paid_count > 0 ? Math.round(stats.total_paid / stats.paid_count) : 0;

  return (
    <div className="flex flex-col gap-3.5">
      {/* The headline figures. Every one of them is a number the server keeps: the
          all-time take, this month's, today's, and what is still owed. */}
      <div className="grid gap-3.5 sm:grid-cols-2 lg:grid-cols-4">
        <KpiTile
          label={t("pay.totalEarned")}
          value={fmtRub(stats.total_paid)}
          note={
            stats.paid_count > 0
              ? `${t("pay.nPayments", { count: stats.paid_count })} · ${t("pay.avgCheck", { sum: fmtRub(avg) })}`
              : undefined
          }
        />
        <KpiTile label={t("pay.thisMonth")} value={fmtRub(stats.earned_month)} />
        <KpiTile label={t("pay.today")} value={fmtRub(stats.earned_today)} />
        <KpiTile
          label={t("pay.awaiting")}
          value={String(stats.pending_count)}
          tone={stats.pending_count > 0 ? "warning" : "default"}
          note={stats.pending_sum ? t("pay.forSum", { sum: fmtRub(stats.pending_sum) }) : undefined}
        />
      </div>

      <div className="grid gap-3.5 lg:grid-cols-2">
        <Panel title={t("pay.byProvider")}>
          {stats.by_provider.length === 0 ? (
            <EmptyState title={t("pay.noPayments")} />
          ) : (
            stats.by_provider.map((p) => (
              <div
                key={p.provider || "manual"}
                className="flex items-center justify-between gap-3 border-t border-gray-100 px-3.5 py-[7px]"
              >
                <span className="truncate text-xs text-ink">
                  {providerMeta(p.provider).label}
                </span>
                <span className="flex shrink-0 items-center gap-3">
                  <span className="text-[11px] text-ink-muted">
                    {t("pay.nPayments", { count: p.count })}
                  </span>
                  <Mono className="text-xs text-ink">{fmtRub(p.sum)}</Mono>
                </span>
              </div>
            ))
          )}
        </Panel>

        {/* The queue an operator actually works: a manual order sits here until it is
            credited by hand, a provider's until the money lands. */}
        <Panel title={t("pay.awaiting")}>
          {pending.length === 0 ? (
            <EmptyState title={t("pay.noPending")} />
          ) : (
            <>
              {pendingPage.shown.map((o) => (
                <div
                  key={o.id}
                  className="flex flex-wrap items-center gap-x-2.5 gap-y-1 border-t border-gray-100 px-3.5 py-2"
                >
                  <span className="size-2 shrink-0 rounded-full bg-warning" />
                  <span className="min-w-0 flex-1 truncate text-[13px] font-medium text-ink">
                    {orderWho(o)}
                  </span>
                  <span className="truncate text-xs text-ink-muted">{o.plan_name}</span>
                  <Mono className="shrink-0 text-xs text-ink">{fmtRub(o.amount_rub)}</Mono>
                  <span className="flex shrink-0 gap-2">
                    <Button size="xs" disabled={busy} onClick={() => creditOrder(o)}>
                      {t("pay.credit")}
                    </Button>
                    <Button
                      size="xs"
                      variant="outline"
                      color="red"
                      disabled={busy}
                      onClick={() => cancelOrder(o)}
                    >
                      {t("common.cancel")}
                    </Button>
                  </span>
                </div>
              ))}
              <ShowMore rest={pendingPage.rest} onClick={pendingPage.showMore} className="p-3.5" />
            </>
          )}
        </Panel>
      </div>

      <Panel title={t("pay.history")}>
        {orders.length === 0 ? (
          <EmptyState title={t("pay.historyEmpty")} />
        ) : (
          <div ref={boxRef}>
            {wide && (
              <div
                className={cn(MICRO, "grid items-center gap-3 border-t border-brand-600/10 px-3.5 py-2")}
                style={{ gridTemplateColumns: TPL }}
              >
                <span className="truncate">{t("pay.colOrder")}</span>
                <span className="truncate">{t("pay.colUser")}</span>
                <span className="truncate">{t("pay.colPlan")}</span>
                <span className="truncate">{t("pay.colAmount")}</span>
                <span className="truncate">{t("pay.colMethod")}</span>
                <span className="truncate">{t("pay.colStatus")}</span>
                <span className="truncate text-right">{t("pay.colWhen")}</span>
              </div>
            )}
            {historyPage.shown.map((o) => {
              const st = statusMeta(o.status);
              const paid = o.status === "paid";
              const when = fmtDateTime(paid ? o.paid_at : o.created_at);
              return (
                <div
                  key={o.id}
                  className={cn(
                    "grid items-center gap-x-3 gap-y-0.5 border-t border-gray-100 px-3.5 py-[7px]",
                    o.status === "cancelled" && "danger-tint",
                    o.status === "pending" && "warning-tint",
                  )}
                  style={{ gridTemplateColumns: wide ? TPL : TPL_NARROW }}
                >
                  <Mono className="truncate text-xs text-ink">#{o.id}</Mono>
                  {wide ? (
                    <>
                      <span className="truncate text-xs text-ink">{orderWho(o)}</span>
                      <span className="truncate text-xs text-ink-muted" title={o.plan_name}>
                        {o.plan_name}
                      </span>
                      <Mono className="text-xs text-ink">{fmtRub(o.amount_rub)}</Mono>
                      <span className="truncate text-xs text-ink-muted">
                        {providerMeta(o.provider).label}
                      </span>
                      <span className="min-w-0">
                        <Badge color={st.color} size="xs">
                          {st.label}
                        </Badge>
                      </span>
                      <Mono
                        className="truncate text-right text-[11px] text-ink-muted"
                        title={t(paid ? "pay.paidWord" : "pay.createdWord")}
                      >
                        {when}
                      </Mono>
                    </>
                  ) : (
                    <>
                      <Mono className="text-right text-[11px] text-ink-muted">{when}</Mono>
                      <span className="col-span-2 flex min-w-0 items-center gap-2">
                        <span className="min-w-0 flex-1 truncate text-[11px] text-ink-muted">
                          {orderWho(o)} · {o.plan_name} · {providerMeta(o.provider).label}
                        </span>
                        <Mono className="shrink-0 text-xs text-ink">
                          {fmtRub(o.amount_rub)}
                        </Mono>
                        <Badge color={st.color} size="xs">
                          {st.label}
                        </Badge>
                      </span>
                    </>
                  )}
                </div>
              );
            })}
            <ShowMore rest={historyPage.rest} onClick={historyPage.showMore} className="p-3.5" />
          </div>
        )}
      </Panel>

      {stepUpNode}
    </div>
  );
}
