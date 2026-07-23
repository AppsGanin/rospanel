import { useCallback, useEffect, useState } from "react";
import { getNodeHealth, type HealthReport, type HealthStatus } from "./api";
import { errMessage, notifyError } from "./notify";
import { Badge, Button, Skeleton } from "./ui";

// STATUS_BADGE maps a check status to a Badge colour + word, and to the tint the
// row itself carries.
//
// Only the rows that need attention are tinted. Everything passing stays on the
// plain surface: a screen of ten identically-coloured cards is what made a failing
// check impossible to spot, and colouring the healthy ones too would just move the
// problem — the eye needs somewhere quiet to not look.
const STATUS_BADGE: Record<HealthStatus, { color: string; word: string; tint: string }> = {
  ok: { color: "green", word: "OK", tint: "" },
  warn: { color: "orange", word: "Внимание", tint: "warning-tint" },
  error: { color: "red", word: "Проблема", tint: "danger-tint" },
  info: { color: "gray", word: "—", tint: "" },
};

// OVERALL maps the report's worst status to a banner.
const OVERALL: Record<string, { title: string; cls: string }> = {
  ok: { title: "Всё в порядке", cls: "success-tint text-success" },
  warn: { title: "Есть предупреждения", cls: "warning-tint text-warning" },
  error: { title: "Есть проблемы", cls: "danger-tint text-danger" },
};

function HealthSkeleton() {
  return (
    <div className="flex flex-col gap-3">
      <Skeleton className="h-16 w-full rounded-2xl" />
      <Skeleton className="h-80 w-full rounded-2xl" />
    </div>
  );
}

// HealthPanel shows one server's diagnostics. nodeId picks the server: 0 is the
// panel's own (the full local report), a node id is that node's — as it last
// reported, since the panel doesn't dial a node to build the report.
export function HealthPanel({ nodeId }: { nodeId: number }) {
  const [report, setReport] = useState<HealthReport | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [refreshing, setRefreshing] = useState(false);

  const load = useCallback(async (manual = false) => {
    if (manual) setRefreshing(true);
    try {
      setReport(await getNodeHealth(nodeId));
    } catch (e) {
      if (manual) notifyError(errMessage(e));
    } finally {
      setLoaded(true);
      if (manual) setRefreshing(false);
    }
  }, [nodeId]);

  useEffect(() => {
    load();
    const id = setInterval(load, 15000); // light auto-refresh
    return () => clearInterval(id);
  }, [load]);

  if (!loaded) return <HealthSkeleton />;
  if (!report) return null;

  const overall = OVERALL[report.status] ?? OVERALL.ok;

  return (
    <div className="flex flex-col gap-3 pb-6">
      <div
        className={`flex items-center justify-between gap-3 rounded-2xl px-4 py-3 ${overall.cls}`}
      >
        <div className="flex items-center gap-3">
          <span className="text-2xl leading-none">
            {report.status === "ok" ? "✓" : report.status === "warn" ? "!" : "✕"}
          </span>
          <div>
            <p className="font-semibold">{overall.title}</p>
            <p className="text-xs opacity-80">
              Проверок: {report.checks.length}
            </p>
          </div>
        </div>
        <Button
          size="sm"
          variant="light"
          color="gray"
          loading={refreshing}
          onClick={() => load(true)}
        >
          Обновить
        </Button>
      </div>

      {/* Deliberately NOT a Card. This panel lives inside a modal, and Card's
          surface is the same --color-white the modal itself is painted with, so a
          card here is invisible — which is how ten checks ended up looking like one
          undifferentiated block. gray-50/200 are derived from the theme (surface
          interpolated toward the text colour), so this reads as one step off the
          modal in a light theme and in a dark one alike, instead of a hardcoded
          grey that only works in one of them. */}
      <div className="overflow-hidden rounded-2xl border border-gray-200 bg-gray-50 divide-y divide-gray-200">
        {report.checks.map((c) => {
          const b = STATUS_BADGE[c.status] ?? STATUS_BADGE.info;
          return (
            <div
              key={c.key}
              className={`flex items-start justify-between gap-3 p-4 ${b.tint}`}
            >
              <div className="min-w-0">
                <p className="font-medium text-ink">{c.label}</p>
                <p className="mt-0.5 text-sm text-ink-muted">{c.detail}</p>
                {c.hint && c.status !== "ok" && (
                  <p className="mt-1.5 text-xs text-ink-muted">💡 {c.hint}</p>
                )}
              </div>
              <Badge color={b.color as never}>{b.word}</Badge>
            </div>
          );
        })}
      </div>
    </div>
  );
}
