// @part head
// Made by the RosPanel rule builder. RULES is what the builder edits; the code
// below runs them, and holds only what these rules use. Edit it freely — once the
// code is changed by hand, the draft stays in the code editor.

const RULES = __RULES__;

// @part events
const DAY = 24 * 60 * 60;

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
  else if (String(e.event).startsWith("user.")) user = data;
  return { event: e.event, data, user, now: new Date().toISOString() };
}

// @part scheduled
function runScheduled(i) {
  const rule = RULES[i];
  const ctx = { event: "cron", data: {}, user: null, now: new Date().toISOString() };
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
// fill puts values into {{path}} placeholders; escape, when given, formats each.
function fill(template, ctx, escape) {
  return String(template || "").replace(/\{\{\s*([a-zA-Z0-9_.]+)\s*\}\}/g, (_, path) => {
    const v = text(get(ctx, path));
    return escape ? escape(v) : v;
  });
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
    case "gt": return Number(v) > Number(c.value);
    case "lt": return Number(v) < Number(c.value);
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
    case "extend": return answered("extend", panel.api("POST", "/v1/users/bulk", { ids: [userId(ctx)], action: "extend", days: a.days }));
    case "enable": return answered("enable", panel.users.update(userId(ctx), { enabled: true }));
    case "disable": return answered("disable", panel.users.update(userId(ctx), { enabled: false }));
    case "tag": return retag(ctx, (tags) => (tags.includes(a.tag) ? tags : [...tags, a.tag]));
    case "untag": return retag(ctx, (tags) => tags.filter((t) => t !== a.tag));
    case "log": return panel.log.info(fill(a.text, ctx));
  }
  throw new Error("unknown action " + a.type);
}

// @part telegram
function telegram(a, ctx) {
  const token = panel.config.telegram_token;
  if (!token) throw new Error("fill in the Telegram bot token in the plugin's settings");
  const chat = fill(a.chat, ctx);
  if (!chat || chat === "0") return panel.log.warn("telegram: no chat to write to (the user has no Telegram?)");
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
  const body = method === "GET" ? undefined : a.body ? fill(a.body, ctx, inJSON) : { event: ctx.event, data: ctx.data };
  const url = fill(a.url, ctx, encodeURIComponent);
  answered(url.split("?")[0], panel.http.fetch(url, { method, headers, body }));
}

// @part retag
function retag(ctx, change) {
  const id = userId(ctx);
  const cur = panel.users.get(id);
  answered("read user", cur);
  const tags = (cur.body && cur.body.data && cur.body.data.tags) || [];
  answered("tags", panel.users.update(id, { tags: change(tags) }));
}

// @part user
function userId(ctx) {
  const id = ctx.user ? ctx.user.id : ctx.data.user_id;
  if (!id) throw new Error(`the ${ctx.event} event names no user`);
  return id;
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
