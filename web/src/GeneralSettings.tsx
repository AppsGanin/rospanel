import { useEffect, useMemo, useRef, useState } from "react";
import {
  applyUpdate,
  checkUpdate,
  deleteBrandingLogo,
  getMe,
  getSettings,
  regenSecret,
  saveBranding,
  setDecoy,
  setProxyMode,
  setupTimezone,
  uploadBrandingLogo,
  type SettingsInfo,
  type ThemeColors,
  type UpdateInfo,
} from "./api";
import { useBrand } from "./brand";
import { useAction } from "./hooks";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { browserTimezone, tzOptions } from "./tz";
import {
  Button,
  CenterLoader,
  Code,
  Select,
  SettingCard,
  Switch,
  TextInput,
  useConfirm,
} from "./ui";

// Curated accent swatches; the accent also drives the whole brand-* ramp.
const ACCENT_PRESETS = [
  "#0d4cd3", "#4f46e5", "#7c3aed", "#0891b2", "#0d9488",
  "#059669", "#dc2626", "#ea580c", "#e11d48", "#475569",
];

type ColorKey = keyof ThemeColors;

const COLOR_FIELDS: Array<{ key: ColorKey; label: string; hint: string }> = [
  { key: "accent", label: "Акцент", hint: "Кнопки, ссылки, активные вкладки, логотип" },
  { key: "text", label: "Текст", hint: "Основной текст и заголовки" },
  { key: "muted", label: "Приглушённый текст", hint: "Подписи, второстепенный текст" },
  { key: "bg", label: "Фон страницы", hint: "Подложка панели и страницы подписки" },
  { key: "surface", label: "Поверхность", hint: "Карточки, поля ввода, модалки" },
];

function normHex(v: string): string {
  return /^#[0-9a-fA-F]{6}$/.test(v.trim()) ? v.trim().toLowerCase() : "";
}

function ColorField({
  label,
  hint,
  value,
  def,
  onChange,
}: {
  label: string;
  hint: string;
  value: string;
  def: string;
  onChange: (v: string) => void;
}) {
  const isDefault = value.toLowerCase() === def.toLowerCase();
  return (
    <div className="flex items-center gap-3">
      <input
        type="color"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        aria-label={label}
        className="h-9 w-11 shrink-0 cursor-pointer rounded border border-gray-300 bg-white p-0.5"
      />
      <div className="min-w-0 flex-1">
        <p className="text-sm font-medium text-ink">{label}</p>
        <p className="truncate text-xs text-ink-muted">{hint}</p>
      </div>
      <input
        value={value}
        onChange={(e) => {
          const h = normHex(e.target.value);
          onChange(h || e.target.value);
        }}
        spellCheck={false}
        className="w-24 rounded-lg border border-gray-300 bg-white px-2 py-1.5 text-sm font-mono uppercase text-ink outline-none focus:border-brand-500"
      />
      {!isDefault && (
        <button
          type="button"
          onClick={() => onChange(def)}
          className="text-xs text-ink-muted underline-offset-2 hover:text-accent hover:underline"
        >
          сброс
        </button>
      )}
    </div>
  );
}

function BrandingCard() {
  const brand = useBrand();
  const [name, setName] = useState("");
  const [theme, setTheme] = useState<ThemeColors>(brand.default_theme);
  const [init, setInit] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const { isBusy, run } = useAction();

  // Seed local fields from the loaded branding once.
  useEffect(() => {
    if (brand.loaded && !init) {
      setName(brand.panel_name === brand.default_name ? "" : brand.panel_name);
      setTheme(brand.theme);
      setInit(true);
    }
  }, [brand.loaded, brand.panel_name, brand.theme, brand.default_name, init]);

  const setColor = (key: ColorKey, v: string) =>
    setTheme((t) => ({ ...t, [key]: v }));

  const resetAll = () => setTheme(brand.default_theme);

  const save = () =>
    run(
      async () => {
        // Only send valid #rrggbb; blanks/invalid fall back to defaults.
        const fix = (k: ColorKey) => normHex(theme[k]) || brand.default_theme[k];
        const clean: ThemeColors = {
          accent: fix("accent"),
          text: fix("text"),
          muted: fix("muted"),
          bg: fix("bg"),
          surface: fix("surface"),
        };
        await saveBranding(name.trim(), clean);
        await brand.refresh();
        notifySuccess("Брендинг сохранён");
      },
      { key: "brand" },
    );

  const onPickLogo = () => fileRef.current?.click();
  const onLogoFile = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = "";
    if (!file) return;
    run(
      async () => {
        await uploadBrandingLogo(file);
        await brand.refresh();
        notifySuccess("Логотип загружен");
      },
      { key: "logo" },
    );
  };
  const removeLogo = () =>
    run(
      async () => {
        await deleteBrandingLogo();
        await brand.refresh();
        notifySuccess("Логотип сброшен на стандартный");
      },
      { key: "logo" },
    );

  return (
    <SettingCard
      title="Брендинг"
      description="Название, цвета и логотип панели. Применяется и на странице подписки."
    >
      <div className="flex flex-col gap-4">
        <TextInput
          label="Название панели"
          placeholder={brand.default_name}
          value={name}
          onChange={setName}
        />

        <div>
          <div className="mb-2 flex items-center justify-between">
            <p className="text-sm font-medium text-ink">Цвета</p>
            <button
              type="button"
              onClick={resetAll}
              className="text-xs text-ink-muted underline-offset-2 hover:text-accent hover:underline"
            >
              Сбросить все
            </button>
          </div>

          <div className="mb-3 flex flex-wrap items-center gap-2">
            {ACCENT_PRESETS.map((c) => (
              <button
                key={c}
                type="button"
                onClick={() => setColor("accent", c)}
                title={c}
                aria-label={`Акцент ${c}`}
                className={
                  "h-7 w-7 rounded-full border transition " +
                  (theme.accent.toLowerCase() === c.toLowerCase()
                    ? "border-white ring-2 ring-brand-600 ring-offset-2"
                    : "border-gray-300 hover:scale-110")
                }
                style={{ background: c }}
              />
            ))}
          </div>

          <div className="flex flex-col gap-3">
            {COLOR_FIELDS.map((f) => (
              <ColorField
                key={f.key}
                label={f.label}
                hint={f.hint}
                value={theme[f.key]}
                def={brand.default_theme[f.key]}
                onChange={(v) => setColor(f.key, v)}
              />
            ))}
          </div>
        </div>

        <div>
          <p className="mb-1.5 text-sm font-medium text-ink">Логотип</p>
          <div className="flex items-center gap-3">
            {brand.has_custom_logo && (
              <img
                src={brand.logoURL}
                alt=""
                className="h-12 w-12 rounded-lg border border-gray-300 bg-white object-contain p-1"
              />
            )}
            <Button
              variant="light"
              color="gray"
              loading={isBusy("logo")}
              onClick={onPickLogo}
            >
              Загрузить логотип
            </Button>
            {brand.has_custom_logo && (
              <Button
                variant="subtle"
                color="red"
                loading={isBusy("logo")}
                onClick={removeLogo}
              >
                Сбросить
              </Button>
            )}
            <input
              ref={fileRef}
              type="file"
              accept="image/png,image/jpeg"
              className="hidden"
              onChange={onLogoFile}
            />
          </div>
          <p className="mt-1.5 text-xs text-ink-muted">
            PNG или JPEG, до 512 КБ, не больше 1024×1024 px.
          </p>
        </div>

        <div>
          <Button loading={isBusy("brand")} onClick={save}>
            Сохранить
          </Button>
        </div>
      </div>
    </SettingCard>
  );
}

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
  const [loaded, setLoaded] = useState(false);
  const [timezone, setTimezone] = useState("");
  const [settings, setSettings] = useState<SettingsInfo | null>(null);
  const [decoy, setDecoyState] = useState("");
  const [pm, setPm] = useState<ProxyMode>({
    enabled: false,
    type: "socks",
    port: 1080,
    user: "",
    pass: "",
  });
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
          setTimezone(m.timezone || browserTimezone());
          setVersion(m.version);
        })
        .catch(() => setTimezone(browserTimezone())),
      getSettings()
        .then((s) => {
          setSettings(s);
          setDecoyState(s.decoy_template || "coming-soon");
          setPm({
            enabled: s.proxy_mode_enabled,
            type: s.proxy_mode_type || "socks",
            port: s.proxy_mode_port || 1080,
            user: s.proxy_mode_user || "",
            pass: s.proxy_mode_pass || "",
          });
        })
        .catch(() => {}),
    ]).finally(() => setLoaded(true));
  }, []);

  const saveTimezone = () =>
    run(
      async () => {
        await setupTimezone(timezone);
        notifySuccess("Часовой пояс сохранён");
      },
      { key: "tz" },
    );

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

  const saveDecoy = () =>
    run(
      async () => {
        await setDecoy(decoy);
        setSettings((s) => (s ? { ...s, decoy_template: decoy } : s));
        notifySuccess("Заглушка обновлена");
      },
      { key: "decoy" },
    );

  const savePM = () =>
    run(
      async () => {
        await setProxyMode(pm);
        notifySuccess(pm.enabled ? "Режим прокси включён" : "Режим прокси выключен");
      },
      { key: "pm" },
    );

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
      <BrandingCard />

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
        <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
          <div className="sm:flex-1">
            <Select
              data={tzList}
              value={timezone}
              onChange={setTimezone}
              searchable
            />
          </div>
          <Button loading={isBusy("tz")} onClick={saveTimezone}>
            Сохранить
          </Button>
        </div>
      </SettingCard>

      <SettingCard
        title="Сайт-заглушка"
        description="Что видят посторонние по любому адресу, кроме секретного пути панели."
      >
        <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
          <div className="sm:flex-1">
            <Select
              data={(settings?.decoy_templates ?? []).map((t) => ({
                value: t,
                label: DECOY_LABELS[t] ?? t,
              }))}
              value={decoy}
              onChange={setDecoyState}
            />
          </div>
          <Button loading={isBusy("decoy")} onClick={saveDecoy}>
            Применить
          </Button>
        </div>
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
        <div className="mt-3">
          <Button
            loading={isBusy("pm")}
            disabled={pm.enabled && (!pm.user.trim() || !pm.pass)}
            onClick={savePM}
          >
            Сохранить
          </Button>
        </div>
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
      {confirmNode}
    </div>
  );
}
