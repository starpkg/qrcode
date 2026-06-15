# `qrcode` — Starlark API Reference

The complete reference for every script-facing builtin, QR-object method, and
configuration accessor exposed by the `qrcode` module. For an overview,
installation, and a quickstart, see the [README](../README.md).

The module exposes two top-level builtins via `load("qrcode", …)` — `encode`
and `template` — plus a set of configuration accessors (`get_<key>` /
`set_<key>`) generated from the module's options. Both builtins return the
**same** QR object, whose attribute (`size`) and four render methods (`ascii`,
`pure_ascii`, `svg`, `bmp`) project one shared module matrix into different
output forms.

## Contents

- [Encoding](#encoding)
- [Format templates](#format-templates)
- [The QR object](#the-qr-object)
  - [`QR.size`](#qrsize)
  - [`QR.ascii(...)`](#qrasciiquiet_zone)
  - [`QR.pure_ascii(...)`](#qrpure_asciiquiet_zone-on-off)
  - [`QR.svg(...)`](#qrsvgquiet_zone-module_size)
  - [`QR.bmp(...)`](#qrbmpquiet_zone-scale)
- [The four output forms](#the-four-output-forms)
- [Configuration](#configuration)

## Encoding

### `encode(content, level?, quiet_zone?)`

Encodes `content` into a QR code, running the underlying encoder once and
returning a [QR object](#the-qr-object) that can be rendered four ways.

**Parameters:**

- `content` (string or bytes): The data to encode. Must not be empty.
- `level` (string, optional): Error-correction level — one of `L`, `M`, `Q`,
  `H` (default: the module's `ec_level` config option, normally `"M"`). Higher
  levels recover from more damage at the cost of capacity.
- `quiet_zone` (int, optional): Default quiet-zone width in modules, used by the
  render methods unless they are passed their own `quiet_zone` (default: the
  module's `quiet_zone` config option, normally `4`). Must not be negative.

**Returns:** A `qrcode.QR` object.

**Errors:**

- `content must not be empty` when `content` is the empty string.
- `quiet_zone must not be negative` when `quiet_zone < 0`.
- `ec_level must be L, M, Q, or H, got …` when `level` is none of `L`/`M`/`Q`/`H`.
- `qrcode: …` when the underlying encoder rejects the input (e.g. content too
  large for the chosen error-correction level).

**Example:**

```python
load("qrcode", "encode")

qr = encode("https://example.com", level="M")
print(qr.size)             # e.g. 25 — module count per side

print(qr.ascii())          # scan it straight from the terminal
print(qr.pure_ascii())     # ASCII-only (# and spaces), two chars per module
svg = qr.svg(module_size=6)
raster = qr.bmp(scale=8)   # 1-bit BMP bytes — write to a .bmp file
```

## Format templates

### `template(kind, level?, quiet_zone?, **params)`

Builds a QR from a named wire-format template, filling the standard boilerplate
from simple parameters — so a script does not hand-write the `mailto:` /
`WIFI:` / `MECARD:` strings. The returned value is a QR identical to
`encode()`'s, so [`size`](#qrsize), [`ascii`](#qrasciiquiet_zone),
[`pure_ascii`](#qrpure_asciiquiet_zone-on-off), [`svg`](#qrsvgquiet_zone-module_size),
and [`bmp`](#qrbmpquiet_zone-scale) all apply, and it shares the same `level` /
`quiet_zone` / `max_output_bytes` behaviour.

**Parameters:**

- `kind` (string, positional — the **only** positional argument): the template
  to fill. One of `url`, `email`, `wifi`, `tel`, `sms`, `geo`, `vcard`.
- `level` (string, reserved keyword): error-correction level, same meaning and
  default as [`encode`](#encodecontent-level-quiet_zone).
- `quiet_zone` (int, reserved keyword): default quiet-zone width, same meaning
  and default as [`encode`](#encodecontent-level-quiet_zone).
- `**params` (keyword arguments): the template parameters for the chosen `kind`
  (see the table below). Non-string values are stringified (e.g. `30` → `"30"`,
  `True` → `"True"`).

**Template kinds and their parameters** (required parameters listed first):

| kind | parameters | wire format |
|------|------------|-------------|
| `url` | `url` (required) | the URL verbatim |
| `email` | `to` (required), `subject`, `body` | `mailto:<to>` plus a URL-encoded `?subject=…&body=…` query when present |
| `wifi` | `ssid` (required), `password`, `security` (`WPA`/`WEP`/`nopass`; default `WPA`), `hidden` (bool) | `WIFI:T:<security>;S:<ssid>;P:<password>;H:<hidden>;;` |
| `tel` | `number` (required) | `tel:<number>` |
| `sms` | `number` (required), `message` | `SMSTO:<number>:<message>` |
| `geo` | `lat` (required), `lng` (required) | `geo:<lat>,<lng>` |
| `vcard` | `name` (required), `phone`, `email`, `org`, `url` | `MECARD:N:<name>;TEL:<phone>;EMAIL:<email>;ORG:<org>;URL:<url>;;` (only present fields are emitted) |

Special characters are escaped per format: `\`, `;`, `,`, `:`, `"` are
backslash-escaped in the `WIFI:` fields and `\`, `;`, `:`, `,` in the `MECARD:`
fields; the `mailto:` query is URL-encoded. For `wifi`, a `hidden` value of
`true` / `True` / `1` renders `H:true`, anything else `H:false`.

**Returns:** A `qrcode.QR` object.

**Errors:**

- `qrcode.template: want exactly 1 positional argument (kind), got N` when
  `kind` is missing or extra positionals are passed.
- `qrcode.template: kind must be a string, got …` when `kind` is not a string.
- `qrcode.template "<kind>": missing required parameter "<key>" (want: …)` when
  a required parameter is empty or absent.
- `qrcode.template: unknown kind "<kind>" (supported: …)` for an unrecognized
  `kind`.
- the same `quiet_zone` / `ec_level` / encoder errors as
  [`encode`](#encodecontent-level-quiet_zone).

**Example:**

```python
load("qrcode", "template")

template("url",   url="https://example.com")
template("wifi",  ssid="Cafe", password="latte123", security="WPA2")
template("email", to="a@b.com", subject="Hi", body="Hello & welcome")
template("tel",   number="+8613800138000")
template("sms",   number="+15551234567", message="ping")
template("geo",   lat=30.27, lng=120.15)
template("vcard", name="Ada Lovelace", phone="+1555", email="ada@x.org", org="Analytical")

# render like any QR — a wifi QR your phone can join from the terminal:
print(template("wifi", ssid="Cafe", password="latte123").ascii())
```

## The QR object

`encode()` and `template()` both return a `qrcode.QR` object. It carries one
attribute and four render methods; every method applies a quiet zone (a light
border around the symbol) and is size-checked against `max_output_bytes`
**before** allocating (see [Configuration](#configuration)).

### `QR.size`

Attribute (not a method). The module count per side of the encoded symbol,
**excluding** the quiet zone.

**Type:** `int`

**Example:**

```python
qr = encode("hello")
print(qr.size)   # e.g. 21 for a small version-1 symbol
```

### `QR.ascii(quiet_zone?)`

Renders the QR using Unicode half-block characters (`█`, `▀`, `▄`, space), two
module rows per text line — about twice as dense vertically, best for terminals.

**Parameters:**

- `quiet_zone` (int, optional): quiet-zone width in modules for this render
  (default: the QR's quiet zone, set at `encode` / `template` time). Must not be
  negative.

**Returns:** A string (one line per two module rows, newline-terminated rows).

**Errors:** `quiet_zone must not be negative`; or a projected-output-size error
when the rendered size would exceed `max_output_bytes`.

**Example:**

```python
print(encode("https://example.com").ascii())
print(encode("https://example.com").ascii(quiet_zone=2))
```

### `QR.pure_ascii(quiet_zone?, on?, off?)`

Renders the QR using only printable ASCII, two characters per module so the
result has a roughly square aspect ratio. Safe to embed anywhere.

**Parameters:**

- `quiet_zone` (int, optional): quiet-zone width in modules (default: the QR's
  quiet zone). Must not be negative.
- `on` (string, optional): the text for a dark (set) module (default: `"##"`).
- `off` (string, optional): the text for a light (unset) module (default:
  `"  "`, two spaces).

**Returns:** A string (one line per module row).

**Errors:** `quiet_zone must not be negative`; or a projected-output-size error
when the rendered size would exceed `max_output_bytes`.

**Example:**

```python
print(encode("hello").pure_ascii())
print(encode("hello").pure_ascii(on="[]", off=".."))
```

### `QR.svg(quiet_zone?, module_size?)`

Renders the QR as a scalable SVG vector image with crisp edges.

**Parameters:**

- `quiet_zone` (int, optional): quiet-zone width in modules (default: the QR's
  quiet zone). Must not be negative.
- `module_size` (int, optional): the pixel size of each module's side (default:
  `4`). Must be positive.

**Returns:** A string containing the `<svg>…</svg>` markup.

**Errors:** `quiet_zone must not be negative`; `module_size must be positive`
when `module_size <= 0`; a `pixel dimension overflow` error when `module_size`
is large enough to overflow the pixel coordinates; or a projected-output-size
error when the rendered size would exceed `max_output_bytes`.

**Example:**

```python
svg = encode("https://example.com").svg(module_size=6)
# write svg to a .svg file, embed it in HTML, etc.
```

### `QR.bmp(quiet_zone?, scale?)`

Renders the QR as a hand-written 1-bit (monochrome) BMP image — no `image/*`
dependency. The output is recognized by `file(1)` and standard image viewers.

**Parameters:**

- `quiet_zone` (int, optional): quiet-zone width in modules (default: the QR's
  quiet zone). Must not be negative.
- `scale` (int, optional): the number of pixels per module side (default: `4`).
  Must be positive.

**Returns:** A bytes value containing the BMP file (header + 2-color palette +
bottom-up 1bpp rows).

**Errors:** `quiet_zone must not be negative`; `scale must be positive` when
`scale <= 0`; or a projected-output-size error when the rendered size would
exceed `max_output_bytes` (the BMP is the largest of the four forms, so this
guard matters most here).

**Example:**

```python
raster = encode("https://example.com").bmp(scale=8)
# raster is bytes — write it to a .bmp file
```

## The four output forms

All four render methods project the same encoded matrix; they differ only in how
each module is drawn and what type comes back. Every form includes a configurable
quiet zone (default 4 modules) for scannability.

- **`ascii`** — Unicode half-block characters (`█ ▀ ▄`), ~2× denser vertically;
  best for terminals. Returns a string.
- **`pure_ascii`** — only `#` and spaces (configurable via `on`/`off`); safe
  anywhere, two characters per module for a square aspect. Returns a string.
- **`svg`** — crisp vector markup, scales to any size. Returns a string.
- **`bmp`** — lossless 1-bit raster, hand-written with `encoding/binary` (no
  `image/*`). Returns bytes. Chosen over `image/png` (+249 KiB) and lossy JPEG
  (whose ringing artifacts hurt scannability).

## Configuration

Each module configuration option is exposed to scripts as a pair of generated
accessor builtins (loaded from the `qrcode` module alongside `encode` and
`template`):

- **`get_<key>()`** — returns the current value of the option.
- **`set_<key>(value)`** — sets the option (returns `None`).

An option's value resolves in priority order: an explicit `set_<key>` value, the
environment variable, then the default. These options serve as the defaults used
by `encode` / `template` (for `level` / `quiet_zone`) and by every render method
(for the output-size cap and the inherited quiet zone) when the corresponding
argument is not supplied.

None of the `qrcode` options are secret, so every option exposes **both**
`get_<key>` and `set_<key>`. (A secret option would expose only its `set_<key>`
accessor — never a getter — but this module has none.)

| Option | Getter | Setter | Type | Env var | Default | Description |
|--------|--------|--------|------|---------|---------|-------------|
| `ec_level` | `get_ec_level` | `set_ec_level` | string | `QRCODE_EC_LEVEL` | `M` | Default error-correction level (`L`, `M`, `Q`, `H`) |
| `quiet_zone` | `get_quiet_zone` | `set_quiet_zone` | int | `QRCODE_QUIET_ZONE` | `4` | Default quiet-zone width in modules |
| `max_output_bytes` | `get_max_output_bytes` | `set_max_output_bytes` | int | `QRCODE_MAX_OUTPUT_BYTES` | `16777216` | Maximum projected size of a single rendered output in bytes (16 MiB); guards against memory amplification |

**Example:**

```python
load(
    "qrcode",
    "encode",
    # getters
    "get_ec_level", "get_quiet_zone", "get_max_output_bytes",
    # setters
    "set_ec_level", "set_quiet_zone", "set_max_output_bytes",
)

set_ec_level("H")
set_quiet_zone(2)
print(get_ec_level())          # "H"
print(get_max_output_bytes())  # 16777216

qr = encode("https://example.com")  # encodes at level H, quiet zone 2
print(qr.ascii())
```

### Bounded output (memory-amplification guard)

A tiny QR plus an unbounded `scale` / `module_size` / `quiet_zone` can project to
hundreds of megabytes (for example `bmp(scale=2000)` ≈ 840 MB). Each render
projects its output size from the padded dimension and per-cell cost and is
rejected with a clean error **before** allocating if it would exceed
`max_output_bytes` — so an unbounded lever cannot amplify a tiny QR into a
multi-hundred-megabyte allocation. The cap is always active: a non-positive
`max_output_bytes` falls back to the 16 MiB default rather than disabling the
guard.
