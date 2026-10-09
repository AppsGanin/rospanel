// Tells the operator's Telegram chat about new users, and once a day how many came.

/** Sends a message through the operator's bot; throws when Telegram refuses it. */
function send(text) {
  const res = panel.http.fetch(`https://api.telegram.org/bot${panel.config.bot_token}/sendMessage`, {
    method: "POST",
    body: { chat_id: panel.config.chat_id, text },
  });
  if (res.status !== 200) throw new Error(`Telegram answered ${res.status}: ${res.body.slice(0, 200)}`);
}

/** @param {PanelEvent<{id: number, name: string}>} e */
export function onEvent(e) {
  const lang = panel.config.lang;
  const user = e.data;
  // One transaction: if Telegram fails, the row is rolled back too and the panel's
  // retry of this event sends it again. An event that comes twice finds the row
  // (the primary key) and sends nothing.
  panel.db.tx(() => {
    const { changes } = panel.db.exec(
      "INSERT OR IGNORE INTO announced(user_id, name, source, at) VALUES (?, ?, ?, ?)",
      user.id, user.name, e.event, e.created_at,
    );
    if (changes === 0) return;
    const how = panel.t(e.event === "user.registered" ? "self_registered" : "created", {}, lang);
    send(`${panel.t("new_user", { name: user.name, id: String(user.id) }, lang)}\n${how}`);
  });
}

export function summary() {
  const since = Math.floor(Date.now() / 1000) - 86400;
  const [{ n }] = panel.db.query("SELECT count(*) AS n FROM announced WHERE at >= ?", since);
  send(panel.t("summary", { count: String(n) }, panel.config.lang));
}
