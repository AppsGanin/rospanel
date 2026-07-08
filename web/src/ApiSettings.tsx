import { type ReactNode, useEffect, useState } from "react";
import {
  type ApiKey,
  type ApiKeysInfo,
  createApiKey,
  getApiKeys,
  revokeApiKey,
  setApiPath,
} from "./api";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  CenterLoader,
  cn,
  IconChevron,
  IconCopy,
  IconShield,
  Modal,
  SaveBar,
  SettingCard,
  Switch,
  TextInput,
  useConfirm,
  useCopy,
} from "./ui";

function fmtTs(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString("ru-RU", {
    dateStyle: "medium",
    timeStyle: "short",
  });
}

/* small inline glyphs for the docs tiles (match the stroke style of ui.tsx) */
const IconDoc = ({ size = 18 }: { size?: number }) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth={2}
    strokeLinecap="round"
    strokeLinejoin="round"
  >
    <path d="M4 4a2 2 0 0 1 2-2h8l6 6v12a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2Z" />
    <path d="M14 2v6h6" />
    <path d="M8 13h8M8 17h5" />
  </svg>
);
const IconBraces = ({ size = 18 }: { size?: number }) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth={2}
    strokeLinecap="round"
    strokeLinejoin="round"
  >
    <path d="M8 3H7a2 2 0 0 0-2 2v4a2 2 0 0 1-2 2 2 2 0 0 1 2 2v4a2 2 0 0 0 2 2h1" />
    <path d="M16 3h1a2 2 0 0 1 2 2v4a2 2 0 0 1 2 2 2 2 0 0 1-2 2v4a2 2 0 0 1-2 2h-1" />
  </svg>
);

// CopyField is a read-only monospace value with a copy button.
function CopyField({ value }: { value: string }) {
  const { copied, copy } = useCopy();
  return (
    <div className="flex items-stretch gap-2">
      <code className="min-w-0 flex-1 truncate rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 font-mono text-sm text-ink">
        {value}
      </code>
      <Button variant="light" color="gray" onClick={() => copy(value)}>
        <IconCopy /> {copied ? "Скопировано" : "Копировать"}
      </Button>
    </div>
  );
}

// DocTile is one clickable documentation destination.
function DocTile({
  href,
  icon,
  title,
  subtitle,
}: {
  href: string;
  icon: ReactNode;
  title: string;
  subtitle: string;
}) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      className="group flex items-center gap-3 rounded-xl border border-gray-200 bg-white p-3 transition hover:border-accent hover:accent-tint"
    >
      <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg accent-tint text-accent">
        {icon}
      </span>
      <span className="min-w-0 flex-1">
        <span className="block text-sm font-semibold text-ink">{title}</span>
        <span className="block truncate text-xs text-ink-muted">{subtitle}</span>
      </span>
      <IconChevron
        className="-rotate-90 text-ink-muted transition group-hover:text-accent"
        size={18}
      />
    </a>
  );
}

// KeyRow is one API key in the list.
function KeyRow({ k, onRevoke }: { k: ApiKey; onRevoke: (k: ApiKey) => void }) {
  const revoked = !!k.revoked_at;
  return (
    <div
      className={cn(
        "flex items-center justify-between gap-3 rounded-xl border border-gray-200 px-3 py-2.5",
        revoked && "opacity-60",
      )}
    >
      <div className="min-w-0">
        <div className="flex items-center gap-2">
          <span className="truncate font-semibold text-ink">{k.name}</span>
          <code className="rounded bg-gray-100 px-1.5 py-0.5 font-mono text-xs text-ink-muted">
            {k.prefix}…
          </code>
        </div>
        <div className="mt-0.5 text-xs text-ink-muted">
          Создан {fmtTs(k.created_at)} · Использован{" "}
          {k.last_used_at ? fmtTs(k.last_used_at) : "ни разу"}
        </div>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        {revoked ? (
          <Badge color="gray">отозван</Badge>
        ) : (
          <Badge color="green">активен</Badge>
        )}
        {!revoked && (
          <Button size="sm" variant="light" color="red" onClick={() => onRevoke(k)}>
            Отозвать
          </Button>
        )}
      </div>
    </div>
  );
}

export function ApiSettings() {
  const [info, setInfo] = useState<ApiKeysInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [name, setName] = useState("");
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<ApiKey | null>(null);
  // Draft of the enable toggle — applied via the bottom SaveBar (not instantly),
  // matching the other settings sections. Key create/revoke/rotate stay immediate.
  const [enabledDraft, setEnabledDraft] = useState(false);
  const [saving, setSaving] = useState(false);
  const { confirm, confirmNode } = useConfirm();

  const refresh = () =>
    getApiKeys()
      .then(setInfo)
      .catch((e) => notifyError(errMessage(e)))
      .finally(() => setLoading(false));

  useEffect(() => {
    refresh();
  }, []);

  // Sync the toggle draft whenever the server's enabled state changes (initial
  // load, after Save, or after rotate) — but not on a purely local flip.
  useEffect(() => {
    if (info) setEnabledDraft(info.enabled);
  }, [info?.enabled]);

  const create = async () => {
    const n = name.trim();
    if (!n) return;
    setCreating(true);
    try {
      const res = await createApiKey(n);
      setCreated(res.key);
      setName("");
      await refresh();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setCreating(false);
    }
  };

  const revoke = async (k: ApiKey) => {
    const ok = await confirm({
      title: "Отозвать ключ?",
      body: `Ключ «${k.name}» перестанет работать немедленно. Интеграции, использующие его, потеряют доступ. Действие необратимо.`,
      confirmLabel: "Отозвать",
      danger: true,
    });
    if (!ok) return;
    try {
      await revokeApiKey(k.id);
      await refresh();
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  const rotatePath = async () => {
    const ok = await confirm({
      title: "Сменить адрес API?",
      body: "Базовый URL изменится — все интеграции нужно будет обновить. Сами ключи продолжат работать по новому адресу.",
      confirmLabel: "Сменить адрес",
      danger: true,
    });
    if (!ok) return;
    try {
      const res = await setApiPath(true, true);
      setInfo((i) => (i ? { ...i, ...res } : i));
      notifySuccess("Адрес API обновлён");
      await refresh();
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  // saveEnabled applies the staged on/off toggle. Enabling mints the base URL;
  // disabling closes access but keeps the keys, which resume once turned back on.
  const saveEnabled = async () => {
    if (!info) return;
    setSaving(true);
    try {
      const res = await setApiPath(enabledDraft);
      setInfo((i) => (i ? { ...i, ...res } : i));
      if (enabledDraft) await refresh();
      notifySuccess(enabledDraft ? "API включён" : "API отключён");
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setSaving(false);
    }
  };

  if (loading) return <CenterLoader />;
  if (!info) return null;

  const enabledDirty = enabledDraft !== info.enabled;

  return (
    <div className="flex flex-col gap-4">
      <SettingCard
        title="Доступ по API"
        description="REST-API для управления пользователями, тарифами и статистикой из внешних систем. Каждый запрос авторизуется ключом: заголовок Authorization: Bearer <ключ>."
        action={<Switch checked={enabledDraft} onChange={setEnabledDraft} />}
      >
        {info.enabled ? (
          <div className="flex flex-col gap-3">
            <div>
              <div className="mb-1 text-sm font-semibold text-ink">
                Базовый адрес
              </div>
              <CopyField value={info.base_url} />
            </div>
            <div className="flex flex-wrap gap-2 pt-2">
              <Button size="sm" variant="light" color="gray" onClick={rotatePath}>
                Сменить адрес
              </Button>
            </div>
          </div>
        ) : (
          <div className="flex items-center gap-3 rounded-xl border border-dashed border-gray-200 bg-gray-50 p-4">
            <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full accent-tint text-accent">
              <IconShield size={20} />
            </span>
            <p className="text-sm text-ink-muted">
              API выключен. Включите переключатель справа, чтобы открыть доступ.
              Ключи можно создать заранее — они заработают после включения.
            </p>
          </div>
        )}
      </SettingCard>

      {info.enabled && (
        <SettingCard
          title="Документация"
          description="Открываются без ключа — сам адрес секретный."
        >
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            <DocTile
              href={`${info.base_url}/v1/docs`}
              icon={<IconDoc />}
              title="Swagger UI"
              subtitle="Интерактивная документация, вызовы из браузера"
            />
            <DocTile
              href={`${info.base_url}/v1/openapi.json`}
              icon={<IconBraces />}
              title="openapi.json"
              subtitle="Машиночитаемая спека OpenAPI 3.0"
            />
          </div>
        </SettingCard>
      )}

      <SettingCard
        title="Ключи API"
        description="Ключ показывается один раз при создании — сохраните его сразу. В панели остаётся только префикс для опознания."
      >
        <div className="flex items-end gap-2">
          <div className="flex-1">
            <TextInput
              label="Название нового ключа"
              value={name}
              onChange={setName}
              placeholder="например, billing-bot"
            />
          </div>
          <Button onClick={create} loading={creating} disabled={!name.trim()}>
            Создать
          </Button>
        </div>

        {info.keys.length > 0 ? (
          <div className="mt-4 flex flex-col gap-2">
            {info.keys.map((k) => (
              <KeyRow key={k.id} k={k} onRevoke={revoke} />
            ))}
          </div>
        ) : (
          <p className="mt-4 text-center text-sm text-ink-muted">
            Ключей пока нет — создайте первый выше.
          </p>
        )}
      </SettingCard>

      {/* One-time reveal of a freshly created key. */}
      <Modal open={!!created} onClose={() => setCreated(null)} title="Ключ создан">
        <p className="text-sm text-ink-muted">
          Скопируйте ключ сейчас — он больше не будет показан. Если потеряете его,
          создайте новый.
        </p>
        {created?.raw_key && (
          <div className="mt-3">
            <CopyField value={created.raw_key} />
          </div>
        )}
        <div className="mt-5 flex justify-end">
          <Button onClick={() => setCreated(null)}>Готово</Button>
        </div>
      </Modal>

      <SaveBar
        dirty={enabledDirty}
        busy={saving}
        onSave={saveEnabled}
        onCancel={() => setEnabledDraft(info.enabled)}
      />

      {confirmNode}
    </div>
  );
}
