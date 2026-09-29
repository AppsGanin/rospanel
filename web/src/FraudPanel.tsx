import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { td } from "./i18n";
import { getFraudSignals, type FraudSignal } from "./api";
import { EmptyState, MICRO, Mono, Panel, cn } from "./ui";

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
  const [sigs, setSigs] = useState<FraudSignal[] | null>(null);
  useEffect(() => {
    getFraudSignals()
      .then(setSigs)
      .catch(() => setSigs(null));
  }, []);
  if (!sigs) return null;

  return (
    <Panel title={t("fraud.title")}>
      <p className="border-t border-gray-100 px-3.5 py-2 text-[11px] text-ink-muted">{t("fraud.hint")}</p>
      {sigs.length === 0 ? (
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
