import { useEffect, useMemo, useState } from "react";
import {
  applyUpdate,
  checkUpdate,
  getMe,
  getSettings,
  regenSecret,
  setDecoy,
  setProxyMode,
  setupTimezone,
  type SettingsInfo,
  type UpdateInfo,
} from "./api";
import { useAction } from "./hooks";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { browserTimezone, tzOptions } from "./tz";
import {
  Button,
  CenterLoader,
  Code,
  SaveBar,
  Select,
  SettingCard,
  Switch,
  TextInput,
  useConfirm,
} from "./ui";

type ProxyMode = {
  enabled: boolean;
  type: string;
  port: number;
  user: string;
  pass: string;
};

const DECOY_LABELS: Record<string, string> = {
  "coming-soon": "Coming soon (скоро открытие)",
  nginx: "Nginx (страница по умолчанию)",
  maintenance: "Технические работы",
  "10gag": "9GAG (развлечения)",
  "503-1": "Ошибка 503 (вариант 1)",
  "503-2": "Ошибка 503 (вариант 2)",
  YouTube: "YouTube",
  converter: "Конвертер файлов",
  downloader: "Загрузчик файлов",
  filecloud: "Файловое облако",
  speedtest: "Speedtest",
};

export function GeneralSettings() {
  const EMPTY_PM: ProxyMode = {
    enabled: false,
    type: "socks",
    port: 1080,
    user: "",
    pass: "",
  };
  const [loaded, setLoaded] = useState(false);
  const [timezone, setTimezone] = useState("");
  const [savedTz, setSavedTz] = useState("");
  const [settings, setSettings] = useState<SettingsInfo | null>(null);
  const [decoy, setDecoyState] = useState("");
  const [savedDecoy, setSavedDecoy] = useState("");
  const [pm, setPm] = useState<ProxyMode>(EMPTY_PM);
  const [savedPm, setSavedPm] = useState<ProxyMode>(EMPTY_PM);
  const [version, setVersion] = useState("");
  const [upd, setUpd] = useState<UpdateInfo | null>(null);
  const [updating, setUpdating] = useState(false);
  const { isBusy, run } = useAction();
  const { confirm, confirmNode } = useConfirm();

  const tzList = useMemo(
    () => tzOptions(timezone || browserTimezone()),
    [timezone],
  );

  useEffect(() => {
    Promise.all([
      getMe()
        .then((m) => {
          const tz = m.timezone || browserTimezone();
          setTimezone(tz);
          setSavedTz(tz);
          setVersion(m.version);
        })
        .catch(() => {
          setTimezone(browserTimezone());
          setSavedTz(browserTimezone());
        }),
      getSettings()
        .then((s) => {
          setSettings(s);
          const dec = s.decoy_template || "coming-soon";
          setDecoyState(dec);
          setSavedDecoy(dec);
          const pmv: ProxyMode = {
            enabled: s.proxy_mode_enabled,
            type: s.proxy_mode_type || "socks",
            port: s.proxy_mode_port || 1080,
            user: s.proxy_mode_user || "",
            pass: s.proxy_mode_pass || "",
          };
          setPm(pmv);
          setSavedPm(pmv);
        })
        .catch(() => {}),
    ]).finally(() => setLoaded(true));
  }, []);

  const pmDirty = JSON.stringify(pm) !== JSON.stringify(savedPm);
  const dirty = timezone !== savedTz || decoy !== savedDecoy || pmDirty;
  // Proxy mode without credentials is an open proxy — block saving it.
  const saveBlocked = pm.enabled && (!pm.user.trim() || !pm.pass);

  // save persists whatever changed (timezone / decoy / proxy mode) behind the
  // single bottom SaveBar. Update-check and secret regen stay immediate actions.
  const save = () =>
    run(
      async () => {
        if (timezone !== savedTz) {
          await setupTimezone(timezone);
          setSavedTz(timezone);
        }
        if (decoy !== savedDecoy) {
          await setDecoy(decoy);
          setSettings((s) => (s ? { ...s, decoy_template: decoy } : s));
          setSavedDecoy(decoy);
        }
        if (pmDirty) {
          await setProxyMode(pm);
          setSavedPm(pm);
        }
        notifySuccess("Настройки сохранены");
      },
      { key: "save" },
    );

  const cancel = () => {
    setTimezone(savedTz);
    setDecoyState(savedDecoy);
    setPm(savedPm);
  };

  const doRegenSecret = async () => {
    const ok = await confirm({
      title: "Перегенерировать секретный путь?",
      body: "URL входа в панель изменится, текущая сессия слетит — вас перекинет на новый адрес. Старая ссылка перестанет работать.",
      confirmLabel: "Перегенерировать",
      danger: true,
    });
    if (!ok) return;
    run(
      async () => {
        const { secret_path } = await regenSecret();
        window.location.href = `${window.location.origin}/${secret_path}/`;
      },
      { key: "secret" },
    );
  };

  const doCheckUpdate = () =>
    run(
      async () => {
        const info = await checkUpdate();
        setUpd(info);
        setVersion(info.current);
        if (info.error) notifyError(info.error);
        else if (!info.available) notifySuccess("У вас последняя версия");
      },
      { key: "upd-check" },
    );

  const doUpdate = async () => {
    if (!upd?.latest) return;
    const ok = await confirm({
      title: `Обновить до v${upd.latest}?`,
      body: "Панель скачает новую версию и перезапустится. Все подключения (VPN и панель) кратко прервутся на несколько секунд. Настройки и пользователи сохранятся — БД не трогается.",
      confirmLabel: "Обновить",
    });
    if (!ok) return;
    setUpdating(true);
    try {
      await applyUpdate();
    } catch (e) {
      setUpdating(false);
      notifyError(errMessage(e));
      return;
    }
    // The server restarts ~2s later; poll until it's back, then reload to pick up
    // the new assets + version.
    notifySuccess("Обновление пошло — ждём перезапуск…");
    let tries = 0;
    const poll = () => {
      getMe()
        .then(() => window.location.reload())
        .catch(() => {
          if (++tries > 25) {
            setUpdating(false);
            notifyError("Панель не ответила — перезагрузите страницу вручную");
            return;
          }
          window.setTimeout(poll, 3000);
        });
    };
    window.setTimeout(poll, 4000);
  };

  if (!loaded) return <CenterLoader />;

  return (
    <div className="flex flex-col gap-4">
      <SettingCard
        title="Обновление панели"
        description={
          <>
            Текущая версия: <b>v{version || "—"}</b>
            {upd?.available && upd.latest && (
              <>
                {" · "}доступна{" "}
                <b className="text-accent">v{upd.latest}</b>
              </>
            )}
          </>
        }
      >
        <div className="flex flex-wrap gap-2">
          <Button
            variant="light"
            color="gray"
            loading={isBusy("upd-check")}
            disabled={updating}
            onClick={doCheckUpdate}
          >
            Проверить обновления
          </Button>
          {upd?.available && (
            <Button loading={updating} onClick={doUpdate}>
              Обновить до v{upd.latest}
            </Button>
          )}
        </div>
        {updating && (
          <p className="mt-2 text-xs text-ink-muted">
            Обновление… панель перезапускается, страница перезагрузится
            автоматически.
          </p>
        )}
      </SettingCard>

      <SettingCard
        title="Часовой пояс"
        description="Граница суток в статистике трафика."
      >
        <Select data={tzList} value={timezone} onChange={setTimezone} searchable />
      </SettingCard>

      <SettingCard
        title="Сайт-заглушка"
        description="Что видят посторонние по любому адресу, кроме секретного пути панели."
      >
        <Select
          data={(settings?.decoy_templates ?? []).map((t) => ({
            value: t,
            label: DECOY_LABELS[t] ?? t,
          }))}
          value={decoy}
          onChange={setDecoyState}
        />
      </SettingCard>

      <SettingCard
        title="Режим прокси"
        description="Поднимает socks/http прокси-инбаунд, чтобы другой RosPanel мог ходить через этот сервер (указать его в прокси в Роутинге)."
        action={
          <Switch
            checked={pm.enabled}
            onChange={(v) => setPm((p) => ({ ...p, enabled: v }))}
          />
        }
      >
        <div className="grid grid-cols-2 gap-2">
          <Select
            label="Тип"
            data={[
              { value: "socks", label: "SOCKS5" },
              { value: "http", label: "HTTP" },
            ]}
            value={pm.type}
            onChange={(v) => setPm((p) => ({ ...p, type: v }))}
          />
          <TextInput
            label="Порт"
            type="number"
            value={String(pm.port)}
            onChange={(v) =>
              setPm((p) => ({ ...p, port: Number(v.replace(/\D/g, "")) || 0 }))
            }
          />
          <TextInput
            label="Логин"
            value={pm.user}
            onChange={(v) => setPm((p) => ({ ...p, user: v }))}
          />
          <TextInput
            label="Пароль"
            value={pm.pass}
            onChange={(v) => setPm((p) => ({ ...p, pass: v }))}
          />
        </div>
        {pm.enabled && (
          <>
            <p className="mt-3 mb-1 text-sm text-ink-muted">
              Строка для пула на другом сервере:
            </p>
            <Code block>
              {`${pm.type === "http" ? "http" : "socks5"}://${
                pm.user ? `${pm.user}:${pm.pass}@` : ""
              }${window.location.hostname}:${pm.port}`}
            </Code>
          </>
        )}
        {pm.enabled && (!pm.user.trim() || !pm.pass) && (
          <p className="mt-2 text-xs text-warning">
            ⚠️ Логин и пароль обязательны — иначе это открытый прокси, через
            который сможет ходить любой.
          </p>
        )}
        <p className="mt-2 text-xs text-ink-muted">
          Не забудь открыть порт {pm.port} в файрволе сервера.
        </p>
      </SettingCard>

      <SettingCard
        title="Секретный путь панели"
        description="Скрытый сегмент адреса, по которому открывается панель. Перегенерация сменит URL входа."
      >
        <Code block className="mb-3">
          /{settings?.secret_path}/
        </Code>
        <Button
          color="red"
          variant="light"
          loading={isBusy("secret")}
          onClick={doRegenSecret}
        >
          Перегенерировать
        </Button>
      </SettingCard>

      <SaveBar
        dirty={dirty}
        busy={isBusy("save")}
        saveDisabled={saveBlocked}
        onSave={save}
        onCancel={cancel}
      />
      {confirmNode}
    </div>
  );
}
