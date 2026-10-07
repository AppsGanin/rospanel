const tg = "https://api.telegram.org/bot123:abc/sendMessage";

test("a new user is announced once", () => {
  mock.http(tg, { body: { ok: true } });

  plugin.event("user.registered", { id: 7, name: "Ann" });
  plugin.event("user.registered", { id: 7, name: "Ann" }); // the same event again

  const sent = mock.calls().filter((c) => c.kind === "http");
  assert.equal(sent.length, 1);
  assert.equal(JSON.parse(sent[0].body), {
    chat_id: "-100500",
    text: "🆕 Новый пользователь: Ann (#7)\nзарегистрировался сам",
  });
});

test("the summary counts the last day", () => {
  mock.http(tg, { body: { ok: true } });
  plugin.db.exec("INSERT INTO announced VALUES (8, 'Old', 'user.created', 0)"); // long ago

  plugin.cron("summary");

  const [call] = mock.calls();
  assert.equal(JSON.parse(call.body).text, "📊 За сутки новых пользователей: 1");
});

test("after a Telegram error the retry sends it", () => {
  mock.http(tg, { status: 502, body: "bad gateway" });
  assert.throws(() => plugin.event("user.created", { id: 9, name: "Bob" }));

  mock.reset();
  mock.http(tg, { body: { ok: true } });
  plugin.event("user.created", { id: 9, name: "Bob" }); // the panel delivers it again
  assert.equal(mock.calls().length, 1);
});
