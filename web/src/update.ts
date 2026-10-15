import { getMe } from "./api";

// reloadOnVersion reloads the page once the panel serves the version it is updating
// to — not merely when it answers. The old process keeps replying for ~2s before it
// restarts, so a bare reachability check would reload against a server about to drop.
// Two signals count: the reported version reaching target, and a down→up transition
// (a failed poll proves the restart happened), for versions formatted oddly.
// onGiveUp is called with "slow" (still answering, still old) or "down" (no answer).
export function reloadOnVersion(target: string, onGiveUp: (why: "slow" | "down") => void) {
  const want = target.replace(/^v/, "");
  let tries = 0;
  let wentDown = false;
  const poll = () => {
    getMe()
      .then((m) => {
        const running = (m.version || "").replace(/^v/, "");
        if (running === want || wentDown) {
          window.location.reload();
        } else if (++tries > 60) {
          onGiveUp("slow");
        } else {
          window.setTimeout(poll, 2000);
        }
      })
      .catch(() => {
        wentDown = true; // panel dropped ⇒ the restart is underway
        if (++tries > 60) {
          onGiveUp("down");
        } else {
          window.setTimeout(poll, 2000);
        }
      });
  };
  window.setTimeout(poll, 3000);
}
