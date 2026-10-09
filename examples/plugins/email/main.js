// Messages for the users the panel's bot cannot reach, by e-mail. Each recipient of
// an event is mailed once: a retried event skips who already got it.

const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

const SUBJECT = {
  message: { ru: "Сообщение от сервиса", en: "A message from the service" },
  auto_message: { ru: "Сообщение от сервиса", en: "A message from the service" },
  broadcast: { ru: "Новости сервиса", en: "Service news" },
  expiring: { ru: "Подписка скоро закончится", en: "Your subscription ends soon" },
  traffic_low: { ru: "Трафик заканчивается", en: "Your traffic is running out" },
};

/** @param {{event_id: string, kind: string, notice?: string, text?: string, buttons?: {text: string, url: string}[], users: any[], data: any}} msg */
export const channel = {
  send(msg) {
    let delivered = 0;
    for (const u of msg.users) {
      const to = String(u.external_id || "");
      if (!EMAIL.test(to)) continue;
      if (panel.db.query("SELECT 1 FROM sent WHERE event_id = ? AND user_id = ?", msg.event_id, u.id).length) continue;
      const lang = u.lang === "en" ? "en" : "ru";
      mail(to, SUBJECT[msg.notice || msg.kind][lang], body(msg, lang));
      panel.db.exec("INSERT INTO sent(event_id, user_id, at) VALUES (?, ?, ?)", msg.event_id, u.id, now());
      delivered++;
    }
    return { delivered };
  },
};

function body(msg, lang) {
  if (msg.kind === "notice") {
    const d = msg.data || {};
    return msg.notice === "expiring"
      ? (lang === "en" ? `Your subscription ends in ${d.days_left} days.` : `Подписка закончится через ${d.days_left} дн.`)
      : (lang === "en" ? `You have used ${d.percent}% of your traffic.` : `Использовано ${d.percent}% трафика.`);
  }
  // The panel's messages are Telegram HTML: a short subset, fine in an e-mail too.
  const buttons = (msg.buttons || []).map((b) => `<p><a href="${escapeAttr(b.url)}">${escapeHTML(b.text)}</a></p>`).join("");
  return `<div>${String(msg.text || "").replace(/\n/g, "<br>")}</div>${buttons}`;
}

function mail(to, subject, html) {
  const res = panel.http.fetch("https://api.resend.com/emails", {
    method: "POST",
    headers: { Authorization: "Bearer " + panel.config.api_key },
    body: { from: panel.config.from, to: [to], subject, html },
  });
  if (res.status >= 300) throw new Error(`Resend answered ${res.status}: ${res.body.slice(0, 200)}`);
}

export function userFields(userId) {
  const user = panel.users.get(userId);
  const email = user.status === 200 ? String(user.body.data.external_id || "") : "";
  const last = panel.db.query("SELECT max(at) AS at FROM sent WHERE user_id = ?", userId)[0].at;
  return { email: EMAIL.test(email) ? email : undefined, last: last ? new Date(last * 1000).toISOString().slice(0, 16).replace("T", " ") : undefined };
}

export function widget(key) {
  const [{ n }] = panel.db.query("SELECT count(*) AS n FROM sent WHERE at > ?", now() - 7 * 86400);
  return { type: "stat", value: n };
}

const now = () => Math.floor(Date.now() / 1000);
const escapeHTML = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
const escapeAttr = escapeHTML;
