import { useTranslation } from "react-i18next";
import { MAX_DEVICE_LIMIT, type Group, type TariffPlan } from "./api";
import {
  fmtBytes,
  fmtSpeed,
  gbToBytes,
  groupSpeedCap,
  quotaOptions,
  resetPeriods,
  speedLimitOptions,
} from "./format";
import i18n from "./i18n";
import { Checkbox, CustomizableSelect, Select, Switch, TextInput } from "./ui";

export const EMPTY_PLAN = (): TariffPlan => ({
  id: 0,
  slug: "",
  name: "",
  price_rub: 100, // a new plan is paid; free is a designation, not a price of 0
  period_days: 30,
  data_limit: 0,
  device_limit: 0,
  speed_limit: 0,
  reset_period: "",
  sort_order: 0,
  enabled: true,
  group_ids: [],
});


// devices() are the plan presets; the editor also takes any other number up to
// MAX_DEVICE_LIMIT (see CustomizableSelect).
const devices = () => [
  { value: "0", label: i18n.t("common.unlimited") },
  { value: "1", label: "1" },
  { value: "2", label: "2" },
  { value: "3", label: "3" },
  { value: "5", label: "5" },
  { value: "10", label: "10" },
];

const periods = () => [
  { value: "0", label: i18n.t("bill.unlimitedTerm") },
  ...[1, 3, 7, 14, 30, 90, 180, 365].map((d) => ({
    value: String(d),
    label: i18n.t("bc.days", { count: d }),
  })),
];

function gbFromBytes(b: number): string {
  if (!b) return "0";
  const gb = b / (1024 * 1024 * 1024);
  const hit = quotaOptions().find((o) => o.value === String(gb));
  return hit ? hit.value : String(gb);
}

function periodLabel(days: number): string {
  if (!days) return i18n.t("common.never");
  return i18n.t("bill.nDays", { count: days });
}

export function planSummary(p: TariffPlan): string {
  const parts: string[] = [];
  if (p.price_rub > 0) {
    parts.push(`${p.price_rub} ₽ / ${periodLabel(p.period_days)}`);
  } else {
    parts.push(`${i18n.t("bill.free")} · ${periodLabel(p.period_days)}`);
  }
  parts.push(p.data_limit ? fmtBytes(p.data_limit) : i18n.t("bill.infTraffic"));
  parts.push(
    p.device_limit
      ? i18n.t("bill.nDevices", { count: p.device_limit })
      : i18n.t("bill.infDevices"),
  );
  // Only when the plan promises one: "unlimited speed" is the norm and would just
  // make every summary longer.
  if (p.speed_limit > 0) parts.push(fmtSpeed(p.speed_limit));
  // Same rule: the derived cycle is the norm, only an explicit one is news.
  if (p.reset_period && p.data_limit) {
    parts.push(i18n.t("bill.resetSummary", { period: resetLabel(p.reset_period) }));
  }
  return parts.join(" · ");
}

// resetLabel renders a plan's explicit cycle with the same words the user card
// uses for the same value.
function resetLabel(period: string): string {
  const hit = resetPeriods().find((o) => o.value === period);
  return hit ? hit.label.toLowerCase() : period;
}

// planResetOptions are the cycles a plan may carry. The first entry is the derived
// default and reads differently for a free plan (refill every duration) and a paid
// one (the quota covers the whole period) — the server decides which, this only
// tells the operator what leaving it blank means.
const planResetOptions = (free: boolean) => [
  { value: "", label: i18n.t(free ? "bill.resetAutoFree" : "bill.resetAutoPaid") },
  ...resetPeriods().filter((o) => o.value !== "none"),
];

export function PlanForm({
  plan,
  onChange,
  isTrial,
  isFree,
  groups,
}: {
  plan: TariffPlan;
  onChange: (p: TariffPlan) => void;
  isTrial: boolean;
  isFree: boolean;
  groups: Group[];
}) {
  const { t } = useTranslation();
  const patch = (p: Partial<TariffPlan>) => onChange({ ...plan, ...p });
  const selected = new Set(plan.group_ids ?? []);
  // A granted group with a speed cap overrides the plan's own for its users.
  const planGroupCap = groupSpeedCap(groups.filter((g) => selected.has(g.id)));
  // A plan is free because it is designated free/trial in the pricing card — never
  // because someone typed 0 here. The server enforces both halves of that.
  const designated = isFree || isTrial;
  const periodVal = periods().some((o) => o.value === String(plan.period_days))
    ? String(plan.period_days)
    : String(plan.period_days || 0);

  return (
    <div className="flex flex-col gap-3">
      <TextInput
        label={t("groups.name")}
        value={plan.name}
        onChange={(v) => patch({ name: v })}
        placeholder={t("bill.namePlaceholder")}
      />
      <TextInput
        label={t("bill.slug")}
        value={plan.slug}
        onChange={(v) => patch({ slug: v.toLowerCase() })}
        placeholder={t("bill.slugPlaceholder")}
      />
      {/* Order, visibility and price are all about being offered for sale, which a
          designated free/trial plan never is — it is assigned automatically and is
          filtered out of every user-facing list server-side. Showing the fields just
          invited setting a price nobody charges or hiding a plan that is not shown
          anyway. */}
      {!designated && (
        <div className="grid gap-3 sm:grid-cols-2">
          <TextInput
            label={t("bill.order")}
            type="number"
            value={String(plan.sort_order)}
            onChange={(v) => patch({ sort_order: Math.max(0, Number(v) || 0) })}
          />
          <label className="flex items-end gap-2 pb-1 text-sm">
            <Switch checked={plan.enabled} onChange={(v) => patch({ enabled: v })} />
            {t("bill.activeVisible")}
          </label>
        </div>
      )}
      <div className={designated ? "grid gap-3" : "grid gap-3 sm:grid-cols-2"}>
        {!designated && (
          <TextInput
            label={t("bill.price")}
            type="number"
            value={String(plan.price_rub)}
            onChange={(v) => patch({ price_rub: Math.max(1, Number(v) || 1) })}
          />
        )}
        <Select
          label={t("bill.term")}
          data={periods()}
          value={periodVal}
          onChange={(v) => patch({ period_days: Number(v) })}
        />
      </div>
      <p className="text-xs text-ink-muted">
        {isTrial
          ? t("bill.trialHint")
          : isFree
            ? t("bill.freeHint")
            : t("bill.paidHint")}
      </p>
      <div className="grid gap-3 sm:grid-cols-2">
        <Select
          label={t("usersPanel.trafficLimit")}
          data={quotaOptions()}
          value={gbFromBytes(plan.data_limit)}
          onChange={(v) => patch({ data_limit: gbToBytes(Number(v)) })}
        />
        {/* The presets cover the plans people actually sell; "other" takes any
            number up to the panel's ceiling, the same as the user card. */}
        <CustomizableSelect
          label={t("userDetail.deviceLimit")}
          data={devices()}
          value={String(plan.device_limit)}
          max={MAX_DEVICE_LIMIT}
          format={(n) => t("bill.nDevices", { count: n })}
          onChange={(v) => patch({ device_limit: Number(v) })}
        />
        <Select
          label={t("userDetail.speedLimit")}
          data={speedLimitOptions()}
          value={String(plan.speed_limit)}
          onChange={(v) => patch({ speed_limit: Number(v) })}
        />
        {/* A cycle needs a quota to refill; without one the choice is meaningless
            and the server ignores it, so the control says so instead of pretending. */}
        <Select
          label={t("bill.resetPeriod")}
          data={planResetOptions(isFree)}
          value={plan.data_limit ? plan.reset_period : ""}
          disabled={!plan.data_limit}
          onChange={(v) => patch({ reset_period: v })}
        />
      </div>
      <p className="text-xs text-ink-muted">{t("bill.resetHint")}</p>
      {/* Access groups: the plan decides WHICH connections its users may reach, not
          only how much traffic. Ticking nothing keeps the plan silent about access —
          the historical behaviour, and what every existing plan has. */}
      <div className="flex flex-col gap-2 border-t border-gray-100 pt-3">
        <div className="flex items-center justify-between">
          <span className="text-sm font-medium text-ink">{t("bill.planGroups")}</span>
          {selected.size > 0 && (
            <span className="text-[11px] text-ink-muted">
              {t("groups.nSelected", { count: selected.size })}
            </span>
          )}
        </div>
        {groups.length === 0 ? (
          <p className="text-xs text-ink-muted">{t("bill.planGroupsNone")}</p>
        ) : (
          <div className="flex max-h-44 flex-col gap-1.5 overflow-y-auto rounded-lg border border-gray-200/80 bg-white/50 p-2">
            {groups.map((g) => (
              <Checkbox
                key={g.id}
                checked={selected.has(g.id)}
                onChange={(c) => {
                  const next = new Set(selected);
                  if (c) next.add(g.id);
                  else next.delete(g.id);
                  patch({ group_ids: [...next] });
                }}
                label={g.name}
                hint={
                  g.speed_limit > 0
                    ? `${t("groups.nConnections", { count: g.grants?.length ?? 0 })} · ${fmtSpeed(g.speed_limit)}`
                    : t("groups.nConnections", { count: g.grants?.length ?? 0 })
                }
              />
            ))}
          </div>
        )}
        <p className="text-xs text-ink-muted">{t("bill.planGroupsHint")}</p>
        {planGroupCap && (
          <p className="text-xs text-warning">
            {t("bill.groupSpeedInForce", {
              name: planGroupCap.name,
              speed: fmtSpeed(planGroupCap.kbps),
            })}
          </p>
        )}
      </div>
    </div>
  );
}
