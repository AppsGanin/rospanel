import { useEffect, useMemo, useState } from "react";
import {
  applyUpdate,
  checkUpdate,
  getMe,
  getSettings,
  regenSecret,
  setLocalBackup,
  setupTimezone,
  setUserAutoDelete,
  type SettingsInfo,
  type UpdateInfo,
} from "./api";
import {
  buildCron,
  CronPicker,
  detectPreset,
  EMPTY_SCHEDULE,
  type Schedule,
} from "./CronPicker";
import { useAction } from "./hooks";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { browserTimezone, tzOptions } from "./tz";
import {
  Button,
  CenterLoader,
  Code,
  Modal,
  SaveBar,
  Select,
  SettingCard,
  Spinner,
  Switch,
  TextInput,
  useConfirm,
} from "./ui";

// LocalBackup is the scheduled on-disk backup: a schedule plus how many archives to
// keep. Independent of the Telegram backup schedule — an operator with no bot still
// wants automatic backups.
type LocalBackup = { schedule: Schedule; keep: number };

const EMPTY_BK: LocalBackup = { schedule: EMPTY_SCHEDULE, keep: 7 };

// Grace period between a user's expiry date and their deletion. "Никогда" is the
// default and is deliberately first: deleting paying customers because a dropdown
// defaulted to something is not a mistake anyone should be able to make by accident.
const AUTODELETE_OPTIONS = [
  { value: "0", label: "Никогда" },
  { value: "7", label: "7 дней после истечения" },
  { value: "30", label: "30 дней после истечения" },
  { value: "90", label: "90 дней после истечения" },
  { value: "180", label: "180 дней после истечения" },
  { value: "365", label: "365 дней после истечения" },
];

// DECOY_LABELS maps decoy slugs to friendly names. Exported so the master/node
// settings dialogs (where the decoy is now chosen) show the same labels.
export const DECOY_LABELS: Record<string, string> = {
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
  const [savedTz, setSavedTz] = useState("");
  const [settings, setSettings] = useState<SettingsInfo | null>(null);
  const [bk, setBk] = useState<LocalBackup>(EMPTY_BK);
  const [savedBk, setSavedBk] = useState<LocalBackup>(EMPTY_BK);
  const [autoDel, setAutoDel] = useState(0);
  const [savedAutoDel, setSavedAutoDel] = useState(0);
  const [version, setVersion] = useState("");
  const [upd, setUpd] = useState<UpdateInfo | null>(null);
  const [updating, setUpdating] = useState(false);
  const { isBusy, run } = useAction();
  const { confirm, confirmNode } = useConfirm();
  const [newSecret, setNewSecret] = useState("");

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
          const bkv: LocalBackup = {
            schedule: detectPreset(s.local_backup_cron || ""),
            keep: s.local_backup_keep ?? 7,
          };
          setBk(bkv);
          setSavedBk(bkv);
          const ad = s.user_autodelete_days ?? 0;
          setAutoDel(ad);
          setSavedAutoDel(ad);
        })
        .catch(() => {}),
    ]).finally(() => setLoaded(true));
  }, []);

  // Compare the built cron, not the picker state: "off" with a stale time/weekday in
  // the inputs is the same schedule as "off" with the defaults, and shouldn't light
  // up the save bar.
  const bkCron = buildCron(bk.schedule);
  const bkDirty =
    bkCron !== buildCron(savedBk.schedule) || bk.keep !== savedBk.keep;
  const adDirty = autoDel !== savedAutoDel;
  const dirty = timezone !== savedTz || bkDirty || adDirty;
  const saveBlocked = false;

  // save persists whatever changed (timezone / backups / auto-delete) behind the
  // single bottom SaveBar. Update-check and secret regen stay immediate actions.
  const save = () =>
    run(
      async () => {
        if (timezone !== savedTz) {
          await setupTimezone(timezone);
          setSavedTz(timezone);
        }
        if (bkDirty) {
          await setLocalBackup({ cron: bkCron, keep: bk.keep });
          setSavedBk(bk);
        }
        if (adDirty) {
          await setUserAutoDelete(autoDel);
          setSavedAutoDel(autoDel);
        }
        notifySuccess("Настройки сохранены");
      },
      { key: "save" },
    );

  const cancel = () => {
    setTimezone(savedTz);
    setBk(savedBk);
    setAutoDel(savedAutoDel);
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
        // Don't redirect straight away — this path is the only way back into the
        // panel and can't be recovered, so show it and let the user save it first.
        setNewSecret(secret_path);
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
    const target = upd.latest.replace(/^v/, "");
    setUpdating(true);
    try {
      await applyUpdate();
    } catch (e) {
      setUpdating(false);
      notifyError(errMessage(e));
      return;
    }
    // Reload only once the panel actually serves the NEW version — not merely when
    // it answers. The old process keeps replying for ~2s before it restarts, so a
    // bare reachability check would reload prematurely against a server about to
    // drop (the "reloaded but panel not up yet" bug). We watch two signals: the
    // reported version reaching `target`, and a down→up transition (a failed poll
    // proves the restart happened) as a fallback if versions are formatted oddly.
    let tries = 0;
    let wentDown = false;
    const poll = () => {
      getMe()
        .then((m) => {
          const running = (m.version || "").replace(/^v/, "");
          if (running === target || wentDown) {
            window.location.reload();
          } else if (++tries > 60) {
            setUpdating(false);
            notifyError("Обновление затянулось — перезагрузите страницу вручную");
          } else {
            window.setTimeout(poll, 2000);
          }
        })
        .catch(() => {
          wentDown = true; // panel dropped ⇒ the restart is underway
          if (++tries > 60) {
            setUpdating(false);
            notifyError("Панель не ответила — перезагрузите страницу вручную");
          } else {
            window.setTimeout(poll, 2000);
          }
        });
    };
    window.setTimeout(poll, 3000);
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
        <Modal
          open={updating}
          onClose={() => {}}
          dismissible={false}
          title="Обновление панели"
        >
          <div className="flex items-start gap-3">
            <Spinner size={22} className="mt-0.5 shrink-0" />
            <p className="text-sm text-ink">
              Панель скачивает новую версию и перезапускается. Не закрывайте эту
              страницу — она перезагрузится автоматически, как только новая версия
              запустится. Это может занять до минуты.
            </p>
          </div>
        </Modal>
      </SettingCard>

      <SettingCard
        title="Часовой пояс"
        description="Граница суток в статистике/логах."
      >
        <Select data={tzList} value={timezone} onChange={setTimezone} searchable />
      </SettingCard>

      <SettingCard
        title="Автоматические бэкапы"
        description="Резервные копии сохраняются на сам сервер, в каталог данных панели (backups/). Работает независимо от Telegram — бот для этого не нужен."
      >
        <CronPicker
          value={bk.schedule}
          onChange={(schedule) => setBk((b) => ({ ...b, schedule }))}
          offLabel="Автоматические бэкапы выключены."
          // Retention only means something once a schedule exists, and it belongs
          // beside it: "каждый день в 03:00, храним 7 копий" is one sentence.
          extra={
            bkCron ? (
              <TextInput
                label="Сколько копий хранить"
                type="number"
                value={String(bk.keep)}
                onChange={(v) =>
                  setBk((b) => ({ ...b, keep: Number(v.replace(/\D/g, "")) || 0 }))
                }
              />
            ) : undefined
          }
        />
        {bkCron && (
          <p className="mt-1 text-xs text-ink-muted">
            Лишние копии удаляются, остаются самые свежие. 0 — не удалять ничего.
          </p>
        )}
        <p className="mt-3 text-xs text-warning">
          ⚠️ Копия лежит на том же диске, что и панель, и содержит ключ шифрования
          секретов — от потери сервера она не спасёт. Скачивайте её к себе или
          включите отправку в Telegram.
        </p>
      </SettingCard>

      <SettingCard
        title="Автоудаление истёкших пользователей"
        description="Пользователь с истёкшим сроком удаляется через указанное время после даты окончания. Записи в журнале остаются — видно, кого и когда удалили."
      >
        <Select
          label="Удалять через"
          data={AUTODELETE_OPTIONS}
          value={String(autoDel)}
          onChange={(v) => setAutoDel(Number(v))}
        />
        <p className="mt-2 text-xs text-ink-muted">
          {autoDel === 0
            ? "Никто не удаляется — истёкшие копятся в списке."
            : "Удаление необратимо. Не затрагивает пользователей без срока и тех, кому срок продлили."}
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

      <Modal
        open={!!newSecret}
        onClose={() => {}}
        dismissible={false}
        title="Секретный путь изменён"
      >
        <div className="flex flex-col gap-4">
          <p className="text-sm leading-relaxed text-ink-muted">
            Панель теперь открывается только по этому адресу. Сохраните его —
            восстановить путь нельзя, а по старому адресу панель больше недоступна.
          </p>
          <Code block copy>
            {`${window.location.origin}/${newSecret}/`}
          </Code>
          <div className="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-800">
            Запишите адрес в надёжное место (менеджер паролей, заметки). Без него вы
            потеряете доступ к панели.
          </div>
          <div className="flex justify-end">
            <Button
              onClick={() => {
                window.location.href = `${window.location.origin}/${newSecret}/`;
              }}
            >
              Я сохранил, перейти на новый адрес
            </Button>
          </div>
        </div>
      </Modal>
    </div>
  );
}
