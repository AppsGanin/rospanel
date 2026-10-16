import { ymdOf, zonedParts, zonedToMs } from "./tz";
import { DatePicker, TextInput } from "./ui";

// A moment as the editor holds it: a date and a wall-clock time in the panel's zone.
export type Moment = { date: string; time: string };

const pad = (n: number) => String(n).padStart(2, "0");

export function toMoment(unix: number): Moment {
  if (!unix) return { date: "", time: "" };
  const p = zonedParts(unix * 1000);
  return { date: ymdOf(unix * 1000), time: `${pad(p.h)}:${pad(p.mi)}` };
}

export function fromMoment(m: Moment): number {
  if (!m.date) return 0;
  const [y, mo, d] = m.date.split("-").map(Number);
  const [h, mi] = (m.time || "00:00").split(":").map(Number);
  return Math.floor(zonedToMs(y, mo, d, h || 0, mi || 0, 0) / 1000);
}

// MomentField is a date and a time on one row, in the panel's timezone.
export function MomentField({
  label,
  hint,
  value,
  onChange,
  clearable,
}: {
  label: string;
  hint?: string;
  value: Moment;
  onChange: (m: Moment) => void;
  clearable?: boolean;
}) {
  return (
    <div className="flex flex-col gap-1">
      <div className="grid grid-cols-[minmax(0,1fr)_7.5rem] items-end gap-2">
        <DatePicker
          label={label}
          value={value.date}
          clearable={clearable}
          onChange={(date) => onChange({ date, time: date ? value.time || "00:00" : "" })}
        />
        <TextInput
          type="time"
          ariaLabel={label}
          value={value.time}
          disabled={!value.date}
          onChange={(time) => onChange({ ...value, time })}
        />
      </div>
      {hint && <p className="text-xs text-ink-muted">{hint}</p>}
    </div>
  );
}
