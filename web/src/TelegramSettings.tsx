import { useEffect, useState } from "react";
import {
  cancelTelegramLink,
  checkTelegramSupport,
  genTelegramLink,
  getTelegram,
  getTelegramLinkStatus,
  type RegMode,
  saveTelegram,
  testTelegramBackup,
  unlinkTelegram,
} from "./api";
import {
  buildCron,
  CronPicker,
  detectPreset,
  EMPTY_SCHEDULE,
  type Schedule,
} from "./CronPicker";
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
  Textarea,
  TextInput,
} from "./ui";

// ADMIN_EVENTS are the admin-bot notification categories shown as toggles. Keys
// must match model.AdminEventCatalog on the backend.
const ADMIN_EVENTS: { key: string; label: string; desc?: string }[] = [
  {
    key: "registered",
    label: "Новая регистрация",
    desc: "Пользователь зарегистрировался в боте",
  },
  { key: "expired", label: "Подписка истекла" },
  { key: "limited", label: "Исчерпан трафик" },
  { key: "device_limited", label: "Превышен лимит устройств" },
  {
    key: "xray_down",
    label: "Сбой Xray",
    desc: "Прокси-процесс упал и был перезапущен",
  },
  {
    key: "cert",
    label: "Сертификат TLS",
    desc: "Успешное продление или ошибка выпуска",
  },
  { key: "payment", label: "Платежи", desc: "Новые заказы и подтверждённые оплаты" },
];

type AdminEvents = Record<string, boolean>;

// sameEvents compares two category maps over the known keys (order-independent).
const sameEvents = (a: AdminEvents, b: AdminEvents) =>
  ADMIN_EVENTS.every((e) => !!a[e.key] === !!b[e.key]);

export function TelegramSettings() {
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [token, setToken] = useState("");
  const [userEnabled, setUserEnabled] = useState(false);
  const [userToken, setUserToken] = useState("");
  const [userRegMode, setUserRegMode] = useState<RegMode>("off");
  const [userRegCode, setUserRegCode] = useState("");
  const [adminEvents, setAdminEvents] = useState<AdminEvents>({});
  const [schedule, setSchedule] = useState<Schedule>(EMPTY_SCHEDULE);
  const [chats, setChats] = useState<number[]>([]);
  const [linkCode, setLinkCode] = useState("");
  const [botUsername, setBotUsername] = useState("");
  const [userBotUsername, setUserBotUsername] = useState("");
  const [supportEnabled, setSupportEnabled] = useState(false);
  const [supportToken, setSupportToken] = useState("");
  const [supportGroupID, setSupportGroupID] = useState("");
  const [supportGreeting, setSupportGreeting] = useState("");
  const [supportBotUsername, setSupportBotUsername] = useState("");
  const [saved, setSaved] = useState({
    enabled: false,
    token: "",
    cron: "",
    userEnabled: false,
    userToken: "",
    userRegMode: "off" as RegMode,
    userRegCode: "",
    adminEvents: {} as AdminEvents,
    supportEnabled: false,
    supportToken: "",
    supportGroupID: "",
    supportGreeting: "",
  });
  const [linking, setLinking] = useState(false);
  const [testing, setTesting] = useState(false);
  const [checking, setChecking] = useState(false);

  const load = () =>
    getTelegram()
      .then((t) => {
        setEnabled(t.enabled);
        setToken(t.token);
        setUserEnabled(t.user_enabled);
        setUserToken(t.user_token);
        setUserRegMode(t.user_reg_mode || "off");
        setUserRegCode(t.user_reg_code || "");
        setAdminEvents(t.admin_events || {});
        setChats(t.chat_ids || []);
        setLinkCode(t.link_code || "");
        setBotUsername(t.bot_username || "");
        setUserBotUsername(t.user_bot_username || "");
        setSchedule(detectPreset(t.backup_cron || ""));
        const groupID = t.support_group_id ? String(t.support_group_id) : "";
        setSupportEnabled(t.support_enabled);
        setSupportToken(t.support_token || "");
        setSupportGroupID(groupID);
        setSupportGreeting(t.support_greeting || "");
        setSupportBotUsername(t.support_bot_username || "");
        setSaved({
          enabled: t.enabled,
          token: t.token,
          cron: t.backup_cron || "",
          userEnabled: t.user_enabled,
          userToken: t.user_token,
          userRegMode: t.user_reg_mode || "off",
          userRegCode: t.user_reg_code || "",
          adminEvents: t.admin_events || {},
          supportEnabled: t.support_enabled,
          supportToken: t.support_token || "",
          supportGroupID: groupID,
          supportGreeting: t.support_greeting || "",
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

  const cron = buildCron(schedule);
  const dirty =
    enabled !== saved.enabled ||
    token.trim() !== saved.token.trim() ||
    cron !== saved.cron ||
    userEnabled !== saved.userEnabled ||
    userToken.trim() !== saved.userToken.trim() ||
    userRegMode !== saved.userRegMode ||
    userRegCode.trim() !== saved.userRegCode.trim() ||
    !sameEvents(adminEvents, saved.adminEvents) ||
    supportEnabled !== saved.supportEnabled ||
    supportToken.trim() !== saved.supportToken.trim() ||
    supportGroupID.trim() !== saved.supportGroupID.trim() ||
    supportGreeting.trim() !== saved.supportGreeting.trim();

  // Linking only makes sense once the bot is enabled and that state is saved (the
  // bot polls against the persisted config; a code is redeemed by the running bot).
  const botConfigDirty =
    enabled !== saved.enabled || token.trim() !== saved.token.trim();
  const canGenerate = enabled && !botConfigDirty;

  const save = async () => {
    setBusy(true);
    try {
      await saveTelegram({
        enabled,
        token: token.trim(),
        backup_cron: cron,
        user_enabled: userEnabled,
        user_token: userToken.trim(),
        user_reg_mode: userRegMode,
        user_reg_code: userRegCode.trim(),
        admin_events: adminEvents,
        support_enabled: supportEnabled,
        support_token: supportToken.trim(),
        support_group_id: Number(supportGroupID.trim()) || 0,
        support_greeting: supportGreeting.trim(),
      });
      setSaved({
        enabled,
        token: token.trim(),
        cron,
        userEnabled,
        userToken: userToken.trim(),
        userRegMode,
        userRegCode: userRegCode.trim(),
        adminEvents,
        supportEnabled,
        supportToken: supportToken.trim(),
        supportGroupID: supportGroupID.trim(),
        supportGreeting: supportGreeting.trim(),
      });
      // The support bot's @username is resolved server-side during the save, so pull
      // the fresh value back rather than leaving a stale one on screen.
      await load();
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
    setUserRegMode(saved.userRegMode);
    setUserRegCode(saved.userRegCode);
    setAdminEvents(saved.adminEvents);
    setSchedule(detectPreset(saved.cron));
    setSupportEnabled(saved.supportEnabled);
    setSupportToken(saved.supportToken);
    setSupportGroupID(saved.supportGroupID);
    setSupportGreeting(saved.supportGreeting);
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

  // Checking runs against the SAVED config, so an unsaved edit would be checked in
  // its old state — refuse rather than report a misleading result.
  const supportConfigDirty =
    supportToken.trim() !== saved.supportToken.trim() ||
    supportGroupID.trim() !== saved.supportGroupID.trim();

  const runCheck = async () => {
    setChecking(true);
    try {
      const r = await checkTelegramSupport();
      setSupportBotUsername(r.bot_username || supportBotUsername);
      notifySuccess(
        r.group_title
          ? `Всё готово: @${r.bot_username} — администратор группы «${r.group_title}»`
          : "Проверка прошла успешно",
      );
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setChecking(false);
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
                className="font-medium text-accent hover:underline"
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
            <div className="relative rounded-lg border border-accent accent-tint p-3 pr-11">
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
        title="Уведомления админу"
        description="Какие события админ-бот присылает в привязанные чаты."
      >
        <div className="flex flex-col gap-3">
          {ADMIN_EVENTS.map((e) => (
            <div
              key={e.key}
              className="flex items-center justify-between gap-3"
            >
              <div>
                <p className="text-sm font-medium text-ink">{e.label}</p>
                {e.desc && (
                  <p className="text-xs text-ink-muted">{e.desc}</p>
                )}
              </div>
              <Switch
                checked={!!adminEvents[e.key]}
                onChange={(v) =>
                  setAdminEvents((cur) => ({ ...cur, [e.key]: v }))
                }
                disabled={!enabled}
              />
            </div>
          ))}
        </div>
      </SettingCard>

      <SettingCard
        title="Пользовательский бот"
        description="Открытый бот для семьи и друзей: регистрация, подписка и статистика."
        action={<Switch checked={userEnabled} onChange={setUserEnabled} />}
      >
        <div className="flex flex-col gap-3">
          <PasswordInput
            label="Токен пользовательского бота (от @BotFather)"
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
                className="font-medium text-accent hover:underline"
              >
                @{userBotUsername}
              </a>
            </p>
          )}
          <div className="flex flex-col gap-2">
            <div>
              <p className="text-sm font-medium text-ink">
                Самостоятельная регистрация
              </p>
              <p className="text-xs text-ink-muted">
                Как новые пользователи получают аккаунт по кнопке
                «Зарегистрироваться». Привязка существующего аккаунта по коду из
                карточки пользователя работает при любом режиме.
              </p>
            </div>
            <Select
              data={[
                { value: "off", label: "Закрыта" },
                { value: "open", label: "Открыта — сразу активен" },
                { value: "moderation", label: "С модерацией (одобряет админ)" },
                { value: "invite", label: "По коду-приглашению" },
              ]}
              value={userRegMode}
              onChange={(v) => setUserRegMode(v as RegMode)}
            />
            {userRegMode === "moderation" && (
              <p className="text-xs text-ink-muted">
                Аккаунт создаётся выключенным. Админу приходит заявка с кнопками
                «Одобрить / Отклонить» (в админ-боте), либо включите пользователя
                вручную в списке.
              </p>
            )}
            {userRegMode === "invite" && (
              <TextInput
                label="Код-приглашение"
                value={userRegCode}
                onChange={setUserRegCode}
                placeholder="например, VPN2026"
                disabled={!userEnabled}
              />
            )}
          </div>
        </div>
      </SettingCard>

      <SettingCard
        title="Поддержка"
        description="Отдельный бот для обращений: пользователь пишет ему, сообщение попадает в отдельную тему группы, ответ в теме уходит обратно пользователю."
        action={<Switch checked={supportEnabled} onChange={setSupportEnabled} />}
      >
        <div className="flex flex-col gap-3">
          <PasswordInput
            label="Токен бота поддержки (от @BotFather)"
            value={supportToken}
            onChange={setSupportToken}
            placeholder="555555555:CC..."
          />
          {supportBotUsername && (
            <p className="text-sm text-ink-muted">
              Бот:{" "}
              <a
                href={`https://t.me/${supportBotUsername}`}
                target="_blank"
                rel="noreferrer"
                className="font-medium text-accent hover:underline"
              >
                @{supportBotUsername}
              </a>
            </p>
          )}
          <TextInput
            label="ID группы поддержки"
            value={supportGroupID}
            onChange={setSupportGroupID}
            placeholder="-1001234567890"
          />
          <p className="text-xs text-ink-muted">
            Создайте супергруппу, включите в её настройках «Темы» и добавьте бота
            поддержки администратором с правом управления темами. Без прав
            администратора бот не увидит ответы — Telegram скрывает от него
            сообщения в группе.
          </p>
          <p className="text-xs text-ink-muted">
            <b>Держите группу закрытой.</b> Бот отправляет пользователю любое
            сообщение из его темы, кем бы оно ни было написано: попавший в группу
            посторонний прочитает всю переписку и сможет писать клиентам от имени
            поддержки.
          </p>
          <Textarea
            label="Приветствие в боте поддержки"
            value={supportGreeting}
            onChange={setSupportGreeting}
            rows={2}
            placeholder="Опишите проблему — ответим в течение дня."
            hint="Пустое поле — текст по умолчанию, без обещаний о сроках ответа."
          />
          <div>
            <Button
              variant="light"
              loading={checking}
              onClick={runCheck}
              disabled={
                !supportToken.trim() ||
                !supportGroupID.trim() ||
                supportConfigDirty
              }
            >
              Проверить
            </Button>
            <p className="mt-1 text-xs text-ink-muted">
              {supportConfigDirty
                ? "Сохраните настройки, затем запускайте проверку."
                : "Проверит доступность группы, включённые темы и права бота."}
            </p>
          </div>
        </div>
      </SettingCard>

      <SettingCard
        title="Бэкапы по расписанию"
        description="Резервные копии отправляются во все привязанные чаты по расписанию (в часовом поясе панели)."
      >
        <div className="flex flex-col gap-3">
          <CronPicker
            value={schedule}
            onChange={setSchedule}
            offLabel="Автоматические бэкапы выключены."
          />
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
        saveDisabled={
          (enabled && !token.trim()) ||
          (userEnabled && !userToken.trim()) ||
          (supportEnabled && (!supportToken.trim() || !supportGroupID.trim()))
        }
      />
    </div>
  );
}
