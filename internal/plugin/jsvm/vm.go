package jsvm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero/api"
)

const (
	// DefaultMemory is QuickJS's heap limit when Limits leaves it unset.
	DefaultMemory = 32 << 20
	// DefaultStack is QuickJS's stack limit. It must stay well below the 1 MiB wasm
	// stack linked in guest/build.sh: the gap is what turns runaway recursion into
	// a RangeError instead of a trap that kills the VM.
	DefaultStack = 512 << 10
	// DefaultGrace is how long past the soft deadline a call may run before its
	// module is closed.
	DefaultGrace = time.Second

	pageSize = 64 << 10
	// capHeadroom is the wasm memory a VM may hold beyond twice its heap limit:
	// the guest's static data, its 1 MiB stack, and allocator slack.
	capHeadroom = 8 << 20
)

// Limits bound one VM.
type Limits struct {
	Memory int           // every byte QuickJS allocates (DefaultMemory when 0)
	Stack  int           // QuickJS stack, bytes (DefaultStack when 0)
	Grace  time.Duration // hard deadline past the soft one (DefaultGrace when 0)
}

func (l Limits) withDefaults() Limits {
	if l.Memory <= 0 {
		l.Memory = DefaultMemory
	}
	if l.Stack <= 0 {
		l.Stack = DefaultStack
	}
	if l.Grace <= 0 {
		l.Grace = DefaultGrace
	}
	return l
}

// memoryCap is the hard wasm memory ceiling for a heap limit, in pages. QuickJS
// counts its heap, not the allocator's fragmentation, so the ceiling sits at twice
// the heap plus headroom — far enough not to trip a plugin within its limit, and a
// fixed bound on what any plugin can take from the process.
func memoryCap(heap int) uint32 {
	return uint32((2*heap + capHeadroom + pageSize - 1) / pageSize)
}

// Host answers a plugin's __host(op, arg) calls. ctx carries the call's soft
// deadline; an error is thrown in JS as an InternalError with its text.
type Host func(ctx context.Context, op string, arg []byte) ([]byte, error)

// ErrClosed is returned by a VM that was closed or whose engine was.
var ErrClosed = errors.New("jsvm: closed")

// ErrNoExport is returned by Call for a name the module does not export as a
// function.
var ErrNoExport = errors.New("main.js exports no function by that name")

// JSError is an exception the plugin's code threw (or the engine threw into it:
// "out of memory", "interrupted", "Maximum call stack size exceeded"). The VM is
// still usable.
type JSError struct {
	Message string // "Error: bad thing"
	Stack   string
}

func (e *JSError) Error() string { return e.Message }

// Interrupted reports whether the call was ended by its soft deadline.
func (e *JSError) Interrupted() bool { return e.Message == "InternalError: interrupted" }

// OutOfMemory reports whether the call hit the heap limit.
func (e *JSError) OutOfMemory() bool { return e.Message == "InternalError: out of memory" }

// TrapError means the VM died: the hard deadline passed, the guest trapped, or the
// host side failed. Every later call returns it; make a new VM.
type TrapError struct{ Cause error }

func (e *TrapError) Error() string { return "jsvm: instance died: " + firstLine(e.Cause.Error()) }
func (e *TrapError) Unwrap() error { return e.Cause }

// VM is one QuickJS instance. Calls on a VM are serialized; different VMs run in
// parallel.
type VM struct {
	mu   sync.Mutex
	mod  api.Module
	lim  Limits
	dead error

	malloc, free, script, load, has, call, outPtr, outLen, heap api.Function
}

// NewVM instantiates a fresh VM with the given limits.
func (e *Engine) NewVM(ctx context.Context, lim Limits) (*VM, error) {
	lim = lim.withDefaults()
	r, err := e.runtime(ctx, memoryCap(lim.Memory))
	if err != nil {
		return nil, err
	}
	mod, err := r.rt.InstantiateModule(ctx, r.mod, moduleConfig())
	if err != nil {
		return nil, fmt.Errorf("jsvm: instantiate: %w", err)
	}
	v := &VM{mod: mod, lim: lim}
	for name, fn := range map[string]*api.Function{
		"malloc": &v.malloc, "free": &v.free, "rp_script": &v.script, "rp_load": &v.load,
		"rp_has": &v.has, "rp_call": &v.call, "rp_out_ptr": &v.outPtr, "rp_out_len": &v.outLen,
		"rp_heap": &v.heap,
	} {
		if *fn = mod.ExportedFunction(name); *fn == nil {
			_ = mod.Close(ctx)
			return nil, fmt.Errorf("jsvm: guest lacks %s", name)
		}
	}
	res, err := mod.ExportedFunction("rp_new").Call(ctx, uint64(lim.Memory), uint64(lim.Stack))
	if err != nil || int32(res[0]) != 0 {
		_ = mod.Close(ctx)
		return nil, fmt.Errorf("jsvm: rp_new: %v %v", res, err)
	}
	return v, nil
}

// Close frees the VM. Safe to call more than once.
func (v *VM) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.dead == nil {
		v.dead = ErrClosed
	}
	_ = v.mod.Close(context.Background())
}

// Err is nil while the VM is usable, and the reason it is not afterwards.
func (v *VM) Err() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.dead
}

// Memory is the VM's wasm linear memory in bytes. It only grows — a VM that once
// held a lot keeps it until it is replaced.
func (v *VM) Memory() uint32 { return v.mod.Memory().Size() }

// MemoryCap is the hard ceiling Memory can reach, in bytes.
func (v *VM) MemoryCap() uint32 { return memoryCap(v.lim.Memory) * pageSize }

// Script runs a classic script in the global scope (the prelude).
func (v *VM) Script(ctx context.Context, name, src string, timeout time.Duration, host Host) error {
	_, err := v.run(ctx, timeout, host, func(cctx context.Context, a *guestArgs) (int32, error) {
		sp, sn, err := a.str(cctx, src)
		if err != nil {
			return 0, err
		}
		np, _, err := a.str(cctx, name) // a C string: the NUL is enough
		if err != nil {
			return 0, err
		}
		return call32(cctx, v.script, sp, sn, np)
	})
	return err
}

// Load evaluates src as the plugin's ES module. Its exports are what Call reaches.
func (v *VM) Load(ctx context.Context, src string, timeout time.Duration, host Host) error {
	_, err := v.run(ctx, timeout, host, func(cctx context.Context, a *guestArgs) (int32, error) {
		p, n, err := a.str(cctx, src)
		if err != nil {
			return 0, err
		}
		return call32(cctx, v.load, p, n)
	})
	return err
}

// Has reports whether path ("onEvent", "payment.create") names an exported
// function of the loaded module.
func (v *VM) Has(ctx context.Context, path string) bool {
	found := false
	_, err := v.run(ctx, time.Second, nil, func(cctx context.Context, a *guestArgs) (int32, error) {
		p, n, err := a.str(cctx, path)
		if err != nil {
			return 0, err
		}
		r, err := call32(cctx, v.has, p, n)
		found = r == 1
		return rpNoOutput, err
	})
	return err == nil && found
}

// Call invokes an exported function with one JSON argument and returns its result
// as JSON ("null" for undefined). A promise is awaited. timeout is the soft
// deadline; the module is closed Limits.Grace after it.
func (v *VM) Call(ctx context.Context, path string, arg []byte, timeout time.Duration, host Host) ([]byte, error) {
	if len(arg) == 0 {
		arg = []byte("null")
	}
	return v.run(ctx, timeout, host, func(cctx context.Context, a *guestArgs) (int32, error) {
		np, nn, err := a.str(cctx, path)
		if err != nil {
			return 0, err
		}
		ap, an, err := a.str(cctx, string(arg))
		if err != nil {
			return 0, err
		}
		return call32(cctx, v.call, np, nn, ap, an)
	})
}

// HeapUsed is the bytes QuickJS holds right now — what Limits.Memory caps.
func (v *VM) HeapUsed(ctx context.Context) int {
	used := 0
	_, _ = v.run(ctx, time.Second, nil, func(cctx context.Context, _ *guestArgs) (int32, error) {
		r, err := call32(cctx, v.heap)
		used = int(r)
		return rpNoOutput, err
	})
	return used
}

// maxGuestString bounds one string copied into the guest (a source, a call's JSON).
const maxGuestString = 1 << 30

// guestArgs copies strings into guest memory for one call and frees them after.
type guestArgs struct {
	v    *VM
	ptrs []uint64
}

// str copies s into guest memory followed by a NUL (JS_Eval requires one after the
// source) and returns its address and length.
func (a *guestArgs) str(ctx context.Context, s string) (ptr, n uint64, err error) {
	// Guest memory is 32-bit and far smaller still: what cannot fit is refused
	// before len(s)+1 is computed or allocated.
	if len(s) >= maxGuestString {
		return 0, 0, errors.New("a string too big for guest memory")
	}
	r, err := a.v.malloc.Call(ctx, uint64(len(s)+1))
	if err != nil {
		return 0, 0, err
	}
	if r[0] == 0 {
		return 0, 0, errors.New("guest malloc failed")
	}
	a.ptrs = append(a.ptrs, r[0])
	buf := make([]byte, len(s)+1)
	copy(buf, s)
	if !a.v.mod.Memory().Write(uint32(r[0]), buf) {
		return 0, 0, errors.New("guest memory write out of range")
	}
	return r[0], uint64(len(s)), nil
}

func (a *guestArgs) release(ctx context.Context) {
	for _, p := range a.ptrs {
		_, _ = a.v.free.Call(ctx, p)
	}
}

func call32(ctx context.Context, fn api.Function, args ...uint64) (int32, error) {
	res, err := fn.Call(ctx, args...)
	if err != nil {
		return 0, err
	}
	return int32(res[0]), nil
}

// Guest status codes (rp.c), plus rpNoOutput for calls that answer in-band.
const (
	rpOK       = 0
	rpJSError  = 1
	rpNoExport = 2
	rpNoOutput = -1
)

// run executes one guest call under the soft and hard deadlines and turns its
// outcome into a result or an error.
func (v *VM) run(ctx context.Context, timeout time.Duration, host Host, f func(context.Context, *guestArgs) (int32, error)) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.dead != nil {
		return nil, v.dead
	}
	soft := time.Now().Add(timeout)
	hostCtx, cancelHost := context.WithDeadline(ctx, soft)
	defer cancelHost()
	st := &callState{soft: soft, host: host, ctx: hostCtx}
	cctx, cancel := context.WithDeadline(context.WithValue(ctx, callKey{}, st), soft.Add(v.lim.Grace))
	defer cancel()

	args := &guestArgs{v: v}
	code, err := f(cctx, args)
	if err == nil {
		args.release(cctx)
	}
	if err == nil && cctx.Err() != nil {
		// The call returned, but only after the hard deadline closed the module.
		err = cctx.Err()
	}
	if err != nil {
		v.dead = &TrapError{Cause: err}
		_ = v.mod.Close(context.Background())
		return nil, v.dead
	}
	switch code {
	case rpNoOutput:
		return nil, nil
	case rpOK:
		return v.output(cctx)
	case rpNoExport:
		return nil, ErrNoExport
	default:
		out, err := v.output(cctx)
		if err != nil {
			return nil, err
		}
		msg, stack, _ := strings.Cut(string(out), "\n")
		return nil, &JSError{Message: msg, Stack: stack}
	}
}

// output copies the guest's last result out of its memory.
func (v *VM) output(ctx context.Context) ([]byte, error) {
	p, err := v.outPtr.Call(ctx)
	if err != nil {
		return nil, err
	}
	n, err := v.outLen.Call(ctx)
	if err != nil {
		return nil, err
	}
	b, ok := v.mod.Memory().Read(uint32(p[0]), uint32(n[0]))
	if !ok {
		return nil, errors.New("jsvm: guest output out of range")
	}
	return bytes.Clone(b), nil
}

// callState is what the host functions need to know about the call they serve.
// It rides in the context wazero passes them.
type callState struct {
	soft    time.Time
	host    Host
	ctx     context.Context // ends at the soft deadline
	pending []byte
}

type callKey struct{}

func stateOf(ctx context.Context) *callState {
	st, _ := ctx.Value(callKey{}).(*callState)
	return st
}

// hostCall serves __host(op, arg): (op, oplen, arg, arglen) → answer length, or
// -(length+1) for an error message. The answer waits in callState for host_take.
func hostCall(ctx context.Context, m api.Module, stack []uint64) {
	st := stateOf(ctx)
	answer := func(b []byte, failed bool) {
		st.pending = b
		n := int32(len(b))
		if failed {
			n = -n - 1
		}
		stack[0] = api.EncodeI32(n)
	}
	if st == nil {
		stack[0] = api.EncodeI32(-1)
		return
	}
	if st.host == nil {
		answer([]byte("no host operations here"), true)
		return
	}
	op, ok1 := m.Memory().Read(api.DecodeU32(stack[0]), api.DecodeU32(stack[1]))
	arg, ok2 := m.Memory().Read(api.DecodeU32(stack[2]), api.DecodeU32(stack[3]))
	if !ok1 || !ok2 {
		answer([]byte("bad host call"), true)
		return
	}
	res, err := safeHost(st, string(op), bytes.Clone(arg))
	if err != nil {
		answer([]byte(err.Error()), true)
		return
	}
	answer(res, false)
}

// safeHost runs the caller's Host, turning a panic into an error for the plugin
// instead of a crash of the panel.
func safeHost(st *callState, op string, arg []byte) (res []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("host %s failed: %v", op, r)
		}
	}()
	return st.host(st.ctx, op, arg)
}

func hostTake(ctx context.Context, m api.Module, stack []uint64) {
	st := stateOf(ctx)
	if st == nil {
		return
	}
	m.Memory().Write(api.DecodeU32(stack[0]), st.pending)
	st.pending = nil
}

func hostInterrupt(ctx context.Context, _ api.Module, stack []uint64) {
	st := stateOf(ctx)
	if st != nil && time.Now().After(st.soft) {
		stack[0] = api.EncodeI32(1)
		return
	}
	stack[0] = api.EncodeI32(0)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
