const sale = (body) => ({
  method: "POST", path: "/", query: {}, ip: "203.0.113.5", body,
  headers: { "x-signature": crypto.hmac("sha256", "s3", body) },
});

test("creates a user for a sale and returns the link", () => {
  mock.api("POST", "/v1/users", { status: 201, body: { data: { id: 42 } } });
  mock.api("GET", "/v1/users/42/subscription", { body: { data: { sub_url: "https://vpn.example/s/abc" } } });
  const r = plugin.call("onHttp", sale(JSON.stringify({ order: "A-1", name: "Ann" })));
  assert.equal(r.status, 200);
  assert.equal(JSON.parse(r.body), { user_id: 42, subscription_url: "https://vpn.example/s/abc" });
  assert.equal(JSON.parse(mock.calls()[0].body), { name: "Ann", plan_id: 2 });
});

test("the same order gives the same account", () => {
  mock.api("GET", "/v1/users/42/subscription", { body: { data: { sub_url: "https://vpn.example/s/abc" } } });
  const r = plugin.call("onHttp", sale(JSON.stringify({ order: "A-1", name: "Ann" })));
  assert.equal(JSON.parse(r.body).user_id, 42);
  assert.ok(!mock.calls().some((c) => c.method === "POST"), "no second account");
});

test("an unsigned request is refused", () => {
  const r = plugin.call("onHttp", { method: "POST", path: "/", query: {}, headers: {}, body: "{}", ip: "1.2.3.4" });
  assert.equal(r.status, 401);
});
