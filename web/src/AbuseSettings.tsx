import { useEffect, useMemo, useState } from "react";
import {
  abuseCategoryLabel,
  getAbuseSettings,
  refreshAbuseFeeds,
  saveAbuseSettings,
  type AbuseFeedStatus,
} from "./api";
import { useAction } from "./hooks";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Button,
  CenterLoader,
  SaveBar,
  SettingCard,
  Switch,
  Textarea,
  TextInput,
} from "./ui";

// Categories in display order. Keys must match model.AbuseCategoryCatalog on the
// backend (and abuse.Category), since the mask is rebuilt from them on save.
//
// `kind` is on screen because it decides what a list can actually catch: a domain
// list only fires when the domain is visible, and on modern clients most traffic
// arrives as a bare IP instead. Without it, "35 764 записей" under a domain list
// reads as though it covered IPs too.
const CATEGORIES: { key: string; desc: string }[] = [
  {
    key: "badip",
    desc: "FireHOL level 1: управляющие серверы ботнетов, атакующие и спам-сети. Отобранный список с минимумом ложных срабатываний — без CDN и шаред-хостинга.",
  },
];

// plural picks the Russian form: 1 диапазон, 2 диапазона, 5 диапазонов.
function plural(n: number, one: string, few: string, many: string) {
  const m10 = n % 10;
  const m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return one;
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few;
  return many;
}

// fmtEntries names what the count actually is — ranges of addresses, not hosts.
function fmtEntries(n: number) {
  return `${n.toLocaleString("ru-RU")} ${plural(n, "диапазон", "диапазона", "диапазонов")}`;
}

function fmtBytes(n?: number) {
  if (!n) return "";
  if (n < 1024) return `${n} Б`;
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} КБ`;
  return `${(n / 1024 / 1024).toFixed(1)} МБ`;
}

function fmtWhen(ts?: number) {
  if (!ts) return "не загружен";
  return new Date(ts * 1000).toLocaleString("ru-RU");
}

export function AbuseSettings() {
  const [loaded, setLoaded] = useState(false);
  const [enabled, setEnabled] = useState(true);
  const [cats, setCats] = useState<Record<string, boolean>>({});
  const [custom, setCustom] = useState("");
  const [alertMin, setAlertMin] = useState(20);
  const [status, setStatus] = useState<AbuseFeedStatus[]>([]);

  // Saved snapshot for dirty-tracking, same shape as the editable state.
  const [saved, setSaved] = useState({
    enabled: true,
    cats: {} as Record<string, boolean>,
    custom: "",
    alertMin: 20,
  });

  const { run, isBusy } = useAction();

  const load = () =>
    getAbuseSettings().then((s) => {
      setEnabled(s.enabled);
      setCats(s.categories ?? {});
      setCustom(s.custom ?? "");
      setAlertMin(s.alert_min || 20);
      setStatus(s.status ?? []);
      setSaved({
        enabled: s.enabled,
        cats: s.categories ?? {},
        custom: s.custom ?? "",
        alertMin: s.alert_min || 20,
      });
      setLoaded(true);
    });

  useEffect(() => {
    load().catch((e) => {
      notifyError(errMessage(e));
      setLoaded(true);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const dirty = useMemo(() => {
    if (enabled !== saved.enabled) return true;
    if (custom !== saved.custom) return true;
    if (alertMin !== saved.alertMin) return true;
    return CATEGORIES.some((c) => !!cats[c.key] !== !!saved.cats[c.key]);
  }, [enabled, cats, custom, alertMin, saved]);

  const save = () =>
    run(
      async () => {
        await saveAbuseSettings({
          enabled,
          categories: cats,
          custom,
          alert_min: alertMin,
        });
        notifySuccess("Настройки блоклистов сохранены");
        await load();
      },
      { key: "save" },
    );

  const cancel = () => {
    setEnabled(saved.enabled);
    setCats(saved.cats);
    setCustom(saved.custom);
    setAlertMin(saved.alertMin);
  };

  const doRefresh = () =>
    run(
      async () => {
        await refreshAbuseFeeds();
        notifySuccess("Обновление списков запущено — займёт до минуты");
        // The download runs in the background; re-read status shortly after.
        window.setTimeout(() => load().catch(() => {}), 8000);
      },
      { key: "refresh" },
    );

  if (!loaded) return <CenterLoader />;

  return (
    <div className="flex flex-col gap-4">
      <SettingCard
        title="Обнаружение злоупотреблений"
        description="Панель сверяет IP-адреса, к которым подключаются пользователи, со списками вредоносных сетей и записывает только совпадения — обычный трафик никуда не сохраняется. Нужно, чтобы по абуз-жалобе можно было понять, чей это был трафик."
      >
        <div className="flex items-center justify-between gap-3">
          <span className="text-sm text-ink">Включено</span>
          <Switch checked={enabled} onChange={setEnabled} />
        </div>
      </SettingCard>

      <SettingCard
        title="Списки"
        description="Проверка идёт по IP-адресу назначения. Отключённый список не скачивается и не занимает память."
      >
        <div className="flex flex-col gap-3">
          {CATEGORIES.map((c) => {
            const st = status.find((s) => s.category === c.key);
            return (
              <div key={c.key} className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-baseline gap-x-2 text-sm text-ink">
                    <span>{abuseCategoryLabel[c.key] ?? c.key}</span>
                    {st && st.entries > 0 && (
                      <span className="text-xs text-ink-muted">
                        {fmtEntries(st.entries)}
                        {st.size ? ` · ${fmtBytes(st.size)}` : ""}
                      </span>
                    )}
                  </div>
                  <div className="text-xs text-ink-muted">{c.desc}</div>
                  {st && (
                    <div className="text-xs text-ink-muted">
                      Обновлён: {fmtWhen(st.updated)}
                    </div>
                  )}
                </div>
                <Switch
                  checked={!!cats[c.key]}
                  disabled={!enabled}
                  onChange={(v) => setCats((p) => ({ ...p, [c.key]: v }))}
                />
              </div>
            );
          })}
        </div>
        <div className="mt-3">
          <Button
            variant="light"
            color="gray"
            loading={isBusy("refresh")}
            disabled={!enabled}
            onClick={doRefresh}
          >
            Обновить списки сейчас
          </Button>
        </div>
      </SettingCard>

      <SettingCard
        title="Свой список"
        description="IP-адреса и подсети (CIDR), по одному в строке. Проверяются первыми — раньше скачанного списка."
      >
        <Textarea
          value={custom}
          onChange={setCustom}
          rows={6}
          placeholder={"203.0.113.0/24\n198.51.100.7\n2001:db8::/32"}
          hint="Строки с # игнорируются. Всё, что не адрес и не подсеть, пропускается."
        />
      </SettingCard>

      <SettingCard
        title="Порог оповещения"
        description="Сколько совпадений за сутки должно набрать одного пользователя, прежде чем в Telegram уйдёт оповещение. Одно совпадение — это шум; закономерность — нет."
      >
        <TextInput
          type="number"
          value={String(alertMin)}
          onChange={(v) => setAlertMin(Math.max(1, Number(v) || 1))}
        />
      </SettingCard>

      <SaveBar
        dirty={dirty}
        busy={isBusy("save")}
        onSave={save}
        onCancel={cancel}
      />
    </div>
  );
}
