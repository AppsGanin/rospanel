import { useEffect, useState } from "react";
import {
  ANNOUNCE_MAX,
  getSettings,
  saveSubSettings,
  type SubSettings,
} from "./api";
import { useAction, useDirtyForm } from "./hooks";
import { notifySuccess } from "./notify";
import { subPathError } from "./validate";
import {
  Card,
  CenterLoader,
  cn,
  SaveBar,
  Select,
  Switch,
  Textarea,
  TextInput,
  ToggleRow,
} from "./ui";

const ROUTING_REPO = "https://github.com/hydraponique/roscomvpn-routing";

const EMPTY_SUB: SubSettings = {
  sub_path: "sub",
  sub_base64: true,
  sub_name_in_title: false,
  sub_title: "",
  sub_routing: true,
  sub_routing_happ: "",
  sub_routing_incy: "",
  sub_routing_mihomo: "",
  sub_update_interval: 1,
  sub_announce: "",
};

// Subscription auto-update cadence (hours; "0" = never).
const INTERVALS = [
  { value: "0", label: "Никогда" },
  { value: "1", label: "1 час" },
  { value: "6", label: "6 часов" },
  { value: "12", label: "12 часов" },
  { value: "24", label: "24 часа" },
  { value: "48", label: "48 часов" },
  { value: "168", label: "Неделя" },
];

export function SubscriptionsPanel() {
  const [loaded, setLoaded] = useState(false);
  const { draft: s, setDraft: setS, isDirty: dirty, load, commit, reset } = useDirtyForm<SubSettings>(EMPTY_SUB);
  const [secret, setSecret] = useState("");
  const { busy, run } = useAction();

  useEffect(() => {
    getSettings()
      .then((d) => {
        const init: SubSettings = {
          sub_path: d.sub_path,
          sub_base64: d.sub_base64,
          sub_name_in_title: d.sub_name_in_title,
          sub_title: d.sub_title,
          sub_routing: d.sub_routing,
          sub_routing_happ: d.sub_routing_happ,
          sub_routing_incy: d.sub_routing_incy,
          sub_routing_mihomo: d.sub_routing_mihomo,
          sub_update_interval: d.sub_update_interval,
          sub_announce: d.sub_announce,
        };
        load(init);
        setSecret(d.secret_path);
      })
      .catch(() => {})
      .finally(() => setLoaded(true));
  }, []);

  const patch = (p: Partial<SubSettings>) => setS((cur) => ({ ...cur, ...p }));
  const pathErr = subPathError(s.sub_path, secret);
  // Count runes the way the server does, not UTF-16 code units: an emoji is one
  // character to the client that renders it, and two to String.length.
  const announceLen = [...s.sub_announce.trim()].length;
  const announceErr = announceLen > ANNOUNCE_MAX;

  const save = () =>
    run(async () => {
      await saveSubSettings(s);
      commit();
      notifySuccess("Настройки подписок сохранены");
    });

  if (!loaded) return <CenterLoader />;

  return (
    <div className="flex flex-col gap-4 pb-20">
      <Card className="p-4">
        <h3 className="mb-3 font-bold text-ink">Формат подписки</h3>
        <div className="flex flex-col gap-4">
          <div>
            <TextInput
              label="Путь подписки"
              placeholder="sub"
              value={s.sub_path}
              onChange={(v) =>
                patch({ sub_path: v.replace(/[^A-Za-z0-9_-]/g, "") })
              }
            />
            {pathErr ? (
              <p className="mt-1 text-xs text-danger">{pathErr}</p>
            ) : (
              <p className="mt-1 text-xs text-ink-muted">
                Адрес подписки: /{s.sub_path || "sub"}/токен. Смена пути ломает
                уже выданные ссылки — раздайте заново.
              </p>
            )}
          </div>
          <ToggleRow
            label="Шифровать в base64"
            hint="Универсальный список ссылок отдаётся в base64 (выкл — обычным текстом)."
            checked={s.sub_base64}
            onChange={(v) => patch({ sub_base64: v })}
          />
          <TextInput
            label="Заголовок подписки"
            placeholder="Например, имя вашего сервера"
            value={s.sub_title}
            onChange={(v) => patch({ sub_title: v })}
          />
          <ToggleRow
            label="Имя пользователя в заголовке подписки"
            hint="Добавлять имя пользователя к заголовку профиля в клиенте (например, «Мой VPN — Маша»)."
            checked={s.sub_name_in_title}
            onChange={(v) => patch({ sub_name_in_title: v })}
          />
          <Select
            label="Интервал автообновления"
            data={INTERVALS}
            value={String(s.sub_update_interval)}
            onChange={(v) => patch({ sub_update_interval: Number(v) })}
          />
          <div>
            <Textarea
              label="Объявление в клиенте"
              placeholder="Например: сервер переезжает 3 августа, ссылка не меняется"
              rows={2}
              value={s.sub_announce}
              onChange={(v) => patch({ sub_announce: v })}
            />
            <p
              className={cn(
                "mt-1 text-xs",
                announceErr ? "text-danger" : "text-ink-muted",
              )}
            >
              Показывается строкой внутри VPN-клиента (Happ, v2RayTun). Пусто — объявления нет. {announceLen}/{ANNOUNCE_MAX}
            </p>
          </div>
        </div>
      </Card>

      <Card className="p-4">
        <div className="mb-3 flex items-center justify-between gap-3">
          <div>
            <h3 className="font-bold text-ink">Маршрутизация</h3>
            <p className="text-xs text-ink-muted">
              Авто-роутинг для клиентов. Готовые URL —{" "}
              <a
                href={ROUTING_REPO}
                target="_blank"
                rel="noreferrer"
                className="text-accent hover:underline"
              >
                roscomvpn-routing
              </a>
              .
            </p>
          </div>
          <Switch
            checked={s.sub_routing}
            onChange={(v) => patch({ sub_routing: v })}
          />
        </div>
        {s.sub_routing && (
          <div className="flex flex-col gap-3">
            <TextInput
              label="Happ — URL правил"
              placeholder="https://.../HAPP/DEFAULT.DEEPLINK"
              value={s.sub_routing_happ}
              onChange={(v) => patch({ sub_routing_happ: v })}
            />
            <TextInput
              label="INCY — URL правил"
              placeholder="https://.../INCY/DEFAULT.DEEPLINK"
              value={s.sub_routing_incy}
              onChange={(v) => patch({ sub_routing_incy: v })}
            />
            <div>
              <TextInput
                label="Mihomo (Clash Meta) — URL правил"
                placeholder="https://.../MIHOMO/default.yaml"
                value={s.sub_routing_mihomo}
                onChange={(v) => patch({ sub_routing_mihomo: v })}
              />
              <p className="mt-1 text-xs text-ink-muted">
                Для Clash/Mihomo прокси пользователя подставляются в шаблон в
                строку{" "}
                <code className="rounded bg-gray-100 px-1 font-mono">
                  # LEAVE THIS LINE!
                </code>{" "}
                (и их имена — в группу прокси). Укажите URL YAML-шаблона с этим
                маркером.
              </p>
            </div>
          </div>
        )}
      </Card>

      <SaveBar
        dirty={dirty}
        busy={busy}
        saveDisabled={!!pathErr || announceErr}
        onSave={save}
        onCancel={reset}
      />
    </div>
  );
}
