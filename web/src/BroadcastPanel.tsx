import { useEffect, useRef, useState } from "react";
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
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  Card,
  CenterLoader,
  IconButton,
  IconClose,
  Select,
  SettingCard,
  TextInput,
  useConfirm,
} from "./ui";

const AUDIENCES: { value: BroadcastAudience; label: string }[] = [
  { value: "all", label: "Все, кто открывал бота" },
  { value: "linked", label: "Только с аккаунтом в панели" },
  { value: "unlinked", label: "Без аккаунта (не завершили регистрацию)" },
  { value: "active", label: "С активной подпиской" },
  { value: "expired", label: "С истёкшей подпиской" },
];

const STATUS: Record<Broadcast["status"], { label: string; color: string }> = {
  running: { label: "Идёт", color: "blue" },
  paused: { label: "Пауза", color: "yellow" },
  done: { label: "Завершена", color: "green" },
  cancelled: { label: "Отменена", color: "gray" },
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
  return new Date(unix * 1000).toLocaleString("ru-RU", {
    day: "2-digit",
    month: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function BroadcastPanel() {
  const [loaded, setLoaded] = useState(false);
  const [list, setList] = useState<Broadcast[]>([]);
  const [text, setText] = useState("");
  const [audience, setAudience] = useState<BroadcastAudience>("all");
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
      title: "Запустить рассылку?",
      body:
        reach === null
          ? "Сообщение уйдёт всем выбранным получателям."
          : `Сообщение уйдёт ${reach} получателям. Отменить отправленное уже нельзя.`,
      confirmLabel: "Запустить",
    });
    if (!ok) return;
    setBusy(true);
    try {
      await createBroadcast(payload, media);
      setText("");
      setButtons([]);
      clearMedia();
      await load();
      notifySuccess("Рассылка запущена");
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
      notifySuccess("Тест отправлен в привязанные админ-чаты");
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
    <div className="flex flex-col gap-4 pb-20">
      {confirmNode}
      <SettingCard
        title="Новая рассылка"
        description="Сообщение уйдёт через пользовательского бота всем, кто не отписался и не заблокировал его."
      >
        <div className="flex flex-col gap-3">
          <Select
            label="Кому"
            data={AUDIENCES}
            value={audience}
            onChange={(v) => setAudience(v as BroadcastAudience)}
          />
          <p className="text-xs text-ink-muted">
            {reach === null
              ? "Считаем получателей…"
              : `Получателей сейчас: ${reach}. Список фиксируется в момент запуска.`}
          </p>

          <HtmlEditor
            label="Текст"
            value={text}
            onChange={setText}
            rows={5}
            placeholder="Например: Плановые работы 20 июля с 03:00 до 05:00."
          />
          <p
            className={`text-xs ${overLimit ? "text-red-600" : "text-ink-muted"}`}
          >
            {[...text].length} / {limit}
            {media && " — с вложением Telegram ограничивает подпись"}
          </p>

          <div>
            <p className="mb-1 text-sm font-medium text-ink">Вложение</p>
            {/* The native file input renders its own untranslated label ("Файл не
                выбран"), which reads as a rendering fault next to styled controls.
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
                  Убрать
                </Button>
              </div>
            ) : (
              <Button
                variant="light"
                size="sm"
                onClick={() => fileRef.current?.click()}
              >
                Выбрать файл
              </Button>
            )}
            <p className="mt-1 text-xs text-ink-muted">
              Картинка придёт с текстом в подписи, любой другой файл — документом.
            </p>
          </div>

          <div className="flex flex-col gap-2">
            <p className="text-sm font-medium text-ink">Кнопки-ссылки</p>
            {buttons.map((b, i) => (
              <div key={i} className="flex items-end gap-2">
                <div className="flex-1">
                  <TextInput
                    label={i === 0 ? "Текст" : undefined}
                    value={b.text}
                    onChange={(v) =>
                      setButtons((cur) =>
                        cur.map((x, j) => (j === i ? { ...x, text: v } : x)),
                      )
                    }
                    placeholder="Инструкция"
                  />
                </div>
                <div className="flex-1">
                  <TextInput
                    label={i === 0 ? "Ссылка" : undefined}
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
                  title="Убрать кнопку"
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
                  variant="subtle"
                  size="sm"
                  onClick={() =>
                    setButtons((cur) => [...cur, { text: "", url: "" }])
                  }
                >
                  Добавить кнопку
                </Button>
              </div>
            )}
          </div>

          <div className="flex flex-wrap gap-2">
            <Button loading={busy} onClick={send} disabled={!canSend}>
              Запустить рассылку
            </Button>
            <Button
              variant="light"
              loading={testing}
              onClick={sendTest}
              disabled={!canSend}
            >
              Отправить тест
            </Button>
          </div>
          <p className="text-xs text-ink-muted">
            Тест придёт от админ-бота в привязанные админ-чаты — проверьте разметку
            и кнопки до запуска, отправленное уже не исправить. Аудитория получит
            это же сообщение от пользовательского бота.
          </p>
        </div>
      </SettingCard>

      <SettingCard title="История">
        {list.length === 0 ? (
          <p className="text-sm text-ink-muted">Рассылок ещё не было.</p>
        ) : (
          <div className="flex flex-col gap-3">
            {list.map((b) => (
              <BroadcastRow key={b.id} b={b} onControl={control} />
            ))}
          </div>
        )}
      </SettingCard>
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
  const done = b.sent + b.failed + b.blocked + b.skipped;
  const pct = b.total > 0 ? Math.round((done / b.total) * 100) : 0;
  const st = STATUS[b.status];

  return (
    <div className="rounded-lg border border-gray-200 p-3">
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <Badge color={st.color}>{st.label}</Badge>
        <span className="text-xs text-ink-muted">
          {fmtTime(b.started_at || b.created_at)}
          {b.created_by && ` · ${b.created_by}`}
        </span>
      </div>

      <p className="mb-2 line-clamp-2 text-sm text-ink">
        {b.text || <span className="text-ink-muted">(без текста)</span>}
      </p>
      {b.media_name && (
        <p className="mb-2 text-xs text-ink-muted">📎 {b.media_name}</p>
      )}

      <div className="mb-1 h-2 w-full overflow-hidden rounded-full bg-gray-200">
        <div
          className="h-full rounded-full bg-accent transition-all"
          style={{ width: `${pct}%` }}
        />
      </div>
      <p className="text-xs text-ink-muted">
        {done} из {b.total} · доставлено {b.sent}
        {b.failed > 0 && ` · ошибок ${b.failed}`}
        {b.blocked > 0 && ` · заблокировали бота ${b.blocked}`}
        {b.skipped > 0 && ` · отписались ${b.skipped}`}
      </p>

      <div className="mt-2 flex flex-wrap gap-2">
        {b.status === "running" && (
          <Button
            variant="subtle"
            size="sm"
            onClick={() => onControl(() => pauseBroadcast(b.id))}
          >
            Пауза
          </Button>
        )}
        {b.status === "paused" && (
          <Button
            variant="subtle"
            size="sm"
            onClick={() => onControl(() => resumeBroadcast(b.id))}
          >
            Продолжить
          </Button>
        )}
        {(b.status === "running" || b.status === "paused") && (
          <Button
            variant="subtle"
            color="red"
            size="sm"
            onClick={() => onControl(() => cancelBroadcast(b.id))}
          >
            Отменить
          </Button>
        )}
        {/* Only a finished run. Cancelling leaves the untouched recipients queued,
            so retrying a cancelled one would deliver the whole remainder the
            operator just stopped — from a button labelled as a retry of a few. */}
        {b.failed > 0 && b.status === "done" && (
          <Button
            variant="subtle"
            size="sm"
            onClick={() => onControl(() => retryBroadcast(b.id))}
          >
            Повторить неудачные ({b.failed})
          </Button>
        )}
      </div>
    </div>
  );
}
