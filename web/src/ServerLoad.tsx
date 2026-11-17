import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { getNodeLoad, type LoadPoint, type LoadRange, type LoadReport } from "./api";
import { AMBER, cssVar, LoadArea, TEAL } from "./charts";
import { fmtBytes, fmtDuration, fmtStamp } from "./format";
import i18n, { locale } from "./i18n";
import { errMessage } from "./notify";
import { inPanelTz } from "./tz";
import { DialogTabs } from "./ServerDialogParts";
import { cn, Modal, Mono, Skeleton } from "./ui";

// The load dialog of a server card: CPU, RAM, disk, network and users online over a
// day, a week or a month, and when the server was down. The panel samples every
// server once a minute.

const RANGES: LoadRange[] = ["24h", "7d", "30d"];

// tickFor labels the time axis: hours over a day, dates beyond it.
function tickFor(range: LoadRange): (t: number) => string {
  const opts: Intl.DateTimeFormatOptions =
    range === "24h" ? { hour: "2-digit", minute: "2-digit" } : { day: "2-digit", month: "2-digit" };
  const f = new Intl.DateTimeFormat(locale(), inPanelTz(opts));
  return (t) => f.format(t * 1000);
}

// barClass colours one step of the availability strip by the share of samples that
// found the server up.
function barClass(up: number | null): string {
  if (up == null) return "bg-gray-200";
  if (up >= 1) return "bg-emerald-500";
  if (up > 0) return "bg-orange-500";
  return "bg-red-500";
}

// fmtRate renders bytes per second as bits, the unit links and plans are sold in.
function fmtRate(bytes: number): string {
  let v = bytes * 8;
  const units = ["b", "k", "m", "g"] as const;
  let i = 0;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  const n = v.toFixed(v < 10 && i > 0 ? 1 : 0);
  return i18n.t(`serverLoad.rate.${units[i]}`, { n });
}

const pct = (v: number) => `${Math.round(v)}%`;
const users = (v: number) => String(Math.round(v));

function barTitle(p: LoadPoint): string {
  const at = fmtStamp(p.t);
  if (p.up == null) return `${at} · ${i18n.t("serverLoad.noData")}`;
  return `${at} · ${i18n.t("serverLoad.upShare", { pct: Math.round(p.up * 100) })}`;
}

function Stat({ label, value, tone }: { label: string; value: string; tone?: string }) {
  return (
    <div className="flex min-w-0 flex-col rounded-xl border border-gray-200 px-3 py-2">
      <span className="text-[11px] text-ink-muted">{label}</span>
      <Mono className={cn("text-base text-ink", tone)}>{value}</Mono>
    </div>
  );
}

export function ServerLoadDialog({
  nodeId,
  title,
  onClose,
}: {
  nodeId: number;
  title: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [range, setRange] = useState<LoadRange>("24h");
  const [rep, setRep] = useState<LoadReport | null>(null);
  const [err, setErr] = useState("");

  useEffect(() => {
    let live = true;
    const load = () =>
      getNodeLoad(nodeId, range)
        .then((r) => {
          if (!live) return;
          setRep(r);
          setErr("");
        })
        .catch((e) => live && setErr(errMessage(e)));
    setRep(null);
    load();
    // A minute is the sampling interval: refreshing faster shows nothing new.
    const timer = window.setInterval(load, 60_000);
    return () => {
      live = false;
      window.clearInterval(timer);
    };
  }, [nodeId, range]);

  const tick = tickFor(range);
  const outages = rep ? [...rep.outages].reverse() : [];
  const brand = cssVar("--color-brand-600", "#0d4cd3");
  const violet = "#7c3aed";

  return (
    <Modal
      open
      onClose={onClose}
      title={title}
      size="xl"
      toolbar={
        <DialogTabs
          value={range}
          onChange={(v) => setRange(v as LoadRange)}
          tabs={RANGES.map((r) => ({ value: r, label: t(`serverLoad.range.${r}`) }))}
        />
      }
    >
      {err && <p className="text-sm text-danger">{err}</p>}
      {!rep && !err && (
        <div className="flex flex-col gap-3">
          <Skeleton className="h-16 w-full rounded-xl" />
          <Skeleton className="h-56 w-full rounded-xl" />
        </div>
      )}
      {rep && (
        <div className="flex flex-col gap-5">
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
            <Stat
              label={t("serverLoad.uptime")}
              value={rep.samples ? `${rep.uptime.toFixed(rep.uptime >= 99.95 ? 0 : 2)}%` : "—"}
              tone={rep.samples && rep.uptime < 99 ? "text-danger" : undefined}
            />
            <Stat label={t("serverLoad.outages")} value={String(rep.outages.length)} />
            <Stat label={t("serverLoad.downtime")} value={fmtDuration(rep.down_seconds)} />
            <Stat label={t("serverLoad.onlinePeak")} value={String(rep.online_max)} />
            <Stat label={t("serverLoad.trafficIn")} value={fmtBytes(rep.rx_bytes)} />
            <Stat label={t("serverLoad.trafficOut")} value={fmtBytes(rep.tx_bytes)} />
          </div>

          {rep.samples === 0 ? (
            <p className="text-sm text-ink-muted">{t("serverLoad.noHistory")}</p>
          ) : (
            <>
              <section className="flex flex-col gap-2">
                <h3 className="text-sm font-medium text-ink">{t("serverLoad.loadTitle")}</h3>
                <LoadArea
                  data={rep.points}
                  max={100}
                  fmt={pct}
                  tick={tick}
                  stamp={fmtStamp}
                  series={[
                    { key: "cpu", peak: "cpu_max", label: "CPU", color: brand },
                    { key: "mem", peak: "mem_max", label: "RAM", color: TEAL },
                    { key: "disk", peak: "disk_max", label: t("serverLoad.disk"), color: AMBER },
                  ]}
                />
              </section>

              <section className="flex flex-col gap-2">
                <h3 className="text-sm font-medium text-ink">{t("serverLoad.netTitle")}</h3>
                <LoadArea
                  data={rep.points}
                  fmt={fmtRate}
                  axisWidth={88}
                  tick={tick}
                  stamp={fmtStamp}
                  series={[
                    { key: "rx", peak: "rx_max", label: t("serverLoad.netIn"), color: brand },
                    { key: "tx", peak: "tx_max", label: t("serverLoad.netOut"), color: TEAL },
                  ]}
                />
              </section>

              <section className="flex flex-col gap-2">
                <h3 className="text-sm font-medium text-ink">{t("serverLoad.onlineTitle")}</h3>
                <LoadArea
                  data={rep.points}
                  fmt={users}
                  height={160}
                  tick={tick}
                  stamp={fmtStamp}
                  series={[{ key: "online", peak: "online_max", label: t("serverLoad.online"), color: violet }]}
                />
              </section>

              <section className="flex flex-col gap-2">
                <h3 className="text-sm font-medium text-ink">{t("serverLoad.availability")}</h3>
                <div className="flex h-7 w-full overflow-hidden rounded-md" role="img" aria-label={t("serverLoad.availability")}>
                  {rep.points.map((p) => (
                    <span key={p.t} title={barTitle(p)} className={cn("min-w-0 flex-1", barClass(p.up))} />
                  ))}
                </div>
                <div className="flex justify-between text-[11px] text-ink-muted">
                  <span>{tick(rep.from)}</span>
                  <span>{tick(rep.to)}</span>
                </div>
              </section>

              <section className="flex flex-col gap-2">
                <h3 className="text-sm font-medium text-ink">{t("serverLoad.outageLog")}</h3>
                {outages.length === 0 ? (
                  <p className="text-sm text-ink-muted">{t("serverLoad.noOutages")}</p>
                ) : (
                  <ul className="flex max-h-60 flex-col divide-y divide-gray-100 overflow-y-auto rounded-xl border border-gray-200">
                    {outages.map((o) => (
                      <li key={o.start} className="flex items-center justify-between gap-3 px-3 py-1.5 text-sm">
                        <Mono className="text-ink">{fmtStamp(o.start)}</Mono>
                        {o.end ? (
                          <Mono className="text-ink-muted">{fmtDuration(o.end - o.start)}</Mono>
                        ) : (
                          <span className="text-danger">
                            {t("serverLoad.ongoing", { dur: fmtDuration(rep.to - o.start) })}
                          </span>
                        )}
                      </li>
                    ))}
                  </ul>
                )}
              </section>
            </>
          )}
        </div>
      )}
    </Modal>
  );
}
