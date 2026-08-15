import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  type AdminAudit,
  type AdminAuditFilter,
  adminAuditExportURL,
  getAdminAuditCatalog,
  listAdminAudit,
} from "./api";
import { currentLang, slugKey, td } from "./i18n";
import { errMessage, notifyError } from "./notify";
import { Badge, Button, Select, SettingCard, Skeleton, TextInput } from "./ui";

// Same page size as the user journal (api.EVENT_PAGE) — two audit trails that read
// the same way should not open at different depths.
const PAGE = 20;

// A YYYY-MM-DD date input → unix seconds at the local day's start (from) or end (to,
// inclusive), or 0 when blank. Local time is what the operator picked, so that is
// what the range means.
function dayStart(d: string): number {
  if (!d) return 0;
  const t = new Date(`${d}T00:00:00`).getTime();
  return Number.isNaN(t) ? 0 : Math.floor(t / 1000);
}
function dayEnd(d: string): number {
  if (!d) return 0;
  const t = new Date(`${d}T23:59:59`).getTime();
  return Number.isNaN(t) ? 0 : Math.floor(t / 1000);
}

function fmtTs(unix: number): string {
  return new Date(unix * 1000).toLocaleString(currentLang(), {
    dateStyle: "medium",
    timeStyle: "short",
  });
}

// Rows the owner should be able to spot at a glance: a failed sign-in, and the two
// actions that are irreversible.
function toneOf(action: string): "danger" | "warn" | "plain" {
  if (action === "admin.login_failed") return "warn";
  if (
    action === "admin.deleted" ||
    action === "panel.factory_reset" ||
    action === "panel.restored"
  ) {
    return "danger";
  }
  return "plain";
}

// details is a small JSON object ({"role":"operator"}, {"from":…,"to":…}) — render it
// as plain "key: value" pairs rather than dumping JSON at the reader.
function fmtDetails(d: AdminAudit["details"]): string {
  if (!d || typeof d !== "object") return "";
  return Object.entries(d)
    .map(([k, v]) => `${k}: ${String(v)}`)
    .join(" · ");
}

function AuditRow({
  ev,
  label,
}: {
  ev: AdminAudit;
  label: string;
}) {
  const tone = toneOf(ev.action);
  const details = fmtDetails(ev.details);
  return (
    <div className="flex flex-col gap-1 rounded-xl border border-gray-200 px-3 py-2.5 sm:flex-row sm:items-center sm:justify-between sm:gap-3">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          {tone === "danger" ? (
            <Badge color="red">{label}</Badge>
          ) : tone === "warn" ? (
            <Badge color="orange">{label}</Badge>
          ) : (
            <span className="font-semibold text-ink">{label}</span>
          )}
          {ev.target && (
            <code className="rounded bg-gray-100 px-1.5 py-0.5 font-mono text-xs text-ink-muted">
              {auditTarget(ev.target)}
            </code>
          )}
        </div>
        <div className="mt-0.5 text-xs text-ink-muted">
          {ev.actor_name || "—"}
          {ev.ip && ` · ${ev.ip}`}
          {details && ` · ${details}`}
        </div>
      </div>
      <div className="shrink-0 text-xs text-ink-muted sm:text-right">
        {fmtTs(ev.created_at)}
      </div>
    </div>
  );
}

// A settings row's target is a dictionary key (the server marks it with this
// prefix) so the journal reads in the admin's language. Everything else a target
// holds — a login, an API key name, a URL — is free-form and shown verbatim. Rows
// written before this existed carry plain Russian text and fall through the same
// way, which is honest: they record what was shown at the time.
const SECTION_PREFIX = "audit.sec.";

function auditTarget(target: string): string {
  return target.startsWith(SECTION_PREFIX) ? td(target) : target;
}

export function AdminAuditPanel() {
  const { t } = useTranslation();
  const [events, setEvents] = useState<AdminAudit[]>([]);
  // Only the KEYS come from the server; the labels are looked up here so the
  // journal follows the panel's language rather than the server's.
  const [categoryKeys, setCategoryKeys] = useState<string[]>([]);
  const [category, setCategory] = useState("");
  const [search, setSearch] = useState("");
  const [debounced, setDebounced] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [next, setNext] = useState(0);
  const [loading, setLoading] = useState(true);
  const [more, setMore] = useState(false);

  useEffect(() => {
    getAdminAuditCatalog()
      .then((c) => setCategoryKeys(c.categories.map((x) => x.key)))
      .catch(() => {}); // the journal still renders, just without the filter
  }, []);

  // Debounce the search box so typing doesn't fire a request per keystroke.
  useEffect(() => {
    const id = setTimeout(() => setDebounced(search.trim()), 300);
    return () => clearTimeout(id);
  }, [search]);

  const options = [
    { value: "", label: t("audit.allEvents") },
    ...categoryKeys.map((k) => ({ value: k, label: td(`audit.cat.${k}`) })),
  ];

  // Rows are titled by their exact action; the filter offers areas.
  const actionLabel = (action: string) => td(`audit.action.${slugKey(action)}`);

  // The active filter, shared by the paged list and the CSV export so they can never
  // show and download different things.
  const filter = useMemo<AdminAuditFilter>(
    () => ({
      category: category || undefined,
      search: debounced || undefined,
      from: dayStart(from) || undefined,
      to: dayEnd(to) || undefined,
    }),
    [category, debounced, from, to],
  );

  // Refetch from the top whenever any part of the filter changes.
  const load = useCallback(() => {
    setLoading(true);
    listAdminAudit({ ...filter, limit: PAGE })
      .then((p) => {
        setEvents(p.events);
        setNext(p.next_before);
      })
      .catch((e) => notifyError(errMessage(e)))
      .finally(() => setLoading(false));
  }, [filter]);

  useEffect(() => {
    load();
  }, [load]);

  const loadMore = () => {
    if (!next) return;
    setMore(true);
    listAdminAudit({ ...filter, before: next, limit: PAGE })
      .then((p) => {
        setEvents((prev) => [...prev, ...p.events]);
        setNext(p.next_before);
      })
      .catch((e) => notifyError(errMessage(e)))
      .finally(() => setMore(false));
  };

  return (
    <SettingCard
      title={t("audit.title")}
      description={t("audit.description", { days: 90 })}
      action={
        <div className="w-48">
          <Select value={category} onChange={setCategory} data={options} />
        </div>
      }
      stackAction
    >
      <div className="mb-3 flex flex-col gap-2 sm:flex-row sm:items-end">
        <div className="flex-1">
          <TextInput
            label={t("audit.search")}
            type="search"
            value={search}
            onChange={setSearch}
            placeholder={t("audit.searchHint")}
          />
        </div>
        <TextInput label={t("audit.from")} type="date" value={from} onChange={setFrom} />
        <TextInput label={t("audit.to")} type="date" value={to} onChange={setTo} />
        <a
          href={adminAuditExportURL(filter)}
          download="rospanel-audit.csv"
          className="inline-flex h-10 shrink-0 items-center justify-center rounded-lg border border-gray-200 px-3 text-sm font-medium text-ink hover:bg-gray-50"
        >
          {t("audit.export")}
        </a>
      </div>
      {loading ? (
        <div className="flex flex-col gap-2">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-14 w-full" />
          ))}
        </div>
      ) : events.length === 0 ? (
        <p className="py-4 text-center text-sm text-ink-muted">
          {t("audit.empty")}
        </p>
      ) : (
        <div className="flex flex-col gap-2">
          {events.map((ev) => (
            <AuditRow
              key={ev.id}
              ev={ev}
              label={actionLabel(ev.action)}
            />
          ))}
          {next > 0 && (
            <div className="mt-2 flex justify-center">
              <Button
                variant="light"
                color="gray"
                loading={more}
                onClick={loadMore}
              >
                {t("common.showMore")}
              </Button>
            </div>
          )}
        </div>
      )}
    </SettingCard>
  );
}
