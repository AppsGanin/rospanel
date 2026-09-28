import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  adjustUserBalance,
  getUserReferrals,
  getUserWallet,
  setUserAutoRenew,
  type BalanceTx,
  type Referral,
  type Wallet,
} from "./api";
import { fmtKop } from "./events";
import { useAction, useShowMore } from "./hooks";
import { currentLang, td } from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { useCan } from "./role";
import { EMPTY_STEP_UP, StepUpFields, stepUpReady, type StepUp } from "./stepup";
import { inPanelTz } from "./tz";
import {
  Button,
  Modal,
  MICRO,
  Mono,
  Panel,
  ReadOnly,
  SettingRow,
  ShowMore,
  Switch,
  TextInput,
  cn,
} from "./ui";

function fmtWhen(unix: number): string {
  return new Date(unix * 1000).toLocaleString(
    currentLang(),
    inPanelTz({ day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" }),
  );
}

// txWhat says where a ledger line's money came from or went.
function txWhat(tx: BalanceTx): string {
  const parts = [td(`wallet.tx.${tx.kind}`)];
  if (tx.order_id) parts.push(td("events.det.order", { id: tx.order_id }));
  if (tx.ref_name) parts.push(tx.ref_name);
  if (tx.promo_code) parts.push(tx.promo_code);
  if (tx.note) parts.push(tx.note);
  return parts.join(" · ");
}

// parseRub reads "150", "-20", "19,90" as kopecks; NaN when it is not a sum.
function parseRub(v: string): number {
  const n = Number(v.trim().replace(",", "."));
  return Number.isFinite(n) ? Math.round(n * 100) : Number.NaN;
}

// UserWallet is the user card's balance block: the balance and its ledger, the
// renewal switch, who invited the user and whom they invited. Shown to whoever may
// see billing; the balance is changed by billing.manage, with the password.
export function UserWallet({ userId, onOpenUser }: { userId: number; onOpenUser?: (id: number) => void }) {
  const { t } = useTranslation();
  const canManage = useCan("billing.manage");
  const [wallet, setWallet] = useState<Wallet | null>(null);
  const [history, setHistory] = useState<BalanceTx[]>([]);
  const [adjust, setAdjust] = useState(false);
  const [invited, setInvited] = useState<Referral[]>([]);
  const [amount, setAmount] = useState("");
  const [note, setNote] = useState("");
  const [creds, setCreds] = useState<StepUp>(EMPTY_STEP_UP);
  const { busy, run } = useAction();

  const reload = useCallback(() => {
    getUserWallet(userId)
      .then((d) => {
        setWallet(d.wallet);
        setHistory(d.history ?? []);
      })
      .catch(() => setWallet(null));
    getUserReferrals(userId)
      .then((l) => setInvited(l ?? []))
      .catch(() => setInvited([]));
  }, [userId]);
  useEffect(reload, [reload]);

  const shown = useShowMore(history, { first: 5, step: 20, resetKey: userId });
  if (!wallet) return null;

  const kop = parseRub(amount);
  const save = () =>
    run(async () => {
      await adjustUserBalance(userId, kop, note.trim(), creds.password);
      setAdjust(false);
      reload();
      notifySuccess(t("wallet.adjusted"));
    }).catch((e) => notifyError(errMessage(e)));

  const toggleRenew = (on: boolean) =>
    run(async () => {
      await setUserAutoRenew(userId, on);
      setWallet({ ...wallet, auto_renew: on });
    }).catch((e) => notifyError(errMessage(e)));


  return (
    <ReadOnly when={!canManage}>
      <Panel
        title={t("wallet.cardTitle")}
        aside={
          <span className="flex items-center gap-2">
            <Mono className="text-sm font-semibold text-ink">{fmtKop(wallet.balance_kop)} ₽</Mono>
            <Button
              size="xs"
              variant="light"
              onClick={() => {
                setAmount("");
                setNote("");
                setCreds(EMPTY_STEP_UP);
                setAdjust(true);
              }}
            >
              {t("wallet.adjust")}
            </Button>
          </span>
        }
      >
        <SettingRow
          label={t("wallet.autoRenew")}
          hint={t("wallet.autoRenewHint")}
          control={<Switch checked={wallet.auto_renew} onChange={toggleRenew} disabled={busy} />}
        />
        {(wallet.referrer_id > 0 || wallet.ref_bonus_days > 0 || wallet.promo_code) && (
          <SettingRow
            hint={
              <span className="flex flex-col gap-0.5">
                {wallet.referrer_id > 0 && (
                  <span>
                    {t("wallet.referrerLabel")}{" "}
                    <UserLink id={wallet.referrer_id} name={wallet.referrer_name} onOpen={onOpenUser} />
                  </span>
                )}
                {wallet.ref_bonus_days > 0 && (
                  <span>{t("wallet.bonusDays", { count: wallet.ref_bonus_days })}</span>
                )}
                {wallet.promo_code && (
                  <span>{t("wallet.promoWaiting", { code: wallet.promo_code })}</span>
                )}
              </span>
            }
          />
        )}
        {invited.length > 0 && (
          <div className="border-t border-gray-100 px-3.5 py-2.5">
            <span className={MICRO}>
              {t("wallet.invitedTitle", { count: invited.length })}
              {wallet.earned_kop > 0 && ` · ${t("wallet.earned", { sum: fmtKop(wallet.earned_kop) })}`}
            </span>
            <div className="mt-1.5 flex flex-col gap-1">
              {invited.map((r) => (
                <div key={r.user_id} className="flex items-center justify-between gap-3 text-xs">
                  <UserLink id={r.user_id} name={r.name} onOpen={onOpenUser} />
                  <span className="shrink-0 text-ink-muted">
                    {t("wallet.invitedLine", { paid: r.paid_rub.toLocaleString(currentLang()), earned: fmtKop(r.earned_kop) })}
                  </span>
                </div>
              ))}
            </div>
          </div>
        )}
        {shown.shown.map((tx) => (
          <div
            key={tx.id}
            className="grid items-center gap-3 border-t border-gray-100 px-3.5 py-[7px]"
            style={{ gridTemplateColumns: "minmax(0,1fr) auto auto" }}
          >
            <span className="min-w-0">
              <span className="block truncate text-xs text-ink" title={txWhat(tx)}>
                {txWhat(tx)}
              </span>
              <span className="text-[11px] text-ink-muted">{fmtWhen(tx.created_at)}</span>
            </span>
            <Mono
              className={cn(
                "text-xs font-medium",
                tx.amount_kop > 0 ? "text-success" : "text-ink",
              )}
            >
              {tx.amount_kop > 0 ? "+" : ""}
              {fmtKop(tx.amount_kop)} ₽
            </Mono>
            <Mono className="w-16 text-right text-[11px] text-ink-muted">
              {fmtKop(tx.balance_kop)} ₽
            </Mono>
          </div>
        ))}
        <ShowMore rest={shown.rest} onClick={shown.showMore} className="p-3.5" />
      </Panel>

      <Modal
        open={adjust}
        onClose={() => setAdjust(false)}
        title={t("wallet.adjustTitle")}
        footer={
          <div className="flex justify-end gap-2">
            <Button variant="light" color="gray" size="sm" onClick={() => setAdjust(false)}>
              {t("common.cancel")}
            </Button>
            <Button
              size="sm"
              loading={busy}
              disabled={!kop || Number.isNaN(kop) || !stepUpReady(creds, false)}
              onClick={save}
            >
              {t("common.save")}
            </Button>
          </div>
        }
      >
        <div className="flex flex-col gap-3">
          <TextInput
            label={t("wallet.amount")}
            placeholder={t("wallet.amountHint")}
            value={amount}
            onChange={setAmount}
          />
          <TextInput label={t("wallet.note")} value={note} onChange={setNote} />
          <StepUpFields value={creds} onChange={setCreds} />
        </div>
      </Modal>
    </ReadOnly>
  );
}

// UserLink names a user and opens their card, when the page can.
function UserLink({ id, name, onOpen }: { id: number; name?: string; onOpen?: (id: number) => void }) {
  const label = name || `#${id}`;
  if (!onOpen) return <span className="text-ink">{label}</span>;
  return (
    <button
      type="button"
      className="truncate text-left font-medium text-accent hover:underline"
      onClick={() => onOpen(id)}
    >
      {label}
    </button>
  );
}
