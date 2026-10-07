const now = () => Math.floor(Date.now() / 1000);

test("shown to a user whose term ends soon", () => {
  const b = plugin.call("subBlocks", { user: { id: 1, expire_at: now() + 86400 }, lang: "ru" });
  assert.equal(b, [
    { type: "markdown", text: "**Скидка 20%** до пятницы" },
    { type: "button", label: "Продлить", url: "https://vpn.example/pay" },
  ]);
});

test("not shown to a user with a month left", () => {
  assert.equal(plugin.call("subBlocks", { user: { id: 1, expire_at: now() + 30 * 86400 }, lang: "ru" }), []);
});

test("the preview button says what the page shows", () => {
  assert.equal(plugin.call("onAction", { key: "preview", user_ids: [] }).message, "На странице: markdown + button");
});
