# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`starpkg/qrcode` is an **L4 domain module** of the Star\* ecosystem: it exposes QR-code generation to Starlark scripts. A script imports the module, encodes a string (or fills a named wire-format template), and renders the resulting QR four ways — half-block ASCII, pure ASCII, SVG, and a hand-written 1-bit BMP.

starpkg's remit is *support for necessary **local** operations + simple abstractions over common **online** services, for ease of use*. QR generation is a purely **local capability**: it touches no network and needs no credentials, so this module lives entirely on the local side of that line — closer to `markdown`/`base32` than to `web`/`s3`/`email`.

It is **pure Go, all platforms, no cgo, and no `image/*` dependency**. The single third-party encoder is `github.com/boombuler/barcode/qr`; the BMP is emitted by hand with `encoding/binary` (≈16 KiB of code) rather than pulling in `image/png` (+249 KiB) or lossy JPEG (whose ringing artifacts hurt scannability).

Layer position: depends downward on `starpkg/base` (the module/config system), `1set/starlet` (the Machine + `dataconv/types`), and transitively `1set/starlight` + `go.starlark.net`. Nothing in the ecosystem depends on it.

## Dev commands

Pure Go library with a Makefile. From this repo:

```bash
make test                                  # -race -cover, the working bar
make ci                                    # -race -cover profile + bench compile (what CI runs)
make bench                                 # benchmarks only
go test ./... -run TestOutputSizeCap       # a single test
gofmt -l . && go vet ./...                 # must be clean before commit
go run github.com/1set/meta/doccov@master .  # doc-coverage gate (exits 0 when every builtin is documented)
```

**Verify on the go floor in Docker** — this repo's floor is **go 1.19** (see Release discipline), older than the local toolchain, so floor behavior must be checked in a container:

```bash
docker run --rm -v "$PWD":/src -v "$HOME/go/pkg/mod":/go/pkg/mod -w /src golang:1.19 go test -race -count=1 ./...
```

This module has no external-service dependency, so there are no credential-gated or auto-skipping tests — every test runs everywhere. (Modules that ship `../test/<module>/*.star` integration scripts keep them in the **private `starpkg/test` repo** and auto-skip when that directory is absent; qrcode keeps all of its tests in-repo.)

## Architecture (the part that spans files)

The module is an **encode-once / render-four-ways bridge**: `encode()` (or `template()`) runs the QR encoder a single time and captures the boolean module matrix; the returned `qrValue` is a Starlark object whose four render methods project that one shared matrix into different output forms.

- **`qrcode.go`** — the module entry. `Module` holds a `base.ConfigurableModule` + its `ConfigurableModuleExt`; `NewModule()` constructs it with three config options (`ec_level`, `quiet_zone`, `max_output_bytes`), each env-backed via `QRCODE_<UPPER>`. `LoadModule()` exposes two builtins: **`encode`** and **`template`**. `encodeMatrix()` is the single call into the third-party encoder (`qr.Encode`, recover-wrapped) that extracts the `[][]bool` matrix; `resolveECLevel` maps `L`/`M`/`Q`/`H` to `qr.ErrorCorrectionLevel`; `maxOutput()` resolves the per-render byte cap.
- **`render.go`** — `qrValue` (the Starlark `HasAttrs` QR object) and its render methods. `AttrNames`/`Attr` surface one attribute (**`size`**) and four method builtins (**`ascii`**, **`pure_ascii`**, **`svg`**, **`bmp`**). `padded()` wraps the matrix in a quiet zone; `checkOutputSize()`/`mulSat()` are the memory-amplification guard; `encodeBMP()` is the hand-written 1-bit BMP writer (`BITMAPFILEHEADER` + `BITMAPINFOHEADER` + 2-color palette + bottom-up 1bpp rows).
- **`template.go`** — `template()` and the pure `templateContent()` helper that assembles the wire string for a `kind` (`url`/`email`/`wifi`/`tel`/`sms`/`geo`/`vcard`). `templateParams` documents each kind's parameters (and feeds the error messages); `wifiEscape`/`mecardEscape` backslash-escape the `WIFI:`/`MECARD:` formats; `starlarkToString` stringifies non-string keyword values (e.g. `30` → `"30"`).

Data flow: `content` (or a templated string) → `encodeMatrix` → `qrValue{matrix, quietZone, maxOutput}` → a render method projects + size-checks → `starlark.String` (ascii/pure_ascii/svg) or `starlark.Bytes` (bmp).

**Third-party wrap point:** the *only* call into `boombuler/barcode` is `qr.Encode` inside `encodeMatrix`, and it is wrapped in a `recover()`. Keep all encoder interaction funnelled through there.

## Invariants / hardening (preserve when editing)

1. **No host panics from script input.** `encodeMatrix` wraps `qr.Encode` in a `recover()` that turns any panic into a clean `qrcode: encode panic: …` error (the same defense-in-depth pattern as the yaml/toml/liquid third-party-parser wraps). boombuler returns errors today, so this is forward-looking — do not remove the deferred recover.
2. **Bounded output (memory-amplification guard).** A tiny QR plus an unbounded `scale`/`module_size`/`quiet_zone` can project to hundreds of MB (e.g. `bmp(scale=2000)` ≈ 840 MB). Every render method computes its projected byte size with `mulSat` (saturating int64 multiply, so a huge input can never overflow/wrap to a guard-bypassing value) and calls `checkOutputSize()` **before** allocating; over the cap is a clean error, not an OOM. New render paths must size-check up front the same way.
3. **`max_output_bytes` always active and host-only.** It is the module's own memory-DoS cap, so it is registered with `SetHostOnly(true)`: `base` emits **no `set_max_output_bytes` builtin** and snapshots its env at construction, so a script cannot raise or re-widen it (only the host, via `QRCODE_MAX_OUTPUT_BYTES`, can). `maxOutput()` falls back to the 16 MiB default when the value is unset or non-positive — a non-positive value must never silently disable the guard. Don't drop the host-only flag; a script-settable cap is not a cap.
4. **Input validation.** Empty `content`, negative `quiet_zone`, non-positive `module_size`/`scale`, and an unknown `ec_level`/template `kind` all return clear errors rather than producing a broken QR.
5. **Backward compatibility (iron rule).** `encode`/`template` defaults (`level` from config, `quiet_zone` from config) and every render default (`module_size=4`, `scale=4`, `on="##"`, `off="  "`) are fixed surface. Any new safety lever must default to the historical behavior so existing scripts render identically.

## Test organization

Group by functional goal — **do not add one `*_test.go` per fix.** Two thematic files, each opened with a commented section list:

- **`qrcode_test.go`** — the core module: the four output forms, hand-written BMP header correctness, error paths, encoder panic-recovery, and the output-size cap (`TestOutputSizeCap` / `TestOutputSizeCapConfigurable`). `run(t, script)` is the shared Starlet harness.
- **`template_test.go`** — the `template()` constructor: `templateContent` wire-format correctness per kind, required-param / unknown-kind errors, and the end-to-end path through Starlark.

Add a new test as a **section in the matching file**, not a new file. Tests are table/example-driven; no third-party test framework. Keep test functions modest in size (Codacy's `nloc` rule).

## Documentation

Three layers must stay in sync (enforced by the doc standard, `plan/starpkg文档标准（DOC-STD）`):

- **`README.md`** — every script-facing builtin and object method documented as a backtick whole-word; the host config levers (`ec_level`, `quiet_zone`, `max_output_bytes` / `QRCODE_*`) under *Configuration*. Names, signatures, and behaviour must match the code.
- **GoDoc** — package comment + a doc comment on every exported symbol (`ModuleName`, `Module`, `NewModule`, `LoadModule`), first word = symbol name (gated by `revive`'s `exported` rule in CI).
- **doc-coverage gate** — `1set/meta/doccov` statically scans `starlark.NewBuiltin("…", …)` calls and fails CI if any builtin is missing from the README. Wired in `.github/workflows/build.yml` via `doc-coverage: true`. *Quirk:* doccov's backtick-span tokenizer pairs backticks document-wide, so triple-backtick code fences can shift the pairing and drop an inline `` `word` `` out of a recognized span — the robust fix is to also mention each builtin **inside a fenced code block** (as the Usage example does for all six). The gate guards against omission, not against an inaccurate description (that is a review concern).

## Release discipline

- **Floor = go 1.19**, matching `go.mod`. A repo's floor only rises in its own isolated pin PR.
- **Pins:** `go.starlark.net` on the ecosystem baseline (`ffb3f39…`), `1set/starlet v0.2.1`, `starpkg/base v0.1.0`, `boombuler/barcode v1.1.0`.
- **CI matrix** = `[1.19.x, 1.25.x]` via the centralized reusable workflow in `1set/meta` (pinned to a commit SHA; bump the pin when meta's workflow changes).
- **Pin upgrade is the last PR of the series**, as one isolated change; never tag before it merges.
- **Bumping the version, the go floor, or tagging are user-confirmed actions** — never tag autonomously; draft the title/notes and tag only after explicit approval; default to patch bumps; published tags are immutable in the module proxy.
