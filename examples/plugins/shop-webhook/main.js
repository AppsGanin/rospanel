// A shop sells access; after each sale it POSTs {"order": "...", "name": "..."} to
// the plugin's address (the plugin card shows it), signed with the shared secret.
// The plugin creates the user once per order and answers with their link, which
// the shop shows to the buyer. Repeating a sale's request gives the same link.

/** @param {HTTPRequest} req */
export function onHttp(req) {
  if (req.method !== "POST") return json(405, { error: "POST only" });
  const want = panel.crypto.hmac("sha256", panel.config.secret, req.body);
  if (String(req.headers["x-signature"] || "").toLowerCase() !== want) return json(401, { error: "bad signature" });

  let sale;
  try {
    sale = JSON.parse(req.body);
  } catch (_) {
    return json(400, { error: "the body must be JSON" });
  }
  const order = String(sale.order || "").trim();
  if (!order || order.length > 100) return json(400, { error: "order is required" });

  let userId = panel.kv.get("order/" + order);
  if (!userId) {
    const created = panel.api("POST", "/v1/users", {
      name: String(sale.name || "shop-" + order).slice(0, 64),
      plan_id: Number(panel.config.plan_id) || 0,
    });
    if (created.status !== 201 && created.status !== 200) {
      panel.log.error("creating the user failed", { order, status: created.status, body: created.body });
      return json(502, { error: "could not create the account" });
    }
    userId = created.body.data.id;
    panel.kv.set("order/" + order, userId);
  }
  const sub = panel.api("GET", `/v1/users/${userId}/subscription`);
  if (sub.status !== 200) return json(502, { error: "could not read the subscription" });
  return json(200, { user_id: userId, subscription_url: sub.body.data.sub_url });
}

const json = (status, body) => ({ status, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
