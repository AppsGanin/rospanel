// A RosPanel plugin. The panel calls the functions this file exports; `panel` is
// how the plugin reaches back (see rospanel.d.ts). Everything runs synchronously:
// no timers, and every panel.* call returns its answer directly.

/** @param {PanelEvent<{id: number, name: string}>} e */
export function onEvent(e) {
  // Delivery is at-least-once: remember what was handled.
  if (panel.kv.get("seen/" + e.id)) return;
  panel.kv.set("seen/" + e.id, true);

  panel.db.exec("INSERT INTO greetings(user_id, text) VALUES (?, ?)", e.data.id, `${panel.config.greeting}, ${e.data.name}`);
  panel.log.info("greeted", { user: e.data.id });
}

export function daily() {
  const [{ n }] = panel.db.query("SELECT count(*) AS n FROM greetings");
  panel.log.info(`greetings so far: ${n}`);
}
