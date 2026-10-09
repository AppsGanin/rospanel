// Package jsvm runs plugin JavaScript in a WebAssembly sandbox: QuickJS-ng compiled
// to wasm (guest/rp.c) and executed by wazero. It knows nothing about the panel —
// a VM loads a script, calls its exports with JSON and answers its __host calls
// through a function the caller passes in.
//
// The sandbox is the point. A plugin is code the operator did not write, running
// inside the process that serves every subscription, so each VM is held to limits
// the host enforces rather than the plugin's good manners:
//
//   - memory: QuickJS's own heap limit turns an allocation spree into a catchable
//     "out of memory", and the wasm memory cap behind it is a hard ceiling the
//     guest cannot grow past whatever it does;
//   - time: past the soft deadline the interrupt handler ends the call with a JS
//     error and the VM lives on; past the hard one the module is closed and the VM
//     is dead;
//   - authority: no std/os modules, no filesystem, no environment, no sockets —
//     __host is the only way out.
//
// Measured on a 1 vCPU master: a call with one host round-trip costs ~180 µs, a VM
// ~2 MB; compiling the guest takes ~1.2 s the first time and ~35 ms from the disk
// cache. Nothing is compiled until the first VM is asked for.
package jsvm

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// guestWasm is rp.wasm, gzipped (1.1 MB → 380 KB of binary). guest/build.sh makes
// it from pinned sources; guest/rp.wasm.sha256 is the hash of the unpacked module.
//
//go:embed guest/rp.wasm.gz
var guestWasm []byte

//go:embed guest/rp.wasm.sha256
var guestSHA256 string

// Options configure an Engine.
type Options struct {
	// CacheDir keeps the guest's compiled machine code across restarts, which is
	// the difference between ~1.2 s and ~35 ms on a small box. Empty keeps it in
	// memory only.
	CacheDir string
}

// Engine compiles the guest once per memory cap and makes VMs from it. It is safe
// for concurrent use.
type Engine struct {
	opts Options

	mu       sync.Mutex
	cache    wazero.CompilationCache
	wasm     []byte
	runtimes map[uint32]*capRuntime // by wasm memory cap, in pages
	closed   bool
}

// capRuntime is one wazero runtime — the memory cap is a runtime-wide setting, so
// VMs with different caps need different runtimes. The compilation cache is shared,
// so a second cap costs an instantiation, not a compile.
type capRuntime struct {
	rt  wazero.Runtime
	mod wazero.CompiledModule
}

// NewEngine returns an engine. It does no work until the first NewVM.
func NewEngine(opts Options) *Engine {
	return &Engine{opts: opts, runtimes: map[uint32]*capRuntime{}}
}

// Close releases every runtime; VMs made by the engine stop working.
func (e *Engine) Close(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	var errs []error
	for k, r := range e.runtimes {
		errs = append(errs, r.rt.Close(ctx))
		delete(e.runtimes, k)
	}
	if e.cache != nil {
		errs = append(errs, e.cache.Close(ctx))
		e.cache = nil
	}
	return errors.Join(errs...)
}

// runtime returns the runtime for a memory cap, compiling the guest on first use.
func (e *Engine) runtime(ctx context.Context, pages uint32) (*capRuntime, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, ErrClosed
	}
	if r := e.runtimes[pages]; r != nil {
		return r, nil
	}
	if e.wasm == nil {
		w, err := unpackGuest()
		if err != nil {
			return nil, err
		}
		e.wasm = w
	}
	if e.cache == nil {
		if e.opts.CacheDir != "" {
			c, err := wazero.NewCompilationCacheWithDir(e.opts.CacheDir)
			if err != nil {
				return nil, fmt.Errorf("jsvm: compilation cache: %w", err)
			}
			e.cache = c
		} else {
			e.cache = wazero.NewCompilationCache()
		}
	}
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithCompilationCache(e.cache).
		WithMemoryLimitPages(pages).
		// The hard deadline: a call whose context ends closes its module, even
		// in the middle of a tight loop the interrupt handler did not catch.
		WithCloseOnContextDone(true))
	ok := false
	defer func() {
		if !ok {
			_ = rt.Close(ctx)
		}
	}()
	// WASI is linked for the clocks and the random source QuickJS needs; each VM's
	// module config grants nothing else (no preopened dirs, no env, no args).
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		return nil, fmt.Errorf("jsvm: wasi: %w", err)
	}
	if err := instantiateHost(ctx, rt); err != nil {
		return nil, err
	}
	mod, err := rt.CompileModule(ctx, e.wasm)
	if err != nil {
		return nil, fmt.Errorf("jsvm: compile guest: %w", err)
	}
	// Compiling leaves tens of megabytes of garbage behind (68 MB at the peak on a
	// 1 vCPU box), which Go would hand back to the OS only slowly — on the test master
	// the panel sat at 190 MB instead of 50 for minutes. This happens once per
	// process, so returning it at once costs one collection.
	debug.FreeOSMemory()
	ok = true
	r := &capRuntime{rt: rt, mod: mod}
	e.runtimes[pages] = r
	return r, nil
}

func unpackGuest() ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(guestWasm))
	if err != nil {
		return nil, fmt.Errorf("jsvm: guest: %w", err)
	}
	w, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("jsvm: guest: %w", err)
	}
	return w, nil
}

// instantiateHost links the "rp" module the guest imports. The functions find the
// call they belong to in the context wazero hands them (see callState), so one
// host module serves every VM on the runtime.
func instantiateHost(ctx context.Context, rt wazero.Runtime) error {
	i32 := api.ValueTypeI32
	_, err := rt.NewHostModuleBuilder("rp").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(hostCall), []api.ValueType{i32, i32, i32, i32}, []api.ValueType{i32}).
		Export("host_call").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(hostTake), []api.ValueType{i32}, nil).
		Export("host_take").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(hostInterrupt), nil, []api.ValueType{i32}).
		Export("interrupt").
		Instantiate(ctx)
	if err != nil {
		return fmt.Errorf("jsvm: host module: %w", err)
	}
	return nil
}

// moduleConfig is what each VM's module may touch: clocks and randomness, nothing
// else. An empty name lets any number of anonymous instances share a runtime.
func moduleConfig() wazero.ModuleConfig {
	return wazero.NewModuleConfig().
		WithName("").
		WithStartFunctions("_initialize").
		WithSysWalltime().
		WithSysNanotime().
		WithRandSource(rand.Reader)
}
