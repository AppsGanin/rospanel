// @part head
// Made by the RosPanel rule builder. RULES is what the builder edits; the code
// below runs them, and holds only what these rules use. Edit it freely — once the
// code is changed by hand, the draft stays in the code editor.

const RULES = __RULES__;

// @part events
const DAY = 24 * 60 * 60;
// The events whose data is a user's own fields: user.* reads them there.
const USER_EVENTS = __USER_EVENTS__;

/** @param {PanelEvent} e */
export function onEvent(e) {
  const ctx = context(e);
  RULES.forEach((rule, i) => {
    if (rule.event !== e.event || !matches(rule, ctx)) return;
    // Delivery is at least once: each action is remembered as done for this event,
    // so a retry after a failure does not repeat what already went through.
    rule.actions.forEach((a, j) => once(`${e.id}/${i}/${j}`, () => act(a, ctx)));
  });
  forgetOld();
}

function context(e) {
  const data = e.data || {};
  let user = null;
  if (data.user && typeof data.user === "object") user = data.user;
  else if (USER_EVENTS.includes(e.event)) user = data;
  return { event: e.event, event_id: e.id, created_at: e.created_at, data, user, now: new Date().toISOString() };
}

// @part scheduled
function runScheduled(i) {
  const rule = RULES[i];
  const ctx = { event: "cron", event_id: "", created_at: Math.floor(Date.now() / 1000), data: {}, user: null, now: new Date().toISOString() };
  if (!matches(rule, ctx)) return;
  rule.actions.forEach((a) => act(a, ctx));
}

// @part text
function get(obj, path) {
  return String(path)
    .split(".")
    .reduce((v, k) => (v === null || v === undefined ? undefined : v[k]), obj);
}

function text(v) {
  if (v === null || v === undefined) return "";
  return typeof v === "object" ? JSON.stringify(v) : String(v);
}

// @part fill
// fill puts values into {{path}} placeholders, {{path|format}} shown through a
// format; escape, when given, then makes each fit where it goes.
function fill(template, ctx, escape) {
  return String(template || "").replace(/\{\{\s*([a-zA-Z0-9_.]+)\s*(?:\|\s*([a-z]+)\s*)?\}\}/g, (_, path, format) => {
    const raw = get(ctx, path);
    const v = format && FORMATS[format] ? FORMATS[format](raw) : text(raw);
    return escape ? escape(v) : v;
  });
}

// Formats: a time (unix seconds, or ISO like {{now}}) as the panel's date, the days
// left until it, bytes in GB, kopecks in roubles.
const FORMATS = {
  date: (v) => stamp(v, false),
  datetime: (v) => stamp(v, true),
  days: (v) => (seconds(v) ? String(Math.max(0, Math.ceil((seconds(v) - Date.now() / 1000) / 86400))) : ""),
  gb: (v) => String(Number(((Number(v) || 0) / 1073741824).toFixed(1))),
  rub: (v) => String(Number(((Number(v) || 0) / 100).toFixed(2))),
};

function seconds(v) {
  if (typeof v !== "number" && typeof v !== "string") return 0;
  if (typeof v === "string" && v !== "" && Number.isNaN(Number(v))) return Math.floor(Date.parse(v) / 1000) || 0;
  return Number(v) || 0;
}

// stamp is the time in the panel's timezone; none (0) is "—".
function stamp(v, withTime) {
  const s = seconds(v);
  if (!s) return "—";
  const d = new Date((s + panel.time.offset(s)) * 1000);
  const p = (n) => String(n).padStart(2, "0");
  const day = `${p(d.getUTCDate())}.${p(d.getUTCMonth() + 1)}.${d.getUTCFullYear()}`;
  return withTime ? `${day} ${p(d.getUTCHours())}:${p(d.getUTCMinutes())}` : day;
}

// @part conditions
function matches(rule, ctx) {
  const conds = rule.conditions || [];
  if (conds.length === 0) return true;
  const ok = (c) => holds(c, text(get(ctx, c.field)));
  return rule.match === "any" ? conds.some(ok) : conds.every(ok);
}

function holds(c, v) {
  const empty = v === "" || v === "0" || v === "false";
  switch (c.op) {
    case "eq": return v === String(c.value ?? "");
    case "ne": return v !== String(c.value ?? "");
    case "contains": return v.includes(String(c.value ?? ""));
    // A value the event does not have is neither more nor less than anything.
    case "gt": return v !== "" && Number(v) > Number(c.value);
    case "lt": return v !== "" && Number(v) < Number(c.value);
    case "empty": return empty;
    case "not_empty": return !empty;
  }
  return false;
}

// @part noconditions
const matches = () => true; // no rule has conditions

// @part act
function act(a, ctx) {
  switch (a.type) {
    case "telegram": return telegram(a, ctx);
    case "discord": return discord(a, ctx);
    case "http": return request(a, ctx);
    case "extend": return onUser(ctx, (id) => answered("extend", panel.api("POST", "/v1/users/bulk", { ids: [id], action: "extend", days: a.days })));
    case "enable": return onUser(ctx, (id) => answered("enable", panel.users.update(id, { enabled: true })));
    case "disable": return onUser(ctx, (id) => answered("disable", panel.users.update(id, { enabled: false })));
    case "tag": return onUser(ctx, (id) => retag(id, (tags) => (tags.includes(a.tag) ? null : [...tags, a.tag])));
    case "untag": return onUser(ctx, (id) => retag(id, (tags) => (tags.includes(a.tag) ? tags.filter((t) => t !== a.tag) : null)));
    case "log": return panel.log.info(fill(a.text, ctx));
  }
  throw new Error("unknown action " + a.type);
}

// @part telegram
function telegram(a, ctx) {
  const token = panel.config.telegram_token;
  if (!token) throw new Error("fill in the Telegram bot token in the plugin's settings");
  const chat = panel.config.telegram_chat;
  if (!chat) throw new Error("fill in the chat ID in the plugin's settings");
  const res = panel.http.fetch(`https://api.telegram.org/bot${token}/sendMessage`, {
    method: "POST",
    body: { chat_id: chat, text: fill(a.text, ctx), disable_web_page_preview: true },
  });
  answered("Telegram", res);
}

// @part discord
function discord(a, ctx) {
  const hook = panel.config.discord_webhook || "";
  if (!hook.startsWith("https://discord.com/api/webhooks/")) {
    throw new Error("the Discord webhook must be a https://discord.com/api/webhooks/… link");
  }
  answered("Discord", panel.http.fetch(hook, { method: "POST", body: { content: fill(a.text, ctx) } }));
}

// @part http
const inJSON = (v) => JSON.stringify(v).slice(1, -1);

function request(a, ctx) {
  const headers = { "Content-Type": "application/json" };
  if (a.auth && panel.config.http_authorization) headers.Authorization = panel.config.http_authorization;
  const method = (a.method || "POST").toUpperCase();
  // No body typed: the event as the panel delivers it.
  const whole = { id: ctx.event_id, event: ctx.event, created_at: ctx.created_at, data: ctx.data };
  const body = method === "GET" ? undefined : a.body ? fill(a.body, ctx, inJSON) : whole;
  const url = fill(a.url, ctx, encodeURIComponent);
  answered(url.split("?")[0], panel.http.fetch(url, { method, headers, body }));
}

// @part retag
// retag writes the user's tags when the change leaves them different (null: as
// they are).
function retag(id, change) {
  const cur = panel.users.get(id);
  answered("read user", cur);
  const next = change((cur.body && cur.body.data && cur.body.data.tags) || []);
  if (next) answered("tags", panel.users.update(id, { tags: next }));
}

// @part user
// onUser runs an action on the event's user. An event that names none this time
// (a sign-up request rejected with no account behind it) is let pass: retrying
// would not bring one.
function onUser(ctx, fn) {
  const id = ctx.user ? ctx.user.id : ctx.data.user_id;
  if (!id) return;
  return fn(id);
}

// @part answered
function answered(what, res) {
  if (res && res.status >= 300) {
    throw new Error(`${what} answered ${res.status}: ${text(res.body).slice(0, 200)}`);
  }
  return res;
}

// @part once
function once(key, fn) {
  if (panel.kv.get("done/" + key)) return;
  fn();
  const now = Math.floor(Date.now() / 1000);
  panel.kv.set("done/" + key, now);
  panel.kv.set("age/" + String(now).padStart(12, "0") + "/" + key, key);
}

// forgetOld drops what was done over a day ago, oldest first, a few at a time.
function forgetOld() {
  const now = Math.floor(Date.now() / 1000);
  for (const { key, value } of panel.kv.list("age/", { limit: 50 })) {
    if (now - Number(key.split("/")[1]) <= DAY) break;
    panel.kv.delete("done/" + value);
    panel.kv.delete(key);
  }
}
