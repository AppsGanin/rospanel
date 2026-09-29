import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { currentLang, td } from "./i18n";
import {
  cancelPaymentOrder,
  confirmPaymentOrder,
  refundOrder,
  getPaymentStats,
  getPaymentFunnel,
  getReferralStats,
  getUser,
  listPaymentOrders,
  type PaymentFunnel,
  type PaymentOrder,
  type PaymentStats,
  type ReferralStats,
  type User,
} from "./api";
import { UserDetail } from "./UserDetail";
import { PaymentCallbacks } from "./PaymentCallbacks";
import { FraudPanel } from "./FraudPanel";
import { useShowMore } from "./hooks";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { EMPTY_STEP_UP, StepUpFields, stepUpReady, useStepUpDialog, type StepUp } from "./stepup";
import { inPanelTz } from "./tz";
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
  Modal,
  Checkbox,
  SegmentedControl,
} from "./ui";
import { useCan } from "./role";
import { fmtKop } from "./events";

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
  balance: { label: "balance", color: "teal" },
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
  return new Date(unix * 1000).toLocaleString(
    currentLang(),
    inPanelTz({
      day: "2-digit",
      month: "2-digit",
      year: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    }),
  );
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

// orderWhat names what an order buys — the plan, or a balance top-up — with the part
// the balance paid and the promo code, when there are any.
function orderWhat(o: PaymentOrder): string {
  let what = o.plan_name ?? "";
  if (o.kind === "topup") what = td("pay.topup");
  else if ((o.periods ?? 1) > 1) what = `${what} × ${o.periods}`;
  const parts = [what];
  if (o.balance_kop && o.provider !== "balance")
    parts.push(td("pay.plusBalance", { sum: fmtKop(o.balance_kop) }));
  if (o.promo_code) parts.push(o.promo_code);
  if (o.refunded_at) parts.push(td(o.refund_source === "provider" ? "pay.refundedProvider" : "pay.refunded"));
  return parts.filter(Boolean).join(" · ");
}

// refundable: a paid plan order whose money has not gone back yet.
function refundable(o: PaymentOrder): boolean {
  return o.status === "paid" && (o.kind ?? "plan") === "plan" && !o.refunded_at;
}

// orderAmount is what the row shows as the sum: the money, or for a purchase from
// the balance, what the balance paid.
function orderAmount(o: PaymentOrder): string {
  if (o.provider === "balance") return `${fmtKop(o.balance_kop ?? 0)} ₽`;
  return fmtRub(o.amount_rub);
}

// orderWho is the account an order belongs to, by name when the row still has one.
function orderWho(o: PaymentOrder): string {
  return o.user_name ?? `user ${o.user_id}`;
}


// onPending reports the number of orders still waiting for an admin after every
// read: the tab above this page carries that count, and a confirmed or cancelled
// order must change it at once rather than at the next poll.
export function PaymentsPage({
  onPending,
  userBotEnabled = false,
}: {
  onPending?: (n: number) => void;
  userBotEnabled?: boolean;
}) {
  const { t } = useTranslation();
  // A referrer's name opens their card — for whoever may read users.
  const canUsers = useCan("users.view");
  const [detail, setDetail] = useState<User | null>(null);
  const openUser = (id: number) => {
    getUser(id)
      .then(setDetail)
      .catch((e) => notifyError(errMessage(e)));
  };
  // The funnel follows the users who joined in the last funnelDays (0 = all time).
  const [funnelDays, setFunnelDays] = useState(30);
  const [funnel, setFunnel] = useState<PaymentFunnel | null>(null);
  useEffect(() => {
    getPaymentFunnel(funnelDays)
      .then(setFunnel)
      .catch(() => setFunnel(null));
  }, [funnelDays]);
  // Crediting or cancelling an order needs billing.manage; the list is billing.view.
  const canManage = useCan("billing.manage");
  const [stats, setStats] = useState<PaymentStats | null>(null);
  const [orders, setOrders] = useState<PaymentOrder[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [boxRef, wide] = useWideBox(WIDE_MIN);
  const [refStats, setRefStats] = useState<ReferralStats | null>(null);
  const canRefund = canManage && !!stats?.wallet;

  const { ask, stepUpNode } = useStepUpDialog();
  // The order whose money is being returned to the balance.
  const [refund, setRefund] = useState<PaymentOrder | null>(null);
  const [refundCancel, setRefundCancel] = useState(false);
  const [refundCreds, setRefundCreds] = useState<StepUp>(EMPTY_STEP_UP);
  const openRefund = (o: PaymentOrder | null) => {
    setRefund(o);
    setRefundCancel(false);
    setRefundCreds(EMPTY_STEP_UP);
  };
  const doRefund = async () => {
    if (!refund) return;
    setBusy(true);
    try {
      const r = await refundOrder(refund.id, refundCancel, refundCreds.password);
      notifySuccess(t("pay.refundedTo", { sum: fmtKop(r.refund_kop) }));
      openRefund(null);
      await refresh();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };


  const refresh = () =>
    Promise.all([getPaymentStats(), listPaymentOrders(), getReferralStats().catch(() => null)])
      .then(([s, o, r]) => {
        setStats(s);
        setOrders(o);
        setRefStats(r);
        onPending?.(s.pending_count ?? 0);
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
      body: `${orderWho(o)} · ${orderWhat(o)} · ${orderAmount(o)}`,
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
      body: `${orderWho(o)} · ${orderWhat(o)} · ${orderAmount(o)}`,
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
                  <span className="truncate text-xs text-ink-muted">{orderWhat(o)}</span>
                  <Mono className="shrink-0 text-xs text-ink">{orderAmount(o)}</Mono>
                  {canManage && (
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
                  )}
                </div>
              ))}
              <ShowMore rest={pendingPage.rest} onClick={pendingPage.showMore} className="p-3.5" />
            </>
          )}
        </Panel>
      </div>

      <div className="grid gap-3.5 lg:grid-cols-2">
        {/* Of the users who joined in the period: how far they got. */}
        <Panel
          title={t("funnel.title")}
          aside={
            <SegmentedControl
              size="xs"
              nav
              value={String(funnelDays)}
              onChange={(v) => setFunnelDays(Number(v))}
              data={[
                { value: "30", label: t("funnel.days30") },
                { value: "90", label: t("funnel.days90") },
                { value: "0", label: t("funnel.all") },
              ]}
            />
          }
        >
          {funnel && (
            <>
              {(
                [
                  ["funnel.joined", funnel.funnel.joined, funnel.funnel.joined],
                  ["funnel.trial", funnel.funnel.trial, funnel.funnel.joined],
                  ["funnel.paid", funnel.funnel.paid, funnel.funnel.joined],
                  ["funnel.renewed", funnel.funnel.renewed, funnel.funnel.paid],
                ] as const
              ).map(([label, n, of]) => (
                <div key={label} className="border-t border-gray-100 px-3.5 py-[7px]">
                  <div className="flex items-center justify-between gap-3">
                    <span className="truncate text-xs text-ink">{t(label)}</span>
                    <span className="flex shrink-0 items-center gap-3">
                      {label !== "funnel.joined" && (
                        <span className="text-[11px] text-ink-muted">{of > 0 ? `${Math.round((n / of) * 100)}%` : "—"}</span>
                      )}
                      <Mono className="text-xs text-ink">{n.toLocaleString(currentLang())}</Mono>
                    </span>
                  </div>
                  <div className="mt-1 h-1 overflow-hidden rounded-full bg-gray-100">
                    <div
                      className="h-full rounded-full bg-brand-600"
                      style={{ width: `${funnel.funnel.joined > 0 ? (n / funnel.funnel.joined) * 100 : 0}%` }}
                    />
                  </div>
                </div>
              ))}
              <p className="border-t border-gray-100 px-3.5 py-2 text-[11px] text-ink-muted">{t("funnel.hint")}</p>
              {funnel.by_source.some((s) => s.source !== "") && (
                <div className="overflow-x-auto border-t border-gray-100">
                  <table className="w-full text-xs">
                    <thead>
                      <tr className={cn(MICRO, "text-left")}>
                        <th className="px-3.5 py-2 font-medium">{t("funnel.source")}</th>
                        <th className="px-2 py-2 text-right font-medium">{t("funnel.joined")}</th>
                        <th className="px-2 py-2 text-right font-medium">{t("funnel.paid")}</th>
                        <th className="px-2 py-2 text-right font-medium">{t("funnel.renewed")}</th>
                        <th className="px-3.5 py-2 text-right font-medium">{t("funnel.revenue")}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {funnel.by_source.map((s) => (
                        <tr key={s.source} className="border-t border-gray-100">
                          <td className="max-w-[10rem] truncate px-3.5 py-[6px] text-ink">
                            {s.source === "" ? t("funnel.noSource") : s.source === "~ref" ? t("funnel.byInvite") : <Mono>{s.source}</Mono>}
                          </td>
                          <td className="px-2 py-[6px] text-right"><Mono>{s.joined}</Mono></td>
                          <td className="px-2 py-[6px] text-right">
                            <Mono>{s.paid}</Mono>
                            <span className="ml-1 text-[11px] text-ink-muted">
                              {s.joined > 0 ? `${Math.round((s.paid / s.joined) * 100)}%` : ""}
                            </span>
                          </td>
                          <td className="px-2 py-[6px] text-right"><Mono>{s.renewed}</Mono></td>
                          <td className="px-3.5 py-[6px] text-right"><Mono>{fmtRub(s.revenue_rub)}</Mono></td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
              <p className="border-t border-gray-100 px-3.5 py-2 text-[11px] text-ink-muted">{t("funnel.sourceHint")}</p>
              {funnel.winback.sent > 0 && (
                <p className="border-t border-gray-100 px-3.5 py-2.5 text-xs text-ink-muted">
                  {t("funnel.winback", {
                    sent: funnel.winback.sent,
                    used: funnel.winback.used,
                    revenue: funnel.winback.revenue_rub.toLocaleString(currentLang()),
                  })}
                </p>
              )}
            </>
          )}
        </Panel>

        {/* What the referral programme brought: shown once someone came by a link. */}
        {refStats && refStats.invited > 0 && (
          <Panel title={t("ref.title")}>
            <p className="border-t border-gray-100 px-3.5 py-2.5 text-xs text-ink-muted">
              {t(refStats.paid_out_kop > 0 ? "ref.statsLine" : "ref.statsLineNoBonus", {
                invited: refStats.invited,
                paying: refStats.paying,
                revenue: refStats.revenue_rub.toLocaleString(currentLang()),
                paid: fmtKop(refStats.paid_out_kop),
              })}
            </p>
            {refStats.top.length > 0 && (
              <>
                <div className={cn(MICRO, "border-t border-gray-100 px-3.5 py-2")}>{t("ref.top")}</div>
                {refStats.top.map((r) => (
                  <div
                    key={r.user_id}
                    className="flex items-center justify-between gap-3 border-t border-gray-100 px-3.5 py-[7px]"
                  >
                    {canUsers ? (
                      <button
                        type="button"
                        className="truncate text-left text-xs font-medium text-brand-600 hover:underline"
                        onClick={() => openUser(r.user_id)}
                      >
                        {r.name}
                      </button>
                    ) : (
                      <span className="truncate text-xs text-ink">{r.name}</span>
                    )}
                    <Mono className="shrink-0 text-xs text-ink-muted">
                      {t("ref.topLine", { invited: r.invited, paying: r.paying, earned: fmtKop(r.earned_kop) })}
                    </Mono>
                  </div>
                ))}
              </>
            )}
          </Panel>
        )}
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
                      <span className="truncate text-xs text-ink-muted" title={orderWhat(o)}>
                        {orderWhat(o)}
                      </span>
                      <Mono className="text-xs text-ink">{orderAmount(o)}</Mono>
                      <span className="truncate text-xs text-ink-muted">
                        {providerMeta(o.provider).label}
                      </span>
                      <span className="flex min-w-0 items-center gap-1.5">
                        <Badge color={st.color} size="xs">
                          {st.label}
                        </Badge>
                        {canRefund && refundable(o) && (
                          <Button size="xs" variant="subtle" color="gray" onClick={() => openRefund(o)}>
                            {t("pay.refund")}
                          </Button>
                        )}
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
                          {orderWho(o)} · {orderWhat(o)} · {providerMeta(o.provider).label}
                        </span>
                        <Mono className="shrink-0 text-xs text-ink">
                          {orderAmount(o)}
                        </Mono>
                        <Badge color={st.color} size="xs">
                          {st.label}
                        </Badge>
                        {canRefund && refundable(o) && (
                          <Button size="xs" variant="subtle" color="gray" onClick={() => openRefund(o)}>
                            {t("pay.refund")}
                          </Button>
                        )}
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

      {canManage && <FraudPanel onOpenUser={canUsers ? openUser : undefined} />}
      {canManage && <PaymentCallbacks providerLabel={(p) => providerMeta(p).label} />}

      {stepUpNode}
      <UserDetail
        user={detail}
        userBotEnabled={userBotEnabled}
        onOpenUser={openUser}
        onChanged={() => {}}
        onClose={() => setDetail(null)}
      />
      <Modal
        open={!!refund}
        onClose={() => openRefund(null)}
        title={t("pay.refundTitle")}
        subtitle={refund ? `#${refund.id} · ${orderWho(refund)} · ${orderWhat(refund)} · ${orderAmount(refund)}` : undefined}
        footer={
          <div className="flex justify-end gap-2">
            <Button variant="light" color="gray" size="sm" onClick={() => openRefund(null)}>
              {t("common.cancel")}
            </Button>
            <Button size="sm" loading={busy} disabled={!stepUpReady(refundCreds, false)} onClick={doRefund}>
              {t("pay.refund")}
            </Button>
          </div>
        }
      >
        <div className="flex flex-col gap-3">
          <p className="text-sm text-ink-muted">{t("pay.refundHint")}</p>
          <Checkbox
            checked={refundCancel}
            onChange={setRefundCancel}
            label={t("pay.refundCancelPlan")}
            hint={t("pay.refundCancelPlanHint")}
          />
          <StepUpFields value={refundCreds} onChange={setRefundCreds} />
        </div>
      </Modal>
    </div>
  );
}
