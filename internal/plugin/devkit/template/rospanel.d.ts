// Types for RosPanel plugins, api 1. `rospanel plugin new` puts this file next to
// main.js; VS Code picks it up through jsconfig.json, in plain JavaScript too.
// Everything here is part of the plugin contract: it is only ever added to.

/** A string, or bytes as {base64: "…"} — what the crypto and db calls accept. */
type Data = string | { base64: string };

interface PanelLog {
  info(msg: unknown, fields?: unknown): void;
  warn(msg: unknown, fields?: unknown): void;
  error(msg: unknown, fields?: unknown): void;
}

interface PanelKV {
  /** The value stored under key (any JSON), or undefined. */
  get<T = unknown>(key: string): T | undefined;
  /** Stores any JSON value, up to 64 KB. */
  set(key: string, value: unknown): void;
  delete(key: string): void;
  /** Pairs whose key starts with prefix, ordered by key; page with {after}. */
  list<T = unknown>(prefix?: string, opts?: { after?: string; limit?: number }): { key: string; value: T }[];
}

type SQLValue = string | number | boolean | null | { base64: string };

interface PanelDB {
  /** Runs a statement on the plugin's own SQLite database. */
  exec(sql: string, ...args: SQLValue[]): { changes: number; last_insert_id: number };
  /** Rows as objects keyed by column; at most 10 000 rows / 4 MB. */
  query<T = Record<string, SQLValue>>(sql: string, ...args: SQLValue[]): T[];
  /** Runs fn in a transaction: committed if it returns, rolled back if it throws. */
  tx<T>(fn: () => T): T;
}

interface HTTPResponse {
  status: number;
  /** Lower-case header names. */
  headers: Record<string, string>;
  /** The body as text — JSON.parse it yourself. */
  body: string;
}

/**
 * Data kept by the panel outside the plugin's memory, for the length of one call
 * (up to 256 MB each, 512 MB and 16 blobs per call). A handle from another call is dead.
 */
interface Blob {
  blob: string;
  size: number;
}

/** A form field: text, a blob sent as a file, or a blob with its file name and type. */
type FormValue = string | number | boolean | Blob | { blob: Blob; filename?: string; type?: string };

interface PanelHTTP {
  /**
   * Requests a URL whose host is in the manifest's "net". A body that is not a
   * string is sent as JSON; a Blob is sent as it is; `form` sends
   * multipart/form-data. `blob: true` puts the answer in a Blob. Private and local
   * addresses are never reachable.
   */
  fetch(
    url: string,
    opts?: { method?: string; headers?: Record<string, string>; body?: unknown; timeout_ms?: number; form?: Record<string, FormValue>; blob?: false },
  ): HTTPResponse;
  fetch(
    url: string,
    opts: { method?: string; headers?: Record<string, string>; body?: unknown; timeout_ms?: number; form?: Record<string, FormValue>; blob: true },
  ): { status: number; headers: Record<string, string>; body: Blob };
}

interface PanelBlob {
  /** A blob from text or {base64}. */
  from(data: Data): Blob;
  hash(b: Blob, alg: HashAlg, enc?: Encoding): string;
  /** The blob as text (UTF-8) or base64 — up to 4 MB, into the plugin's memory. */
  text(b: Blob, enc?: "base64"): string;
}

type HashAlg = "md5" | "sha1" | "sha256" | "sha512";
type Encoding = "hex" | "base64" | "base64url";
type SignAlg = "RS256" | "RS512" | "ES256" | "Ed25519";

interface PanelCrypto {
  hash(alg: HashAlg, data: Data, enc?: Encoding): string;
  hmac(alg: HashAlg, key: Data, data: Data, enc?: Encoding): string;
  /** Signature, base64 (ES256 as raw r||s, as JWS wants). Keys are PEM. */
  sign(alg: SignAlg, privateKeyPem: string, data: Data): string;
  verify(alg: SignAlg, keyPem: string, data: Data, signatureBase64: string): boolean;
  /** A signed JWT: header.claims.signature. */
  jwt(alg: SignAlg, privateKeyPem: string, claims: Record<string, unknown>, header?: Record<string, unknown>): string;
  randomHex(bytes?: number): string;
  randomUUID(): string;
}

interface APIResponse<T = any> {
  status: number;
  /** Parsed JSON when the answer is JSON, the text otherwise. */
  body: T;
}

interface Panel {
  /** This plugin. */
  readonly plugin: { id: string; version: string };
  /** The operator's settings (the manifest's "settings"), secrets included. */
  readonly config: Readonly<Record<string, string>>;
  readonly log: PanelLog;
  readonly kv: PanelKV;
  readonly db: PanelDB;
  readonly http: PanelHTTP;
  readonly crypto: PanelCrypto;
  readonly blob: PanelBlob;
  /**
   * Calls the panel's REST API (/v1, see /v1/docs) with the permissions in the
   * manifest. Inside a decision hook only GET is allowed. `{blob: true}` puts the
   * answer's body in a Blob (an export too big for the plugin's memory).
   */
  api<T = any>(method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE", path: string, body?: unknown, opts?: { blob?: false }): APIResponse<T>;
  api(method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE", path: string, body: unknown, opts: { blob: true }): APIResponse<Blob>;
  readonly users: {
    get<T = any>(id: number | string): APIResponse<T>;
    list<T = any>(query?: string): APIResponse<T>;
    update<T = any>(id: number | string, patch: Record<string, unknown>): APIResponse<T>;
  };
  /** A string from i18n/<lang>.json, {name} placeholders filled from params. */
  t(key: string, params?: Record<string, string>, lang?: string): string;
}

declare const panel: Panel;

/** console.* goes to the plugin's log in the panel. */
declare const console: { log(...a: unknown[]): void; info(...a: unknown[]): void; warn(...a: unknown[]): void; error(...a: unknown[]): void; debug(...a: unknown[]): void };

// ---- What the panel passes to the exports -----------------------------------

/** onEvent(e): the same payload the webhooks carry. */
interface PanelEvent<D = any> {
  /** Unique per event: use it to make onEvent idempotent (delivery is at-least-once). */
  id: string;
  event: string;
  created_at: number;
  data: D;
}

/** payment.create(req) */
interface PaymentCreateRequest {
  amount_rub: number;
  order_id: number;
  description: string;
  return_url: string;
  webhook_url: string;
  email: string;
}

type PaymentStatus = "paid" | "pending" | "cancelled" | "refunded";

interface PaymentResult {
  provider_id?: string;
  status: PaymentStatus;
  /** Required: the panel checks it against the order. */
  amount_kopecks: number;
  currency: string;
}

/** onHttp(req) — requests to /<secret>/x/<plugin id>/<path>. */
interface HTTPRequest {
  method: string;
  path: string;
  query: Record<string, string>;
  headers: Record<string, string>;
  body: string;
  ip: string;
}

/**
 * beforeSignup(req): a new account about to be made. channel: "web" is POST /v1/signup
 * by external_id, "telegram" the same by telegram_id, "miniapp" and "bot" the
 * panel's own Mini App and bot.
 */
interface SignupRequest {
  channel: "web" | "telegram" | "miniapp" | "bot";
  telegram_id?: number;
  username?: string;
  external_id?: string;
  ip?: string;
  ref?: string;
  source?: string;
  lang?: string;
}

/** beforeDeviceBind(req): a device the user has not bound yet, about to take a slot. */
interface DeviceRequest {
  user_id: number;
  /** The device's id (x-hwid). */
  hwid: string;
  device_os?: string;
  device_model?: string;
  user_agent?: string;
  ip?: string;
  /** Devices bound before this one. */
  count: number;
  /** The user's device limit, 0 = none. */
  cap: number;
  lang?: string;
}

/**
 * A decision hook's answer (or a bare false). reason is a key of the plugin's i18n
 * files, or plain text; the refused person reads it.
 */
interface Decision {
  allow: boolean;
  reason?: string;
}

/** quotePrice(req): a plan being priced for a user (experimental). */
interface PriceRequest {
  user_id: number;
  plan_id: number;
  plan: string;
  periods: number;
  devices: number;
  /** The panel's price after its period discount, before a promo code. */
  base_rub: number;
  lang?: string;
}

/**
 * quotePrice's answer: from half of base_rub to base_rub, or null for "no opinion".
 * note (an i18n key or text) says why.
 */
interface PriceAnswer {
  price_rub: number;
  note?: string;
}

/** Who pressed a button or sent a command in the panel's bot. id is 0 without an account. */
interface BotUser {
  id: number;
  name?: string;
  telegram_id: number;
  username?: string;
}

/** A bot button: data comes back to bot.onCallback, url opens a page (https only). */
type BotButton = { text: string; data: string } | { text: string; url: string };

/** What the bot shows: plain text (not HTML) and rows of buttons. A string is text alone. */
type BotReply = string | { text: string; buttons?: (BotButton | BotButton[])[] };

/** The bot export: menu adds buttons under the bot's menu, onCallback answers them. */
interface PluginBot {
  menu?(req: { user: BotUser; lang: string }): BotButton[];
  onCallback?(req: { user: BotUser; data: string; lang: string }): BotReply;
  onCommand?(req: { user: BotUser; command: string; args: string; lang: string }): BotReply;
}

/**
 * transformSubscription(req) (experimental): what a client is served, to change.
 * Return the same shape — {links} or {config} — or null to leave it as it is.
 * config is a YAML text for "clash" and a JSON document for "singbox" and "xray".
 */
type SubscriptionRequest = {
  user: { id: number; name: string; status: string; expire_at: number; plan_id: number; data_limit: number; used: number; telegram_id: number };
} & ({ format: "links"; links: string[] } | { format: "clash"; config: string } | { format: "singbox" | "xray"; config: any });

/** onAction(req): a button an admin pressed. user_ids is [] for a global one. */
interface ActionRequest {
  key: string;
  user_ids: number[];
}

/** What the admin sees after pressing it. */
interface ActionResult {
  ok?: boolean;
  message?: string;
}

/** widget(key) returns one of these. */
type WidgetData =
  | { type: "stat"; value: string | number; hint?: string }
  | { type: "table"; columns: string[]; rows: (string | number)[][] }
  | { type: "list"; items: (string | number)[] };

/** subBlocks(req) returns up to 10 of these. Buttons go to https only. */
type SubBlock =
  | { type: "text" | "notice" | "markdown"; text: string }
  | { type: "button"; label: string; url: string };

/** channel.send(msg): one message for the users the panel's bot did not reach. */
interface ChannelMessage {
  /** The event's id: with a user's id, what makes a retry deliver nobody twice. */
  event_id: string;
  kind: "message" | "auto_message" | "broadcast" | "notice";
  /** For a notice: "expiring" or "traffic_low" (the figures are in data). */
  notice?: string;
  /** Telegram HTML. */
  text?: string;
  buttons?: { text: string; url: string }[];
  users: { id: number; name?: string; external_id?: string; telegram_id?: number; lang?: string; mailing?: boolean }[];
  data: any;
}

// ---- The test runner (`rospanel plugin test`) — test.js only ------------------

/** A test. The plugin's database and kv carry over from one test to the next; mocks do not. */
declare function test(name: string, fn: () => void): void;

declare const assert: {
  equal(actual: unknown, expected: unknown, message?: string): void;
  ok(value: unknown, message?: string): void;
  throws(fn: () => unknown, message?: string): void;
};

declare const mock: {
  /** Answers panel.http.fetch for URLs starting with prefix. */
  http(prefix: string, response: { status?: number; headers?: Record<string, string>; body?: unknown }): void;
  /** Answers panel.api for method + path (a path ending in * matches a prefix). */
  api(method: string, path: string, response: { status?: number; body?: unknown }): void;
  /** The requests the plugin made so far. */
  calls(): { kind: "http" | "api"; method: string; url: string; body: string }[];
  reset(): void;
};

/** panel.crypto's functions, to sign what a test sends the plugin. */
declare const crypto: Pick<PanelCrypto, "hash" | "hmac" | "sign" | "jwt">;

declare const plugin: {
  /** Calls an export of main.js ("onEvent", "payment.create", …). */
  call<T = any>(name: string, arg?: unknown): T;
  /** Delivers an event to onEvent, as the panel would. */
  event<T = any>(event: string, data?: unknown): T;
  /** Runs a cron job. */
  cron(name: string): void;
  /** The plugin's own storage, to seed or inspect. */
  kv: PanelKV;
  db: Omit<PanelDB, "tx">;
};
