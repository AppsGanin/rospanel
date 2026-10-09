package jsvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// One engine for the package: compiling the guest is the slow part (~0.3 s, more
// under -race), and every test gets fresh VMs from it anyway.
var (
	testEngineOnce sync.Once
	testEngine     *Engine
)

func engine(t testing.TB) *Engine {
	t.Helper()
	testEngineOnce.Do(func() { testEngine = NewEngine(Options{}) })
	return testEngine
}

func newVM(t testing.TB, lim Limits, src string) *VM {
	t.Helper()
	ctx := context.Background()
	v, err := engine(t).NewVM(ctx, lim)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Close)
	if src != "" {
		if err := v.Load(ctx, src, 5*time.Second, nil); err != nil {
			t.Fatalf("load: %v", err)
		}
	}
	return v
}

func call(t testing.TB, v *VM, name, arg string, timeout time.Duration, host Host) (string, error) {
	t.Helper()
	out, err := v.Call(context.Background(), name, []byte(arg), timeout, host)
	return string(out), err
}

func jsErr(t *testing.T, err error) *JSError {
	t.Helper()
	var je *JSError
	if !errors.As(err, &je) {
		t.Fatalf("want a JS error, got %v", err)
	}
	return je
}

// The committed wasm is the one guest/rp.wasm.sha256 records — the hash CI's
// rebuild compares against. A hand-edited .gz without a rebuild fails here.
func TestGuestMatchesRecordedHash(t *testing.T) {
	w, err := unpackGuest()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(w)
	if got, want := hex.EncodeToString(sum[:]), strings.TrimSpace(guestSHA256); got != want {
		t.Fatalf("rp.wasm.gz unpacks to %s, rp.wasm.sha256 says %s — rebuild with guest/build.sh", got, want)
	}
}

func TestCallRoundTrip(t *testing.T) {
	v := newVM(t, Limits{}, `
		let n = 0;
		export function onEvent(e) { n++; return {n, user: e.data.id, v: JSON.parse(__host("kv.get", JSON.stringify({k: "x"})))}; }
		export async function twice(x) { await null; return x * 2; }
		export const payment = { create(req) { return {pay_url: "https://pay/" + req.order_id}; } };
		export function nothing() {}
	`)
	host := func(_ context.Context, op string, arg []byte) ([]byte, error) {
		if op != "kv.get" || string(arg) != `{"k":"x"}` {
			return nil, fmt.Errorf("unexpected %s %s", op, arg)
		}
		return []byte(`"val"`), nil
	}
	for i, c := range []struct{ name, arg, want string }{
		{"onEvent", `{"data":{"id":42}}`, `{"n":1,"user":42,"v":"val"}`},
		{"onEvent", `{"data":{"id":7}}`, `{"n":2,"user":7,"v":"val"}`}, // state survives between calls
		{"twice", `21`, `42`},
		{"payment.create", `{"order_id":5}`, `{"pay_url":"https://pay/5"}`},
		{"nothing", ``, `null`},
	} {
		got, err := call(t, v, c.name, c.arg, time.Second, host)
		if err != nil || got != c.want {
			t.Fatalf("%d %s: got %s, %v; want %s", i, c.name, got, err, c.want)
		}
	}
}

func TestPreludeScriptRunsInGlobalScope(t *testing.T) {
	ctx := context.Background()
	v := newVM(t, Limits{}, "")
	if err := v.Script(ctx, "prelude.js", `globalThis.panel = { ping: () => "pong" };`, time.Second, nil); err != nil {
		t.Fatal(err)
	}
	if err := v.Load(ctx, `export function f() { return panel.ping(); }`, time.Second, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := call(t, v, "f", "", time.Second, nil); err != nil || got != `"pong"` {
		t.Fatalf("got %s, %v", got, err)
	}
}

func TestHasAndMissingExport(t *testing.T) {
	ctx := context.Background()
	v := newVM(t, Limits{}, `export function a() {} export const obj = { b() {} }; export const notFn = 1;`)
	for path, want := range map[string]bool{"a": true, "obj.b": true, "notFn": false, "zzz": false, "obj.zzz": false, "": false} {
		if got := v.Has(ctx, path); got != want {
			t.Errorf("Has(%q) = %v, want %v", path, got, want)
		}
	}
	if _, err := call(t, v, "zzz", "", time.Second, nil); !errors.Is(err, ErrNoExport) {
		t.Fatalf("want ErrNoExport, got %v", err)
	}
}

func TestThrownErrorKeepsStack(t *testing.T) {
	v := newVM(t, Limits{}, `export function boom() { throw new Error("bad thing"); }`)
	_, err := call(t, v, "boom", "", time.Second, nil)
	je := jsErr(t, err)
	if je.Message != "Error: bad thing" || !strings.Contains(je.Stack, "boom") {
		t.Fatalf("got %q / %q", je.Message, je.Stack)
	}
	if v.Err() != nil {
		t.Fatal("a thrown error must not kill the VM")
	}
}

func TestHostErrorIsCatchable(t *testing.T) {
	v := newVM(t, Limits{}, `export function f() { try { __host("nope", ""); } catch (e) { return String(e); } }`)
	host := func(context.Context, string, []byte) ([]byte, error) { return nil, errors.New("denied: nope") }
	if got, err := call(t, v, "f", "", time.Second, host); err != nil || got != `"InternalError: denied: nope"` {
		t.Fatalf("got %s, %v", got, err)
	}
}

func TestHostPanicBecomesError(t *testing.T) {
	v := newVM(t, Limits{}, `export function f() { return __host("x", ""); }`)
	host := func(context.Context, string, []byte) ([]byte, error) { panic("boom") }
	_, err := call(t, v, "f", "", time.Second, host)
	if je := jsErr(t, err); !strings.Contains(je.Message, "host x failed: boom") {
		t.Fatalf("got %q", je.Message)
	}
}

func TestRejectedAndPendingPromises(t *testing.T) {
	v := newVM(t, Limits{}, `
		export async function rejects() { throw new TypeError("nope"); }
		export function never() { return new Promise(() => {}); }
	`)
	if _, err := call(t, v, "rejects", "", time.Second, nil); jsErr(t, err).Message != "TypeError: nope" {
		t.Fatalf("got %v", err)
	}
	if _, err := call(t, v, "never", "", time.Second, nil); !strings.Contains(jsErr(t, err).Message, "never settled") {
		t.Fatalf("got %v", err)
	}
}

// A tight loop ends at the soft deadline as a JS error, and the VM — with its
// state — is still there for the next call.
func TestSoftDeadlineInterruptsAndKeepsVM(t *testing.T) {
	v := newVM(t, Limits{}, `let n = 0; export function spin() { n++; while (true) {} } export function count() { return n; }`)
	start := time.Now()
	_, err := call(t, v, "spin", "", 100*time.Millisecond, nil)
	if !jsErr(t, err).Interrupted() {
		t.Fatalf("got %v", err)
	}
	if d := time.Since(start); d > 900*time.Millisecond {
		t.Fatalf("interrupt took %v", d)
	}
	if got, err := call(t, v, "count", "", time.Second, nil); err != nil || got != "1" {
		t.Fatalf("after interrupt: %s, %v", got, err)
	}
}

// try/catch cannot swallow the interrupt and keep running.
func TestInterruptIsUncatchable(t *testing.T) {
	v := newVM(t, Limits{}, `export function f() { for (;;) { try { while (true) {} } catch (e) {} } }`)
	if _, err := call(t, v, "f", "", 100*time.Millisecond, nil); !jsErr(t, err).Interrupted() {
		t.Fatalf("got %v", err)
	}
}

// A host function that ignores its deadline holds the guest past the hard one:
// the module is closed and the VM is reported dead from then on.
func TestHardDeadlineKillsVM(t *testing.T) {
	v := newVM(t, Limits{Grace: 50 * time.Millisecond}, `export function f() { return __host("slow", ""); }`)
	host := func(context.Context, string, []byte) ([]byte, error) {
		time.Sleep(300 * time.Millisecond)
		return []byte(`1`), nil
	}
	_, err := call(t, v, "f", "", 50*time.Millisecond, host)
	var te *TrapError
	if !errors.As(err, &te) {
		t.Fatalf("want TrapError, got %v", err)
	}
	if _, err := call(t, v, "f", "", time.Second, host); !errors.As(err, &te) {
		t.Fatalf("a dead VM must stay dead, got %v", err)
	}
	if v.Err() == nil {
		t.Fatal("Err() must report the death")
	}
}

// Host calls see the soft deadline in their context.
func TestHostContextCarriesSoftDeadline(t *testing.T) {
	v := newVM(t, Limits{}, `export function f() { return __host("wait", ""); }`)
	host := func(ctx context.Context, _ string, _ []byte) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	start := time.Now()
	_, err := call(t, v, "f", "", 100*time.Millisecond, host)
	if !strings.Contains(jsErr(t, err).Message, "deadline exceeded") {
		t.Fatalf("got %v", err)
	}
	if d := time.Since(start); d > 900*time.Millisecond {
		t.Fatalf("host ctx outlived the soft deadline: %v", d)
	}
	if v.Err() != nil {
		t.Fatal("a host that honours its deadline must not cost the VM")
	}
}

// The allocation spree that took qjs to 2 GB: here a catchable error inside the
// heap limit, wasm memory under its cap, and the VM keeps working.
func TestMemoryBombStaysInsideLimits(t *testing.T) {
	v := newVM(t, Limits{Memory: 8 << 20}, `
		export function bomb() { const a = []; while (true) a.push("x".repeat(1024) + Math.random()); }
		export function big() { return new ArrayBuffer(512 * 1024 * 1024).byteLength; }
		export function ok() { return 1; }
	`)
	for _, fn := range []string{"bomb", "big"} {
		_, err := call(t, v, fn, "", 10*time.Second, nil)
		if !jsErr(t, err).OutOfMemory() {
			t.Fatalf("%s: got %v", fn, err)
		}
		if v.Memory() > v.MemoryCap() {
			t.Fatalf("%s: memory %d over cap %d", fn, v.Memory(), v.MemoryCap())
		}
		if got, err := call(t, v, "ok", "", time.Second, nil); err != nil || got != "1" {
			t.Fatalf("%s: VM unusable after OOM: %s, %v", fn, got, err)
		}
	}
}

// Memory kept across calls is held to the same limit.
func TestLeakAcrossCallsIsCapped(t *testing.T) {
	v := newVM(t, Limits{Memory: 8 << 20}, `
		const keep = [];
		export function leak(n) { keep.push("y".repeat(n)); return keep.length; }
	`)
	for i := 0; i < 100; i++ {
		if _, err := call(t, v, "leak", "1048576", time.Second, nil); err != nil {
			if !jsErr(t, err).OutOfMemory() {
				t.Fatalf("got %v", err)
			}
			if i < 3 || i > 12 {
				t.Fatalf("an 8 MB heap stopped a 1 MB/call leak after %d calls", i)
			}
			return
		}
	}
	t.Fatal("the leak was never stopped")
}

func TestRecursionIsRangeError(t *testing.T) {
	v := newVM(t, Limits{}, `
		export function deep() { function f(n) { return f(n + 1) + 1; } return f(0); }
		export function ok() { return 2; }
	`)
	_, err := call(t, v, "deep", "", 5*time.Second, nil)
	if je := jsErr(t, err); !strings.HasPrefix(je.Message, "RangeError") {
		t.Fatalf("got %q", je.Message)
	}
	if got, err := call(t, v, "ok", "", time.Second, nil); err != nil || got != "2" {
		t.Fatalf("VM unusable after overflow: %s, %v", got, err)
	}
}

// Nothing but __host reaches outside: no Node/browser globals, no std/os modules,
// no imports at all.
func TestNoAmbientAuthority(t *testing.T) {
	v := newVM(t, Limits{}, `
		export function globals() {
			const r = {};
			for (const k of ["require", "process", "std", "os", "fetch", "XMLHttpRequest", "WebSocket",
				"setTimeout", "setInterval", "print", "console", "Deno", "Bun", "scriptArgs"]) r[k] = typeof globalThis[k];
			return r;
		}
		export function now() { return [typeof Date.now(), Math.random() < 1, new Date(0).toISOString()]; }
	`)
	got, err := call(t, v, "globals", "", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatal(err)
	}
	for k, typ := range m {
		if typ != "undefined" {
			t.Errorf("%s is %s inside the sandbox", k, typ)
		}
	}
	if got, err := call(t, v, "now", "", time.Second, nil); err != nil || got != `["number",true,"1970-01-01T00:00:00.000Z"]` {
		t.Fatalf("clocks/random: %s, %v", got, err)
	}

	ctx := context.Background()
	for _, src := range []string{
		`import * as std from "qjs:std"; export function f() { return std.loadFile("/etc/passwd"); }`,
		`import * as os from "qjs:os"; export function f() { return os.readdir("/"); }`,
		`import x from "./other.js"; export function f() {}`,
	} {
		w, err := engine(t).NewVM(ctx, Limits{})
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Load(ctx, src, time.Second, nil); err == nil {
			t.Errorf("import loaded: %s", src)
		}
		w.Close()
	}
}

func TestSyntaxErrorOnLoad(t *testing.T) {
	ctx := context.Background()
	v, err := engine(t).NewVM(ctx, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if err := v.Load(ctx, `export function (`, time.Second, nil); !strings.HasPrefix(jsErr(t, err).Message, "SyntaxError") {
		t.Fatalf("got %v", err)
	}
}

func TestClosedVM(t *testing.T) {
	v := newVM(t, Limits{}, `export function f() { return 1; }`)
	v.Close()
	v.Close() // twice is fine
	if _, err := call(t, v, "f", "", time.Second, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("got %v", err)
	}
}

// VMs run in parallel; calls on one VM are serialized. Under -race this is the
// check that the host functions find their own call's state.
func TestConcurrentVMs(t *testing.T) {
	const vms, calls = 6, 40
	src := `let n = 0; export function f(x) { n++; return __host("echo", JSON.stringify(x)) + ":" + n; }`
	host := func(_ context.Context, _ string, arg []byte) ([]byte, error) { return arg, nil }
	var wg sync.WaitGroup
	errs := make(chan error, vms*2)
	for i := 0; i < vms; i++ {
		v := newVM(t, Limits{}, src)
		for g := 0; g < 2; g++ { // two goroutines share each VM
			wg.Add(1)
			go func(i, g int) {
				defer wg.Done()
				for c := 0; c < calls; c++ {
					want := fmt.Sprintf(`"%d-%d-%d"`, i, g, c)
					got, err := v.Call(context.Background(), "f", []byte(want), 5*time.Second, host)
					var s string
					if err == nil {
						err = json.Unmarshal(got, &s)
					}
					if err != nil || !strings.HasPrefix(s, want+":") {
						errs <- fmt.Errorf("vm %d: got %s, %v", i, got, err)
						return
					}
				}
			}(i, g)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestDiskCacheReused(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		e := NewEngine(Options{CacheDir: dir})
		v, err := e.NewVM(ctx, Limits{})
		if err != nil {
			t.Fatal(err)
		}
		if err := v.Load(ctx, `export function f() { return 3; }`, time.Second, nil); err != nil {
			t.Fatal(err)
		}
		if got, err := v.Call(ctx, "f", nil, time.Second, nil); err != nil || string(got) != "3" {
			t.Fatalf("got %s, %v", got, err)
		}
		v.Close()
		if err := e.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEngineCloseStopsNewVMs(t *testing.T) {
	ctx := context.Background()
	e := NewEngine(Options{})
	if err := e.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.NewVM(ctx, Limits{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("got %v", err)
	}
}

func BenchmarkCallWithHostRoundTrip(b *testing.B) {
	v := newVM(b, Limits{}, `export function onEvent(e) { return {u: e.data.id, v: __host("kv.get", "x")}; }`)
	host := func(context.Context, string, []byte) ([]byte, error) { return []byte(`"v"`), nil }
	arg := []byte(`{"id":"1","event":"user.created","data":{"id":42,"name":"bob"}}`)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := v.Call(ctx, "onEvent", arg, time.Second, host); err != nil {
			b.Fatal(err)
		}
	}
}
