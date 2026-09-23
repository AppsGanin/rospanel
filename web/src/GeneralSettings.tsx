import { useEffect, useMemo, useState } from "react";
import i18n from "./i18n";
import { useTranslation } from "react-i18next";
import {
  applyUpdate,
  checkUpdate,
  getMe,
  getSettings,
  getConnPolicy,
  saveConnPolicy,
  getTrustedNets,
  listNodes,
  saveTrustedNets,
  type ConnPolicy,
  saveMaintenance,
  saveProbeDetect,
  saveProbeBlock,
  saveWatchdog,
  type WatchdogInfo,
  getStatusPage,
  regenSecret,
  saveStatusPage,
  setLocalBackup,
  setupTimezone,
  setUserAutoDelete,
  type SettingsInfo,
  type StatusPageSettings,
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
import { ConnPolicyCard, EMPTY_POLICY } from "./ConnPolicyCard";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { useCan, useIsOwner } from "./role";
import { browserTimezone, setPanelTimezone, tzOptions } from "./tz";
import {
  ReadOnly,
  Button,
  CenterLoader,
  cn,
  Code,
  IconButton,
  IconRestart,
  Modal,
  Mono,
  Panel,
  SaveBar,
  Select,
  SettingRow,
  Spinner,
  Switch,
  TagsInput,
  TextInput,
  ToggleRow,
  useConfirm,
} from "./ui";

// LocalBackup is the scheduled on-disk backup: a schedule plus how many archives to
// keep. Independent of the Telegram backup schedule — an operator with no bot still
// wants automatic backups.
type LocalBackup = { schedule: Schedule; keep: number };

const EMPTY_BK: LocalBackup = { schedule: EMPTY_SCHEDULE, keep: 7 };

// The status page is off with the conventional path until the panel says otherwise.
const EMPTY_STATUS: StatusPageSettings = { enabled: false, path: "status" };

// Grace period between a user's expiry date and their deletion. "Never" is the
// default and is deliberately first: deleting paying customers because a dropdown
// defaulted to something is not a mistake anyone should be able to make by accident.
const autodeleteOptions = () => [
  { value: "0", label: i18n.t("general.never") },
  ...[7, 30, 90, 180, 365].map((d) => ({
    value: String(d),
    label: i18n.t("general.daysAfterExpiry", { count: d }),
  })),
];

// decoyLabel maps decoy slugs to friendly names. Exported so the master/node
// settings dialogs (where the decoy is now chosen) show the same labels.
export const decoyLabel = (slug: string): string => {
  switch (slug) {
    case "coming-soon":
      return i18n.t("decoy.comingSoon");
    case "nginx":
      return i18n.t("decoy.nginx");
    case "maintenance":
      return i18n.t("decoy.maintenance");
    case "10gag":
      return i18n.t("decoy.gag");
    case "503-1":
      return i18n.t("decoy.err503a");
    case "503-2":
      return i18n.t("decoy.err503b");
    case "converter":
      return i18n.t("decoy.converter");
    case "downloader":
      return i18n.t("decoy.downloader");
    case "filecloud":
      return i18n.t("decoy.filecloud");
    default:
      return slug; // YouTube, Speedtest — brand names, same in every language
  }
};

export function GeneralSettings() {
  const { t } = useTranslation();
  // This tab gathers five permissions' worth of settings; each block below shows,
  // and is editable, by its own.
  const canUpdate = useCan("system.update");
  const canSetView = useCan("settings.view");
  const canSetManage = useCan("settings.manage");
  const canSecView = useCan("security.view");
  const canSecManage = useCan("security.manage");
  // The local backup schedule is the owner's, like backups themselves.
  const canBackup = useIsOwner();
  const canServers = useCan("servers.view");
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
  // How many nodes an update could take along, and whether the one running does.
  const [nodeCount, setNodeCount] = useState(0);
  const [updNodes, setUpdNodes] = useState(false);
  const { isBusy, run } = useAction();
  const { confirm, confirmNode } = useConfirm();
  const [newSecret, setNewSecret] = useState("");
  const [status, setStatus] = useState<StatusPageSettings>(EMPTY_STATUS);
  const [policy, setPolicy] = useState<ConnPolicy>(EMPTY_POLICY);
  const [policySaved, setPolicySaved] = useState<ConnPolicy>(EMPTY_POLICY);
  // Trusted networks ride the Save bar like the policy: an address list is typed, and
  // half of one should not be in force.
  const [trusted, setTrusted] = useState<string[]>([]);
  const [trustedSaved, setTrustedSaved] = useState<string[]>([]);
  const [savedStatus, setSavedStatus] = useState<StatusPageSettings>(EMPTY_STATUS);
  // Maintenance is a live toggle (it takes effect the moment it's flipped), so it
  // saves on change rather than riding the page's Save bar.
  const [maintenance, setMaintenanceState] = useState(false);
  // Probe detection is a live toggle too. The scanner list loads lazily when the
  // card is open.
  const [probeDetect, setProbeDetectState] = useState(false);
  const [probeBlock, setProbeBlockState] = useState(false);
  // Watchdog: a live toggle plus the read-only auto-recovery counters.
  const [watchdog, setWatchdog] = useState<WatchdogInfo | null>(null);


  const tzList = useMemo(
    () => tzOptions(timezone || browserTimezone()),
    [timezone],
  );

  // biome-ignore lint/correctness/useExhaustiveDependencies: loads once; the permissions do not change while the page is open
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
      canSetView &&
        getStatusPage()
          .then((s) => {
            setStatus(s);
            setSavedStatus(s);
          })
          .catch(() => {}),
      canSecView &&
        getConnPolicy()
          .then((info) => {
            setPolicy(info.policy);
            setPolicySaved(info.policy);
          })
          .catch(() => {}),
      canSecView &&
        getTrustedNets()
          .then(({ nets }) => {
            setTrusted(nets);
            setTrustedSaved(nets);
          })
          .catch(() => {}),
      // GET /api/settings answers the settings, security and servers views; an
      // update-only role has none of them, and nothing of it to show.
      (canSetView || canSecView || canBackup) &&
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
          setMaintenanceState(s.maintenance_mode);
          setProbeDetectState(s.probe_detect);
          setProbeBlockState(s.probe_block);
          setWatchdog(s.watchdog);
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
  const statusDirty =
    status.enabled !== savedStatus.enabled || status.path !== savedStatus.path;
  // The source policy is a draft like everything else on this page: one Save at the
  // bottom, one Cancel.
  const policyDirty = JSON.stringify(policy) !== JSON.stringify(policySaved);
  const trustedDirty = trusted.join() !== trustedSaved.join();
  const dirty =
    timezone !== savedTz || bkDirty || adDirty || statusDirty || policyDirty || trustedDirty;
  // The path is a bare URL segment; the server refuses anything else (and any
  // collision with the panel's other surfaces), but there is no reason to let the
  // operator get that far with an obviously wrong value.
  const statusPathErr =
    status.enabled && !/^[A-Za-z0-9_-]+$/.test(status.path)
      ? t("general.statusPathBad")
      : "";
  const saveBlocked = !!statusPathErr;

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
        if (statusDirty) {
          await saveStatusPage(status);
          setSavedStatus(status);
        }
        if (policyDirty) {
          await saveConnPolicy(policy);
          setPolicySaved(policy);
        }
        if (trustedDirty) {
          await saveTrustedNets(trusted);
          // Shown back as the server stored them: "198.51.100.7/24" is 198.51.100.0/24.
          const { nets } = await getTrustedNets();
          setTrusted(nets);
          setTrustedSaved(nets);
        }
        notifySuccess(t("general.saved"));
        // Last, once everything is stored: a new zone redraws the dashboard, this
        // page with it, and the page reads back what was just saved.
        setPanelTimezone(timezone);
      },
      { key: "save" },
    );

  const cancel = () => {
    setTimezone(savedTz);
    setBk(savedBk);
    setAutoDel(savedAutoDel);
    setStatus(savedStatus);
    setPolicy(policySaved);
    setTrusted(trustedSaved);
  };

  const doRegenSecret = async () => {
    const ok = await confirm({
      title: t("general.regenTitle"),
      body: t("general.regenBody"),
      confirmLabel: t("general.regen"),
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
        // The node list is the servers' to read; without it the nodes are not offered
        // an update from here (they still update from the servers page).
        if (info.available && canServers) {
          listNodes()
            .then((r) => setNodeCount(r.nodes.length))
            .catch(() => setNodeCount(0));
        }
        if (info.error) notifyError(info.error);
        else if (!info.available) notifySuccess(t("general.upToDate"));
      },
      { key: "upd-check" },
    );

  const doUpdate = async (withNodes: boolean) => {
    if (!upd?.latest) return;
    const ok = await confirm({
      title: t("general.updateTitle", { version: upd.latest }),
      body: t(withNodes ? "general.updateBodyNodes" : "general.updateBody"),
      confirmLabel: t("general.update"),
    });
    if (!ok) return;
    const target = upd.latest.replace(/^v/, "");
    setUpdNodes(withNodes);
    setUpdating(true);
    try {
      const r = await applyUpdate(withNodes);
      if (withNodes) {
        if (r.nodes_error) notifyError(t("general.updateNodesFailed"));
        else notifySuccess(t("nodes.updateStarted", { count: r.nodes ?? 0 }));
      }
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
            notifyError(t("general.updateSlow"));
          } else {
            window.setTimeout(poll, 2000);
          }
        })
        .catch(() => {
          wentDown = true; // panel dropped ⇒ the restart is underway
          if (++tries > 60) {
            setUpdating(false);
            notifyError(t("general.noAnswer"));
          } else {
            window.setTimeout(poll, 2000);
          }
        });
    };
    window.setTimeout(poll, 3000);
  };

  if (!loaded) return <CenterLoader />;

  return (
    <div className="flex flex-1 flex-col gap-3.5">
      {/* What is running, and what could be. The check is a section action: it
          answers the whole section, not one of its rows. */}
      {canUpdate && (
      <Panel
        title={t("general.updateSection")}
        aside={
          <Button
            size="xs"
            variant="light"
            color="gray"
            loading={isBusy("upd-check")}
            disabled={updating}
            onClick={doCheckUpdate}
          >
            {t("general.checkUpdates")}
          </Button>
        }
      >
        <SettingRow
          label={t("general.currentVersion")}
          control={<Mono className="text-xs text-ink">v{version || "—"}</Mono>}
        />
        {upd?.available && upd.latest && (
          <SettingRow
            label={t("general.availableVersion")}
            control={
              <div className="flex flex-wrap items-center justify-end gap-2">
                <Mono className="text-xs text-accent">v{upd.latest}</Mono>
                <Button
                  size="xs"
                  loading={updating && !updNodes}
                  disabled={updating}
                  onClick={() => doUpdate(false)}
                >
                  {t("general.updatePanel")}
                </Button>
                {nodeCount > 0 && (
                  <Button
                    size="xs"
                    variant="light"
                    loading={updating && updNodes}
                    disabled={updating}
                    onClick={() => doUpdate(true)}
                  >
                    {t("general.updatePanelAndNodes")}
                  </Button>
                )}
              </div>
            }
          />
        )}
        <Modal
          open={updating}
          onClose={() => {}}
          dismissible={false}
          title={t("general.updateSection")}
        >
          <div className="flex items-start gap-3">
            <Spinner size={22} className="mt-0.5 shrink-0" />
            <p className="text-sm text-ink">{t("general.updatingHint")}</p>
          </div>
        </Modal>
      </Panel>
      )}

      {/* The panel itself: where it can be reached from, what it shows the world,
          and what clock its numbers are counted on. */}
      {canSetView && (
      <ReadOnly when={!canSetManage}>
      <Panel title={t("general.secPanel")}>
        <SettingRow
          label={t("wizard.timezone")}
          hint={t("general.timezoneHint")}
          wideField
          field={
            <Select data={tzList} value={timezone} onChange={setTimezone} searchable />
          }
        />
        <SettingRow
          label={t("general.secretPath")}
          hint={t("general.secretPathHint")}
          control={
            <IconButton
              color="red"
              title={t("general.regen")}
              disabled={isBusy("secret")}
              onClick={doRegenSecret}
            >
              <IconRestart />
            </IconButton>
          }
        >
          <Code block>/{settings?.secret_path}/</Code>
        </SettingRow>
        <SettingRow
          label={t("general.statusPage")}
          hint={t("general.statusPageHint")}
          control={
            <Switch
              checked={status.enabled}
              onChange={(enabled) => setStatus((s) => ({ ...s, enabled }))}
            />
          }
        >
          {status.enabled && (
            <>
              <TextInput
                label={t("general.statusPagePath")}
                value={status.path}
                mono
                onChange={(path) =>
                  setStatus((s) => ({
                    ...s,
                    path: path.replace(/[^A-Za-z0-9_-]/g, ""),
                  }))
                }
              />
              <p
                className={cn(
                  "mt-1 text-[11px]",
                  statusPathErr ? "text-danger" : "text-ink-muted",
                )}
              >
                {statusPathErr ||
                  t("general.statusPagePathHint", { path: status.path || "status" })}
              </p>
            </>
          )}
        </SettingRow>
        {/* Live switch: it takes effect on the flip, not on Save. */}
        <ToggleRow
          label={t("general.maintenance")}
          hint={t("general.maintenanceHint")}
          checked={maintenance}
          onChange={(v) =>
            run(async () => {
              await saveMaintenance(v);
              setMaintenanceState(v);
              notifySuccess(
                v
                  ? t("general.maintenanceEnabled")
                  : t("general.maintenanceDisabled"),
              );
            })
          }
        />
      </Panel>
      </ReadOnly>
      )}

      {/* What the panel keeps and for how long. */}
      {(canBackup || canSetView) && (
      <Panel title={t("general.secData")}>
        {canBackup && (
        <SettingRow
          label={t("general.autoBackups")}
          hint={t("general.autoBackupsHint")}
        >
          <CronPicker
            value={bk.schedule}
            onChange={(schedule) => setBk((b) => ({ ...b, schedule }))}
            offLabel={t("general.autoBackupsOff")}
            // Retention only means something once a schedule exists, and it belongs
            // beside it: "every day at 03:00, keep 7 copies" is one sentence.
            extra={
              bkCron ? (
                <TextInput
                  label={t("general.keepCopies")}
                  type="number"
                  value={String(bk.keep)}
                  onChange={(v) =>
                    setBk((b) => ({
                      ...b,
                      keep: Number(v.replace(/\D/g, "")) || 0,
                    }))
                  }
                />
              ) : undefined
            }
          />
          {bkCron && (
            <p className="mt-1.5 text-[11px] text-ink-muted">
              {t("general.keepCopiesHint")}
            </p>
          )}
          <p className="mt-2 text-[11px] leading-relaxed text-warning">
            {t("general.backupWarn")}
          </p>
        </SettingRow>
        )}
        {canSetView && (
        <ReadOnly when={!canSetManage}>
        <SettingRow
          label={t("general.autodelete")}
          hint={t("general.autodeleteHint")}
          field={
            <Select
              data={autodeleteOptions()}
              value={String(autoDel)}
              onChange={(v) => setAutoDel(Number(v))}
            />
          }
        >
          <p className="text-[11px] leading-relaxed text-ink-muted">
            {autoDel === 0 ? t("general.autodeleteOff") : t("general.autodeleteOn")}
          </p>
        </SettingRow>
        </ReadOnly>
        )}
      </Panel>
      )}

      {/* What the panel does about being poked at, and about its own Xray hanging.
          The switches are live; the trusted list is a draft on the Save bar. */}
      {(canSecView || canSetView) && (
      <Panel title={t("general.secProtect")}>
        {canSecView && (
        <ReadOnly when={!canSecManage}>
        <ToggleRow
          label={t("general.probeDetect")}
          hint={t("general.probeDetectHint")}
          checked={probeDetect}
          onChange={(v) =>
            run(async () => {
              await saveProbeDetect(v);
              setProbeDetectState(v);
            })
          }
        />
        {probeDetect && (
          <ToggleRow
            label={t("general.probeBlock")}
            hint={t("general.probeBlockHint")}
            checked={probeBlock}
            onChange={(v) =>
              run(async () => {
                await saveProbeBlock(v);
                setProbeBlockState(v);
              })
            }
          />
        )}
        <SettingRow label={t("general.trusted")} hint={t("general.trustedHint")}>
          <TagsInput value={trusted} onChange={setTrusted} placeholder="203.0.113.10" />
        </SettingRow>
        </ReadOnly>
        )}
        {canSetView && watchdog && (
        <ReadOnly when={!canSetManage}>
          <ToggleRow
            label={t("general.watchdog")}
            hint={t("general.watchdogHint")}
            checked={watchdog.enabled}
            onChange={(v) =>
              run(async () => {
                await saveWatchdog(v);
                setWatchdog((w) => (w ? { ...w, enabled: v } : w));
              })
            }
          />
        </ReadOnly>
        )}
      </Panel>
      )}

      {canSecView && (
        <ReadOnly when={!canSecManage}>
          <ConnPolicyCard value={policy} onChange={setPolicy} />
        </ReadOnly>
      )}

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
        title={t("general.secretPathChanged")}
      >
        <div className="flex flex-col gap-4">
          <p className="text-sm leading-relaxed text-ink-muted">
            {t("wizard.newPathIntro")}
          </p>
          <Code block copy>
            {`${window.location.origin}/${newSecret}/`}
          </Code>
          <div className="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-800">
            {t("wizard.newPathWarn")}
          </div>
          <div className="flex justify-end">
            <Button
              onClick={() => {
                window.location.href = `${window.location.origin}/${newSecret}/`;
              }}
            >
              {t("wizard.savedGoToNew")}
            </Button>
          </div>
        </div>
      </Modal>
    </div>
  );
}
