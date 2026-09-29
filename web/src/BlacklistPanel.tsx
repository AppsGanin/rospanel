import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { currentLang } from "./i18n";
import { getBlacklist, refreshBlacklist, saveBlacklist, type BlacklistInfo } from "./api";
import { errMessage, notifyError } from "./notify";
import { inPanelTz } from "./tz";
import { Button, Panel, SettingRow, Switch, TextInput } from "./ui";

// BlacklistPanel is the shared Telegram blacklist: accounts other VPN services
// banned. Saved on its own, apart from the page's save bar — turning it on fetches
// the list at once.
export function BlacklistPanel() {
  const { t } = useTranslation();
  const [info, setInfo] = useState<BlacklistInfo | null>(null);
  const [url, setUrl] = useState("");
  const [busy, setBusy] = useState(false);

  const take = (i: BlacklistInfo) => {
    setInfo(i);
    setUrl(i.url);
  };
  // biome-ignore lint/correctness/useExhaustiveDependencies: runs once on mount; take is redefined every render
  useEffect(() => {
    getBlacklist()
      .then(take)
      .catch(() => setInfo(null));
  }, []);
  if (!info) return null;

  const run = (p: Promise<BlacklistInfo>) => {
    setBusy(true);
    p.then(take)
      .catch((e) => {
        notifyError(errMessage(e));
        getBlacklist().then(take).catch(() => {});
      })
      .finally(() => setBusy(false));
  };
  const synced = info.synced_at
    ? new Date(info.synced_at * 1000).toLocaleString(
        currentLang(),
        inPanelTz({ day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" }),
      )
    : "";

  return (
    <Panel
      title={t("blacklist.title")}
      aside={
        <Switch checked={info.enabled} disabled={busy} onChange={(v) => run(saveBlacklist(v, url))} />
      }
    >
      <SettingRow hint={t("blacklist.hint")} />
      <SettingRow
        label={t("blacklist.url")}
        wideField
        field={
          <span className="flex items-center gap-2">
            <TextInput value={url} onChange={setUrl} placeholder={info.default_url} />
            {url !== info.url && (
              <Button size="xs" loading={busy} onClick={() => run(saveBlacklist(info.enabled, url))}>
                {t("common.save")}
              </Button>
            )}
          </span>
        }
      />
      <SettingRow
        label={
          info.count > 0
            ? t("blacklist.status", { count: info.count, when: synced })
            : t("blacklist.notLoaded")
        }
        hint={info.error ? <span className="text-danger">{info.error}</span> : undefined}
        control={
          <Button size="xs" variant="light" loading={busy} onClick={() => run(refreshBlacklist())}>
            {t("blacklist.refresh")}
          </Button>
        }
      />
    </Panel>
  );
}
