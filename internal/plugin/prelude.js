// The plugin SDK: what `panel` is inside the sandbox. Every call is one __host
// round-trip carrying JSON both ways; the host checks permissions, limits and
// deadlines — nothing here is a security boundary, it is only the shape authors
// program against. Part of the public contract (api 1): add, never rename.
(() => {
	"use strict";
	const host = globalThis.__host;
	delete globalThis.__host;

	const call = (op, arg) => JSON.parse(host(op, JSON.stringify(arg === undefined ? null : arg)));
	const str = (v) => (typeof v === "string" ? v : JSON.stringify(v));

	const log = (level) => (msg, fields) => {
		call("log", { level, msg: str(msg), fields: fields === undefined ? null : fields });
	};

	const info = call("plugin.info");

	const panel = {
		plugin: Object.freeze({ id: info.id, version: info.version }),
		config: Object.freeze(call("config.get")),

		log: Object.freeze({ info: log("info"), warn: log("warn"), error: log("error") }),

		kv: Object.freeze({
			get(key) {
				const r = call("kv.get", { key: String(key) });
				return r.found ? JSON.parse(r.value) : undefined;
			},
			set(key, value) {
				if (value === undefined) throw new TypeError("kv.set: value is undefined (use kv.delete)");
				call("kv.set", { key: String(key), value: JSON.stringify(value) });
			},
			delete(key) {
				call("kv.delete", { key: String(key) });
			},
			list(prefix, opts) {
				const o = opts || {};
				return call("kv.list", { prefix: prefix == null ? "" : String(prefix), after: o.after || "", limit: o.limit || 0 }).map(
					(e) => ({ key: e.key, value: JSON.parse(e.value) }),
				);
			},
		}),

		db: Object.freeze({
			exec(sql, ...args) {
				return call("db.exec", { sql: String(sql), args });
			},
			query(sql, ...args) {
				return call("db.query", { sql: String(sql), args });
			},
			tx(fn) {
				call("db.begin");
				try {
					const r = fn();
					call("db.commit");
					return r;
				} catch (e) {
					try {
						call("db.rollback");
					} catch (_) {}
					throw e;
				}
			},
		}),

		// panel.api("GET", "/v1/users/12") → {status, body}. body is parsed JSON when the
		// answer is JSON, the text otherwise.
		api(method, path, body) {
			return call("api", { method: String(method), path: String(path), body: body === undefined ? null : body });
		},

		http: Object.freeze({
			// panel.http.fetch(url, {method, headers, body, timeout_ms}) → {status, headers, body}.
			// body: a string is sent as is, anything else as JSON. The answer's body is text.
			fetch(url, opts) {
				const o = opts || {};
				let body = o.body;
				const headers = Object.assign({}, o.headers || {});
				if (body !== undefined && body !== null && typeof body !== "string") {
					body = JSON.stringify(body);
					if (!Object.keys(headers).some((h) => h.toLowerCase() === "content-type")) headers["Content-Type"] = "application/json";
				}
				return call("http.fetch", { url: String(url), method: o.method || "GET", headers, body: body == null ? "" : body, timeout_ms: o.timeout_ms || 0 });
			},
		}),

		crypto: Object.freeze({
			hash: (alg, data, enc) => call("crypto.hash", { alg, data, enc: enc || "hex" }),
			hmac: (alg, key, data, enc) => call("crypto.hmac", { alg, key, data, enc: enc || "hex" }),
			sign: (alg, pem, data) => call("crypto.sign", { alg, pem, data }),
			verify: (alg, pem, data, sig) => call("crypto.verify", { alg, pem, data, sig }),
			jwt: (alg, pem, claims, header) => call("crypto.jwt", { alg, pem, claims, header: header || null }),
			randomHex: (n) => call("crypto.random", { n: n || 16 }),
			randomUUID: () => call("crypto.uuid"),
		}),

		t(key, params, lang) {
			return call("t", { key: String(key), params: params || null, lang: lang || "" });
		},
	};

	const users = Object.freeze({
		get: (id) => panel.api("GET", "/v1/users/" + encodeURIComponent(id)),
		list: (query) => panel.api("GET", "/v1/users" + (query ? "?" + query : "")),
		update: (id, patch) => panel.api("PATCH", "/v1/users/" + encodeURIComponent(id), patch),
	});
	panel.users = users;

	globalThis.panel = Object.freeze(panel);
	const line = (level) => (...a) => panel.log[level](a.map((x) => (typeof x === "string" ? x : JSON.stringify(x))).join(" "));
	globalThis.console = Object.freeze({ log: line("info"), info: line("info"), warn: line("warn"), error: line("error"), debug: line("info") });
})();
