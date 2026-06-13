package qrcode

// Tests for the qrcode module.
//
// Sections:
//   - the four output forms
//   - hand-written BMP header correctness
//   - error paths
//   - output-size cap (memory-amplification guard)

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/1set/starlet"
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
		"empty content": `load("qrcode","encode"); encode("")`,
		"bad level":     `load("qrcode","encode"); encode("x", level="Z")`,
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := run(t, script); err == nil {
				t.Errorf("%s: expected error, got nil", name)
			}
		})
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
