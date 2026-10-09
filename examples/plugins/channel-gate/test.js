const api = "https://api.telegram.org/bot123:abc/getChatMember?chat_id=%40my_channel&user_id=";

test("a member signs up", () => {
  mock.http(api + "7", { body: { ok: true, result: { status: "member" } } });
  assert.equal(plugin.call("beforeSignup", { channel: "bot", telegram_id: 7 }).allow, true);
});

test("someone outside the channel is refused", () => {
  mock.http(api + "8", { body: { ok: true, result: { status: "left" } } });
  assert.equal(plugin.call("beforeSignup", { channel: "miniapp", telegram_id: 8 }), { allow: false, reason: "join" });
});

test("Telegram not answering lets the sign-up through", () => {
  mock.http(api + "9", { status: 502, body: "bad gateway" });
  assert.equal(plugin.call("beforeSignup", { channel: "bot", telegram_id: 9 }).allow, true);
});

test("a website sign-up follows the setting", () => {
  assert.equal(plugin.call("beforeSignup", { channel: "web", external_id: "a@b.c" }).allow, true);
});

test("the bot's button tells a non-member where to go", () => {
  mock.http(api + "8", { body: { ok: true, result: { status: "left" } } });
  const r = plugin.call("bot.onCallback", { user: { id: 1, telegram_id: 8 }, data: "check", lang: "en" });
  assert.equal(r.text, "You are not subscribed to @my_channel.");
  assert.equal(r.buttons, [{ text: "Open the channel", url: "https://t.me/my_channel" }]);
});
