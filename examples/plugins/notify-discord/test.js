const HOOK = "https://discord.com/api/webhooks/123/test-token";

test("posts a new user", () => {
  mock.http(HOOK, { status: 204 });
  plugin.event("user.created", { id: 5, name: "Ann" });
  const calls = mock.calls();
  assert.equal(calls.length, 1);
  assert.equal(JSON.parse(calls[0].body), { content: "🆕 New user: Ann" });
});

test("a repeated delivery is posted once", () => {
  mock.http(HOOK, { status: 204 });
  plugin.call("onEvent", { id: "evt-1", event: "node.down", created_at: 0, data: { name: "NL-1" } });
  plugin.call("onEvent", { id: "evt-1", event: "node.down", created_at: 0, data: { name: "NL-1" } });
  assert.equal(mock.calls().length, 1);
});

test("a Discord error makes the panel retry", () => {
  const before = plugin.kv.list("sent/").length;
  mock.http(HOOK, { status: 429 });
  assert.throws(() => plugin.event("user.created", { id: 6, name: "Bob" }));
  assert.equal(plugin.kv.list("sent/").length, before, "the failed event is not remembered as sent");
});

test("ignores events it does not describe", () => {
  plugin.call("onEvent", { id: "x", event: "user.deleted", created_at: 0, data: {} });
  assert.equal(mock.calls().length, 0);
});
