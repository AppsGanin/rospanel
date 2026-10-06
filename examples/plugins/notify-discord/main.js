// Discord notifications: one message per event, into the channel whose webhook the
// operator pasted. Delivery to the plugin is at-least-once, so each event id is
// remembered for a day and a repeat is skipped.

const DAY = 24 * 60 * 60;

/** @param {PanelEvent} e */
export function onEvent(e) {
  if (panel.kv.get("sent/" + e.id)) return;
  const text = message(e);
  if (!text) return;

  const res = panel.http.fetch(webhook(), { method: "POST", body: { content: text } });
  if (res.status === 429) throw new Error("Discord rate limit — the panel will retry");
  if (res.status >= 300) throw new Error(`Discord answered ${res.status}: ${res.body.slice(0, 200)}`);

  panel.kv.set("sent/" + e.id, Math.floor(Date.now() / 1000));
  forgetOld();
}

function webhook() {
  const url = panel.config.webhook_url || "";
  if (!url.startsWith("https://discord.com/api/webhooks/")) {
    throw new Error("the webhook must be a https://discord.com/api/webhooks/… link");
  }
  return url;
}

function message(e) {
  const lang = panel.config.lang || "ru";
  const d = e.data || {};
  switch (e.event) {
    case "user.created":
      return panel.t("userCreated", { name: d.name || String(d.id) }, lang);
    case "payment.paid":
      if (panel.config.payments !== "1") return "";
      return panel.t("paymentPaid", { amount: String(d.amount_rub ?? "?"), name: d.user_name || String(d.user_id ?? "") }, lang);
    case "node.down":
      return panel.t("nodeDown", { name: d.name || d.host || String(d.node_id) }, lang);
    case "node.up":
      return panel.t("nodeUp", { name: d.name || d.host || String(d.node_id) }, lang);
  }
  return "";
}

// forgetOld drops remembered event ids older than a day, a few at a time.
function forgetOld() {
  const now = Math.floor(Date.now() / 1000);
  for (const { key, value } of panel.kv.list("sent/", { limit: 50 })) {
    if (now - value > DAY) panel.kv.delete(key);
  }
}
