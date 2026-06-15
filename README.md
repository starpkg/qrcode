# 🔳 `qrcode` — QR codes for Starlark

[![Go Reference](https://pkg.go.dev/badge/github.com/starpkg/qrcode.svg)](https://pkg.go.dev/github.com/starpkg/qrcode)
[![codecov](https://codecov.io/gh/starpkg/qrcode/graph/badge.svg)](https://codecov.io/gh/starpkg/qrcode)
![binary footprint](https://img.shields.io/badge/binary_footprint-%2B0.4_MB-blue)

Generate QR codes from Starlark in **four output forms** — half-block ASCII,
pure ASCII, SVG, and a 1-bit BMP — built on
[boombuler/barcode](https://github.com/boombuler/barcode).

## Overview

`qrcode` is a **starpkg** module: starpkg gives Starlark support for necessary
local operations plus simple abstractions over common online services, for ease
of use. QR generation is a purely **local capability** — no network, no service
credentials — so this module sits firmly on the local side of that line.

- **Encode once, render four ways** — `encode()` runs the encoder a single time;
  the returned QR renders the shared module matrix as `ascii`, `pure_ascii`,
  `svg`, or `bmp`.
- **Format templates** — `template()` fills the standard `mailto:` / `WIFI:` /
  `MECARD:` wire formats from simple parameters, so you do not hand-write them.
- **No `image/*` dependency** — the raster form is a hand-written 1-bit BMP
  emitted with `encoding/binary` (≈16 KiB), avoiding `image/png` (+249 KiB) and
  lossy JPEG (whose ringing artifacts hurt scannability).
- **Bounded by default** — every render is size-checked against
  `max_output_bytes` before allocating, so an unbounded `scale` / `module_size`
  / `quiet_zone` cannot amplify a tiny QR into a multi-hundred-megabyte
  allocation.

For the complete per-builtin reference — signatures, parameters, returns,
errors, examples — and the configuration accessors, see
**[docs/API.md](docs/API.md)**.

## Installation

```bash
go get github.com/starpkg/qrcode
```

## Quickstart

```python
load("qrcode", "encode")

qr = encode("https://example.com", level="M")
print(qr.size)             # e.g. 25 — module count per side

print(qr.ascii())          # scan it straight from the terminal
print(qr.pure_ascii())     # ASCII-only (# and spaces), two chars per module
svg = qr.svg(module_size=6)
raster = qr.bmp(scale=8)   # 1-bit BMP bytes — write to a .bmp file
```

A Wi-Fi QR your phone can join, built from a template:

```python
load("qrcode", "template")

print(template("wifi", ssid="Cafe", password="latte123", security="WPA2").ascii())
```

## Starlark API at a glance

Top-level builtins (`load("qrcode", …)`):

- `encode(content, level?, quiet_zone?)` — encode a string/bytes into a QR object.
- `template(kind, level?, quiet_zone?, **params)` — build a QR from a named wire
  format (`url`, `email`, `wifi`, `tel`, `sms`, `geo`, `vcard`).

QR object (returned by both builtins):

- `size` — attribute: module count per side, excluding the quiet zone.
- `ascii(quiet_zone?)` — half-block rendering for terminals; returns a string.
- `pure_ascii(quiet_zone?, on?, off?)` — ASCII-only rendering; returns a string.
- `svg(quiet_zone?, module_size?)` — scalable vector image; returns a string.
- `bmp(quiet_zone?, scale?)` — 1-bit BMP image; returns bytes.

See **[docs/API.md](docs/API.md)** for the full signatures, return values,
errors, template parameters, and examples of every builtin and method above.

## Configuration

The module's options (`ec_level`, `quiet_zone`, `max_output_bytes`) are
configured via environment variables (`QRCODE_EC_LEVEL` / `QRCODE_QUIET_ZONE` /
`QRCODE_MAX_OUTPUT_BYTES`) or per-option `get_<key>` / `set_<key>` accessor
builtins, and serve as defaults for `encode` / `template` and the render
methods. See the
[Configuration section of docs/API.md](docs/API.md#configuration) for the full
option table, defaults, accessors, and the memory-amplification guard.

## License

MIT
