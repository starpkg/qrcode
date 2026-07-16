package qrcode

// Tests for the qrcode module.
//
// Sections:
//   - the four output forms
//   - hand-written BMP header correctness
//   - error paths
//   - encoder panic-recovery (defense-in-depth)
//   - output-size cap (memory-amplification guard)
//   - overflow hardening (huge scale/module_size/quiet_zone → clean error, no panic)
//   - pure helpers (resolveECLevel, mulSat/addSat/paddedDim, upper, maxOutput)
//   - the QR Starlark value protocol (String/Type/AttrNames/Attr/Hash/Truth)
//   - argument validation (negative quiet_zone, non-positive module_size/scale)

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"github.com/1set/starlet"
	"github.com/boombuler/barcode/qr"
	"go.starlark.net/starlark"
)

func run(t *testing.T, script string) (map[string]interface{}, error) {
	t.Helper()
	m := starlet.NewDefault()
	m.SetScriptContent([]byte(script))
	m.SetLazyloadModules(map[string]starlet.ModuleLoader{ModuleName: NewModule().LoadModule()})
	return m.Run()
}

// --- the four output forms ---------------------------------------------------

func TestFourForms(t *testing.T) {
	res, err := run(t, `
load("qrcode", "encode")
qr = encode("https://example.com")
sz = qr.size
a = qr.ascii()
pa = qr.pure_ascii()
s = qr.svg(module_size=3)
b = qr.bmp(scale=2)
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if sz, _ := res["sz"].(int64); sz < 21 || (sz-21)%4 != 0 {
		t.Errorf("size = %v, want a valid QR dimension (21 + 4k)", res["sz"])
	}
	if a, _ := res["a"].(string); len(a) == 0 || !strings.Contains(a, "█") {
		t.Errorf("ascii output missing block characters")
	}
	if s, _ := res["s"].(string); !strings.HasPrefix(s, "<svg") || !strings.HasSuffix(s, "</svg>") || !strings.Contains(s, "<rect") {
		t.Errorf("svg output malformed")
	}
	// bmp bytes start with the "BM" magic.
	switch b := res["b"].(type) {
	case string:
		if !strings.HasPrefix(b, "BM") {
			t.Errorf("bmp missing BM magic")
		}
	case []byte:
		if len(b) < 2 || b[0] != 'B' || b[1] != 'M' {
			t.Errorf("bmp missing BM magic")
		}
	default:
		t.Errorf("bmp is %T, want bytes", res["b"])
	}
}

func TestPureASCIICharset(t *testing.T) {
	res, err := run(t, `
load("qrcode", "encode")
out = encode("hi", quiet_zone=1).pure_ascii(on="##", off="  ")
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	s, _ := res["out"].(string)
	for _, r := range s {
		if r != '#' && r != ' ' && r != '\n' {
			t.Errorf("pure_ascii contains non-ASCII-block rune %q", r)
			break
		}
	}
}

// --- hand-written BMP header correctness -------------------------------------

func TestBMPHeader(t *testing.T) {
	// A 2×2 grid, scale 3 ⇒ 6×6 px image.
	g := [][]bool{{true, false}, {false, true}}
	scale := 3
	data := encodeBMP(g, scale)
	dim := len(g) * scale // 6

	if data[0] != 'B' || data[1] != 'M' {
		t.Fatalf("missing BM magic")
	}
	le := binary.LittleEndian
	if got := le.Uint32(data[2:6]); int(got) != len(data) {
		t.Errorf("file size field = %d, want %d", got, len(data))
	}
	offset := le.Uint32(data[10:14])
	if offset != 14+40+8 {
		t.Errorf("pixel offset = %d, want 62", offset)
	}
	if w := int32(le.Uint32(data[18:22])); int(w) != dim {
		t.Errorf("width = %d, want %d", w, dim)
	}
	if h := int32(le.Uint32(data[22:26])); int(h) != dim {
		t.Errorf("height = %d, want %d", h, dim)
	}
	if bpp := le.Uint16(data[28:30]); bpp != 1 {
		t.Errorf("bpp = %d, want 1", bpp)
	}
	// Total size = headers + palette + padded rows.
	rowBytes := ((dim + 31) / 32) * 4
	want := 62 + rowBytes*dim
	if len(data) != want {
		t.Errorf("total bytes = %d, want %d", len(data), want)
	}
}

// --- error paths -------------------------------------------------------------

func TestErrors(t *testing.T) {
	cases := map[string]string{
		"empty content":        `load("qrcode","encode"); encode("")`,
		"bad level":            `load("qrcode","encode"); encode("x", level="Z")`,
		"unknown kwarg":        `load("qrcode","encode"); encode("x", bogus=1)`,
		"missing content arg":  `load("qrcode","encode"); encode()`,
		"bad quiet_zone type":  `load("qrcode","encode"); encode("x", quiet_zone="big")`,
		"render unknown kwarg": `load("qrcode","encode"); encode("x").bmp(bogus=1)`,
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := run(t, script); err == nil {
				t.Errorf("%s: expected error, got nil", name)
			}
		})
	}
}

// TestEncodeContentTooLong covers the third-party encoder's own error path
// surfaced as a clean "qrcode: …" error: content far beyond any QR version's
// capacity must error, not panic or silently truncate.
func TestEncodeContentTooLong(t *testing.T) {
	matrix, err := encodeMatrix(strings.Repeat("A", 100000), qr.M)
	if err == nil {
		t.Fatalf("oversized content: expected encoder error, got a %dx%d matrix", len(matrix), len(matrix))
	}
	if !strings.HasPrefix(err.Error(), "qrcode:") {
		t.Errorf("encoder error %q is not wrapped with the qrcode: prefix", err)
	}
	// Same path through Starlark returns an error rather than crashing.
	if _, rerr := run(t, `load("qrcode","encode"); s = "A" * 100000; encode(s)`); rerr == nil {
		t.Errorf("oversized content through Starlark should error")
	}
}

// --- encoder panic-recovery (defense-in-depth) -------------------------------

// TestEncodeMatrixNoPanic confirms the recover-wrapped encoder still produces a
// valid module matrix for normal input (the recover guard matches the
// yaml/toml/liquid 3rd-party-parser pattern; boombuler returns errors today, so
// the guard is defense-in-depth and must not perturb the happy path).
func TestEncodeMatrixNoPanic(t *testing.T) {
	matrix, err := encodeMatrix("https://example.com", qr.M)
	if err != nil {
		t.Fatalf("encodeMatrix: unexpected error: %v", err)
	}
	n := len(matrix)
	if n < 21 || (n-21)%4 != 0 {
		t.Fatalf("matrix dimension = %d, want a valid QR dimension (21 + 4k)", n)
	}
	for y, row := range matrix {
		if len(row) != n {
			t.Fatalf("row %d has width %d, want square matrix of %d", y, len(row), n)
		}
	}
}

// --- output-size cap (memory-amplification guard) ----------------------------

// TestOutputSizeCap covers the M5 guard: an unbounded scale/module_size/quiet_zone
// could amplify a tiny QR into a multi-hundred-MB allocation. The guard projects
// the output size and rejects before allocating; a normal render still works.
func TestOutputSizeCap(t *testing.T) {
	// A huge scale must error cleanly — no ~840 MB allocation, no panic.
	// At the default 16 MiB cap, bmp(scale=2000) projects far over the limit
	// and is rejected before encodeBMP is ever reached.
	// bmp/scale and the ⟨form⟩/quiet_zone levers are the amplifiers. (SVG output
	// scales with the module *count*, so a huge quiet_zone — not module_size —
	// is its amplification vector.)
	huge := []string{
		`load("qrcode","encode"); encode("https://example.com").bmp(scale=2000)`,
		`load("qrcode","encode"); encode("https://example.com").svg(quiet_zone=100000)`,
		`load("qrcode","encode"); encode("https://example.com").pure_ascii(quiet_zone=100000)`,
		`load("qrcode","encode"); encode("https://example.com").ascii(quiet_zone=100000)`,
	}
	for _, script := range huge {
		err := func() (e error) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("render panicked instead of erroring: %v", r)
				}
			}()
			_, e = run(t, script)
			return
		}()
		if err == nil {
			t.Errorf("expected an output-size error, got nil for: %s", script)
			continue
		}
		if !strings.Contains(err.Error(), "max_output_bytes") {
			t.Errorf("error %q does not mention the cap (max_output_bytes); script: %s", err, script)
		}
	}

	// A normal render still succeeds and returns real bytes.
	res, err := run(t, `
load("qrcode", "encode")
b = encode("https://example.com").bmp(scale=4)
n = len(b)
`)
	if err != nil {
		t.Fatalf("normal render errored: %v", err)
	}
	if n, _ := res["n"].(int64); n < 62 {
		t.Errorf("normal bmp length = %v, want a real image", res["n"])
	}
}

// TestOutputSizeCapConfigurable verifies the cap is tunable via QRCODE_MAX_OUTPUT_BYTES:
// a tiny cap rejects an otherwise-fine render, and a generous cap admits a render
// the default would have refused.
func TestOutputSizeCapConfigurable(t *testing.T) {
	t.Run("tiny cap rejects normal render", func(t *testing.T) {
		t.Setenv("QRCODE_MAX_OUTPUT_BYTES", "16")
		_, err := run(t, `load("qrcode","encode"); encode("https://example.com").bmp(scale=4)`)
		if err == nil || !strings.Contains(err.Error(), "max_output_bytes") {
			t.Errorf("tiny cap: want output-size error, got %v", err)
		}
	})
	t.Run("generous cap admits a larger render", func(t *testing.T) {
		// scale=400 on a ~33-module QR projects well past 16 MiB but under 1 GiB.
		t.Setenv("QRCODE_MAX_OUTPUT_BYTES", "1073741824") // 1 GiB
		res, err := run(t, `
load("qrcode", "encode")
b = encode("hi").bmp(scale=400)
n = len(b)
`)
		if err != nil {
			t.Fatalf("generous cap: unexpected error: %v", err)
		}
		if n, _ := res["n"].(int64); n < 62 {
			t.Errorf("generous cap: bmp length = %v, want a real image", res["n"])
		}
	})
}

// --- overflow hardening ------------------------------------------------------

// TestRenderOverflowNoPanic is the adversarial complement to the size cap: a
// near-MaxInt64 scale/module_size/quiet_zone must not wrap the projected-size
// arithmetic past the guard. Before the saturating-arithmetic fix, a scale in
// the band where the projected size saturated to MaxInt64 and then "+ 62"
// wrapped negative slipped past checkOutputSize and crashed encodeBMP with
// "makeslice: cap out of range"; a huge quiet_zone overflowed the padded
// dimension in plain int and crashed make(); and a huge module_size silently
// emitted an SVG with wrapped (corrupt) coordinates. Every one must now resolve
// to a clean error with no host panic. Each case is run under a recover so a
// regression surfaces as a test failure, not a crashed test binary.
func TestRenderOverflowNoPanic(t *testing.T) {
	const (
		maxInt = `9223372036854775807`
		// scale ~4e16: projects to a saturated size whose "+62" used to wrap
		// negative — the narrow band that bypassed the cap and panicked.
		band = `40000000000000000`
		// qz/module_size ~5e18: 2*qz used to overflow the plain-int padded dim.
		huge = `5000000000000000000`
	)
	cases := []struct {
		name, script, wantSubstr string
	}{
		{"bmp scale band", `load("qrcode","encode"); encode("hi").bmp(scale=` + band + `)`, "max_output_bytes"},
		{"bmp scale max", `load("qrcode","encode"); encode("hi").bmp(scale=` + maxInt + `)`, "max_output_bytes"},
		{"bmp quiet_zone huge", `load("qrcode","encode"); encode("hi").bmp(quiet_zone=` + huge + `)`, "max_output_bytes"},
		{"bmp quiet_zone+scale", `load("qrcode","encode"); encode("hi").bmp(quiet_zone=3037000000, scale=3037000000)`, "max_output_bytes"},
		{"svg quiet_zone huge", `load("qrcode","encode"); encode("hi").svg(quiet_zone=` + huge + `)`, "max_output_bytes"},
		{"svg module_size huge", `load("qrcode","encode"); encode("hi").svg(module_size=` + huge + `)`, "pixel dimension overflow"},
		{"svg module_size max", `load("qrcode","encode"); encode("hi").svg(module_size=` + maxInt + `)`, "pixel dimension overflow"},
		{"ascii quiet_zone huge", `load("qrcode","encode"); encode("hi").ascii(quiet_zone=` + huge + `)`, "max_output_bytes"},
		{"ascii quiet_zone max", `load("qrcode","encode"); encode("hi").ascii(quiet_zone=` + maxInt + `)`, "max_output_bytes"},
		{"pure_ascii quiet_zone huge", `load("qrcode","encode"); encode("hi").pure_ascii(quiet_zone=` + huge + `)`, "max_output_bytes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("render panicked instead of erroring: %v", r)
					}
				}()
				_, err = run(t, c.script)
			}()
			if err == nil {
				t.Fatalf("expected a clean error, got nil (silent/corrupt output)")
			}
			if !strings.Contains(err.Error(), c.wantSubstr) {
				t.Errorf("error %q does not contain %q", err, c.wantSubstr)
			}
		})
	}
}

// TestSVGLargeModuleSizeStillRenders guards the lower edge of the new overflow
// check: a large-but-valid module_size (whose pixel dimension still fits a Go
// int) must keep rendering exactly as before — the additive guard must not
// reject historically-valid sizes.
func TestSVGLargeModuleSizeStillRenders(t *testing.T) {
	res, err := run(t, `
load("qrcode", "encode")
s = encode("hi").svg(module_size=100000)
ok = s.startswith("<svg") and s.endswith("</svg>")
`)
	if err != nil {
		t.Fatalf("large-but-valid module_size errored: %v", err)
	}
	if res["ok"] != true {
		t.Errorf("svg(module_size=100000) did not render valid SVG")
	}
}

// --- pure helpers ------------------------------------------------------------

func TestResolveECLevel(t *testing.T) {
	ok := []struct {
		name string
		want qr.ErrorCorrectionLevel
	}{
		{"", qr.M}, // empty defaults to M
		{"M", qr.M},
		{"L", qr.L},
		{"Q", qr.Q},
		{"H", qr.H},
	}
	for _, c := range ok {
		got, err := resolveECLevel(c.name)
		if err != nil {
			t.Errorf("resolveECLevel(%q): unexpected error %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("resolveECLevel(%q) = %v, want %v", c.name, got, c.want)
		}
	}
	for _, bad := range []string{"Z", "m", "l", "MM", "0", " M"} {
		if _, err := resolveECLevel(bad); err == nil {
			t.Errorf("resolveECLevel(%q): expected error, got nil", bad)
		} else if !strings.Contains(err.Error(), "L, M, Q, or H") {
			t.Errorf("resolveECLevel(%q) error %q missing the allowed-set hint", bad, err)
		}
	}
}

func TestMulSat(t *testing.T) {
	cases := []struct {
		a, b, want int64
	}{
		{0, 100, 0},
		{100, 0, 0},
		{3, 4, 12},
		{math.MaxInt64, 1, math.MaxInt64},
		{math.MaxInt64, 2, math.MaxInt64}, // overflow → saturate
		{1 << 40, 1 << 40, math.MaxInt64}, // 2^80 → saturate
		{math.MaxInt32, 2, 2 * math.MaxInt32},
	}
	for _, c := range cases {
		if got := mulSat(c.a, c.b); got != c.want {
			t.Errorf("mulSat(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestAddSat(t *testing.T) {
	cases := []struct {
		a, b, want int64
	}{
		{0, 0, 0},
		{10, 32, 42},
		{math.MaxInt64, 0, math.MaxInt64},
		{math.MaxInt64, 1, math.MaxInt64},  // overflow → saturate, never wraps negative
		{math.MaxInt64, 62, math.MaxInt64}, // the bmp header constant that used to wrap
		{math.MaxInt64 - 5, 5, math.MaxInt64},
		{math.MaxInt64 - 5, 6, math.MaxInt64},
	}
	for _, c := range cases {
		got := addSat(c.a, c.b)
		if got != c.want {
			t.Errorf("addSat(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got < 0 {
			t.Errorf("addSat(%d, %d) wrapped negative: %d", c.a, c.b, got)
		}
	}
}

func TestPaddedDim(t *testing.T) {
	cases := []struct {
		n, qz int
		want  int64
	}{
		{21, 4, 29},
		{25, 0, 25},
		{0, 0, 0},
		// A huge quiet_zone must saturate, never wrap negative (the plain-int
		// `n + 2*qz` it replaced overflowed to a negative dimension).
		{21, math.MaxInt64 / 2, math.MaxInt64},
		{21, int(maxPlatformInt), math.MaxInt64},
	}
	for _, c := range cases {
		got := paddedDim(c.n, c.qz)
		if got != c.want {
			t.Errorf("paddedDim(%d, %d) = %d, want %d", c.n, c.qz, got, c.want)
		}
		if got < 0 {
			t.Errorf("paddedDim(%d, %d) wrapped negative: %d", c.n, c.qz, got)
		}
	}
}

func TestUpper(t *testing.T) {
	cases := map[string]string{
		"ec_level":         "EC_LEVEL",
		"quiet_zone":       "QUIET_ZONE",
		"max_output_bytes": "MAX_OUTPUT_BYTES",
		"":                 "",
		"AlreadyUpper":     "ALREADYUPPER",
		"mix3d_99":         "MIX3D_99", // digits/underscores unchanged
	}
	for in, want := range cases {
		if got := upper(in); got != want {
			t.Errorf("upper(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestMaxOutputFallback verifies invariant 3: a non-positive max_output_bytes
// must never silently disable the guard — maxOutput() falls back to the default.
func TestMaxOutputFallback(t *testing.T) {
	t.Run("zero falls back and still caps", func(t *testing.T) {
		t.Setenv("QRCODE_MAX_OUTPUT_BYTES", "0")
		m := NewModule()
		if got := m.maxOutput(); got != defaultMaxOutputBytes {
			t.Errorf("maxOutput() with 0 = %d, want default %d", got, defaultMaxOutputBytes)
		}
		// And the guard is still live (a huge render is still rejected).
		_, err := run(t, `load("qrcode","encode"); encode("hi").bmp(scale=4000)`)
		if err == nil || !strings.Contains(err.Error(), "max_output_bytes") {
			t.Errorf("zero cap did not fall back to the default guard, got %v", err)
		}
	})
	t.Run("negative falls back", func(t *testing.T) {
		t.Setenv("QRCODE_MAX_OUTPUT_BYTES", "-1")
		if got := NewModule().maxOutput(); got != defaultMaxOutputBytes {
			t.Errorf("maxOutput() with -1 = %d, want default %d", got, defaultMaxOutputBytes)
		}
	})
}

// TestMaxOutputBytesIsHostOnly is the regression guard for the capability-gate
// fix: max_output_bytes is the module's own memory-DoS cap, so a script must not
// be able to raise it. base emits no set_<name> builtin for a host-only option,
// so loading set_max_output_bytes must fail — otherwise a script could
// set_max_output_bytes(1<<62) and then bmp(scale=huge) to OOM the host.
func TestMaxOutputBytesIsHostOnly(t *testing.T) {
	// The setter is not exported to Starlark: base emits no set_<name> for a
	// host-only option, so a script cannot raise the cap.
	if _, err := run(t, `load("qrcode", "set_max_output_bytes")`); err == nil {
		t.Fatal("set_max_output_bytes is loadable — the cap is script-widenable")
	}
	// The value is still readable (not secret), and the cap is genuinely live and
	// unraisable: a huge render is rejected, with no setter available to lift it.
	_, err := run(t, `
load("qrcode", "encode", "get_max_output_bytes")
_ = get_max_output_bytes()
encode("hi").bmp(scale=4000)
`)
	if err == nil || !strings.Contains(err.Error(), "max_output_bytes") {
		t.Fatalf("huge render should hit the (unraisable) cap, got %v", err)
	}
}

// --- the QR Starlark value protocol ------------------------------------------

func TestQRValueProtocol(t *testing.T) {
	matrix, err := encodeMatrix("https://example.com", qr.M)
	if err != nil {
		t.Fatalf("encodeMatrix: %v", err)
	}
	q := &qrValue{matrix: matrix, quietZone: 4, maxOutput: defaultMaxOutputBytes}

	if got, want := q.Type(), "qrcode.QR"; got != want {
		t.Errorf("Type() = %q, want %q", got, want)
	}
	if s := q.String(); !strings.HasPrefix(s, "<qrcode.QR size=") || !strings.HasSuffix(s, ">") {
		t.Errorf("String() = %q, want <qrcode.QR size=N>", s)
	}
	if q.Truth() != starlark.True {
		t.Errorf("Truth() = %v, want True", q.Truth())
	}
	if _, err := q.Hash(); err == nil {
		t.Errorf("Hash() should error (QR is unhashable)")
	}
	q.Freeze() // must not panic

	names := q.AttrNames()
	want := map[string]bool{"ascii": true, "pure_ascii": true, "svg": true, "bmp": true, "size": true}
	if len(names) != len(want) {
		t.Errorf("AttrNames() = %v, want the 5 documented attrs", names)
	}
	for _, n := range names {
		if !want[n] {
			t.Errorf("AttrNames() returned undocumented attr %q", n)
		}
		v, err := q.Attr(n)
		if err != nil || v == nil {
			t.Errorf("Attr(%q) = (%v, %v), want a value", n, v, err)
		}
	}
	// size attribute is the module count.
	sz, err := q.Attr("size")
	if err != nil {
		t.Fatalf("Attr(size): %v", err)
	}
	if i, ok := sz.(starlark.Int); !ok {
		t.Errorf("size is %T, want starlark.Int", sz)
	} else if n, _ := i.Int64(); int(n) != len(matrix) {
		t.Errorf("size = %d, want %d", n, len(matrix))
	}
	// An unknown attribute returns (nil, nil) — Starlark's "no such attr" signal.
	if v, err := q.Attr("nope"); v != nil || err != nil {
		t.Errorf("Attr(unknown) = (%v, %v), want (nil, nil)", v, err)
	}
}

// --- argument validation -----------------------------------------------------

// TestArgValidation covers the clean-error branches reachable without a TTY or
// network: negative quiet_zone on encode and on each render method, and the
// non-positive module_size/scale rejections.
func TestArgValidation(t *testing.T) {
	cases := []struct {
		name, script, wantSubstr string
	}{
		{"encode negative quiet_zone", `load("qrcode","encode"); encode("x", quiet_zone=-1)`, "quiet_zone must not be negative"},
		{"ascii negative quiet_zone", `load("qrcode","encode"); encode("x").ascii(quiet_zone=-1)`, "quiet_zone must not be negative"},
		{"pure_ascii negative quiet_zone", `load("qrcode","encode"); encode("x").pure_ascii(quiet_zone=-1)`, "quiet_zone must not be negative"},
		{"svg negative quiet_zone", `load("qrcode","encode"); encode("x").svg(quiet_zone=-1)`, "quiet_zone must not be negative"},
		{"bmp negative quiet_zone", `load("qrcode","encode"); encode("x").bmp(quiet_zone=-1)`, "quiet_zone must not be negative"},
		{"svg module_size zero", `load("qrcode","encode"); encode("x").svg(module_size=0)`, "module_size must be positive"},
		{"svg module_size negative", `load("qrcode","encode"); encode("x").svg(module_size=-3)`, "module_size must be positive"},
		{"bmp scale zero", `load("qrcode","encode"); encode("x").bmp(scale=0)`, "scale must be positive"},
		{"bmp scale negative", `load("qrcode","encode"); encode("x").bmp(scale=-3)`, "scale must be positive"},
		{"encode empty content", `load("qrcode","encode"); encode("")`, "content must not be empty"},
		{"encode bad level", `load("qrcode","encode"); encode("x", level="Z")`, "L, M, Q, or H"},
		{"unknown attr", `load("qrcode","encode"); encode("x").nope`, "has no .nope"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := run(t, c.script)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), c.wantSubstr) {
				t.Errorf("error %q does not contain %q", err, c.wantSubstr)
			}
		})
	}
}
