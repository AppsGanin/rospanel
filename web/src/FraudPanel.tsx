import { useState } from "react";
import { useTranslation } from "react-i18next";
import { td } from "./i18n";
import { getFraudSignals, type FraudSignal } from "./api";
import { errMessage, notifyError } from "./notify";
import { Button, EmptyState, MICRO, Mono, Panel, cn } from "./ui";

// The order the kinds are shown in: account abuse first, then money.
const KINDS = [
  "trial_farm",
  "shared_device",
  "self_referral",
  "promo_burst",
  "failed_payments",
  "payment_burst",
  "refunds",
];

// FraudPanel lists patterns worth a look. It decides nothing: every row names the
// accounts and what they share, and opens their cards.
export function FraudPanel({ onOpenUser }: { onOpenUser?: (id: number) => void }) {
  const { t } = useTranslation();
  // Loaded on demand: the scans behind it are the heaviest reads on the page.
  const [sigs, setSigs] = useState<FraudSignal[] | null>(null);
  const [busy, setBusy] = useState(false);
  const load = () => {
    setBusy(true);
    getFraudSignals()
      .then(setSigs)
      .catch((e) => notifyError(errMessage(e)))
      .finally(() => setBusy(false));
  };

  return (
    <Panel
      title={t("fraud.title")}
      aside={
        <Button size="xs" variant="light" loading={busy} onClick={load}>
          {t(sigs ? "common.refresh" : "fraud.check")}
        </Button>
      }
    >
      <p className="border-t border-gray-100 px-3.5 py-2 text-[11px] text-ink-muted">{t("fraud.hint")}</p>
      {!sigs ? null : sigs.length === 0 ? (
        <EmptyState title={t("fraud.none")} />
      ) : (
        KINDS.filter((k) => sigs.some((s) => s.kind === k)).map((kind) => (
          <div key={kind}>
            <div className={cn(MICRO, "border-t border-gray-100 px-3.5 pt-2.5 pb-1")}>
              {td(`fraud.kind.${kind}`)}
            </div>
            <p className="px-3.5 pb-1.5 text-[11px] text-ink-muted">{td(`fraud.desc.${kind}`)}</p>
            {sigs
              .filter((s) => s.kind === kind)
              .map((s) => (
                <div
                  key={`${s.kind}:${s.key}:${s.at}`}
                  className="flex flex-wrap items-center gap-x-2.5 gap-y-1 border-t border-gray-100 px-3.5 py-[7px]"
                >
                  {!["failed_payments", "payment_burst", "refunds"].includes(kind) && (
                    <Mono className="max-w-[14rem] truncate text-xs text-ink" title={s.key}>
                      {s.key}
                    </Mono>
                  )}
                  <span className="shrink-0 text-[11px] text-ink-muted">×{s.count}</span>
                  <span className="flex min-w-0 flex-1 flex-wrap gap-x-2 gap-y-0.5">
                    {s.users.map((u) =>
                      onOpenUser ? (
                        <button
                          type="button"
                          key={u.id}
                          className="truncate text-xs font-medium text-brand-600 hover:underline"
                          onClick={() => onOpenUser(u.id)}
                        >
                          {u.name || `#${u.id}`}
                        </button>
                      ) : (
                        <span key={u.id} className="truncate text-xs text-ink">
                          {u.name || `#${u.id}`}
                        </span>
                      ),
                    )}
                  </span>
                </div>
              ))}
          </div>
        ))
      )}
    </Panel>
  );
}
