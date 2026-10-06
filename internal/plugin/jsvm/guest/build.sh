#!/bin/sh
# Builds rp.wasm.gz — QuickJS-ng plus rp.c — from the pinned sources in VERSIONS.
#
# Run it in a throwaway Linux container from the repository root:
#
#   docker run --rm -v "$PWD/internal/plugin/jsvm/guest:/guest" golang:1.26.6 /guest/build.sh
#
# The result is committed; CI rebuilds it and compares the sha256, so a change to
# rp.c, a version bump or a toolchain drift shows up as a diff instead of passing
# silently.
set -eu
. /guest/VERSIONS
work=$(mktemp -d)
cd "$work"

case "$(uname -m)" in
x86_64) arch=x86_64; sdk_sha=$WASI_SDK_SHA256_X86_64 ;;
aarch64 | arm64) arch=arm64; sdk_sha=$WASI_SDK_SHA256_ARM64 ;;
*) echo "unsupported build host $(uname -m)" >&2; exit 1 ;;
esac

fetch() { # url sha256 file
	curl -fsSL -o "$3" "$1"
	echo "$2  $3" | sha256sum -c - >/dev/null || { echo "sha256 mismatch for $1" >&2; exit 1; }
}

fetch "https://github.com/WebAssembly/wasi-sdk/releases/download/wasi-sdk-${WASI_SDK_VERSION%%.*}/wasi-sdk-${WASI_SDK_VERSION}-${arch}-linux.tar.gz" "$sdk_sha" sdk.tgz
fetch "https://github.com/quickjs-ng/quickjs/archive/refs/tags/${QUICKJS_NG_VERSION}.tar.gz" "$QUICKJS_NG_SHA256" qjs.tgz
mkdir sdk qjs
tar xzf sdk.tgz -C sdk --strip-components=1
tar xzf qjs.tgz -C qjs --strip-components=1

# -mexec-model=reactor: a library with _initialize, not a program with main.
# stack-size: the real wasm stack; rp.c keeps QuickJS's own limit below it so deep
# recursion is a RangeError and never a trap. Never define QJS_DISABLE_PARSER — even
# as 0 it switches the parser off ("eval is not supported").
sdk/bin/clang --target=wasm32-wasip1 -O2 -flto \
	-D_WASI_EMULATED_SIGNAL -D_WASI_EMULATED_PROCESS_CLOCKS \
	-Iqjs -mexec-model=reactor \
	qjs/quickjs.c qjs/libregexp.c qjs/libunicode.c qjs/dtoa.c /guest/rp.c \
	-lwasi-emulated-signal -lwasi-emulated-process-clocks \
	-Wl,-z,stack-size=1048576 -Wl,--export=malloc -Wl,--export=free -Wl,--strip-all \
	-Wno-everything -o rp.wasm

gzip -n -9 -c rp.wasm >/guest/rp.wasm.gz
sha256sum rp.wasm | cut -d' ' -f1 >/guest/rp.wasm.sha256
echo "rp.wasm $(wc -c <rp.wasm) bytes, sha256 $(cat /guest/rp.wasm.sha256)"
