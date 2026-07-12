// Shared schedule picker: a friendly preset chooser that reads and writes a plain
// 5-field cron string (the dialect internal/cron parses, evaluated in the operator
// timezone). Used by both backup schedules — the Telegram one and the local one —
// so the two can't drift apart in what they accept or how they render it.
import type { ReactNode } from "react";
import { Select, TextInput } from "./ui";

// Presets map a friendly choice to a cron expression. "daily"/"weekly" build their
// cron from the time/weekday inputs; "custom" takes a raw expression.
export const PRESETS = [
  { value: "off", label: "Выключено" },
  { value: "hourly", label: "Каждый час" },
  { value: "every6", label: "Каждые 6 часов" },
  { value: "every12", label: "Каждые 12 часов" },
  { value: "daily", label: "Ежедневно в…" },
  { value: "weekly", label: "Еженедельно в…" },
  { value: "custom", label: "Своё (cron)" },
] as const;

export const WEEKDAYS = [
  { value: "1", label: "Понедельник" },
  { value: "2", label: "Вторник" },
  { value: "3", label: "Среда" },
  { value: "4", label: "Четверг" },
  { value: "5", label: "Пятница" },
  { value: "6", label: "Суббота" },
  { value: "0", label: "Воскресенье" },
];

export type Preset = (typeof PRESETS)[number]["value"];

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
  offLabel = "Расписание выключено.",
  extra,
}: {
  value: Schedule;
  onChange: (s: Schedule) => void;
  offLabel?: string;
  // A field that belongs on the same row as the schedule — "сколько копий хранить"
  // for the local backups. It rides inside the picker's row rather than sitting in a
  // block underneath, so the whole schedule reads as one line on a desktop.
  extra?: ReactNode;
}) {
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
            label="Расписание"
            value={value.preset}
            onChange={(v) => set({ preset: v as Preset })}
            data={PRESETS as unknown as { value: string; label: string }[]}
          />
        </div>
        {value.preset === "weekly" && (
          <div className="min-w-45 flex-1">
            <Select
              label="День недели"
              value={value.weekday}
              onChange={(v) => set({ weekday: v })}
              data={WEEKDAYS}
            />
          </div>
        )}
        {timed && (
          <div className="min-w-45 flex-1">
            <TextInput
              label="Время"
              type="time"
              value={value.time}
              onChange={(v) => set({ time: v })}
            />
          </div>
        )}
        {value.preset === "custom" && (
          <div className="min-w-45 flex-1">
            <TextInput
              label="Cron-выражение"
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
          offLabel
        )}
      </p>
    </div>
  );
}
