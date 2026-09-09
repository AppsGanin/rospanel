import { useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import {
  cancelTelegramLink,
  checkTelegramSupport,
  genTelegramLink,
  getTelegram,
  getTelegramLinkStatus,
  listSupportGroups,
  type RegMode,
  type SupportGroup,
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
import i18n, { LANGS } from "./i18n";
import { notifyError, notifySuccess, errMessage } from "./notify";
import {
  Button,
  CenterLoader,
  Code,
  IconButton,
  IconClose,
  Mono,
  Panel,
  PasswordInput,
  SaveBar,
  Select,
  SettingRow,
  Switch,
  TextInput,
  Textarea,
  ToggleRow,
} from "./ui";

// ADMIN_EVENTS are the admin-bot notification categories shown as toggles. Keys
// must match model.AdminEventCatalog on the backend.
const ADMIN_EVENTS: { key: string; label: string; desc?: string }[] = [
  {
    key: "registered",
    label: "tg.evRegLabel",
    desc: "tg.evRegDesc",
  },
  { key: "expired", label: "tg.evExpired", desc: "tg.evExpiredDesc" },
  { key: "limited", label: "tg.evLimited", desc: "tg.evLimitedDesc" },
  {
    key: "device_limited",
    label: "tg.evDeviceLimited",
    desc: "tg.evDeviceLimitedDesc",
  },
  {
    key: "xray_down",
    label: "tg.evXrayLabel",
    desc: "tg.evXrayDesc",
  },
  {
    key: "cert",
    label: "tg.evTlsLabel",
    desc: "tg.evTlsDesc",
  },
  { key: "payment", label: "tg.evPaymentLabel", desc: "tg.evPaymentDesc" },
  {
    key: "abuse",
    label: "tg.evAbuseLabel",
    desc: "tg.evAbuseDesc",
  },
  {
    key: "probe",
    label: "tg.evProbeLabel",
    desc: "tg.evProbeDesc",
  },
  {
    key: "login",
    label: "tg.evLoginLabel",
    desc: "tg.evLoginDesc",
  },
  {
    key: "node_traffic",
    label: "tg.evNodeTrafficLabel",
    desc: "tg.evNodeTrafficDesc",
  },
];

// USER_EVENTS are what the user bot tells the person themselves. Keys must match
// model.UserNotifyCatalog on the backend.
const USER_EVENTS: { key: string; label: string; desc?: string }[] = [
  {
    key: "expiring",
    label: "tg.uEndingLabel",
    desc: "tg.uEndingDesc",
  },
  { key: "expired", label: "tg.evExpired", desc: "tg.uExpiredDesc" },
  {
    key: "traffic_low",
    label: "tg.uLowTrafficLabel",
    desc: "tg.uLowTrafficDesc",
  },
  { key: "limited", label: "tg.uOutOfTraffic", desc: "tg.uOutOfTrafficDesc" },
  {
    key: "device_limited",
    label: "tg.uDevicesLabel",
    desc: "tg.uDevicesDesc",
  },
  {
    key: "disabled",
    label: "tg.uSuspendedLabel",
    desc: "tg.uSuspendedDesc",
  },
  { key: "payment", label: "tg.uPaymentLabel", desc: "tg.uPaymentDesc" },
  {
    key: "registration",
    label: "tg.uDecisionLabel",
    desc: "tg.uDecisionDesc",
  },
];

const expiringDayOptions = () => [1, 3, 7, 14].map((d) => ({
  value: String(d),
  label: i18n.t("tg.daysBefore", { count: d }),
}));

type AdminEvents = Record<string, boolean>;

// groupIssue labels a candidate that cannot work yet, so the reason is visible at
// the moment of choosing rather than after clicking Check.
function groupIssue(g: SupportGroup): string {
  if (!g.is_forum) return ` — ${i18n.t("tg.noTopics")}`;
  if (!g.is_admin) return ` — ${i18n.t("tg.notAdmin")}`;
  return "";
}

// sameEvents compares two category maps over the known keys (order-independent).
const sameEvents = (
  a: AdminEvents,
  b: AdminEvents,
  keys: { key: string }[] = ADMIN_EVENTS,
) => keys.every((e) => !!a[e.key] === !!b[e.key]);

export function TelegramSettings() {
  const { t } = useTranslation();
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [token, setToken] = useState("");
  const [userEnabled, setUserEnabled] = useState(false);
  const [userToken, setUserToken] = useState("");
  const [userRegMode, setUserRegMode] = useState<RegMode>("off");
  const [userRegCode, setUserRegCode] = useState("");
  const [adminEvents, setAdminEvents] = useState<AdminEvents>({});
  const [userEvents, setUserEvents] = useState<AdminEvents>({});
  const [expiringDays, setExpiringDays] = useState("3");
  const [botLang, setBotLang] = useState("ru");
  const [proxy, setProxy] = useState("");
  const [proxyMode, setProxyMode] = useState("direct");
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
  const [supportGroups, setSupportGroups] = useState<SupportGroup[]>([]);
  const [manualGroup, setManualGroup] = useState(false);
  const [saved, setSaved] = useState({
    enabled: false,
    token: "",
    cron: "",
    userEnabled: false,
    userToken: "",
    userRegMode: "off" as RegMode,
    userRegCode: "",
    adminEvents: {} as AdminEvents,
    userEvents: {} as AdminEvents,
    expiringDays: "3",
    botLang: "ru",
    supportEnabled: false,
    supportToken: "",
    supportGroupID: "",
    supportGreeting: "",
    proxy: "",
    proxyMode: "direct",
  });
  const [linking, setLinking] = useState(false);
  const [testing, setTesting] = useState(false);
  const [checking, setChecking] = useState(false);

  const load = () =>
    getTelegram()
      .then((cfg) => {
        setEnabled(cfg.enabled);
        setToken(cfg.token);
        setUserEnabled(cfg.user_enabled);
        setUserToken(cfg.user_token);
        setUserRegMode(cfg.user_reg_mode || "off");
        setUserRegCode(cfg.user_reg_code || "");
        setAdminEvents(cfg.admin_events || {});
        setUserEvents(cfg.user_events || {});
        setExpiringDays(String(cfg.user_expiring_days || 3));
        setChats(cfg.chat_ids || []);
        setLinkCode(cfg.link_code || "");
        setBotUsername(cfg.bot_username || "");
        setUserBotUsername(cfg.user_bot_username || "");
        setSchedule(detectPreset(cfg.backup_cron || ""));
        setBotLang(cfg.lang || "ru");
        setProxy(cfg.proxy || "");
        setProxyMode(cfg.proxy_mode || "direct");
        const groupID = cfg.support_group_id ? String(cfg.support_group_id) : "";
        setSupportEnabled(cfg.support_enabled);
        setSupportToken(cfg.support_token || "");
        setSupportGroupID(groupID);
        setSupportGreeting(cfg.support_greeting || "");
        setSupportBotUsername(cfg.support_bot_username || "");
        setSaved({
          enabled: cfg.enabled,
          token: cfg.token,
          cron: cfg.backup_cron || "",
          botLang: cfg.lang || "ru",
          userEnabled: cfg.user_enabled,
          userToken: cfg.user_token,
          userRegMode: cfg.user_reg_mode || "off",
          userRegCode: cfg.user_reg_code || "",
          adminEvents: cfg.admin_events || {},
          userEvents: cfg.user_events || {},
          expiringDays: String(cfg.user_expiring_days || 3),
          supportEnabled: cfg.support_enabled,
          supportToken: cfg.support_token || "",
          supportGroupID: groupID,
          supportGreeting: cfg.support_greeting || "",
          proxy: cfg.proxy || "",
          proxyMode: cfg.proxy_mode || "direct",
        });
      })
      .catch((e) => notifyError(errMessage(e)));

  const loadGroups = () =>
    listSupportGroups()
      .then(setSupportGroups)
      .catch(() => {
        /* transient — the poll below retries */
      });

  // biome-ignore lint/correctness/useExhaustiveDependencies: runs once on mount; the loader is redefined every render, so listing it would refetch in a loop
  useEffect(() => {
    load().finally(() => setLoaded(true));
    loadGroups();
  }, []);

  // While the operator is setting support up, they are alt-tabbing to Telegram to
  // add the bot to a group. Poll so it appears in the picker on its own instead of
  // needing a page reload to show up.
  // biome-ignore lint/correctness/useExhaustiveDependencies: starts and stops the poll on the two fields that gate it; loadGroups is redefined every render and would restart the interval on each one
  useEffect(() => {
    if (!supportToken.trim() || supportGroupID) return;
    const id = setInterval(loadGroups, 4000);
    return () => clearInterval(id);
  }, [supportToken, supportGroupID]);

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
    !sameEvents(userEvents, saved.userEvents, USER_EVENTS) ||
    expiringDays !== saved.expiringDays ||
    botLang !== saved.botLang ||
    supportEnabled !== saved.supportEnabled ||
    supportToken.trim() !== saved.supportToken.trim() ||
    supportGroupID.trim() !== saved.supportGroupID.trim() ||
    supportGreeting.trim() !== saved.supportGreeting.trim() ||
    proxy.trim() !== saved.proxy.trim() ||
    proxyMode !== saved.proxyMode;

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
        lang: botLang,
        user_enabled: userEnabled,
        user_token: userToken.trim(),
        user_reg_mode: userRegMode,
        user_reg_code: userRegCode.trim(),
        admin_events: adminEvents,
        user_events: userEvents,
        user_expiring_days: Number(expiringDays) || 3,
        support_enabled: supportEnabled,
        support_token: supportToken.trim(),
        support_group_id: Number(supportGroupID.trim()) || 0,
        support_greeting: supportGreeting.trim(),
        proxy_mode: proxyMode,
        proxy: proxy.trim(),
      });
      setSaved({
        enabled,
        token: token.trim(),
        cron,
        botLang,
        userEnabled,
        userToken: userToken.trim(),
        userRegMode,
        userRegCode: userRegCode.trim(),
        adminEvents,
        userEvents,
        expiringDays,
        supportEnabled,
        supportToken: supportToken.trim(),
        supportGroupID: supportGroupID.trim(),
        supportGreeting: supportGreeting.trim(),
        proxy: proxy.trim(),
        proxyMode,
      });
      // The support bot's @username is resolved server-side during the save, so pull
      // the fresh value back rather than leaving a stale one on screen.
      await load();
      // Turning the user bot on or off changes which surfaces exist elsewhere (the
      // broadcast tab, the per-user send button). Same event pattern the billing
      // toggle uses, so they update without a reload.
      window.dispatchEvent(new Event("rospanel:telegram-changed"));
      notifySuccess(t("tg.saved"));
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
    setUserEvents(saved.userEvents);
    setExpiringDays(saved.expiringDays);
    setBotLang(saved.botLang);
    setSchedule(detectPreset(saved.cron));
    setSupportEnabled(saved.supportEnabled);
    setSupportToken(saved.supportToken);
    setSupportGroupID(saved.supportGroupID);
    setSupportGreeting(saved.supportGreeting);
    setProxy(saved.proxy);
    setProxyMode(saved.proxyMode);
  };

  const generate = async () => {
    setLinking(true);
    try {
      const r = await genTelegramLink();
      setLinkCode(r.code);
      if (r.bot_username) setBotUsername(r.bot_username);
      notifySuccess(t("tg.codeCreated"));
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
      notifySuccess(t("tg.chatUnlinked"));
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  const sendTest = async () => {
    setTesting(true);
    try {
      await testTelegramBackup();
      notifySuccess(t("tg.testBackupSent"));
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
          ? t("tg.checkOkDetailed", { bot: r.bot_username, group: r.group_title })
          : t("tg.checkOk"),
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

  // Every bot says which account it turned out to be — the token alone does not.
  const botLink = (username: string) => (
    <SettingRow
      label={t("tg.botIs")}
      control={
        <a
          href={`https://t.me/${username}`}
          target="_blank"
          rel="noreferrer"
          className="text-xs font-medium text-accent hover:underline"
        >
          @{username}
        </a>
      }
    />
  );

  // A saved group the candidate list does not contain — the usual case, since
  // Telegram never replays "the bot was added" and a group joined earlier stays
  // invisible until something happens in it. The id is set either way, so it has to
  // be shown: an empty-looking field next to a working support bot reads as lost
  // configuration.
  const savedGroup = supportGroupID.trim();
  const savedListed = supportGroups.some((g) => String(g.chat_id) === savedGroup);
  const showManualGroup = manualGroup || (!!savedGroup && !savedListed && supportGroups.length === 0);

  return (
    <div className="flex flex-1 flex-col gap-3.5">
      {/* First on the page because it decides whether anything below it can work
          at all: on a server that cannot reach Telegram, all three bots go silent
          and the subscription page's "open in app" buttons die with them. */}
      <Panel title={t("tg.proxy")}>
        <SettingRow hint={t("tg.proxyHint")} />
        {/* Two choices only. WARP and Opera are not modes here: Routing owns those
            egresses and publishes the address of each one it has running, so
            sending Telegram through one is just pasting it below. */}
        <SettingRow
          label={t("tg.proxyMode")}
          field={
            <Select
              value={proxyMode}
              onChange={setProxyMode}
              data={[
                { value: "direct", label: t("tg.proxyModeDirect") },
                { value: "custom", label: t("tg.proxyModeCustom") },
              ]}
            />
          }
        />
        {proxyMode === "custom" && (
          <SettingRow
            label={t("tg.proxyUrl")}
            wideField
            field={
              <PasswordInput
                value={proxy}
                onChange={setProxy}
                placeholder="socks5://127.0.0.1:1080"
              />
            }
          />
        )}
      </Panel>

      <Panel
        title={t("tg.adminBot")}
        aside={<Switch checked={enabled} onChange={onToggleEnabled} />}
      >
        <SettingRow hint={t("tg.adminBotHint")} />
        <SettingRow
          label={t("tg.adminToken")}
          wideField
          field={
            <PasswordInput
              value={token}
              onChange={setToken}
              placeholder="123456789:AA..."
            />
          }
        />
        {/* Panel-wide on purpose: the admin bot also pushes unprompted alerts,
            which carry no Telegram update to read a language from. The client and
            support bots ignore this and follow each person's own language. */}
        <SettingRow
          label={t("tg.botLang")}
          hint={t("tg.botLangHint")}
          field={
            <Select
              value={botLang}
              onChange={setBotLang}
              data={LANGS.map((l) => ({ value: l.code, label: l.label }))}
            />
          }
        />
        {botUsername && botLink(botUsername)}
      </Panel>

      <Panel
        title={t("tg.linkChat")}
        aside={
          <Button
            size="xs"
            variant="light"
            loading={linking}
            onClick={generate}
            disabled={!canGenerate}
          >
            {t("tg.generateCode")}
          </Button>
        }
      >
        <SettingRow hint={t("tg.linkChatHint")} />
        {enabled && linkCode ? (
          <SettingRow
            control={
              <IconButton title={t("tg.cancelLink")} onClick={cancelLink}>
                <IconClose size={18} />
              </IconButton>
            }
          >
            <div className="accent-tint rounded-lg border border-accent p-2.5">
              <p className="text-xs text-ink">
                {t("tg.sendToBot")} <Code>/start {linkCode}</Code>
              </p>
              {startLink && (
                <Button className="mt-2" size="xs" href={startLink} target="_blank">
                  {t("tg.openBotAndLink")}
                </Button>
              )}
            </div>
          </SettingRow>
        ) : (
          <SettingRow
            hint={
              !enabled
                ? t("tg.enableBotFirst")
                : botConfigDirty
                  ? t("tg.saveThenCode")
                  : t("tg.noActiveCode")
            }
          />
        )}
        {chats.length === 0 ? (
          <SettingRow
            label={t("tg.linkedChats", { count: chats.length })}
            hint={t("tg.noneYet")}
          />
        ) : (
          <>
            <SettingRow label={t("tg.linkedChats", { count: chats.length })} />
            {chats.map((id) => (
              <div
                key={id}
                className="flex items-center justify-between gap-3 border-t border-gray-100 px-3.5 py-[7px]"
              >
                <Mono className="truncate text-xs text-ink">{id}</Mono>
                <IconButton
                  color="red"
                  title={t("userDetail.unlink")}
                  onClick={() => unlink(id)}
                >
                  <IconClose size={16} />
                </IconButton>
              </div>
            ))}
          </>
        )}
      </Panel>

      <Panel title={t("tg.adminNotifs")}>
        <SettingRow hint={t("tg.adminNotifsHint")} />
        {ADMIN_EVENTS.map((e) => (
          <ToggleRow
            key={e.key}
            label={t(e.label as "tg.evExpired")}
            hint={e.desc ? t(e.desc as "tg.evRegDesc") : undefined}
            checked={!!adminEvents[e.key]}
            onChange={(v) => setAdminEvents((cur) => ({ ...cur, [e.key]: v }))}
            disabled={!enabled}
          />
        ))}
      </Panel>

      <Panel
        title={t("tg.userBot")}
        aside={<Switch checked={userEnabled} onChange={setUserEnabled} />}
      >
        <SettingRow hint={t("tg.userBotHint")} />
        <SettingRow
          label={t("tg.userToken")}
          wideField
          field={
            <PasswordInput
              value={userToken}
              onChange={setUserToken}
              placeholder="987654321:BB..."
            />
          }
        />
        {userBotUsername && botLink(userBotUsername)}
        <SettingRow
          label={t("tg.selfSignup")}
          hint={
            userRegMode === "moderation"
              ? t("tg.moderationHint")
              : t("tg.selfSignupHint")
          }
          field={
            <Select
              data={[
                { value: "off", label: t("tg.regOff") },
                { value: "open", label: t("tg.regOpen") },
                { value: "moderation", label: t("tg.regModeration") },
                { value: "invite", label: t("tg.regInvite") },
              ]}
              value={userRegMode}
              onChange={(v) => setUserRegMode(v as RegMode)}
            />
          }
        />
        {userRegMode === "invite" && (
          <SettingRow
            label={t("tg.inviteCode")}
            field={
              <TextInput
                value={userRegCode}
                onChange={setUserRegCode}
                placeholder={t("tg.invitePlaceholder")}
                disabled={!userEnabled}
              />
            }
          />
        )}
      </Panel>

      <Panel title={t("tg.userNotifs")}>
        <SettingRow hint={t("tg.userNotifsHint")} />
        {!userEnabled && (
          <SettingRow
            hint={<span className="text-warning">{t("tg.userBotOff")}</span>}
          />
        )}
        {USER_EVENTS.map((e) => (
          <ToggleRow
            key={e.key}
            label={t(e.label as "tg.evExpired")}
            hint={e.desc ? t(e.desc as "tg.evRegDesc") : undefined}
            checked={!!userEvents[e.key]}
            onChange={(v) => setUserEvents((cur) => ({ ...cur, [e.key]: v }))}
            disabled={!userEnabled}
          />
        ))}
        {/* How early "your subscription is ending" goes out — a setting of the
            notice above it, so it follows it and only when it is on. */}
        {userEvents.expiring && (
          <SettingRow
            label={t("tg.remindDays")}
            field={
              <Select
                data={expiringDayOptions()}
                value={expiringDays}
                onChange={setExpiringDays}
              />
            }
          />
        )}
      </Panel>

      <Panel
        title={t("tg.support")}
        aside={<Switch checked={supportEnabled} onChange={setSupportEnabled} />}
      >
        <SettingRow hint={t("tg.supportHint")} />
        <SettingRow
          label={t("tg.supportToken")}
          wideField
          field={
            <PasswordInput
              value={supportToken}
              onChange={setSupportToken}
              placeholder="555555555:CC..."
            />
          }
        />
        {supportBotUsername && botLink(supportBotUsername)}
        {showManualGroup ? (
          <SettingRow
            label={t("tg.supportGroupId")}
            hint={
              <>
                {t("tg.groupIdHint")}{" "}
                {/* Only when there is a list to go back to: with no candidates the
                    link would switch to a picker with nothing in it. */}
                {supportGroups.length > 0 && (
                  <button
                    type="button"
                    className="text-accent hover:underline"
                    onClick={() => setManualGroup(false)}
                  >
                    {t("tg.pickFromList")}
                  </button>
                )}
              </>
            }
            field={
              <TextInput
                value={supportGroupID}
                onChange={setSupportGroupID}
                placeholder="-1001234567890"
                mono
              />
            }
          />
        ) : supportGroups.length > 0 ? (
          <SettingRow
            label={t("tg.supportGroup")}
            hint={
              <>
                {t("tg.groupsWithBot")}{" "}
                <button
                  type="button"
                  className="text-accent hover:underline"
                  onClick={() => setManualGroup(true)}
                >
                  {t("tg.enterIdManually")}
                </button>
              </>
            }
            wideField
            field={
              <Select
                data={[
                  { value: "", label: t("tg.pickGroup") },
                  // The saved group first when the list does not carry it, so the
                  // menu opens on what is actually configured rather than on
                  // "pick a group".
                  ...(savedGroup && !savedListed
                    ? [{ value: savedGroup, label: savedGroup }]
                    : []),
                  ...supportGroups.map((g) => ({
                    value: String(g.chat_id),
                    // The id is shown alongside the name because names repeat and
                    // are renamed, and it is the id that actually gets saved.
                    label: `${g.title || t("tg.untitled")} · ${g.chat_id}${groupIssue(g)}`,
                  })),
                ]}
                value={supportGroupID}
                onChange={setSupportGroupID}
              />
            }
          />
        ) : (
          /* No candidates yet. Showing a bare ID field here would contradict the
             very promise printed next to it — so the empty state says what the
             panel is waiting for instead, and manual entry stays one click away. */
          <SettingRow label={t("tg.supportGroup")}>
            <div className="flex flex-col gap-1.5 text-[11px] leading-relaxed text-ink-muted">
              {saved.supportToken.trim() ? (
                /* No spinner: nothing is loading — the panel is waiting on the
                   operator, and an animation that never resolves would promise
                   progress that isn't happening. */
                <>
                  <p>
                    {t("tg.addBotHint", {
                      bot: supportBotUsername ? ` @${supportBotUsername}` : "",
                    })}
                  </p>
                  {/* The case an empty list strands, and the common one: the bot is
                      normally already in the group by the time anyone opens these
                      settings. Telegram gives a bot no way to list the groups it
                      belongs to and never replays the "you were added" event, so a
                      group joined earlier stays invisible until something happens in
                      it. Neither recovery is guessable, so both are spelled out. */}
                  <p>
                    <Trans i18nKey="tg.groupMissingHint" components={{ b: <b /> }} />
                  </p>
                  <p>{t("tg.reAddHint")}</p>
                </>
              ) : (
                <p>{t("tg.tokenFirst")}</p>
              )}
              <button
                type="button"
                className="self-start text-[11px] text-accent hover:underline"
                onClick={() => setManualGroup(true)}
              >
                {t("tg.enterIdManually")}
              </button>
            </div>
          </SettingRow>
        )}
        {supportEnabled && !supportGroupID.trim() && (
          /* Says why the save will be refused, instead of leaving the operator to
             discover it from an error toast — and names the way out, which is not
             obvious: the bot starts polling on a token alone, so saving with the
             switch OFF is what makes the group list appear. */
          <SettingRow
            hint={<span className="text-warning">{t("tg.needGroupHint")}</span>}
          />
        )}
        <SettingRow
          hint={
            <>
              {t("tg.setupHint")}{" "}
              <Trans i18nKey="tg.keepPrivate" components={{ b: <b /> }} />
            </>
          }
        />
        <SettingRow label={t("tg.supportGreeting")} hint={t("tg.greetingHint")}>
          <Textarea
            value={supportGreeting}
            onChange={setSupportGreeting}
            rows={2}
            placeholder={t("tg.greetingPlaceholder")}
          />
        </SettingRow>
        <SettingRow
          hint={supportConfigDirty ? t("tg.saveThenCheck") : t("tg.checkWhat")}
          control={
            <Button
              size="xs"
              variant="light"
              loading={checking}
              onClick={runCheck}
              disabled={
                !supportToken.trim() || !supportGroupID.trim() || supportConfigDirty
              }
            >
              {t("tg.check")}
            </Button>
          }
        />
      </Panel>

      <Panel
        title={t("tg.scheduledBackups")}
        aside={
          <Button
            size="xs"
            variant="light"
            loading={testing}
            onClick={sendTest}
            disabled={chats.length === 0 || !token.trim()}
          >
            {t("tg.sendTestBackup")}
          </Button>
        }
      >
        <SettingRow
          hint={
            chats.length === 0 || !token.trim()
              ? t("tg.needTokenAndChat")
              : t("tg.scheduledBackupsHint")
          }
        />
        <SettingRow>
          <CronPicker
            value={schedule}
            onChange={setSchedule}
            offLabel={t("general.autoBackupsOff")}
          />
        </SettingRow>
      </Panel>

      <SaveBar
        dirty={dirty}
        busy={busy}
        onSave={save}
        onCancel={cancel}
        // Only a missing TOKEN disables saving — that is the one thing no bot can be
        // enabled without. A missing support group deliberately does NOT: this bar
        // saves every Telegram section at once, so gating it on one half-filled
        // section would freeze the admin bot, the user bot and the backup schedule
        // behind a field the operator may not be ready to fill.
        saveDisabled={
          (enabled && !token.trim()) ||
          (userEnabled && !userToken.trim()) ||
          (supportEnabled && !supportToken.trim())
        }
      />
    </div>
  );
}
