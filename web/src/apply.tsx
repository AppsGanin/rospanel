// Shared "applying settings" flow for changes that restart Xray. Saving routing,
// DNS, WARP, protocol toggles etc. regenerates the config and bounces Xray, which
// briefly drops :443 (and with it the panel). This shows a blocking modal and
// waits until Xray has actually come back before unblocking the UI.

import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ApiError, getXrayStatus } from "./api";
import { errMessage, notifyError } from "./notify";
import { Modal, Spinner } from "./ui";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

// waitForReload polls until the save has actually landed. Config validation keeps
// the old process alive for several seconds, and the restart itself briefly makes the
// panel unreachable — both are handled by polling and ignoring transient errors.
//
// Two ways to be done, because not every save restarts Xray: a NEWER process start,
// or a newer apply timestamp. The server skips the restart when the config it
// generated is the one already running (renaming a lane, switching a fingerprint,
// toggling TLS fragment — none of those reach the config file), and waiting only on
// started_at would then spin out the full timeout on a save that finished instantly.
async function waitForReload(before: number, beforeApplied: number): Promise<boolean> {
  const deadline = Date.now() + 120_000;
  while (Date.now() < deadline) {
    await sleep(2000);
    try {
      const s = await getXrayStatus();
      if (s.running && s.started_at > before) return true;
      if (s.applied_at !== undefined && s.applied_at > beforeApplied) return true;
    } catch {
      // panel unreachable during the restart window — keep waiting
    }
  }
  return false;
}

// useXrayApply wraps a config-changing save: it runs `saveFn`, then blocks on the
// <ApplyingModal/> until Xray has restarted. `applying` drives the modal and can
// also feed a button's loading state.
export function useXrayApply() {
  const [applying, setApplying] = useState(false);
  const apply = async (saveFn: () => Promise<void>) => {
    let before = 0;
    let beforeApplied = 0;
    try {
      const s = await getXrayStatus();
      before = s.started_at;
      beforeApplied = s.applied_at ?? 0;
    } catch {
      // ignore — fall back to 0 (any running process counts as restarted)
    }
    setApplying(true);
    try {
      await saveFn();
      await waitForReload(before, beforeApplied);
    } catch (e) {
      // An ApiError means the server answered and said no — report it as given.
      //
      // Anything else is the connection dropping, which proves nothing: a save that
      // restarts Xray takes the panel's own connection with it (:443 belongs to Xray;
      // the panel sits on its fallback), so the reply is lost even though the save
      // landed. Reporting that as a failure is how a successful save came to show
      // "Failed to fetch". Wait it out instead, and only complain if the config never
      // settles.
      if (e instanceof ApiError) {
        notifyError(errMessage(e));
      } else if (!(await waitForReload(before, beforeApplied))) {
        notifyError(errMessage(e));
      }
    } finally {
      setApplying(false);
    }
  };
  return { applying, apply };
}

export function ApplyingModal({ open }: { open: boolean }) {
  const { t } = useTranslation();
  return (
    <Modal
      open={open}
      onClose={() => {}}
      dismissible={false}
      title={t("apply.title")}
    >
      <div className="flex flex-col items-center gap-4 py-2">
        <Spinner size={36} className="text-brand-500" />
        <p className="text-center text-sm text-ink-muted">{t("apply.body")}</p>
      </div>
    </Modal>
  );
}
