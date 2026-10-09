// A CSV of every user, sent to a Telegram chat as a file. The CSV is built page by
// page and handed to Telegram as a blob in a multipart form (sendDocument).

const cell = (v) => {
  const s = v === null || v === undefined ? "" : String(v);
  return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
};
const date = (unix) => (unix ? new Date(unix * 1000).toISOString().slice(0, 10) : "");

function csv() {
  const rows = [["id", "name", "status", "used_gb", "limit_gb", "expires"].join(",")];
  for (let offset = 0; ; offset += 500) {
    const page = panel.api("GET", `/v1/users?limit=500&offset=${offset}`).body.data || [];
    for (const u of page) {
      const used = ((u.used_up || 0) + (u.used_down || 0)) / 1e9;
      rows.push([u.id, u.name, u.status, used.toFixed(2), u.data_limit ? (u.data_limit / 1e9).toFixed(2) : "", date(u.expire_at)].map(cell).join(","));
    }
    if (page.length < 500) return { text: rows.join("\n") + "\n", count: rows.length - 1 };
  }
}

function send() {
  const { text, count } = csv();
  const res = panel.http.fetch(`https://api.telegram.org/bot${panel.config.bot_token}/sendDocument`, {
    method: "POST",
    form: {
      chat_id: panel.config.chat_id,
      caption: `Users: ${count}`,
      document: { blob: panel.blob.from(text), filename: `users-${new Date().toISOString().slice(0, 10)}.csv`, type: "text/csv" },
    },
  });
  if (res.status !== 200) throw new Error(`Telegram answered ${res.status}: ${res.body.slice(0, 200)}`);
  return count;
}

export function weekly() {
  send();
}

/** @param {ActionRequest} req @returns {ActionResult} */
export function onAction() {
  return { ok: true, message: `Sent: ${send()} users` };
}
