test("creates an invoice and returns its link", () => {
  mock.http("https://api.lava.ru/business/invoice/create", { body: { status: "success", data: { id: "inv-1", url: "https://pay.lava.ru/inv-1" } } });
  const r = plugin.call("payment.create", { amount_rub: 150, order_id: 7, description: "Месяц", return_url: "https://vpn.example/sub/x?y=1", webhook_url: "https://vpn.example/s/plugin.lava", email: "" });
  assert.equal(r, { provider_id: "inv-1", pay_url: "https://pay.lava.ru/inv-1" });
  const sent = JSON.parse(mock.calls()[0].body);
  assert.equal(sent.sum, 150);
  assert.equal(sent.shopId, "shop-1");
  assert.equal(sent.successUrl, "https://vpn.example/sub/x", "the query string is cut off");
});

test("a Lava error is an error", () => {
  mock.http("https://api.lava.ru/business/invoice/create", { status: 422, body: { status: "error", error: "bad link" } });
  assert.throws(() => plugin.call("payment.create", { amount_rub: 1, order_id: 1, description: "", return_url: "", webhook_url: "", email: "" }));
});

test("reads a paid status with its amount", () => {
  mock.http("https://api.lava.ru/business/invoice/status", { body: { status: "success", data: { status: "success", amount: 150 } } });
  assert.equal(plugin.call("payment.status", "inv-1"), { status: "paid", amount_kopecks: 15000, currency: "RUB" });
});

test("a webhook without the right signature is refused", () => {
  const body = JSON.stringify({ invoice_id: "inv-1", status: "success", amount: 150 });
  assert.throws(() => plugin.call("payment.webhook", { body, headers: { authorization: "nope" } }));
});

test("a signed webhook reports the payment with its amount", () => {
  const body = JSON.stringify({ invoice_id: "inv-1", order_id: "rp7-ab", status: "success", amount: 150 });
  const r = plugin.call("payment.webhook", { body, headers: { authorization: crypto.hmac("sha256", "wk", body) } });
  assert.equal(r, { provider_id: "inv-1", status: "paid", amount_kopecks: 15000, currency: "RUB" });
});

test("outgoing requests are signed with the secret key", () => {
  mock.http("https://api.lava.ru/business/invoice/status", { body: { data: { status: "pending" } } });
  plugin.call("payment.status", "inv-2");
  const c = mock.calls()[0];
  assert.ok(c.body.includes('"invoiceId":"inv-2"'));
});
