import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { AppLogs } from "./AppLogs";
import { getBackupInfo, resetPanel, restartPanel } from "./api";
import { EMPTY_STEP_UP, type StepUp, StepUpFields, stepUpReady, useTotpEnabled } from "./stepup";
import { useFetch } from "./hooks";
import { errMessage, notifyError } from "./notify";
import {
  BACKUP_ACCEPT,
  ManifestCard,
  RestoreWaiting,
  useRestore,
  ValidationNote,
} from "./restore";
import { Button, Card, cn, Modal, PasswordInput } from "./ui";

/* ----------------------------------------------------------------- icons */
function IconList() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
      <path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01" />
    </svg>
  );
}
function IconArchive() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M21 8v13H3V8M1 3h22v5H1zM10 12h4" />
    </svg>
  );
}
function IconPower() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 3v9M18.4 6.6a9 9 0 1 1-12.8 0" />
    </svg>
  );
}
function IconReset() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M3 12a9 9 0 1 0 3-6.7L3 8M3 3v5h5" />
    </svg>
  );
}
function IconDownload() {
  return (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 3v12M7 10l5 5 5-5M5 21h14" />
    </svg>
  );
}
function IconUpload() {
  return (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 21V9M7 14l5-5 5 5M5 3h14" />
    </svg>
  );
}

/* --------------------------------------------------------------- pieces */
function ManageBtn({
  icon,
  label,
  danger,
  onClick,
}: {
  icon: React.ReactNode;
  label: string;
  danger?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      className={cn(
        "flex flex-1 items-center justify-center gap-2 px-2 py-2 text-sm font-medium transition",
        danger ? "text-danger hover:text-danger" : "text-ink-muted hover:text-ink",
      )}
    >
      {icon}
      <span className="truncate">{label}</span>
    </button>
  );
}

// Row is one labelled action line inside the backup modal (export / import).
function Row({
  title,
  desc,
  children,
}: {
  title: string;
  desc: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-center justify-between gap-4 p-4">
      <div className="min-w-0">
        <p className="font-semibold text-ink">{title}</p>
        <p className="mt-0.5 text-sm text-ink-muted">{desc}</p>
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  );
}

const sqBtn =
  "flex h-11 w-11 items-center justify-center rounded-lg bg-brand-600 text-onaccent transition hover:bg-brand-700";

/* --------------------------------------------------------------- card */
export function ManagementCard() {
  const { t } = useTranslation();
  const totpEnabled = useTotpEnabled();
  const { data: info } = useFetch(getBackupInfo);
  const [logsOpen, setLogsOpen] = useState(false);
  const [backupOpen, setBackupOpen] = useState(false);
  const [resetOpen, setResetOpen] = useState(false);
  const [resetCreds, setResetCreds] = useState<StepUp>(EMPTY_STEP_UP);
  const [restorePw, setRestorePw] = useState("");
  const [resetting, setResetting] = useState(false);
  const [resetUrl, setResetUrl] = useState<string | null>(null);
  const [restartOpen, setRestartOpen] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [restartWait, setRestartWait] = useState(false);
  const { fileRef, inspection, manifest, inspecting, restoring, done, pick, restore } =
    useRestore();

  const closeReset = () => {
    setResetOpen(false);
    setResetCreds(EMPTY_STEP_UP);
  };

  const doReset = async () => {
    setResetting(true);
    try {
      const { url } = await resetPanel(resetCreds.password, resetCreds.code);
      closeReset();
      setResetUrl(url || `${window.location.origin}/rospanel/`);
    } catch (e) {
      notifyError(errMessage(e));
      setResetting(false);
    }
  };

  // The panel answers before it goes down, so the wait screen (which polls the
  // current address and returns here once it answers) starts on the 200.
  const doRestart = async () => {
    setRestarting(true);
    try {
      await restartPanel();
      setRestartOpen(false);
      setRestartWait(true);
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setRestarting(false);
    }
  };

  const closeBackup = () => {
    setBackupOpen(false);
    pick(null); // drop any picked-but-unconfirmed file
  };

  // Full-screen takeover while the panel restarts after a restore or reset.
  // RestoreWaiting renders its own (non-dismissible) Modal — do NOT wrap it.
  if (done) {
    return <RestoreWaiting manifest={done} currentDomain={info?.domain} />;
  }
  if (resetUrl) {
    return <RestoreWaiting url={resetUrl} />;
  }
  // A plain restart keeps the address, so the wait screen polls where we already are
  // (and drops the cert/redirect wording, which only applies to a move).
  if (restartWait) {
    return <RestoreWaiting url={window.location.href} sameAddress />;
  }

  return (
    <>
      <Card className="p-4">
        <h3 className="mb-2 font-bold text-ink">{t("manage.title")}</h3>
        <div className="flex flex-col items-stretch divide-y divide-gray-200 border-t border-gray-100 pt-1 sm:flex-row sm:divide-x sm:divide-y-0">
          {/* Diagnostics is per-server now — it lives on each card under Servers,
              where the server it describes is. */}
          <ManageBtn icon={<IconList />} label={t("manage.logs")} onClick={() => setLogsOpen(true)} />
          {/* One word, like every other button in this row — the modal it opens is
              still titled "backup and restore", so nothing is hidden. */}
          <ManageBtn icon={<IconArchive />} label={t("manage.backups")} onClick={() => setBackupOpen(true)} />
          <ManageBtn
            icon={<IconPower />}
            label={t("manage.restart")}
            onClick={() => setRestartOpen(true)}
          />
          <ManageBtn icon={<IconReset />} label={t("manage.reset")} danger onClick={() => setResetOpen(true)} />
        </div>
      </Card>

      {logsOpen && <AppLogs onClose={() => setLogsOpen(false)} />}

      {/* Backup & restore */}
      <Modal open={backupOpen} onClose={closeBackup} title={t("manage.backupRestore")}>
        {!manifest ? (
          <div className="flex flex-col divide-y divide-gray-100 overflow-hidden rounded-xl border border-gray-200">
            <Row
              title={t("manage.exportTitle")}
              desc={t("manage.exportDesc")}
            >
              <a href="api/backup" download="rospanel-backup.tar.gz" onClick={closeBackup} className={sqBtn}>
                <IconDownload />
              </a>
            </Row>
            <Row
              title={t("manage.importTitle")}
              desc={t("manage.importDesc")}
            >
              <input
                ref={fileRef}
                type="file"
                accept={BACKUP_ACCEPT}
                className="hidden"
                onChange={(e) => pick(e.target.files?.[0] ?? null)}
              />
              <button className={sqBtn} disabled={inspecting} onClick={() => fileRef.current?.click()}>
                <IconUpload />
              </button>
            </Row>
          </div>
        ) : (
          <>
            <ManifestCard m={manifest} label={t("wizard.inBackup")} />
            {inspection && <ValidationNote inspection={inspection} />}
            {manifest.domain && info?.domain && manifest.domain !== info.domain && (
              <p className="mt-3 text-sm text-warning">
                {t("manage.domainDiffers", { backup: manifest.domain, current: info.domain })}
              </p>
            )}
            {inspection?.valid && (
              <p className="mt-3 text-sm text-danger">
                {t("manage.restoreWarn")}
              </p>
            )}
            {/* A restore replaces the admin roster this session is authenticated
                against, so the panel re-asks for the password before staging it. */}
            {inspection?.valid && (
              <div className="mt-3">
                <PasswordInput
                  label={t("creds.currentPassword")}
                  value={restorePw}
                  onChange={setRestorePw}
                />
              </div>
            )}
            <div className="mt-4 flex justify-end gap-2">
              <Button
                variant="outline"
                color="gray"
                size="sm"
                onClick={() => {
                  setRestorePw("");
                  pick(null);
                }}
              >
                {t("common.back")}
              </Button>
              <Button
                variant="filled"
                color="red"
                size="sm"
                loading={restoring}
                disabled={!inspection?.valid || !restorePw}
                onClick={() => restore(restorePw)}
              >
                {t("manage.restore")}
              </Button>
            </div>
          </>
        )}
      </Modal>

      {/* Restart the panel process */}
      <Modal open={restartOpen} onClose={() => setRestartOpen(false)} title={t("manage.restartTitle")}>
        <p className="text-sm text-ink-muted">
          {t("manage.restartBody")}
        </p>
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="outline" color="gray" size="sm" onClick={() => setRestartOpen(false)}>
            {t("common.cancel")}
          </Button>
          <Button variant="filled" color="red" size="sm" loading={restarting} onClick={doRestart}>
            {t("manage.restartConfirm")}
          </Button>
        </div>
      </Modal>

      {/* Reset */}
      <Modal open={resetOpen} onClose={closeReset} title={t("manage.resetTitle")}>
        <p className="text-sm text-danger">
          <Trans
            i18nKey="manage.resetBody"
            components={{ c: <code /> }}
          />
        </p>
        {/* The panel re-asks for the password — and for a fresh authenticator code
            when this admin has one: a reset is irreversible, so a session cookie alone
            must not be enough to trigger it. */}
        <StepUpFields value={resetCreds} onChange={setResetCreds} />
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="outline" color="gray" size="sm" onClick={closeReset}>
            {t("common.cancel")}
          </Button>
          <Button
            variant="filled"
            color="red"
            size="sm"
            loading={resetting}
            disabled={!stepUpReady(resetCreds, totpEnabled)}
            onClick={doReset}
          >
            {t("manage.resetConfirm")}
          </Button>
        </div>
      </Modal>
    </>
  );
}
