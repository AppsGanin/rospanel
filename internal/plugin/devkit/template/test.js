// `rospanel plugin test` runs this file. It is not part of the package.

test("greets a new user once", () => {
  plugin.event("user.created", { id: 7, name: "Ann" });
  const rows = plugin.db.query("SELECT user_id, text FROM greetings");
  assert.equal(rows, [{ user_id: 7, text: "Hello, Ann" }]);
});

test("runs the daily job", () => {
  plugin.cron("daily");
});
