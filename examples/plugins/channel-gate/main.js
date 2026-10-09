// A new account through Telegram only for a member of the operator's channel.
// Telegram's getChatMember answers that; the bot must be an administrator there.

const MEMBER = ["creator", "administrator", "member", "restricted"];

/** @returns {"yes" | "no" | "unknown"} */
function member(telegramId) {
  const res = panel.http.fetch(
    `https://api.telegram.org/bot${panel.config.bot_token}/getChatMember?chat_id=${encodeURIComponent(panel.config.channel)}&user_id=${telegramId}`,
    { timeout_ms: 250 },
  );
  if (res.status !== 200) return "unknown";
  const st = JSON.parse(res.body).result?.status;
  if (st === "restricted") return JSON.parse(res.body).result.is_member ? "yes" : "no";
  return MEMBER.includes(st) ? "yes" : "no";
}

/** @param {SignupRequest} req @returns {Decision} */
export function beforeSignup(req) {
  if (!req.telegram_id) return { allow: panel.config.web !== "false", reason: "need_telegram" };
  // Telegram not answering lets the sign-up through: the panel works without us.
  return { allow: member(req.telegram_id) !== "no", reason: "join" };
}

function status(user, lang) {
  const m = member(user.telegram_id);
  const key = m === "yes" ? "member" : m === "no" ? "not_member" : "unknown";
  return {
    text: panel.t(key, { channel: panel.config.channel }, lang),
    buttons: m === "no" ? [{ text: panel.t("open", {}, lang), url: "https://t.me/" + panel.config.channel.replace(/^@/, "") }] : [],
  };
}

/** @type {PluginBot} */
export const bot = {
  menu({ lang }) {
    return [{ text: panel.t("check_button", {}, lang), data: "check" }];
  },
  onCallback({ user, lang }) {
    return status(user, lang);
  },
  onCommand({ user, lang }) {
    return status(user, lang);
  },
};
