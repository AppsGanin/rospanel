import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import i18n, { currentLang } from "./i18n";
import {
  type Broadcast,
  type BroadcastAudience,
  type BroadcastButton,
  broadcastAudience,
  cancelBroadcast,
  createBroadcast,
  listBroadcasts,
  pauseBroadcast,
  resumeBroadcast,
  retryBroadcast,
  testBroadcast,
} from "./api";
import { HtmlEditor } from "./HtmlEditor";
import { useShowMore } from "./hooks";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  CenterLoader,
  cn,
  EmptyState,
  IconButton,
  IconClose,
  MICRO,
  Mono,
  Panel,
  Select,
  ShowMore,
  TextInput,
  useConfirm,
} from "./ui";

// The audience picker. `days` marks the filters that take a horizon, which travels
// inside the value the server stores ("seen:7").
const audiences = (): { value: string; label: string; days?: boolean }[] => [
  { value: "all", label: i18n.t("bc.audAll") },
  { value: "linked", label: i18n.t("bc.audLinked") },
  { value: "unlinked", label: i18n.t("bc.audUnlinked") },
  { value: "active", label: i18n.t("bc.audActive") },
  { value: "expired", label: i18n.t("bc.audExpired") },
  { value: "expiring", label: i18n.t("bc.audExpiring"), days: true },
  { value: "seen", label: i18n.t("bc.audSeen"), days: true },
  { value: "unseen", label: i18n.t("bc.audUnseen"), days: true },
  { value: "never", label: i18n.t("bc.audNever") },
];

// audienceLabel turns a stored audience ("seen:7") back into words for the history,
// where the run is over and the picker that produced it is long reset.
function audienceLabel(a: string): string {
  const [kind, days] = a.split(":");
  const base = audiences().find((x) => x.value === kind)?.label ?? kind;
  return days ? `${base} ${i18n.t("bc.days", { count: Number(days) })}` : base;
}

const dayChoices = () =>
  [1, 3, 7, 14, 30, 90].map((d) => ({
    value: String(d),
    label: i18n.t("bc.days", { count: d }),
  }));

const statusMeta = (
  status: Broadcast["status"],
): { label: string; color: string } => {
  switch (status) {
    case "running":
      return { label: i18n.t("bc.stRunning"), color: "blue" };
    case "paused":
      return { label: i18n.t("bc.stPaused"), color: "yellow" };
    case "done":
      return { label: i18n.t("bc.stDone"), color: "green" };
    default:
      return { label: i18n.t("bc.stCancelled"), color: "gray" };
  }
};

// Telegram's own caps. Exceeded, it refuses each message separately, so the whole
// broadcast would fail one recipient at a time — the counter is shown while there is
// still something to do about it.
const TEXT_MAX = 4096;
const CAPTION_MAX = 1024;
const BUTTONS_MAX = 8;

// Polled only while it is actually moving. A paused run changes nothing on its own,
// and treating it as live left the tab polling every 1.5s forever against a progress
// bar that never moves — on a panel whose store has a single connection.
const isLive = (b: Broadcast) => b.status === "running";

function fmtTime(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString(currentLang(), {
    day: "2-digit",
    month: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function BroadcastPanel() {
  const { t } = useTranslation();
  const [loaded, setLoaded] = useState(false);
  const [list, setList] = useState<Broadcast[]>([]);
  // The server returns the last 50 runs, each a multi-line row with a progress bar,
  // so the history alone can be several screens. No reset key: a running broadcast
  // re-polls this list, and collapsing it under the operator mid-read would be worse
  // than carrying the expansion.
  const history = useShowMore(list);
  const [text, setText] = useState("");
  const [audienceKind, setAudienceKind] = useState("all");
  const [audienceDays, setAudienceDays] = useState("7");
  const needsDays = !!audiences().find((a) => a.value === audienceKind)?.days;
  // What the server stores and resolves: the horizon rides inside the value.
  const audience: BroadcastAudience = needsDays
    ? `${audienceKind}:${audienceDays}`
    : audienceKind;
  const [buttons, setButtons] = useState<BroadcastButton[]>([]);
  const [media, setMedia] = useState<File | null>(null);
  const [reach, setReach] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [testing, setTesting] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const { confirm, confirmNode } = useConfirm();

  const load = () =>
    listBroadcasts()
      .then(setList)
      .catch((e) => notifyError(errMessage(e)));

  useEffect(() => {
    load().finally(() => setLoaded(true));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Poll only while something is actually moving, and stop the moment it isn't.
  useEffect(() => {
    if (!list.some(isLive)) return;
    const id = setInterval(() => {
      listBroadcasts()
        .then(setList)
        .catch(() => {
          /* transient — the next tick retries */
        });
    }, 1500);
    return () => clearInterval(id);
  }, [list]);

  useEffect(() => {
    let dropped = false;
    broadcastAudience(audience)
      .then((r) => !dropped && setReach(r.count))
      .catch(() => !dropped && setReach(null));
    return () => {
      dropped = true;
    };
  }, [audience]);

  const limit = media ? CAPTION_MAX : TEXT_MAX;
  const overLimit = [...text].length > limit;
  const empty = !text.trim() && !media;
  const badButton = buttons.some((b) => !b.text.trim() || !b.url.trim());
  const canSend = !empty && !overLimit && !badButton;
  const payload = { text, audience, buttons };

  const clearMedia = () => {
    setMedia(null);
    if (fileRef.current) fileRef.current.value = "";
  };

  const send = async () => {
    const ok = await confirm({
      title: t("bc.startTitle"),
      body:
        reach === null
          ? t("bc.startBodyUnknown")
          : t("bc.startBody", { count: reach }),
      confirmLabel: t("bc.start"),
    });
    if (!ok) return;
    setBusy(true);
    try {
      await createBroadcast(payload, media);
      setText("");
      setButtons([]);
      clearMedia();
      await load();
      notifySuccess(t("bc.started"));
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const sendTest = async () => {
    setTesting(true);
    try {
      await testBroadcast(payload, media);
      notifySuccess(t("bc.testSent"));
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setTesting(false);
    }
  };

  const control = async (fn: () => Promise<Broadcast>) => {
    try {
      await fn();
      await load();
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  if (!loaded) return <CenterLoader />;

  return (
    <div className="flex flex-col gap-3.5">
      {confirmNode}
      {/* The composer as bands, not as a stack of labelled form fields: who it goes
          to, what it says, what rides along, and the two ways to send it. */}
      <Panel
        title={t("bc.title")}
        aside={
          <span className="min-w-0 text-xs text-ink-muted">
            {t("bc.description")}
          </span>
        }
      >
        <div className="flex flex-col">
          <div className="flex flex-wrap items-center gap-2 border-t border-gray-100 px-3.5 py-3">
            <span className={cn(MICRO, "w-full sm:w-16")}>{t("bc.to")}</span>
            <div className="min-w-0 flex-1 sm:max-w-80">
              <Select
                data={audiences().map((a) => ({ value: a.value, label: a.label }))}
                value={audienceKind}
                onChange={setAudienceKind}
              />
            </div>
            {needsDays && (
              <div className="w-36">
                <Select
                  data={dayChoices()}
                  value={audienceDays}
                  onChange={setAudienceDays}
                />
              </div>
            )}
            {/* The count the operator is really deciding on. A sentence, not a
                figure, so it takes the line under the picker rather than a chip. */}
            <p className="w-full text-[11px] text-ink-muted">
              {reach === null ? t("bc.counting") : t("bc.reachNow", { count: reach })}
            </p>
          </div>

          <div className="flex flex-col gap-1.5 border-t border-gray-100 px-3.5 py-3">
            <HtmlEditor
              value={text}
              onChange={setText}
              rows={4}
              placeholder={t("bc.textPlaceholder")}
            />
            <div className="flex flex-wrap items-baseline justify-between gap-2">
              <span className="text-[11px] text-ink-muted">
                {t("bc.markupHint")}
                {media && ` · ${t("bc.captionNote")}`}
              </span>
              <Mono
                className={cn(
                  "text-[11px]",
                  overLimit ? "text-danger" : "text-ink-muted",
                )}
              >
                {[...text].length} / {limit}
              </Mono>
            </div>
          </div>

          <div className="border-t border-gray-100 px-3.5 py-3">
            <p className={cn(MICRO, "mb-1.5")}>{t("bc.attachment")}</p>
            {/* The native file input renders its own browser-locale label, which
                reads as a rendering fault next to styled controls.
                Hidden, driven by a button that says what it does. */}
            <input
              ref={fileRef}
              type="file"
              className="hidden"
              onChange={(e) => setMedia(e.target.files?.[0] ?? null)}
            />
            {media ? (
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-sm text-ink">📎 {media.name}</span>
                <Button variant="subtle" size="xs" onClick={clearMedia}>
                  {t("userDetail.removeAttachment")}
                </Button>
              </div>
            ) : (
              <Button
                variant="light"
                size="sm"
                onClick={() => fileRef.current?.click()}
              >
                {t("bc.pickFile")}
              </Button>
            )}
            <p className="mt-1 text-[11px] text-ink-muted">
              {t("bc.attachmentHint")}
            </p>
          </div>

          <div className="flex flex-col gap-2 border-t border-gray-100 px-3.5 py-3">
            <p className={MICRO}>{t("bc.buttons")}</p>
            {buttons.map((b, i) => (
              <div key={i} className="flex items-end gap-2">
                <div className="flex-1">
                  <TextInput
                    label={i === 0 ? t("bc.text") : undefined}
                    value={b.text}
                    onChange={(v) =>
                      setButtons((cur) =>
                        cur.map((x, j) => (j === i ? { ...x, text: v } : x)),
                      )
                    }
                    placeholder={t("bc.buttonPlaceholder")}
                  />
                </div>
                <div className="flex-1">
                  <TextInput
                    label={i === 0 ? t("bc.link") : undefined}
                    value={b.url}
                    onChange={(v) =>
                      setButtons((cur) =>
                        cur.map((x, j) => (j === i ? { ...x, url: v } : x)),
                      )
                    }
                    placeholder="https://example.com"
                  />
                </div>
                <IconButton
                  title={t("bc.removeButton")}
                  onClick={() =>
                    setButtons((cur) => cur.filter((_, j) => j !== i))
                  }
                >
                  <IconClose size={18} />
                </IconButton>
              </div>
            ))}
            {buttons.length < BUTTONS_MAX && (
              <div>
                <Button
                  variant="light"
                  size="sm"
                  onClick={() =>
                    setButtons((cur) => [...cur, { text: "", url: "" }])
                  }
                >
                  {t("bc.addButton")}
                </Button>
              </div>
            )}
          </div>

          {/* The section's own footer: the two ways to send, and what the test one
              does, on the panel's bottom edge. */}
          <div className="flex flex-wrap items-center gap-2 border-t border-brand-600/10 bg-gray-50/95 px-3.5 py-2.5">
            <Button size="sm" loading={busy} onClick={send} disabled={!canSend}>
              {t("bc.startBroadcast")}
            </Button>
            <Button
              size="sm"
              variant="outline"
              color="gray"
              loading={testing}
              onClick={sendTest}
              disabled={!canSend}
            >
              {t("bc.sendTest")}
            </Button>
            <span className="min-w-0 text-[11px] text-ink-muted">
              {t("bc.testHint")}
            </span>
          </div>
        </div>
      </Panel>

      <Panel title={t("bc.history")}>
        {list.length === 0 ? (
          <EmptyState title={t("bc.historyEmpty")} />
        ) : (
          <>
            {history.shown.map((b) => (
              <BroadcastRow key={b.id} b={b} onControl={control} />
            ))}
            <ShowMore rest={history.rest} onClick={history.showMore} className="p-3.5" />
          </>
        )}
      </Panel>
    </div>
  );
}

function BroadcastRow({
  b,
  onControl,
}: {
  b: Broadcast;
  onControl: (fn: () => Promise<Broadcast>) => void;
}) {
  // Every terminal state, skipped included — it is part of total, and omitting it
  // froze the bar below 100% on a finished run with no way to correct itself
  // (polling stops once the run is done).
  const { t } = useTranslation();
  const done = b.sent + b.failed + b.blocked + b.skipped;
  const pct = b.total > 0 ? Math.round((done / b.total) * 100) : 0;
  const st = statusMeta(b.status);

  return (
    <div className="flex flex-col gap-1.5 border-t border-gray-100 px-3.5 py-2.5">
      <div className="flex flex-wrap items-center gap-2">
        <Mono className="text-[11px] text-ink-muted">
          {fmtTime(b.started_at || b.created_at)}
        </Mono>
        <Badge color={st.color} size="xs">
          {st.label}
        </Badge>
        <span className="truncate text-[11px] text-ink-muted">
          {audienceLabel(b.audience)}
        </span>
        {b.created_by && (
          <span className="truncate text-[11px] text-ink-muted">{b.created_by}</span>
        )}
        {b.media_name && (
          <span className="truncate text-[11px] text-ink-muted">📎 {b.media_name}</span>
        )}
      </div>

      <p className="line-clamp-2 text-xs text-ink">
        {b.text || <span className="text-ink-muted">{t("bc.noText")}</span>}
      </p>

      <div className="flex items-center gap-2">
        <span className="h-1 min-w-0 flex-1 overflow-hidden rounded-full bg-gray-200">
          <span
            className="block h-full rounded-full bg-brand-600 transition-all"
            style={{ width: `${pct}%` }}
          />
        </span>
        <Mono className="shrink-0 text-[11px] text-ink-muted">
          {t("bc.progress", { done, total: b.total, sent: b.sent })}
          {b.failed > 0 && ` · ${t("bc.failedN", { count: b.failed })}`}
          {b.blocked > 0 && ` · ${t("bc.blockedN", { count: b.blocked })}`}
          {b.skipped > 0 && ` · ${t("bc.skippedN", { count: b.skipped })}`}
        </Mono>
      </div>

      <div className="flex flex-wrap gap-2 empty:hidden">
        {b.status === "running" && (
          <Button
            variant="outline"
            color="gray"
            size="xs"
            onClick={() => onControl(() => pauseBroadcast(b.id))}
          >
            {t("bc.pause")}
          </Button>
        )}
        {b.status === "paused" && (
          <Button
            variant="outline"
            color="gray"
            size="xs"
            onClick={() => onControl(() => resumeBroadcast(b.id))}
          >
            {t("bc.resume")}
          </Button>
        )}
        {(b.status === "running" || b.status === "paused") && (
          <Button
            variant="outline"
            color="red"
            size="xs"
            onClick={() => onControl(() => cancelBroadcast(b.id))}
          >
            {t("bc.cancel")}
          </Button>
        )}
        {/* Only a finished run. Cancelling leaves the untouched recipients queued,
            so retrying a cancelled one would deliver the whole remainder the
            operator just stopped — from a button labelled as a retry of a few. */}
        {b.failed > 0 && b.status === "done" && (
          <Button
            variant="outline"
            color="gray"
            size="xs"
            onClick={() => onControl(() => retryBroadcast(b.id))}
          >
            {t("bc.retryFailed", { count: b.failed })}
          </Button>
        )}
      </div>
    </div>
  );
}
