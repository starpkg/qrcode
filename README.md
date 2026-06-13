# 🔳 `qrcode` — QR codes for Starlark

[![Go Reference](https://pkg.go.dev/badge/github.com/starpkg/qrcode.svg)](https://pkg.go.dev/github.com/starpkg/qrcode)

Generate QR codes from Starlark in **four output forms** — half-block ASCII,
pure ASCII, SVG, and a 1-bit BMP — built on
[boombuler/barcode](https://github.com/boombuler/barcode).

**No `image/*` dependency.** The raster form is a hand-written 1-bit BMP emitted
with `encoding/binary` (≈16 KiB), avoiding `image/png` (+249 KiB) and lossy JPEG
(ringing artifacts hurt scannability). `encode()` runs the encoder once; the
returned QR renders the shared module matrix four ways.

## Installation

```bash
go get github.com/starpkg/qrcode
```

## Functions

| Function | Signature | Description |
|----------|-----------|-------------|
| `encode` | `encode(content, level="M", quiet_zone=4) -> QR` | Encode `content`. `level` is the error-correction level (`L`/`M`/`Q`/`H`). |
| `QR.size` | attribute → `int` | Module count per side (excluding quiet zone). |
| `QR.ascii` | `QR.ascii(quiet_zone=<default>) -> str` | Half-block rendering (two module rows per text line). |
| `QR.pure_ascii` | `QR.pure_ascii(quiet_zone=<default>, on="##", off="  ") -> str` | ASCII-only rendering, two characters per module. |
| `QR.svg` | `QR.svg(quiet_zone=<default>, module_size=4) -> str` | Scalable vector image. |
| `QR.bmp` | `QR.bmp(quiet_zone=<default>, scale=4) -> bytes` | 1-bit BMP image bytes. |

## Usage

```python
load("qrcode", "encode")

qr = encode("https://example.com", level="M")
print(qr.size)             # e.g. 25

print(qr.ascii())          # scan it straight from the terminal
svg = qr.svg(module_size=6)
png_free_bytes = qr.bmp(scale=8)   # write to a .bmp file
```

## The four forms

- **`ascii`** — half-block characters (`█ ▀ ▄`), ~2× denser vertically; best for
  terminals.
- **`pure_ascii`** — only `#` and spaces; safe anywhere, two chars per module for
  a square aspect.
- **`svg`** — crisp vector, scales to any size.
- **`bmp`** — lossless 1-bit raster, hand-written (no `image/*`), recognized by
  `file(1)` and image viewers.

All forms include a configurable quiet zone (default 4 modules) for
scannability.

## Configuration

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `ec_level` | `string` | `"M"` | Default error-correction level (L/M/Q/H) |
| `quiet_zone` | `int` | `4` | Default quiet-zone width in modules |
| `max_output_bytes` | `int` | `16777216` | Maximum projected size of a single rendered output (16 MiB); guards against memory amplification |

Settable via `QRCODE_EC_LEVEL` / `QRCODE_QUIET_ZONE` / `QRCODE_MAX_OUTPUT_BYTES`.

Each render projects its output size from the padded dimension and per-cell cost
and is rejected with a clean error before allocating if it would exceed
`max_output_bytes` — so an unbounded `scale`/`module_size`/`quiet_zone` cannot
amplify a tiny QR into a multi-hundred-MB allocation.
