test("the CSV goes to Telegram as a file", () => {
  mock.api("GET", "/v1/users?limit=500&offset=0", {
    body: { data: [
      { id: 1, name: "Ann", status: "active", used_up: 1e9, used_down: 5e8, data_limit: 1e10, expire_at: 1790000000 },
      { id: 2, name: 'Bob "the, builder"', status: "disabled", used_up: 0, used_down: 0, data_limit: 0, expire_at: 0 },
    ] },
  });
  mock.http("https://api.telegram.org/bot123:abc/sendDocument", { body: { ok: true } });

  assert.equal(plugin.call("onAction", { key: "send", user_ids: [] }), { ok: true, message: "Sent: 2 users" });

  const sent = mock.calls().find((c) => c.kind === "http").body;
  assert.ok(sent.includes('name="chat_id"\r\n\r\n-100500'));
  assert.ok(sent.includes('filename="users-'));
  assert.ok(sent.includes("Content-Type: text/csv"));
  assert.ok(sent.includes('1,Ann,active,1.50,10.00,2026-09-21'));
  assert.ok(sent.includes('2,"Bob ""the, builder""",disabled,0.00,,'));
});

test("a Telegram error fails the job", () => {
  mock.api("GET", "/v1/users?limit=500&offset=0", { body: { data: [] } });
  mock.http("https://api.telegram.org/bot123:abc/sendDocument", { status: 400, body: { ok: false } });
  assert.throws(() => plugin.cron("weekly"));
});
