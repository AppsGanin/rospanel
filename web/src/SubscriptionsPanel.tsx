import { useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import {
  ANNOUNCE_MAX,
  getSettings,
  saveSubSettings,
  type SubSettings,
} from "./api";
import { useAction, useDirtyForm } from "./hooks";
import i18n from "./i18n";
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
const intervals = () => [
  { value: "0", label: i18n.t("subs.never") },
  ...[1, 6, 12, 24, 48].map((h) => ({
    value: String(h),
    label: i18n.t("subs.hours", { count: h }),
  })),
  { value: "168", label: i18n.t("subs.week") },
];

export function SubscriptionsPanel() {
  const { t } = useTranslation();
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
      notifySuccess(t("subs.saved"));
    });

  if (!loaded) return <CenterLoader />;

  return (
    <div className="flex flex-col gap-4 pb-20">
      <Card className="p-4">
        <h3 className="mb-3 font-bold text-ink">{t("subs.format")}</h3>
        <div className="flex flex-col gap-4">
          <div>
            <TextInput
              label={t("subs.path")}
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
                {t("subs.pathHint", { path: s.sub_path || "sub" })}
              </p>
            )}
          </div>
          <ToggleRow
            label={t("subs.base64")}
            hint={t("subs.base64Hint")}
            checked={s.sub_base64}
            onChange={(v) => patch({ sub_base64: v })}
          />
          <TextInput
            label={t("subs.title")}
            placeholder={t("subs.titlePlaceholder")}
            value={s.sub_title}
            onChange={(v) => patch({ sub_title: v })}
          />
          <ToggleRow
            label={t("subs.nameInTitle")}
            hint={t("subs.nameInTitleHint")}
            checked={s.sub_name_in_title}
            onChange={(v) => patch({ sub_name_in_title: v })}
          />
          <Select
            label={t("subs.updateInterval")}
            data={intervals()}
            value={String(s.sub_update_interval)}
            onChange={(v) => patch({ sub_update_interval: Number(v) })}
          />
          <div>
            <Textarea
              label={t("subs.announce")}
              placeholder={t("subs.announcePlaceholder")}
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
              {t("subs.announceHint")} {announceLen}/{ANNOUNCE_MAX}
            </p>
          </div>
        </div>
      </Card>

      <Card className="p-4">
        <div className="mb-3 flex items-center justify-between gap-3">
          <div>
            <h3 className="font-bold text-ink">{t("subs.routing")}</h3>
            <p className="text-xs text-ink-muted">
              {t("subs.routingHint")}{" "}
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
              label={t("subs.happRules")}
              placeholder="https://.../HAPP/DEFAULT.DEEPLINK"
              value={s.sub_routing_happ}
              onChange={(v) => patch({ sub_routing_happ: v })}
            />
            <TextInput
              label={t("subs.incyRules")}
              placeholder="https://.../INCY/DEFAULT.DEEPLINK"
              value={s.sub_routing_incy}
              onChange={(v) => patch({ sub_routing_incy: v })}
            />
            <div>
              <TextInput
                label={t("subs.mihomoRules")}
                placeholder="https://.../MIHOMO/default.yaml"
                value={s.sub_routing_mihomo}
                onChange={(v) => patch({ sub_routing_mihomo: v })}
              />
              <p className="mt-1 text-xs text-ink-muted">
                <Trans
                  i18nKey="subs.mihomoHint"
                  components={{
                    marker: (
                      <code className="rounded bg-gray-100 px-1 font-mono" />
                    ),
                  }}
                />
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
