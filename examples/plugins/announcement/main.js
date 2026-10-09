// The operator's announcement on the subscription page: Markdown, an optional
// button, for everyone or only for those whose term ends within three days.

const DAY = 86400;

/** @param {{user: {id: number, expire_at: number}, lang: string}} req */
export function subBlocks(req) {
  const text = String(panel.config.text || "").trim();
  if (!text) return [];
  if (panel.config.who === "expiring") {
    const left = req.user.expire_at - Math.floor(Date.now() / 1000);
    if (!req.user.expire_at || left <= 0 || left > 3 * DAY) return [];
  }
  const blocks = [{ type: "markdown", text }];
  const url = String(panel.config.button_url || "");
  if (panel.config.button_label && url.startsWith("https://")) {
    blocks.push({ type: "button", label: panel.config.button_label, url });
  }
  return blocks;
}

export function onAction(req) {
  const shown = subBlocks({ user: { id: 0, expire_at: Math.floor(Date.now() / 1000) + DAY }, lang: "ru" });
  if (!shown.length) return { ok: false, message: "Текст не задан — на странице ничего не появится" };
  return { ok: true, message: `На странице: ${shown.map((b) => b.type).join(" + ")}` };
}
