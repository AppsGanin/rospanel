import { useEffect, useState } from "react";
import {
  cancelTelegramLink,
  genTelegramLink,
  getTelegram,
  getTelegramLinkStatus,
  saveTelegram,
  testTelegramBackup,
  unlinkTelegram,
} from "./api";
import { notifyError, notifySuccess, errMessage } from "./notify";
import {
  Button,
  CenterLoader,
  Code,
  IconButton,
  IconClose,
  PasswordInput,
  SaveBar,
  Select,
  SettingCard,
  Switch,
  TextInput,
} from "./ui";

// Schedule presets map a friendly choice to a cron expression. "daily"/"weekly"
// build their cron from the time/weekday inputs; "custom" takes a raw expression.
const PRESETS = [
  { value: "off", label: "Выключено" },
  { value: "hourly", label: "Каждый час" },
  { value: "every6", label: "Каждые 6 часов" },
  { value: "every12", label: "Каждые 12 часов" },
  { value: "daily", label: "Ежедневно в…" },
  { value: "weekly", label: "Еженедельно в…" },
  { value: "custom", label: "Своё (cron)" },
] as const;

const WEEKDAYS = [
  { value: "1", label: "Понедельник" },
  { value: "2", label: "Вторник" },
  { value: "3", label: "Среда" },
  { value: "4", label: "Четверг" },
  { value: "5", label: "Пятница" },
  { value: "6", label: "Суббота" },
  { value: "0", label: "Воскресенье" },
];

type Preset = (typeof PRESETS)[number]["value"];

// detectPreset reverse-maps a stored cron back into the UI controls.
function detectPreset(cron: string): {
  preset: Preset;
  time: string;
  weekday: string;
  custom: string;
} {
  const c = cron.trim();
  if (c === "") return { preset: "off", time: "03:00", weekday: "1", custom: "" };
  if (c === "0 * * * *")
    return { preset: "hourly", time: "03:00", weekday: "1", custom: "" };
  if (c === "0 */6 * * *")
    return { preset: "every6", time: "03:00", weekday: "1", custom: "" };
  if (c === "0 */12 * * *")
    return { preset: "every12", time: "03:00", weekday: "1", custom: "" };
  const daily = c.match(/^(\d{1,2}) (\d{1,2}) \* \* \*$/);
  if (daily)
    return {
      preset: "daily",
      time: hhmm(daily[2], daily[1]),
      weekday: "1",
      custom: "",
    };
  const weekly = c.match(/^(\d{1,2}) (\d{1,2}) \* \* ([0-6])$/);
  if (weekly)
    return {
      preset: "weekly",
      time: hhmm(weekly[2], weekly[1]),
      weekday: weekly[3],
      custom: "",
    };
  return { preset: "custom", time: "03:00", weekday: "1", custom: c };
}

const hhmm = (h: string, m: string) =>
  `${h.padStart(2, "0")}:${m.padStart(2, "0")}`;

// buildCron assembles the cron string from the current UI controls.
function buildCron(
  preset: Preset,
  time: string,
  weekday: string,
  custom: string,
): string {
  const [h, m] = (time || "03:00").split(":").map((x) => parseInt(x, 10) || 0);
  switch (preset) {
    case "off":
      return "";
    case "hourly":
      return "0 * * * *";
    case "every6":
      return "0 */6 * * *";
    case "every12":
      return "0 */12 * * *";
    case "daily":
      return `${m} ${h} * * *`;
    case "weekly":
      return `${m} ${h} * * ${weekday}`;
    case "custom":
      return custom.trim();
  }
}

export function TelegramSettings() {
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [token, setToken] = useState("");
  const [userEnabled, setUserEnabled] = useState(false);
  const [userToken, setUserToken] = useState("");
  const [userRegEnabled, setUserRegEnabled] = useState(true);
  const [preset, setPreset] = useState<Preset>("off");
  const [time, setTime] = useState("03:00");
  const [weekday, setWeekday] = useState("1");
  const [custom, setCustom] = useState("");
  const [chats, setChats] = useState<number[]>([]);
  const [linkCode, setLinkCode] = useState("");
  const [botUsername, setBotUsername] = useState("");
  const [userBotUsername, setUserBotUsername] = useState("");
  const [saved, setSaved] = useState({
    enabled: false,
    token: "",
    cron: "",
    userEnabled: false,
    userToken: "",
    userRegEnabled: true,
  });
  const [linking, setLinking] = useState(false);
  const [testing, setTesting] = useState(false);

  const load = () =>
    getTelegram()
      .then((t) => {
        setEnabled(t.enabled);
        setToken(t.token);
        setUserEnabled(t.user_enabled);
        setUserToken(t.user_token);
        setUserRegEnabled(t.user_reg_enabled);
        setChats(t.chat_ids || []);
        setLinkCode(t.link_code || "");
        setBotUsername(t.bot_username || "");
        setUserBotUsername(t.user_bot_username || "");
        const d = detectPreset(t.backup_cron || "");
        setPreset(d.preset);
        setTime(d.time);
        setWeekday(d.weekday);
        setCustom(d.custom);
        setSaved({
          enabled: t.enabled,
          token: t.token,
          cron: t.backup_cron || "",
          userEnabled: t.user_enabled,
          userToken: t.user_token,
          userRegEnabled: t.user_reg_enabled,
        });
      })
      .catch((e) => notifyError(errMessage(e)));

  useEffect(() => {
    load().finally(() => setLoaded(true));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // While a link code is pending (and the bot is enabled), poll the lightweight
  // status endpoint so a chat linked via the bot shows up — and the code box
  // disappears — without a reload. Disabling the bot stops the poll.
  useEffect(() => {
    if (!enabled || !linkCode) return;
    const id = setInterval(async () => {
      try {
        const st = await getTelegramLinkStatus();
        setChats(st.chat_ids || []);
        if (!st.pending) setLinkCode(""); // code consumed → linked
      } catch {
        /* ignore transient errors */
      }
    }, 3000);
    return () => clearInterval(id);
  }, [enabled, linkCode]);

  const cron = buildCron(preset, time, weekday, custom);
  const dirty =
    enabled !== saved.enabled ||
    token.trim() !== saved.token.trim() ||
    cron !== saved.cron ||
    userEnabled !== saved.userEnabled ||
    userToken.trim() !== saved.userToken.trim() ||
    userRegEnabled !== saved.userRegEnabled;

  // Linking only makes sense once the bot is enabled and that state is saved (the
  // bot polls against the persisted config; a code is redeemed by the running bot).
  const botConfigDirty =
    enabled !== saved.enabled || token.trim() !== saved.token.trim();
  const canGenerate = enabled && !botConfigDirty;

  const save = async () => {
    setBusy(true);
    try {
      await saveTelegram(
        enabled,
        token.trim(),
        cron,
        userEnabled,
        userToken.trim(),
        userRegEnabled,
      );
      setSaved({
        enabled,
        token: token.trim(),
        cron,
        userEnabled,
        userToken: userToken.trim(),
        userRegEnabled,
      });
      notifySuccess("Настройки Telegram сохранены");
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const cancel = () => {
    setEnabled(saved.enabled);
    setToken(saved.token);
    setUserEnabled(saved.userEnabled);
    setUserToken(saved.userToken);
    setUserRegEnabled(saved.userRegEnabled);
    const d = detectPreset(saved.cron);
    setPreset(d.preset);
    setTime(d.time);
    setWeekday(d.weekday);
    setCustom(d.custom);
  };

  const generate = async () => {
    setLinking(true);
    try {
      const r = await genTelegramLink();
      setLinkCode(r.code);
      if (r.bot_username) setBotUsername(r.bot_username);
      notifySuccess("Код привязки создан");
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setLinking(false);
    }
  };

  // cancelLink drops the pending code server-side and stops the poll (the X button).
  const cancelLink = async () => {
    setLinkCode("");
    try {
      await cancelTelegramLink();
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  // Turning the bot off cancels any pending link request — it can't be completed
  // while the bot isn't running.
  const onToggleEnabled = (v: boolean) => {
    setEnabled(v);
    if (!v && linkCode) {
      setLinkCode("");
      cancelTelegramLink().catch(() => {});
    }
  };

  const unlink = async (id: number) => {
    try {
      await unlinkTelegram(id);
      setChats((cur) => cur.filter((c) => c !== id));
      notifySuccess("Чат отвязан");
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  const sendTest = async () => {
    setTesting(true);
    try {
      await testTelegramBackup();
      notifySuccess("Тестовый бэкап отправлен");
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setTesting(false);
    }
  };

  if (!loaded) return <CenterLoader />;

  const startLink =
    botUsername && linkCode
      ? `https://t.me/${botUsername}?start=${linkCode}`
      : "";

  return (
    <div className="flex flex-col gap-4 pb-20">
      <SettingCard
        title="Админ-бот"
        description="Управление пользователями и бэкапы. Доступ только по коду привязки из панели."
        action={<Switch checked={enabled} onChange={onToggleEnabled} />}
      >
        <div className="flex flex-col gap-3">
          <PasswordInput
            label="Токен админ-бота (от @BotFather)"
            value={token}
            onChange={setToken}
            placeholder="123456789:AA..."
          />
          {botUsername && (
            <p className="text-sm text-ink-muted">
              Бот:{" "}
              <a
                href={`https://t.me/${botUsername}`}
                target="_blank"
                rel="noreferrer"
                className="font-medium text-brand-700 hover:underline"
              >
                @{botUsername}
              </a>
            </p>
          )}
        </div>
      </SettingCard>

      <SettingCard
        title="Привязка админ-чата"
        description="Сгенерируйте код и откройте админ-бота — только вы получите доступ к управлению панелью."
        action={
          <Button
            variant="light"
            loading={linking}
            onClick={generate}
            disabled={!canGenerate}
          >
            Сгенерировать код
          </Button>
        }
      >
        <div className="flex flex-col gap-3">
          {enabled && linkCode ? (
            <div className="relative rounded-lg border border-brand-100 bg-brand-50 p-3 pr-11">
              <div className="absolute right-1.5 top-1.5">
                <IconButton title="Отменить привязку" onClick={cancelLink}>
                  <IconClose size={18} />
                </IconButton>
              </div>
              <p className="text-sm text-ink">
                Отправьте боту: <Code>/start {linkCode}</Code>
              </p>
              {startLink && (
                <Button
                  className="mt-2"
                  size="sm"
                  href={startLink}
                  target="_blank"
                >
                  Открыть бота и привязать
                </Button>
              )}
            </div>
          ) : !enabled ? (
            <p className="text-sm text-ink-muted">
              Включите бота выше, чтобы привязать чат.
            </p>
          ) : botConfigDirty ? (
            <p className="text-sm text-ink-muted">
              Сохраните настройки, затем создавайте код привязки.
            </p>
          ) : (
            <p className="text-sm text-ink-muted">
              Активного кода нет. Нажмите «Сгенерировать код».
            </p>
          )}

          <div>
            <p className="mb-1 text-sm font-medium text-ink">
              Привязанные чаты ({chats.length})
            </p>
            {chats.length === 0 ? (
              <p className="text-sm text-ink-muted">Пока ни одного.</p>
            ) : (
              <div className="flex flex-col gap-2">
                {chats.map((id) => (
                  <div
                    key={id}
                    className="flex items-center justify-between rounded-lg border border-gray-200 px-3 py-2"
                  >
                    <span className="font-mono text-sm text-ink">{id}</span>
                    <Button
                      variant="subtle"
                      color="red"
                      size="sm"
                      onClick={() => unlink(id)}
                    >
                      Отвязать
                    </Button>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      </SettingCard>

      <SettingCard
        title="Пользовательский бот"
        description="Открытый бот для семьи и друзей: регистрация, подписка и статистика. Создайте второго бота у @BotFather."
        action={<Switch checked={userEnabled} onChange={setUserEnabled} />}
      >
        <div className="flex flex-col gap-3">
          <PasswordInput
            label="Токен пользовательского бота"
            value={userToken}
            onChange={setUserToken}
            placeholder="987654321:BB..."
          />
          {userBotUsername && (
            <p className="text-sm text-ink-muted">
              Бот:{" "}
              <a
                href={`https://t.me/${userBotUsername}`}
                target="_blank"
                rel="noreferrer"
                className="font-medium text-brand-700 hover:underline"
              >
                @{userBotUsername}
              </a>
            </p>
          )}
          <div className="flex items-center justify-between gap-3">
            <div>
              <p className="text-sm font-medium text-ink">
                Самостоятельная регистрация
              </p>
              <p className="text-xs text-ink-muted">
                Новые пользователи нажимают «Зарегистрироваться» в боте и получают
                аккаунт.
              </p>
            </div>
            <Switch
              checked={userRegEnabled}
              onChange={setUserRegEnabled}
              disabled={!userEnabled}
            />
          </div>
        </div>
      </SettingCard>

      <SettingCard
        title="Бэкапы по расписанию"
        description="Резервные копии отправляются во все привязанные чаты по расписанию (в часовом поясе панели)."
      >
        <div className="flex flex-col gap-3">
          <Select
            label="Расписание"
            value={preset}
            onChange={(v) => setPreset(v as Preset)}
            data={PRESETS as unknown as { value: string; label: string }[]}
          />
          {(preset === "daily" || preset === "weekly") && (
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              {preset === "weekly" && (
                <Select
                  label="День недели"
                  value={weekday}
                  onChange={setWeekday}
                  data={WEEKDAYS}
                />
              )}
              <TextInput
                label="Время"
                type="time"
                value={time}
                onChange={setTime}
              />
            </div>
          )}
          {preset === "custom" && (
            <TextInput
              label="Cron-выражение"
              value={custom}
              onChange={setCustom}
              mono
              placeholder="0 3 * * *"
            />
          )}
          <p className="text-xs text-ink-muted">
            {cron ? (
              <>
                Cron: <span className="font-mono">{cron}</span>
              </>
            ) : (
              "Автоматические бэкапы выключены."
            )}
          </p>
          <div>
            <Button
              variant="light"
              loading={testing}
              onClick={sendTest}
              disabled={chats.length === 0 || !token.trim()}
            >
              Отправить тестовый бэкап
            </Button>
            {(chats.length === 0 || !token.trim()) && (
              <p className="mt-1 text-xs text-ink-muted">
                Нужен токен и хотя бы один привязанный чат.
              </p>
            )}
          </div>
        </div>
      </SettingCard>

      <SaveBar
        dirty={dirty}
        busy={busy}
        onSave={save}
        onCancel={cancel}
        saveDisabled={(enabled && !token.trim()) || (userEnabled && !userToken.trim())}
      />
    </div>
  );
}
