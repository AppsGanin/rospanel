import { useCallback, useEffect, useState } from "react";
import { getHealth, type HealthReport, type HealthStatus } from "./api";
import { errMessage, notifyError } from "./notify";
import { Badge, Button, Card, Skeleton } from "./ui";

// STATUS_BADGE maps a check status to a Badge colour + word.
const STATUS_BADGE: Record<HealthStatus, { color: string; word: string }> = {
  ok: { color: "green", word: "OK" },
  warn: { color: "orange", word: "Внимание" },
  error: { color: "red", word: "Проблема" },
  info: { color: "gray", word: "—" },
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
      {[...Array(5)].map((_, i) => (
        <Skeleton key={i} className="h-20 w-full rounded-2xl" />
      ))}
    </div>
  );
}

export function HealthPanel() {
  const [report, setReport] = useState<HealthReport | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [refreshing, setRefreshing] = useState(false);

  const load = useCallback(async (manual = false) => {
    if (manual) setRefreshing(true);
    try {
      setReport(await getHealth());
    } catch (e) {
      if (manual) notifyError(errMessage(e));
    } finally {
      setLoaded(true);
      if (manual) setRefreshing(false);
    }
  }, []);

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

      {report.checks.map((c) => {
        const b = STATUS_BADGE[c.status] ?? STATUS_BADGE.info;
        return (
          <Card key={c.key} className="p-4">
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <p className="font-medium text-ink">{c.label}</p>
                <p className="mt-0.5 text-sm text-ink-muted">{c.detail}</p>
                {c.hint && c.status !== "ok" && (
                  <p className="mt-1.5 text-xs text-ink-muted">💡 {c.hint}</p>
                )}
              </div>
              <Badge color={b.color as never}>{b.word}</Badge>
            </div>
          </Card>
        );
      })}
    </div>
  );
}
