import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { getNodeHealth, type HealthCheck, type HealthReport, type HealthStatus } from "./api";
import i18n, { td } from "./i18n";
import { errMessage, notifyError } from "./notify";
import { Badge, Button, Skeleton } from "./ui";

// STATUS_BADGE maps a check status to a Badge colour + word, and to the tint the
// row itself carries.
//
// Only the rows that need attention are tinted. Everything passing stays on the
// plain surface: a screen of ten identically-coloured cards is what made a failing
// check impossible to spot, and colouring the healthy ones too would just move the
// problem — the eye needs somewhere quiet to not look.
const statusBadge = (): Record<
  HealthStatus,
  { color: string; word: string; tint: string }
> => ({
  ok: { color: "green", word: "OK", tint: "" },
  warn: { color: "orange", word: i18n.t("health.warn"), tint: "warning-tint" },
  error: { color: "red", word: i18n.t("health.problem"), tint: "danger-tint" },
  info: { color: "gray", word: "—", tint: "" },
});

// overall maps the report's worst status to a banner.
const overallFor = (status: string): { title: string; cls: string } => {
  switch (status) {
    case "warn":
      return { title: i18n.t("health.someWarnings"), cls: "warning-tint text-warning" };
    case "error":
      return { title: i18n.t("health.someProblems"), cls: "danger-tint text-danger" };
    default:
      return { title: i18n.t("health.allGood"), cls: "success-tint text-success" };
  }
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
// checkDetail renders one check's detail line. Most are a key plus arguments; a few
// carry verbatim text the panel did not word (Xray's own config error). The
// short-lived-certificate note is appended rather than baked into the key: it
// applies to two different TLS states, and duplicating each of them just to vary a
// trailing clause would double the keys for no gain.
function checkDetail(c: HealthCheck): string {
  const base = c.detail_key ? td(c.detail_key, c.args) : (c.detail ?? "");
  return c.args?.shortLived ? `${base} · ${i18n.t("health.shortLivedNote")}` : base;
}

export function HealthPanel({ nodeId }: { nodeId: number }) {
  const { t } = useTranslation();
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

  const overall = overallFor(report.status);

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
              {t("health.checksCount", { count: report.checks.length })}
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
          {t("common.refresh")}
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
          const badges = statusBadge();
          const b = badges[c.status] ?? badges.info;
          return (
            <div
              key={c.key}
              className={`flex items-start justify-between gap-3 p-4 ${b.tint}`}
            >
              <div className="min-w-0">
                <p className="font-medium text-ink">{td(c.label_key)}</p>
                <p className="mt-0.5 text-sm text-ink-muted">{checkDetail(c)}</p>
                {c.hint_key && c.status !== "ok" && (
                  <p className="mt-1.5 text-xs text-ink-muted">💡 {td(c.hint_key)}</p>
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
