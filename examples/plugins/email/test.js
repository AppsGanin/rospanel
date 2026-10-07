const msg = (over) => Object.assign({
  event_id: "e1", kind: "broadcast", text: "News\nline 2", buttons: [{ text: "Open", url: "https://x.example" }],
  users: [{ id: 1, external_id: "ann@example.com", lang: "en" }, { id: 2, external_id: "no-email" }],
  data: {},
}, over);

test("mails the users with an address, once per event", () => {
  mock.http("https://api.resend.com/emails", { status: 200, body: { id: "m1" } });
  assert.equal(plugin.call("channel.send", msg()), { delivered: 1 });
  const sent = JSON.parse(mock.calls()[0].body);
  assert.equal(sent.to, ["ann@example.com"]);
  assert.equal(sent.subject, "Service news");
  assert.ok(sent.html.includes("News<br>line 2") && sent.html.includes('href="https://x.example"'));
  // A retried event mails nobody twice.
  assert.equal(plugin.call("channel.send", msg()), { delivered: 0 });
});

test("a reminder is worded by the plugin", () => {
  mock.http("https://api.resend.com/emails", { status: 200, body: {} });
  plugin.call("channel.send", msg({ event_id: "e2", kind: "notice", notice: "expiring", text: "", buttons: [], data: { days_left: 3 } }));
  assert.equal(JSON.parse(mock.calls()[0].body).html, "Your subscription ends in 3 days.");
});

test("a refused e-mail is retried by the panel", () => {
  mock.http("https://api.resend.com/emails", { status: 429, body: {} });
  assert.throws(() => plugin.call("channel.send", msg({ event_id: "e3" })));
});

test("the card shows the address and the last e-mail", () => {
  mock.api("GET", "/v1/users/1", { body: { data: { external_id: "ann@example.com" } } });
  const f = plugin.call("userFields", 1);
  assert.equal(f.email, "ann@example.com");
  assert.ok(f.last);
});

test("the widget counts the week", () => {
  assert.equal(plugin.call("widget", "sent"), { type: "stat", value: 2 });
});
