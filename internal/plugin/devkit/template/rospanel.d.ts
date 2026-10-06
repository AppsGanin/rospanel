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

interface PanelHTTP {
  /**
   * Requests a URL whose host is in the manifest's "net". A body that is not a
   * string is sent as JSON. Private and local addresses are never reachable.
   */
  fetch(
    url: string,
    opts?: { method?: string; headers?: Record<string, string>; body?: unknown; timeout_ms?: number },
  ): HTTPResponse;
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
  /**
   * Calls the panel's REST API (/v1, see /v1/docs) with the permissions in the
   * manifest. Inside a decision hook only GET is allowed.
   */
  api<T = any>(method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE", path: string, body?: unknown): APIResponse<T>;
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

/** beforeSignup(req) */
interface SignupRequest {
  channel: "telegram" | "web" | "miniapp" | "bot";
  telegram_id?: number;
  username?: string;
  ip?: string;
  email?: string;
  ref?: string;
  source?: string;
  lang?: string;
}

/** A decision hook's answer. reason is a key of the plugin's i18n files. */
interface Decision {
  allow: boolean;
  reason?: string;
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
