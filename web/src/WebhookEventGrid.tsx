import { useTranslation } from "react-i18next";
import type { WebhookEventDef } from "./api";
import { slugKey, td } from "./i18n";
import { Box } from "./RouteGrid";
import { cn } from "./ui";

// WebhookEventGrid is where a webhook is given its events: every one the panel sends,
// in groups, each with the key the receiver switches on. Every box ticked is stored as
// the empty set — all events, the ones a later release adds included — and the last
// box cannot be cleared, since an empty choice would mean that same "all".

// The group each event belongs to; a key not listed here (a newer server than this
// panel build) lands in "other" rather than nowhere.
const GROUPS: { key: string; events: string[] }[] = [
  {
    key: "account",
    events: [
      "user.created",
      "registration.requested",
      "user.registered",
      "registration.rejected",
      "user.deleted",
      "user.enabled",
      "user.disabled",
      "user.sub_rotated",
      "user.telegram_linked",
      "user.telegram_unlinked",
    ],
  },
  {
    key: "usage",
    events: [
      "user.limits_changed",
      "user.term_started",
      "user.expiring",
      "user.expired",
      "user.traffic_low",
      "user.limited",
      "user.traffic_reset",
      "user.device_bound",
      "user.device_unbound",
      "user.device_limited",
      "user.abuse",
    ],
  },
  { key: "plan", events: ["plan.changed", "plan.downgraded", "plan.cancelled"] },
  {
    key: "money",
    events: [
      "payment.created",
      "payment.paid",
      "payment.cancelled",
      "payment.refunded",
      "balance.adjusted",
    ],
  },
  { key: "referral", events: ["user.referred", "referral.reward", "promo.winback", "promo.redeemed"] },
  { key: "messages", events: ["user.message", "user.auto_message", "broadcast.sent", "user.mailing"] },
  { key: "servers", events: ["node.down", "node.up", "xray.down", "xray.up"] },
];

export function WebhookEventGrid({
  catalog,
  selected,
  onChange,
}: {
  catalog: WebhookEventDef[];
  selected: Set<string>;
  onChange: (next: Set<string>) => void;
}) {
  const { t } = useTranslation();
  const all = selected.size === 0;
  const keys = catalog.map((e) => e.key);
  const known = new Set(GROUPS.flatMap((g) => g.events));
  const sections = [
    ...GROUPS.map((g) => ({ key: g.key, items: g.events.filter((e) => keys.includes(e)) })),
    { key: "other", items: keys.filter((k) => !known.has(k)) },
  ].filter((s) => s.items.length > 0);

  const set = (items: string[], on: boolean) => {
    const next = new Set(all ? keys : selected);
    for (const k of items) {
      if (on) next.add(k);
      else next.delete(k);
    }
    if (next.size === 0) return;
    onChange(keys.every((k) => next.has(k)) ? new Set() : next);
  };

  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-xs font-medium text-ink">{t("hooks.events")}</span>
      <div className="max-h-[46vh] overflow-y-auto rounded-xl border border-gray-200">
        {sections.map((s, i) => {
          const on = s.items.filter((k) => all || selected.has(k)).length;
          return (
            <div key={s.key} className={cn(i > 0 && "border-t border-gray-100")}>
              <div className="flex items-center gap-2.5 bg-gray-50 px-3.5 py-2">
                <Box
                  checked={on === s.items.length}
                  partial={on > 0}
                  label={td(`hooks.group.${s.key}`)}
                  onChange={(v) => set(s.items, v)}
                />
                <span className="truncate text-xs font-medium text-ink">
                  {td(`hooks.group.${s.key}`)}
                </span>
                <span className="ml-auto shrink-0 font-mono text-[11px] text-ink-muted">
                  {on}/{s.items.length}
                </span>
              </div>
              <div className="grid sm:grid-cols-2">
                {s.items.map((k) => (
                  <div
                    key={k}
                    className="flex items-center gap-2.5 border-t border-gray-100 py-[6px] pr-3.5 pl-7"
                  >
                    <Box
                      checked={all || selected.has(k)}
                      label={td(`webhookEvent.${slugKey(k)}`)}
                      onChange={(v) => set([k], v)}
                    />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-xs text-ink">
                        {td(`webhookEvent.${slugKey(k)}`)}
                      </span>
                      <span className="block truncate font-mono text-[10.5px] text-ink-muted">
                        {k}
                      </span>
                    </span>
                  </div>
                ))}
                {/* An odd last row gets a partner, so its divider spans both columns. */}
                {s.items.length % 2 === 1 && (
                  <div aria-hidden className="hidden border-t border-gray-100 sm:block" />
                )}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
