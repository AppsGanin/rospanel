// What test.js sees: test(), assert, mock and plugin. Its own sandbox, beside the
// plugin's; plugin.call reaches the plugin through the host.
(() => {
	"use strict";
	const host = globalThis.__host;
	delete globalThis.__host;
	const call = (op, arg) => JSON.parse(host(op, JSON.stringify(arg === undefined ? null : arg)));

	const tests = [];
	globalThis.test = (name, fn) => {
		tests.push({ name: String(name), fn });
	};

	// canon sorts object keys, so equal() compares values, not key order.
	const canon = (v) => {
		if (Array.isArray(v)) return v.map(canon);
		if (v && typeof v === "object") {
			const o = {};
			for (const k of Object.keys(v).sort()) o[k] = canon(v[k]);
			return o;
		}
		return v;
	};
	const show = (v) => (v === undefined ? "undefined" : JSON.stringify(v));
	globalThis.assert = Object.freeze({
		equal(actual, expected, msg) {
			if (JSON.stringify(canon(actual)) !== JSON.stringify(canon(expected))) {
				throw new Error(`${msg ? msg + ": " : ""}expected ${show(expected)}, got ${show(actual)}`);
			}
		},
		ok(v, msg) {
			if (!v) throw new Error(msg || `expected a truthy value, got ${show(v)}`);
		},
		throws(fn, msg) {
			try {
				fn();
			} catch (_) {
				return;
			}
			throw new Error(msg || "expected an exception");
		},
	});

	const body = (b) => (typeof b === "string" ? b : b === undefined ? "" : JSON.stringify(b));
	globalThis.mock = Object.freeze({
		http(prefix, r) {
			const o = r || {};
			call("mock.http", { prefix: String(prefix), status: o.status || 200, headers: o.headers || {}, body: body(o.body) });
		},
		api(method, path, r) {
			const o = r || {};
			call("mock.api", { method: String(method), path: String(path), status: o.status || 200, body: o.body === undefined ? null : o.body });
		},
		calls: () => call("mock.calls"),
		reset: () => call("mock.reset"),
	});

	const dev = (op, arg) => call("dev.op", { op, arg: arg === undefined ? null : arg });
	globalThis.plugin = Object.freeze({
		call: (name, arg) => call("plugin.call", { name: String(name), arg: arg === undefined ? null : arg }),
		event: (event, data) => call("plugin.event", { event: String(event), data: data === undefined ? {} : data }),
		cron: (name) => {
			call("plugin.call", { name: String(name), arg: null });
		},
		kv: Object.freeze({
			get(k) {
				const r = dev("kv.get", { key: String(k) });
				return r.found ? JSON.parse(r.value) : undefined;
			},
			set(k, v) {
				dev("kv.set", { key: String(k), value: JSON.stringify(v) });
			},
			delete(k) {
				dev("kv.delete", { key: String(k) });
			},
			list(prefix, opts) {
				const o = opts || {};
				return dev("kv.list", { prefix: prefix || "", after: o.after || "", limit: o.limit || 0 }).map((e) => ({
					key: e.key,
					value: JSON.parse(e.value),
				}));
			},
		}),
		db: Object.freeze({
			exec: (sql, ...args) => dev("db.exec", { sql: String(sql), args }),
			query: (sql, ...args) => dev("db.query", { sql: String(sql), args }),
		}),
	});

	const line = (...a) => call("log", { msg: a.map((x) => (typeof x === "string" ? x : JSON.stringify(x))).join(" ") });
	globalThis.console = Object.freeze({ log: line, info: line, warn: line, error: line, debug: line });

	globalThis.__run = () => {
		const out = [];
		for (const t of tests) {
			call("mock.reset");
			try {
				t.fn();
				out.push({ name: t.name, ok: true, error: "", stack: "" });
			} catch (e) {
				const msg = e && e.message !== undefined ? (e.name && e.name !== "Error" ? e.name + ": " : "") + e.message : String(e);
				out.push({ name: t.name, ok: false, error: msg, stack: e && e.stack ? String(e.stack) : "" });
			}
		}
		return out;
	};
})();
