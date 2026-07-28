// Shared schedule picker: a friendly preset chooser that reads and writes a plain
// 5-field cron string (the dialect internal/cron parses, evaluated in the operator
// timezone). Used by both backup schedules — the Telegram one and the local one —
// so the two can't drift apart in what they accept or how they render it.
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import i18n, { currentLang } from "./i18n";
import { Select, TextInput } from "./ui";

// weekdayName renders a cron weekday number (0 = Sunday) via Intl, so a new
// language needs no calendar strings of its own.
function weekdayName(d: number): string {
  // 2021-08-01 was a Sunday, so +d lands on the wanted weekday.
  const s = new Intl.DateTimeFormat(currentLang(), { weekday: "long" }).format(
    new Date(2021, 7, 1 + d),
  );
  return s.charAt(0).toUpperCase() + s.slice(1);
}

// Presets map a friendly choice to a cron expression. "daily"/"weekly" build their
// cron from the time/weekday inputs; "custom" takes a raw expression.
export const presets = () => [
  { value: "off", label: i18n.t("cron.off") },
  { value: "hourly", label: i18n.t("cron.hourly") },
  { value: "every6", label: i18n.t("cron.every6") },
  { value: "every12", label: i18n.t("cron.every12") },
  { value: "daily", label: i18n.t("cron.daily") },
  { value: "weekly", label: i18n.t("cron.weekly") },
  { value: "custom", label: i18n.t("cron.custom") },
];

export const weekdays = () => [
  // Weekday names come from Intl (cron numbers days 0=Sunday), so a new language
  // needs no calendar strings.
  ...[1, 2, 3, 4, 5, 6, 0].map((d) => ({
    value: String(d),
    label: weekdayName(d),
  })),
];

export type Preset =
  | "off"
  | "hourly"
  | "every6"
  | "every12"
  | "daily"
  | "weekly"
  | "custom";

export type Schedule = {
  preset: Preset;
  time: string;
  weekday: string;
  custom: string;
};

export const EMPTY_SCHEDULE: Schedule = {
  preset: "off",
  time: "03:00",
  weekday: "1",
  custom: "",
};

const hhmm = (h: string, m: string) =>
  `${h.padStart(2, "0")}:${m.padStart(2, "0")}`;

// detectPreset reverse-maps a stored cron back into the UI controls. Anything it
// doesn't recognise round-trips through the "custom" field rather than being lost.
export function detectPreset(cron: string): Schedule {
  const c = cron.trim();
  if (c === "") return { ...EMPTY_SCHEDULE };
  if (c === "0 * * * *") return { ...EMPTY_SCHEDULE, preset: "hourly" };
  if (c === "0 */6 * * *") return { ...EMPTY_SCHEDULE, preset: "every6" };
  if (c === "0 */12 * * *") return { ...EMPTY_SCHEDULE, preset: "every12" };
  const daily = c.match(/^(\d{1,2}) (\d{1,2}) \* \* \*$/);
  if (daily)
    return { ...EMPTY_SCHEDULE, preset: "daily", time: hhmm(daily[2], daily[1]) };
  const weekly = c.match(/^(\d{1,2}) (\d{1,2}) \* \* ([0-6])$/);
  if (weekly)
    return {
      ...EMPTY_SCHEDULE,
      preset: "weekly",
      time: hhmm(weekly[2], weekly[1]),
      weekday: weekly[3],
    };
  return { ...EMPTY_SCHEDULE, preset: "custom", custom: c };
}

// buildCron assembles the cron string from the current controls ("" = disabled).
export function buildCron(s: Schedule): string {
  const [h, m] = (s.time || "03:00").split(":").map((x) => parseInt(x, 10) || 0);
  switch (s.preset) {
    case "off":
      return "";
    case "hourly":
      return "0 * * * *";
    case "every6":
      return "0 */6 * * *";
    case "every12":
      return "0 */12 * * *";
    case "daily":
      return `${m} ${h} * * *`;
    case "weekly":
      return `${m} ${h} * * ${s.weekday}`;
    case "custom":
      return s.custom.trim();
  }
}

export function CronPicker({
  value,
  onChange,
  offLabel,
  extra,
}: {
  value: Schedule;
  onChange: (s: Schedule) => void;
  offLabel?: string;
  // A field that belongs on the same row as the schedule — "how many copies to keep"
  // for the local backups. It rides inside the picker's row rather than sitting in a
  // block underneath, so the whole schedule reads as one line on a desktop.
  extra?: ReactNode;
}) {
  const { t } = useTranslation();
  const set = (patch: Partial<Schedule>) => onChange({ ...value, ...patch });
  const cron = buildCron(value);
  const timed = value.preset === "daily" || value.preset === "weekly";

  return (
    <div className="flex flex-col gap-3">
      {/* Wrapping flex, not a fixed grid: the field count changes with the preset
          (weekly adds a weekday, the local backups add a retention box), and a
          column count picked for one of those looks wrong for the others. Each field
          keeps a floor width and shares what's left, so they sit on one row on a
          desktop and stack on a phone. */}
      <div className="flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-start">
        <div className="min-w-45 flex-1">
          <Select
            label={t("cron.schedule")}
            value={value.preset}
            onChange={(v) => set({ preset: v as Preset })}
            data={presets()}
          />
        </div>
        {value.preset === "weekly" && (
          <div className="min-w-45 flex-1">
            <Select
              label={t("cron.weekday")}
              value={value.weekday}
              onChange={(v) => set({ weekday: v })}
              data={weekdays()}
            />
          </div>
        )}
        {timed && (
          <div className="min-w-45 flex-1">
            <TextInput
              label={t("cron.time")}
              type="time"
              value={value.time}
              onChange={(v) => set({ time: v })}
            />
          </div>
        )}
        {value.preset === "custom" && (
          <div className="min-w-45 flex-1">
            <TextInput
              label={t("cron.expression")}
              value={value.custom}
              onChange={(v) => set({ custom: v })}
              mono
              placeholder="0 3 * * *"
            />
          </div>
        )}
        {extra && <div className="min-w-45 flex-1">{extra}</div>}
      </div>
      <p className="text-xs text-ink-muted">
        {cron ? (
          <>
            Cron: <span className="font-mono">{cron}</span>
          </>
        ) : (
          (offLabel ?? t("cron.offLabel"))
        )}
      </p>
    </div>
  );
}
