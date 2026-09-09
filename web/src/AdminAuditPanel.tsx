import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  type AdminAudit,
  type AdminAuditFilter,
  adminAuditExportURL,
  getAdminAuditCatalog,
  listAdminAudit,
} from "./api";
import { fmtStamp } from "./format";
import { slugKey, td } from "./i18n";
import { errMessage, notifyError } from "./notify";
import {
  Button,
  cn,
  DatePicker,
  IconButton,
  IconExport,
  EmptyState,
  MICRO,
  Mono,
  Panel,
  Select,
  Skeleton,
  TextInput,
  useWideBox,
} from "./ui";

// Same page size as the user journal (api.EVENT_PAGE) — two audit trails that read
// the same way should not open at different depths.
const PAGE = 20;

// Mirrors model.AdminAuditRetentionDays.
const RETENTION_DAYS = 90;

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

// The journal's columns, one template for the header and every row.
const TPL =
  "minmax(0,1.6fr) minmax(0,1.4fr) minmax(0,1fr) minmax(0,1fr) minmax(0,1fr)";
const TPL_NARROW = "minmax(0,1fr) auto";
const WIDE_MIN = 640;

function AuditRow({
  ev,
  label,
  wide,
}: {
  ev: AdminAudit;
  label: string;
  wide: boolean;
}) {
  const tone = toneOf(ev.action);
  const details = fmtDetails(ev.details);
  const target = ev.target ? auditTarget(ev.target) : "";
  const when = fmtStamp(ev.created_at);
  // A failed sign-in and the two irreversible actions are the rows an owner scans
  // for; they say so in colour rather than in a badge.
  const actionCls =
    tone === "danger" ? "text-danger" : tone === "warn" ? "text-warning" : "text-ink";
  return (
    <div
      className={cn(
        "grid items-center gap-x-3 gap-y-0.5 border-t border-gray-100 px-3.5 py-[7px]",
        tone === "danger" && "danger-tint",
      )}
      style={{ gridTemplateColumns: wide ? TPL : TPL_NARROW }}
    >
      <span className={cn("truncate text-xs", actionCls)} title={label}>
        {label}
      </span>
      {wide ? (
        <>
          <span
            className="truncate text-xs text-ink-muted"
            title={details ? `${target} · ${details}` : target}
          >
            {target || "—"}
            {details && ` · ${details}`}
          </span>
          <span className="truncate text-xs text-accent">{ev.actor_name || "—"}</span>
          <Mono className="truncate text-[11px] text-ink-muted">{ev.ip || "—"}</Mono>
          <Mono className="truncate text-right text-[11px] text-ink-muted">{when}</Mono>
        </>
      ) : (
        <>
          <Mono className="text-right text-[11px] text-ink-muted">{when}</Mono>
          <span className="col-span-2 truncate text-[11px] text-ink-muted">
            <span className="text-accent">{ev.actor_name || "—"}</span>
            {target && ` · ${target}`}
            {ev.ip && ` · ${ev.ip}`}
            {details && ` · ${details}`}
          </span>
        </>
      )}
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
  const [boxRef, wide] = useWideBox(WIDE_MIN);

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
    <Panel
      title={t("audit.title")}
      aside={
        <span className="flex min-w-0 items-center gap-3">
          <span className="min-w-0 text-xs text-ink-muted">
            {t("events.retention", { count: RETENTION_DAYS })}
          </span>
          {/* A plain link, not a fetch: the file is an attachment the browser saves,
              and it carries exactly the filter below. In the header rather than in
              the filter row so it stays in reach however the row wraps. */}
          <IconButton
            href={adminAuditExportURL(filter)}
            title={t("audit.export")}
          >
            <IconExport />
          </IconButton>
        </span>
      }
    >
      <div className="flex flex-wrap items-end gap-2 border-t border-gray-100 px-3.5 py-3">
        <div className="min-w-40 flex-1">
          <TextInput
            type="search"
            value={search}
            onChange={setSearch}
            placeholder={t("audit.searchHint")}
          />
        </div>
        <div className="w-44">
          <Select value={category} onChange={setCategory} data={options} />
        </div>
        {/* The panel's own calendar rather than the browser's native date input: it
            renders the same on every platform, and the label lives in the field. */}
        <div className="w-36">
          <DatePicker
            value={from}
            onChange={setFrom}
            placeholder={t("audit.from")}
            clearable
          />
        </div>
        <div className="w-36">
          <DatePicker
            value={to}
            onChange={setTo}
            min={from || undefined}
            placeholder={t("audit.to")}
            clearable
          />
        </div>
      </div>

      {loading ? (
        <div className="flex flex-col gap-2.5 border-t border-gray-100 p-3.5">
          {[0, 1, 2, 3, 4].map((i) => (
            <Skeleton key={i} className="h-4 w-full" />
          ))}
        </div>
      ) : events.length === 0 ? (
        <EmptyState title={t("audit.empty")} />
      ) : (
        <div ref={boxRef}>
          {wide && (
            <div
              className={cn(MICRO, "grid items-center gap-3 border-t border-gray-100 px-3.5 py-2")}
              style={{ gridTemplateColumns: TPL }}
            >
              <span className="truncate">{t("audit.colAction")}</span>
              <span className="truncate">{t("audit.colTarget")}</span>
              <span className="truncate">{t("audit.colAdmin")}</span>
              <span className="truncate">{t("audit.colIp")}</span>
              <span className="truncate text-right">{t("audit.colWhen")}</span>
            </div>
          )}
          {events.map((ev) => (
            <AuditRow key={ev.id} ev={ev} label={actionLabel(ev.action)} wide={wide} />
          ))}
          {next > 0 && (
            <div className="border-t border-gray-100 p-3.5">
              <Button
                variant="light"
                color="gray"
                size="sm"
                fullWidth
                loading={more}
                onClick={loadMore}
              >
                {t("common.showMore")}
              </Button>
            </div>
          )}
        </div>
      )}
    </Panel>
  );
}
