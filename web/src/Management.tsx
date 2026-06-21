import { useState } from "react";
import { AppLogs } from "./AppLogs";
import { getBackupInfo, resetPanel } from "./api";
import { useFetch } from "./hooks";
import { errMessage, notifyError } from "./notify";
import {
  BACKUP_ACCEPT,
  ManifestCard,
  RestoreWaiting,
  useRestore,
  ValidationNote,
} from "./restore";
import { Button, Card, cn, Modal } from "./ui";

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
        danger ? "text-red-600 hover:text-red-700" : "text-ink-muted hover:text-ink",
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
  "flex h-11 w-11 items-center justify-center rounded-lg bg-brand-600 text-white transition hover:bg-brand-700";

/* --------------------------------------------------------------- card */
export function ManagementCard() {
  const { data: info } = useFetch(getBackupInfo);
  const [logsOpen, setLogsOpen] = useState(false);
  const [backupOpen, setBackupOpen] = useState(false);
  const [resetOpen, setResetOpen] = useState(false);
  const [resetting, setResetting] = useState(false);
  const [resetUrl, setResetUrl] = useState<string | null>(null);
  const { fileRef, inspection, manifest, inspecting, restoring, done, pick, restore } =
    useRestore();

  const doReset = async () => {
    setResetting(true);
    try {
      const { url } = await resetPanel();
      setResetOpen(false);
      setResetUrl(url || `${window.location.origin}/rospanel/`);
    } catch (e) {
      notifyError(errMessage(e));
      setResetting(false);
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

  return (
    <>
      <Card className="p-4">
        <h3 className="mb-2 font-bold text-ink">Управление</h3>
        <div className="flex flex-col items-stretch divide-y divide-gray-200 border-t border-gray-100 pt-1 sm:flex-row sm:divide-x sm:divide-y-0">
          <ManageBtn icon={<IconList />} label="Логи" onClick={() => setLogsOpen(true)} />
          <ManageBtn
            icon={<IconArchive />}
            label="Бэкап и восстановление"
            onClick={() => setBackupOpen(true)}
          />
          <ManageBtn icon={<IconReset />} label="Сброс" danger onClick={() => setResetOpen(true)} />
        </div>
      </Card>

      {logsOpen && <AppLogs onClose={() => setLogsOpen(false)} />}

      {/* Backup & restore */}
      <Modal open={backupOpen} onClose={closeBackup} title="Бэкап и восстановление">
        {!manifest ? (
          <div className="flex flex-col divide-y divide-gray-100 overflow-hidden rounded-xl border border-gray-200">
            <Row
              title="Экспорт базы данных"
              desc="Скачать архив с базой данных, TLS-сертификатами и конфигом Xray."
            >
              <a href="api/backup" download="rospanel-backup.tar.gz" onClick={closeBackup} className={sqBtn}>
                <IconDownload />
              </a>
            </Row>
            <Row
              title="Импорт базы данных"
              desc="Загрузить архив для восстановления данных из резервной копии."
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
            <ManifestCard m={manifest} label="В бэкапе" />
            {inspection && <ValidationNote inspection={inspection} />}
            {manifest.domain && info?.domain && manifest.domain !== info.domain && (
              <p className="mt-3 text-sm text-orange-600">
                Домен в бэкапе ({manifest.domain}) отличается от текущего ({info.domain}). После
                восстановления войдите через новый адрес.
              </p>
            )}
            {inspection?.valid && (
              <p className="mt-3 text-sm text-red-600">
                Все текущие данные будут заменены. Панель перезапустится — войдите заново.
              </p>
            )}
            <div className="mt-4 flex justify-end gap-2">
              <Button variant="outline" color="gray" size="sm" onClick={() => pick(null)}>
                Назад
              </Button>
              <Button
                variant="filled"
                color="red"
                size="sm"
                loading={restoring}
                disabled={!inspection?.valid}
                onClick={restore}
              >
                Восстановить
              </Button>
            </div>
          </>
        )}
      </Modal>

      {/* Reset */}
      <Modal open={resetOpen} onClose={() => setResetOpen(false)} title="Сброс до заводских настроек">
        <p className="text-sm text-red-600">
          Все данные будут удалены без возможности восстановления: пользователи, настройки,
          секретный путь, TLS-сертификат. Панель перезапустится в режиме первого запуска по адресу{" "}
          <code>/rospanel/</code> (admin/admin). Сделайте бэкап заранее.
        </p>
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="outline" color="gray" size="sm" onClick={() => setResetOpen(false)}>
            Отмена
          </Button>
          <Button variant="filled" color="red" size="sm" loading={resetting} onClick={doReset}>
            Сбросить всё
          </Button>
        </div>
      </Modal>
    </>
  );
}
