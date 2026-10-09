// Lava Business (api.lava.ru). Every request body is signed with the secret key
// (HMAC-SHA256, hex, in the Signature header); a webhook is signed with the
// additional key in its Authorization header. The panel matches the amount we
// report against the order — never skip it.

const API = "https://api.lava.ru/business";

const STATUS = {
  success: "paid",
  created: "pending", pending: "pending", processing: "pending",
  cancel: "cancelled", cancelled: "cancelled", expired: "cancelled", error: "cancelled", failed: "cancelled",
};

function post(path, payload) {
  const body = JSON.stringify(payload); // signed as sent: build the string once
  const res = panel.http.fetch(API + path, {
    method: "POST",
    body,
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json",
      Signature: panel.crypto.hmac("sha256", panel.config.secret_key, body),
    },
  });
  let j;
  try {
    j = JSON.parse(res.body);
  } catch (_) {
    throw new Error(`Lava answered ${res.status}: ${res.body.slice(0, 200)}`);
  }
  if (res.status >= 300 || j.status === "error") throw new Error(`Lava: ${j.error || j.message || res.status}`);
  return j.data || j;
}

// Lava refuses a success URL with a query string.
const bare = (url) => String(url || "").split("?")[0];

const kopecks = (rub) => Math.round(Number(rub || 0) * 100);

export const payment = {
  /** @param {PaymentCreateRequest} req */
  create(req) {
    const d = post("/invoice/create", {
      sum: req.amount_rub,
      orderId: `rp${req.order_id}-${panel.crypto.randomHex(4)}`,
      shopId: panel.config.shop_id,
      hookUrl: req.webhook_url,
      successUrl: bare(req.return_url),
      failUrl: bare(req.return_url),
      expire: Math.min(Math.max(Number(panel.config.lifetime) || 60, 1), 7200),
      comment: String(req.description || "").slice(0, 255),
    });
    const id = d.id || d.invoice_id;
    const url = d.url || d.payment_url;
    if (!id || !url) throw new Error("Lava returned no invoice");
    return { provider_id: String(id), pay_url: url };
  },

  status(invoiceId) {
    const d = post("/invoice/status", { shopId: panel.config.shop_id, invoiceId });
    return {
      status: STATUS[String(d.status || "").toLowerCase()] || "pending",
      amount_kopecks: kopecks(d.amount ?? d.sum),
      currency: "RUB",
    };
  },

  webhook({ body, headers }) {
    const got = String(headers.authorization || "").trim().toLowerCase();
    const want = panel.crypto.hmac("sha256", panel.config.webhook_key, body);
    if (!got || got !== want) throw new Error("bad signature");
    const b = JSON.parse(body);
    return {
      provider_id: String(b.invoice_id),
      status: STATUS[String(b.status || "").toLowerCase()] || "pending",
      amount_kopecks: kopecks(b.amount),
      currency: "RUB",
    };
  },
};
