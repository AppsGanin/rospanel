import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  abuseCategoryLabel,
  EMPTY_ABUSE_MEASURES,
  getAbuseSettings,
  refreshAbuseFeeds,
  saveAbuseSettings,
  type AbuseFeedStatus,
  type AbuseMeasures,
} from "./api";
import { useAction } from "./hooks";
import i18n, { currentLang } from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Button,
  CenterLoader,
  Mono,
  Panel,
  SaveBar,
  SettingRow,
  Switch,
  TextInput,
  Textarea,
} from "./ui";

// Categories in display order. Keys must match model.AbuseCategoryCatalog on the
// backend (and abuse.Category), since the mask is rebuilt from them on save.
//
// `kind` is on screen because it decides what a list can actually catch: a domain
// list only fires when the domain is visible, and on modern clients most traffic
// arrives as a bare IP instead. Without it, "35,764 entries" under a domain list
// reads as though it covered IPs too.
const CATEGORIES: { key: string; desc: string }[] = [
  { key: "badip", desc: "abuse.badipDesc" },
];

// fmtEntries names what the count actually is — ranges of addresses, not hosts.
function fmtEntries(n: number) {
  return i18n.t("abuse.ranges", {
    count: n,
    formatted: n.toLocaleString(currentLang()),
  });
}

function fmtBytes(n?: number) {
  if (!n) return "";
  if (n < 1024) return i18n.t("abuse.bytes", { n });
  if (n < 1024 * 1024) return i18n.t("abuse.kbytes", { n: Math.round(n / 1024) });
  return i18n.t("abuse.mbytes", { n: (n / 1024 / 1024).toFixed(1) });
}

function fmtWhen(ts?: number) {
  if (!ts) return i18n.t("abuse.notLoaded");
  return new Date(ts * 1000).toLocaleString(currentLang());
}

export function AbuseSettings() {
  const { t } = useTranslation();
  const [loaded, setLoaded] = useState(false);
  const [enabled, setEnabled] = useState(true);
  const [cats, setCats] = useState<Record<string, boolean>>({});
  const [custom, setCustom] = useState("");
  const [alertMin, setAlertMin] = useState(20);
  const [measures, setMeasures] = useState<AbuseMeasures>(EMPTY_ABUSE_MEASURES);
  const [status, setStatus] = useState<AbuseFeedStatus[]>([]);

  // Saved snapshot for dirty-tracking, same shape as the editable state.
  const [saved, setSaved] = useState({
    enabled: true,
    cats: {} as Record<string, boolean>,
    custom: "",
    alertMin: 20,
    measures: EMPTY_ABUSE_MEASURES,
  });

  const { run, isBusy } = useAction();

  const load = () =>
    getAbuseSettings().then((s) => {
      setEnabled(s.enabled);
      setCats(s.categories ?? {});
      setCustom(s.custom ?? "");
      setAlertMin(s.alert_min || 20);
      const m = { ...EMPTY_ABUSE_MEASURES, ...(s.measures ?? {}) };
      setMeasures(m);
      setStatus(s.status ?? []);
      setSaved({
        enabled: s.enabled,
        cats: s.categories ?? {},
        custom: s.custom ?? "",
        alertMin: s.alert_min || 20,
        measures: m,
      });
      setLoaded(true);
    });

  // biome-ignore lint/correctness/useExhaustiveDependencies: runs once on mount; the loader is redefined every render, so listing it would refetch in a loop
  useEffect(() => {
    load().catch((e) => {
      notifyError(errMessage(e));
      setLoaded(true);
    });
  }, []);

  const dirty = useMemo(() => {
    if (enabled !== saved.enabled) return true;
    if (custom !== saved.custom) return true;
    if (alertMin !== saved.alertMin) return true;
    if (JSON.stringify(measures) !== JSON.stringify(saved.measures)) return true;
    return CATEGORIES.some((c) => !!cats[c.key] !== !!saved.cats[c.key]);
  }, [enabled, cats, custom, alertMin, measures, saved]);

  const save = () =>
    run(
      async () => {
        await saveAbuseSettings({
          enabled,
          categories: cats,
          custom,
          alert_min: alertMin,
          measures,
        });
        notifySuccess(t("abuse.saved"));
        await load();
      },
      { key: "save" },
    );

  const cancel = () => {
    setEnabled(saved.enabled);
    setCats(saved.cats);
    setCustom(saved.custom);
    setAlertMin(saved.alertMin);
    setMeasures(saved.measures);
  };

  // A rung's threshold is a count of matches; 0 is the off switch. The other two
  // numbers only matter while a rung that uses them is on, so they are not
  // validated here — the server refuses a ladder that could not mean what it says.
  const num = (k: keyof AbuseMeasures, v: string) =>
    setMeasures((p) => ({ ...p, [k]: Math.max(0, Math.floor(Number(v) || 0)) }));
  const measuresOn = measures.throttle_min > 0 || measures.disable_min > 0;

  const doRefresh = () =>
    run(
      async () => {
        await refreshAbuseFeeds();
        notifySuccess(t("abuse.refreshStarted"));
        // The download runs in the background; re-read status shortly after.
        window.setTimeout(() => load().catch(() => {}), 8000);
      },
      { key: "refresh" },
    );

  if (!loaded) return <CenterLoader />;

  // Every measure is a threshold in matches-per-day, so they share one row shape.
  const measureRow = (
    k: keyof AbuseMeasures,
    label: string,
    hint?: string,
    disabled?: boolean,
  ) => (
    <SettingRow
      label={label}
      hint={hint}
      field={
        <TextInput
          type="number"
          value={String(measures[k])}
          disabled={!enabled || disabled}
          onChange={(v) => num(k, v)}
        />
      }
    />
  );

  return (
    <div className="flex flex-1 flex-col gap-3.5">
      {/* The feature's own switch lives in the section header: it governs every row
          below it, not one of them. */}
      <Panel
        title={t("abuse.title")}
        aside={<Switch checked={enabled} onChange={setEnabled} />}
      >
        <SettingRow hint={t("abuse.description")} />
      </Panel>

      <Panel
        title={t("abuse.lists")}
        aside={
          <Button
            size="xs"
            variant="outline"
            color="gray"
            loading={isBusy("refresh")}
            disabled={!enabled}
            onClick={doRefresh}
          >
            {t("abuse.refreshNow")}
          </Button>
        }
      >
        <SettingRow hint={t("abuse.listsDescription")} />
        {CATEGORIES.map((c) => {
          const st = status.find((s) => s.category === c.key);
          return (
            <SettingRow
              key={c.key}
              label={
                <span className="flex flex-wrap items-baseline gap-x-2">
                  {abuseCategoryLabel(c.key)}
                  {st && st.entries > 0 && (
                    <Mono className="text-[11px] font-normal text-ink-muted">
                      {fmtEntries(st.entries)}
                      {st.size ? ` · ${fmtBytes(st.size)}` : ""}
                    </Mono>
                  )}
                </span>
              }
              hint={
                <>
                  {t(c.desc as "abuse.badipDesc")}
                  {st && (
                    <>
                      {" "}
                      <Mono className="text-[11px]">
                        {t("abuse.updatedAt", { when: fmtWhen(st.updated) })}
                      </Mono>
                    </>
                  )}
                </>
              }
              control={
                <Switch
                  checked={!!cats[c.key]}
                  disabled={!enabled}
                  onChange={(v) => setCats((p) => ({ ...p, [c.key]: v }))}
                />
              }
            />
          );
        })}
      </Panel>

      <Panel title={t("abuse.customList")}>
        <SettingRow hint={t("abuse.customListDescription")}>
          <Textarea
            value={custom}
            onChange={setCustom}
            rows={6}
            placeholder={"203.0.113.0/24\n198.51.100.7\n2001:db8::/32"}
            hint={t("abuse.customHint")}
          />
        </SettingRow>
      </Panel>

      {/* The ladder: one row per rung, each a count of matches per day. */}
      <Panel title={t("abuse.measures")}>
        <SettingRow hint={t("abuse.measuresDescription")} />
        <SettingRow
          label={t("abuse.threshold")}
          hint={t("abuse.thresholdDescription")}
          field={
            <TextInput
              type="number"
              value={String(alertMin)}
              onChange={(v) => setAlertMin(Math.max(1, Number(v) || 1))}
            />
          }
        />
        {measureRow("warn_min", t("abuse.warnMin"), t("abuse.warnMinHint"))}
        {measureRow("throttle_min", t("abuse.throttleMin"))}
        {measureRow(
          "throttle_kbps",
          t("abuse.throttleKbps"),
          undefined,
          measures.throttle_min === 0,
        )}
        {measureRow("disable_min", t("abuse.disableMin"))}
        {measureRow("hours", t("abuse.hours"), t("abuse.hoursHint"), !measuresOn)}
      </Panel>

      <SaveBar
        dirty={dirty}
        busy={isBusy("save")}
        onSave={save}
        onCancel={cancel}
      />
    </div>
  );
}
