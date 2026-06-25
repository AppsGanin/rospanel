// Shared backup-restore building blocks used by both the first-run wizard and
// the Settings → Бэкап panel: the manifest preview card, the file-pick/inspect/
// restore lifecycle hook, and the post-restore "panel restarting" screen.

import { useEffect, useRef, useState } from "react";
import {
  inspectBackup,
  restoreBackup,
  type BackupInspection,
  type BackupManifest,
} from "./api";
import { errMessage, notifyError } from "./notify";
import { Modal, Spinner } from "./ui";

// File-picker accept list. macOS doesn't recognize the compound ".tar.gz"
// extension, so the gzip/tar MIME types are listed alongside it.
export const BACKUP_ACCEPT =
  ".tar.gz,.tgz,application/gzip,application/x-gzip,application/x-tar";

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex gap-2">
      <span className="text-ink-muted">{label}</span>
      <span className="font-medium text-ink">{value}</span>
    </div>
  );
}

// ManifestCard previews what a backup contains (or what the current server holds).
export function ManifestCard({
  m,
  label,
}: {
  m: BackupManifest;
  label?: string;
}) {
  const date = m.created_at
    ? new Date(m.created_at).toLocaleString("ru-RU", {
        dateStyle: "medium",
        timeStyle: "short",
      })
    : null;
  return (
    <div className="flex flex-col gap-1 rounded-xl bg-gray-50 px-4 py-3 text-sm">
      {label && (
        <div className="mb-1 text-xs font-semibold uppercase tracking-wide text-ink-muted">
          {label}
        </div>
      )}
      {m.domain && <Row label="Домен" value={m.domain} />}
      <Row label="Путь панели" value={`/${m.secret_path}/`} />
      <Row label="Пользователей" value={String(m.user_count)} />
      {date && <Row label="Создан" value={date} />}
    </div>
  );
}

// ValidationNote flags a problem with an inspected backup. A valid backup shows
// nothing (the manifest preview already conveys what it contains).
export function ValidationNote({
  inspection,
}: {
  inspection: BackupInspection;
}) {
  if (inspection.valid) {
    return null;
  }
  return (
    <p className="mt-2 text-sm text-danger">
      ✗ {inspection.issue || "Бэкап нельзя восстановить."}
    </p>
  );
}

// useRestore manages the file-pick → inspect → restore lifecycle. `done` holds
// the restored manifest once the upload succeeds (the caller then renders
// <RestoreWaiting/>).
export function useRestore() {
  const fileRef = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const [inspection, setInspection] = useState<BackupInspection | null>(null);
  const [inspecting, setInspecting] = useState(false);
  const [restoring, setRestoring] = useState(false);
  const [done, setDone] = useState<BackupManifest | null>(null);

  // pick inspects the chosen file: it previews the manifest and validates that
  // the embedded database is real and non-empty before the destructive restore.
  const pick = async (f: File | null) => {
    setFile(f);
    setInspection(null);
    if (!f) return;
    setInspecting(true);
    try {
      setInspection(await inspectBackup(f));
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setInspecting(false);
    }
  };

  const restore = async () => {
    if (!file) return;
    setRestoring(true);
    try {
      await restoreBackup(file);
      setDone(inspection?.manifest ?? null);
    } catch (e) {
      notifyError(errMessage(e));
      setRestoring(false);
    }
  };

  return {
    fileRef,
    file,
    inspection,
    manifest: inspection?.manifest ?? null,
    inspecting,
    restoring,
    done,
    pick,
    restore,
  };
}

// RestoreWaiting shows a spinner after a restore and polls the restored panel's
// URL, redirecting there as soon as it answers. `currentDomain` (the domain the
// browser is on now) drives the cross-domain hint.
export function RestoreWaiting({
  manifest,
  currentDomain,
  url,
}: {
  manifest?: BackupManifest;
  currentDomain?: string;
  // Explicit target URL (used by factory reset, where the panel moves to a
  // different host than the current origin). Falls back to the manifest.
  url?: string;
}) {
  const newUrl =
    url ??
    (manifest?.domain
      ? `https://${manifest.domain}/${manifest.secret_path}/`
      : `${window.location.origin}/${manifest?.secret_path ?? ""}/`);

  useEffect(() => {
    let alive = true;
    let elapsed = 0;
    const maxWait = 30000; // give up polling after this and just navigate
    (async () => {
      while (alive) {
        await new Promise((r) => setTimeout(r, 2500));
        if (!alive) return;
        elapsed += 2500;
        try {
          await fetch(newUrl, { signal: AbortSignal.timeout(4000) });
          if (alive) window.location.href = newUrl;
          return;
        } catch {
          // Panel still down — or its TLS cert changed (e.g. after a factory
          // reset the address/cert identity shifts and fetch can't probe past a
          // cert error). Once enough time has passed, navigate regardless: a
          // browser cert prompt is better than hanging here forever.
          if (elapsed >= maxWait && alive) {
            window.location.href = newUrl;
            return;
          }
        }
      }
    })();
    return () => {
      alive = false;
    };
  }, [newUrl]);

  const crossDomain =
    !!manifest?.domain && !!currentDomain && manifest.domain !== currentDomain;

  return (
    <Modal open onClose={() => {}} dismissible={false} title="Панель перезапускается…">
      <div className="flex flex-col items-center gap-5 py-2">
        <Spinner size={40} className="text-brand-500" />
        <p className="text-center text-sm text-ink-muted">
          Вас автоматически перенаправит, как только панель поднимется. Если
          браузер предупредит о сертификате после смены адреса — это нормально,
          продолжите по ссылке.
        </p>
        <div className="flex flex-col items-center gap-1">
          <p className="text-xs text-ink-muted">Адрес панели:</p>
          <a
            href={newUrl}
            className="break-all font-mono text-xs text-accent hover:underline"
          >
            {newUrl}
          </a>
        </div>
        {crossDomain && (
          <p className="text-center text-sm text-warning">
            Домен изменился. После входа проверьте раздел Настройки → Домен.
          </p>
        )}
      </div>
    </Modal>
  );
}
