// Shared "applying settings" flow for changes that restart Xray. Saving routing,
// DNS, WARP, protocol toggles etc. regenerates the config and bounces Xray, which
// briefly drops :443 (and with it the panel). This shows a blocking modal and
// waits until Xray has actually come back before unblocking the UI.

import { useState } from "react";
import { getXrayStatus } from "./api";
import { errMessage, notifyError } from "./notify";
import { Modal, Spinner } from "./ui";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

// waitForReload polls until Xray reports a NEWER process start than `before`,
// meaning the config change has finished restarting it. Config validation keeps
// the old process alive for several seconds, and the restart itself briefly makes
// the panel unreachable — both are handled by polling on started_at and ignoring
// transient errors.
async function waitForReload(before: number) {
  const deadline = Date.now() + 120_000;
  while (Date.now() < deadline) {
    await sleep(2000);
    try {
      const s = await getXrayStatus();
      if (s.running && s.started_at > before) return;
    } catch {
      // panel unreachable during the restart window — keep waiting
    }
  }
}

// useXrayApply wraps a config-changing save: it runs `saveFn`, then blocks on the
// <ApplyingModal/> until Xray has restarted. `applying` drives the modal and can
// also feed a button's loading state.
export function useXrayApply() {
  const [applying, setApplying] = useState(false);
  const apply = async (saveFn: () => Promise<void>) => {
    let before = 0;
    try {
      before = (await getXrayStatus()).started_at;
    } catch {
      // ignore — fall back to before=0 (any running process counts as restarted)
    }
    setApplying(true);
    try {
      await saveFn();
      await waitForReload(before);
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setApplying(false);
    }
  };
  return { applying, apply };
}

export function ApplyingModal({ open }: { open: boolean }) {
  return (
    <Modal
      open={open}
      onClose={() => {}}
      dismissible={false}
      title="Применение настроек"
    >
      <div className="flex flex-col items-center gap-4 py-2">
        <Spinner size={36} className="text-brand-500" />
        <p className="text-center text-sm text-ink-muted">
          Xray перезапускается с новой конфигурацией. Это может занять до 30
          секунд — не закрывайте страницу.
        </p>
      </div>
    </Modal>
  );
}
