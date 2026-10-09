// rp.c is the whole guest side of the plugin engine: QuickJS-ng plus a handful of
// exports. Everything crossing the wasm boundary is a UTF-8 string — JSON in, JSON
// out — so the host never touches a JSValue and this file stays the only C we own.
//
// What a plugin can reach is exactly what is defined here: there is no quickjs-libc
// (no std, no os, no console), and the one way out of the sandbox is __host.
#include <malloc.h>
#include <stdlib.h>
#include <string.h>
#include "quickjs.h"

#define IMPORT(n) __attribute__((import_module("rp"), import_name(n)))
#define EXPORT(n) __attribute__((export_name(n)))

// host_call runs one host operation. It returns the length of the answer, or
// -(length+1) when the answer is an error message; host_take then copies it in.
IMPORT("host_call") int host_call(const char *op, int oplen, const char *arg, int arglen);
IMPORT("host_take") void host_take(char *dst);
// interrupt is polled by QuickJS's interrupt handler: non-zero ends the running
// call with an uncatchable "interrupted" error once the soft deadline has passed.
IMPORT("interrupt") int host_interrupt(void);

enum { RP_OK = 0, RP_JS_ERROR = 1, RP_NO_EXPORT = 2 };

// The heap limit is kept here rather than by JS_SetMemoryLimit. When QuickJS runs
// out of memory it cannot always allocate the error object and throws null instead
// (see JS_ThrowError2) — indistinguishable from a plugin's own `throw null`. Owning
// the allocator, we know when a refusal happened and can name it.
static size_t heap_limit;
static size_t heap_used;
static int oom_hit;

static int over(size_t add) { return heap_limit && (heap_used + add > heap_limit || heap_used + add < heap_used); }

static void *rp_malloc(void *opaque, size_t size) {
	if (over(size)) {
		oom_hit = 1;
		return NULL;
	}
	void *p = malloc(size);
	if (!p) {
		oom_hit = 1;
		return NULL;
	}
	heap_used += malloc_usable_size(p);
	return p;
}

static void *rp_calloc(void *opaque, size_t count, size_t size) {
	if (size && count > (size_t)-1 / size) {
		oom_hit = 1;
		return NULL;
	}
	if (over(count * size)) {
		oom_hit = 1;
		return NULL;
	}
	void *p = calloc(count, size);
	if (!p) {
		oom_hit = 1;
		return NULL;
	}
	heap_used += malloc_usable_size(p);
	return p;
}

static void rp_free(void *opaque, void *ptr) {
	if (!ptr)
		return;
	heap_used -= malloc_usable_size(ptr);
	free(ptr);
}

static void *rp_realloc(void *opaque, void *ptr, size_t size) {
	if (!ptr)
		return rp_malloc(opaque, size);
	size_t old = malloc_usable_size(ptr);
	if (size > old && over(size - old)) {
		oom_hit = 1;
		return NULL;
	}
	void *p = realloc(ptr, size);
	if (!p) {
		if (size)
			oom_hit = 1;
		return NULL;
	}
	heap_used = heap_used - old + malloc_usable_size(p);
	return p;
}

static size_t rp_usable(const void *ptr) { return malloc_usable_size((void *)ptr); }

static const JSMallocFunctions rp_mf = {rp_calloc, rp_malloc, rp_free, rp_realloc, rp_usable};

static JSRuntime *rt;
static JSContext *ctx;
static JSValue ns; // the plugin module's namespace, once loaded
static char *out;  // the last result or error text, read back by the host
static int outlen;

static void set_out(const char *s, size_t n) {
	free(out);
	out = malloc(n + 1);
	if (!out) {
		outlen = 0;
		return;
	}
	memcpy(out, s, n);
	out[n] = 0;
	outlen = (int)n;
}

// set_exc moves the pending exception into out as "message\nstack".
static void set_exc(void) {
	JSValue e = JS_GetException(ctx);
	if (oom_hit && JS_IsNull(e)) {
		set_out("InternalError: out of memory\n", 29);
		return;
	}
	const char *msg = JS_ToCString(ctx, e);
	JSValue st = JS_IsError(e) ? JS_GetPropertyStr(ctx, e, "stack") : JS_UNDEFINED;
	const char *stack = JS_IsString(st) ? JS_ToCString(ctx, st) : NULL;
	size_t a = msg ? strlen(msg) : 0, b = stack ? strlen(stack) : 0;
	char *buf = malloc(a + b + 2);
	if (buf) {
		memcpy(buf, msg ? msg : "", a);
		buf[a] = '\n';
		memcpy(buf + a + 1, stack ? stack : "", b);
		set_out(buf, a + 1 + b);
		free(buf);
	} else {
		set_out("out of memory", 13);
	}
	if (msg)
		JS_FreeCString(ctx, msg);
	if (stack)
		JS_FreeCString(ctx, stack);
	JS_FreeValue(ctx, st);
	JS_FreeValue(ctx, e);
}

// __host(op, arg) -> string. A host error becomes a thrown InternalError.
static JSValue js_host(JSContext *c, JSValueConst this_val, int argc, JSValueConst *argv) {
	size_t ol = 0, al = 0;
	if (argc < 1)
		return JS_ThrowTypeError(c, "__host: op required");
	const char *op = JS_ToCStringLen(c, &ol, argv[0]);
	if (!op)
		return JS_EXCEPTION;
	const char *arg = argc > 1 ? JS_ToCStringLen(c, &al, argv[1]) : NULL;
	if (argc > 1 && !arg) {
		JS_FreeCString(c, op);
		return JS_EXCEPTION;
	}
	int n = host_call(op, (int)ol, arg ? arg : "", (int)al);
	JS_FreeCString(c, op);
	if (arg)
		JS_FreeCString(c, arg);
	int failed = n < 0;
	if (failed)
		n = -n - 1;
	char *buf = malloc((size_t)n + 1);
	if (!buf)
		return JS_ThrowOutOfMemory(c);
	host_take(buf);
	JSValue r = failed ? JS_ThrowInternalError(c, "%.*s", n, buf) : JS_NewStringLen(c, buf, (size_t)n);
	free(buf);
	return r;
}

static int interrupt_cb(JSRuntime *r, void *opaque) { return host_interrupt(); }

// rp_new creates the runtime. mem_limit caps every byte QuickJS allocates (an
// exceeded limit is a catchable "out of memory"); max_stack must stay below the
// wasm stack linked in build.sh so recursion ends in a RangeError, not a trap.
EXPORT("rp_new") int rp_new(int mem_limit, int max_stack) {
	heap_limit = mem_limit > 0 ? (size_t)mem_limit : 0;
	rt = JS_NewRuntime2(&rp_mf, NULL);
	if (!rt)
		return -1;
	if (max_stack > 0)
		JS_SetMaxStackSize(rt, (size_t)max_stack);
	JS_SetInterruptHandler(rt, interrupt_cb, NULL);
	ctx = JS_NewContext(rt);
	if (!ctx)
		return -2;
	JSValue g = JS_GetGlobalObject(ctx);
	JS_SetPropertyStr(ctx, g, "__host", JS_NewCFunction(ctx, js_host, "__host", 2));
	JS_FreeValue(ctx, g);
	ns = JS_UNDEFINED;
	return 0;
}

// drain runs queued promise jobs until none is left.
static int drain(void) {
	JSContext *c;
	for (;;) {
		int r = JS_ExecutePendingJob(rt, &c);
		if (r <= 0)
			return r;
	}
}

// settle resolves a call's result: a promise is awaited (all jobs are drained
// first, and a promise that is still pending then can never settle — there are no
// timers), anything else is returned as is. It consumes v.
static JSValue settle(JSValue v) {
	if (drain() < 0) {
		JS_FreeValue(ctx, v);
		return JS_EXCEPTION;
	}
	if (!JS_IsPromise(v))
		return v;
	JSPromiseStateEnum st = JS_PromiseState(ctx, v);
	JSValue r = JS_PromiseResult(ctx, v);
	JS_FreeValue(ctx, v);
	if (st == JS_PROMISE_REJECTED)
		return JS_Throw(ctx, r);
	if (st == JS_PROMISE_PENDING) {
		JS_FreeValue(ctx, r);
		return JS_ThrowInternalError(ctx, "promise never settled (there are no timers)");
	}
	return r;
}

// rp_script evaluates a classic script in the global scope — the prelude that
// builds `panel` out of __host before the plugin runs.
EXPORT("rp_script") int rp_script(const char *src, int len, const char *name) {
	oom_hit = 0;
	JSValue v = JS_Eval(ctx, src, (size_t)len, name, JS_EVAL_TYPE_GLOBAL | JS_EVAL_FLAG_STRICT);
	if (JS_IsException(v)) {
		set_exc();
		return RP_JS_ERROR;
	}
	v = settle(v);
	if (JS_IsException(v)) {
		set_exc();
		return RP_JS_ERROR;
	}
	JS_FreeValue(ctx, v);
	return RP_OK;
}

// rp_load evaluates the plugin source as an ES module and keeps its namespace for
// rp_call. There is no module loader: an import fails, the bundle is one file.
EXPORT("rp_load") int rp_load(const char *src, int len) {
	oom_hit = 0;
	JSValue fn = JS_Eval(ctx, src, (size_t)len, "main.js", JS_EVAL_TYPE_MODULE | JS_EVAL_FLAG_COMPILE_ONLY);
	if (JS_IsException(fn)) {
		set_exc();
		return RP_JS_ERROR;
	}
	JSModuleDef *m = JS_VALUE_GET_PTR(fn);
	JSValue v = settle(JS_EvalFunction(ctx, fn));
	if (JS_IsException(v)) {
		set_exc();
		return RP_JS_ERROR;
	}
	JS_FreeValue(ctx, v);
	JS_FreeValue(ctx, ns);
	ns = JS_GetModuleNamespace(ctx, m);
	if (JS_IsException(ns)) {
		set_exc();
		return RP_JS_ERROR;
	}
	return RP_OK;
}

// lookup finds an export by a dotted path: "onEvent" or "payment.create". On
// success *thisv holds the object the function was found on.
static JSValue lookup(const char *path, JSValue *thisv) {
	JSValue cur = JS_DupValue(ctx, ns);
	JSValue parent = JS_UNDEFINED;
	const char *p = path;
	while (*p) {
		const char *dot = strchr(p, '.');
		size_t n = dot ? (size_t)(dot - p) : strlen(p);
		char key[64];
		if (n == 0 || n >= sizeof(key) || !JS_IsObject(cur)) {
			JS_FreeValue(ctx, cur);
			JS_FreeValue(ctx, parent);
			return JS_UNDEFINED;
		}
		memcpy(key, p, n);
		key[n] = 0;
		JSValue next = JS_GetPropertyStr(ctx, cur, key);
		JS_FreeValue(ctx, parent);
		parent = cur;
		cur = next;
		p = dot ? dot + 1 : p + n;
	}
	*thisv = parent;
	return cur;
}

// rp_has reports whether the export path names a function (1) or not (0).
EXPORT("rp_has") int rp_has(const char *name, int nlen) {
	char path[256];
	if (nlen <= 0 || nlen >= (int)sizeof(path) || JS_IsUndefined(ns))
		return 0;
	memcpy(path, name, (size_t)nlen);
	path[nlen] = 0;
	JSValue thisv = JS_UNDEFINED;
	JSValue f = lookup(path, &thisv);
	int ok = JS_IsFunction(ctx, f);
	JS_FreeValue(ctx, f);
	JS_FreeValue(ctx, thisv);
	return ok;
}

// rp_call calls an export with one JSON argument and leaves JSON.stringify of
// the (awaited) result in out — "null" for undefined.
EXPORT("rp_call") int rp_call(const char *name, int nlen, const char *arg, int alen) {
	char path[256];
	oom_hit = 0;
	if (nlen <= 0 || nlen >= (int)sizeof(path) || JS_IsUndefined(ns)) {
		set_out("no such export", 14);
		return RP_NO_EXPORT;
	}
	memcpy(path, name, (size_t)nlen);
	path[nlen] = 0;
	JSValue thisv = JS_UNDEFINED;
	JSValue f = lookup(path, &thisv);
	if (!JS_IsFunction(ctx, f)) {
		JS_FreeValue(ctx, f);
		JS_FreeValue(ctx, thisv);
		set_out("no such export", 14);
		return RP_NO_EXPORT;
	}
	JSValue a = JS_ParseJSON(ctx, arg, (size_t)alen, "<input>");
	if (JS_IsException(a)) {
		JS_FreeValue(ctx, f);
		JS_FreeValue(ctx, thisv);
		set_exc();
		return RP_JS_ERROR;
	}
	JSValue r = JS_Call(ctx, f, thisv, 1, (JSValueConst *)&a);
	JS_FreeValue(ctx, a);
	JS_FreeValue(ctx, f);
	JS_FreeValue(ctx, thisv);
	if (!JS_IsException(r))
		r = settle(r);
	if (JS_IsException(r)) {
		set_exc();
		return RP_JS_ERROR;
	}
	JSValue s = JS_JSONStringify(ctx, r, JS_UNDEFINED, JS_UNDEFINED);
	JS_FreeValue(ctx, r);
	if (JS_IsException(s)) {
		set_exc();
		return RP_JS_ERROR;
	}
	if (JS_IsUndefined(s)) {
		set_out("null", 4);
		return RP_OK;
	}
	size_t n;
	const char *cs = JS_ToCStringLen(ctx, &n, s);
	JS_FreeValue(ctx, s);
	if (!cs) {
		set_exc();
		return RP_JS_ERROR;
	}
	set_out(cs, n);
	JS_FreeCString(ctx, cs);
	return RP_OK;
}

EXPORT("rp_out_ptr") char *rp_out_ptr(void) { return out; }
EXPORT("rp_out_len") int rp_out_len(void) { return outlen; }

// rp_heap is the bytes QuickJS holds right now — what mem_limit caps.
EXPORT("rp_heap") int rp_heap(void) { return (int)heap_used; }
