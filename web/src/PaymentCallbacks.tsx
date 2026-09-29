import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { currentLang, td } from "./i18n";
import { listPaymentCallbacks, type PaymentCallback } from "./api";
import { errMessage, notifyError } from "./notify";
import { inPanelTz } from "./tz";
import { Badge, Button, EmptyState, MICRO, Modal, Mono, Panel, SegmentedControl, cn } from "./ui";

const PAGE = 50;

const OUTCOME_COLOR: Record<string, "green" | "gray" | "orange" | "red"> = {
  paid: "green",
  duplicate: "gray",
  cancelled: "gray",
  refunded: "orange",
  pending: "gray",
  mismatch: "red",
  no_order: "red",
  rejected: "red",
  error: "red",
};

function fmtWhen(unix: number): string {
  return new Date(unix * 1000).toLocaleString(
    currentLang(),
    inPanelTz({ day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit" }),
  );
}

// prettyBody indents a JSON body; anything else is shown as it came.
function prettyBody(body: string): string {
  try {
    return JSON.stringify(JSON.parse(body), null, 2);
  } catch {
    return body;
  }
}

// PaymentCallbacks is the journal of what payment providers sent and what came of it.
export function PaymentCallbacks({ providerLabel }: { providerLabel: (p: string) => string }) {
  const { t } = useTranslation();
  const [failed, setFailed] = useState(false);
  const [rows, setRows] = useState<PaymentCallback[]>([]);
  const [more, setMore] = useState(false);
  const [open, setOpen] = useState<PaymentCallback | null>(null);

  const load = (before?: number) =>
    listPaymentCallbacks({ failed, before })
      .then((r) => {
        setRows((prev) => (before ? [...prev, ...r] : r));
        setMore(r.length === PAGE);
      })
      .catch((e) => notifyError(errMessage(e)));

  // biome-ignore lint/correctness/useExhaustiveDependencies: reloads when the filter changes; load is redefined every render
  useEffect(() => {
    load();
  }, [failed]);

  return (
    <Panel
      title={t("callbacks.title")}
      aside={
        <SegmentedControl
          size="xs"
          nav
          value={failed ? "failed" : "all"}
          onChange={(v) => setFailed(v === "failed")}
          data={[
            { value: "all", label: t("callbacks.all") },
            { value: "failed", label: t("callbacks.onlyFailed") },
          ]}
        />
      }
    >
      {rows.length === 0 ? (
        <EmptyState title={t("callbacks.empty")} />
      ) : (
        <>
          {rows.map((c) => (
            <button
              type="button"
              key={c.id}
              onClick={() => setOpen(c)}
              className="flex w-full flex-wrap items-center gap-x-2.5 gap-y-1 border-t border-gray-100 px-3.5 py-[7px] text-left hover:bg-gray-50"
            >
              <Mono className="shrink-0 text-[11px] text-ink-muted">{fmtWhen(c.at)}</Mono>
              <span className="shrink-0 text-xs text-ink">{providerLabel(c.provider)}</span>
              <Badge size="xs" color={OUTCOME_COLOR[c.outcome] ?? "gray"}>
                {td(`callbacks.outcome.${c.outcome}`)}
              </Badge>
              {c.order_id > 0 && (
                <span className="shrink-0 text-xs text-ink-muted">{t("callbacks.order", { id: c.order_id })}</span>
              )}
              <Mono className="min-w-0 flex-1 truncate text-right text-[11px] text-ink-muted">
                {c.error || c.provider_id}
              </Mono>
            </button>
          ))}
          {more && (
            <div className="border-t border-gray-100 p-3.5">
              <Button size="xs" variant="light" color="gray" onClick={() => load(rows[rows.length - 1].id)}>
                {t("common.showMore")}
              </Button>
            </div>
          )}
        </>
      )}
      <Modal
        open={!!open}
        onClose={() => setOpen(null)}
        size="lg"
        title={open ? `${providerLabel(open.provider)} · ${td(`callbacks.outcome.${open.outcome}`)}` : undefined}
        subtitle={
          open
            ? [fmtWhen(open.at), open.remote_ip, open.order_id > 0 && t("callbacks.order", { id: open.order_id }), open.provider_id]
                .filter(Boolean)
                .join(" · ")
            : undefined
        }
      >
        {open && (
          <div className="flex flex-col gap-3">
            {open.error && <p className="text-sm text-danger">{open.error}</p>}
            {open.headers && (
              <div>
                <div className={cn(MICRO, "mb-1")}>{t("callbacks.headers")}</div>
                <pre className="max-h-48 overflow-auto rounded-lg bg-gray-50 p-2.5 text-[11px] text-ink">{open.headers}</pre>
              </div>
            )}
            <div>
              <div className={cn(MICRO, "mb-1")}>{t("callbacks.body")}</div>
              <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-gray-50 p-2.5 text-[11px] text-ink">
                {prettyBody(open.body ?? "")}
              </pre>
            </div>
          </div>
        )}
      </Modal>
    </Panel>
  );
}
