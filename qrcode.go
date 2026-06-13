// Package qrcode provides a Starlark module for generating QR codes in four
// output forms — half-block ASCII, pure ASCII, SVG, and a hand-written 1-bit
// BMP — with no image/* dependencies (the BMP is emitted with encoding/binary).
//
// encode() runs the QR encoder once (boombuler/barcode) and returns a QR value
// whose methods render the shared module matrix the four ways.
package qrcode

import (
	"fmt"

	"github.com/1set/starlet"
	"github.com/1set/starlet/dataconv/types"
	"github.com/boombuler/barcode/qr"
	"github.com/starpkg/base"
	"go.starlark.net/starlark"
)

// ModuleName is the name used in Starlark's load() for this module.
const ModuleName = "qrcode"

const (
	configKeyECLevel        = "ec_level"
	configKeyQuietZone      = "quiet_zone"
	configKeyMaxOutputBytes = "max_output_bytes"
)

const (
	defaultECLevel   = "M"
	defaultQuietZone = 4
	// defaultMaxOutputBytes caps the projected size of any single rendered
	// output, guarding against memory-amplification: an unbounded scale or
	// module_size can turn a tiny QR into a multi-hundred-MB allocation.
	defaultMaxOutputBytes = 16 << 20 // 16 MiB
)

var none = starlark.None

// Module wraps a ConfigurableModule with QR functions.
type Module struct {
	cfgMod *base.ConfigurableModule
	ext    *base.ConfigurableModuleExt
}

// NewModule creates a new Module with default configuration.
func NewModule() *Module {
	cm, _ := base.NewConfigurableModuleWithConfigOptions(
		genConfigOption(configKeyECLevel, "Default error-correction level (L, M, Q, H)", defaultECLevel),
		genConfigOption(configKeyQuietZone, "Default quiet-zone width in modules", defaultQuietZone),
		genConfigOption(configKeyMaxOutputBytes, "Maximum projected size of a single rendered output in bytes", defaultMaxOutputBytes),
	)
	return &Module{cfgMod: cm, ext: cm.Extend()}
}

func genConfigOption[T any](name, description string, defaultValue T) *base.ConfigOption[T] {
	return base.NewConfigOption(defaultValue).
		WithName(name).
		WithDescription(description).
		WithEnvVar("QRCODE_" + upper(name))
}

// LoadModule returns the Starlark module loader.
func (m *Module) LoadModule() starlet.ModuleLoader {
	funcs := starlark.StringDict{
		"encode": starlark.NewBuiltin(ModuleName+".encode", m.encode),
	}
	return m.cfgMod.LoadModule(ModuleName, funcs)
}

func resolveECLevel(name string) (qr.ErrorCorrectionLevel, error) {
	switch name {
	case "", "M":
		return qr.M, nil
	case "L":
		return qr.L, nil
	case "Q":
		return qr.Q, nil
	case "H":
		return qr.H, nil
	default:
		return 0, fmt.Errorf("ec_level must be L, M, Q, or H, got %q", name)
	}
}

// encode(content, level=<cfg>, quiet_zone=<cfg>) -> QR
func (m *Module) encode(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		content   types.StringOrBytes
		level     = m.ext.GetString(configKeyECLevel)
		quietZone = m.ext.GetInt(configKeyQuietZone)
	)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "content", &content, "level?", &level, "quiet_zone?", &quietZone); err != nil {
		return none, err
	}
	if content.GoString() == "" {
		return none, fmt.Errorf("%s: content must not be empty", b.Name())
	}
	if quietZone < 0 {
		return none, fmt.Errorf("%s: quiet_zone must not be negative", b.Name())
	}
	ecl, err := resolveECLevel(level)
	if err != nil {
		return none, fmt.Errorf("%s: %w", b.Name(), err)
	}

	bc, err := qr.Encode(content.GoString(), ecl, qr.Unicode)
	if err != nil {
		return none, fmt.Errorf("qrcode: %w", err)
	}
	bounds := bc.Bounds()
	n := bounds.Dx()
	matrix := make([][]bool, n)
	for y := 0; y < n; y++ {
		row := make([]bool, n)
		for x := 0; x < n; x++ {
			r, _, _, _ := bc.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			row[x] = r == 0 // black module
		}
		matrix[y] = row
	}
	return &qrValue{matrix: matrix, quietZone: quietZone, maxOutput: m.maxOutput()}, nil
}

// maxOutput returns the configured per-render output cap, falling back to the
// default when unset or non-positive (a non-positive value would disable the
// guard, which the cap exists to prevent).
func (m *Module) maxOutput() int {
	if v := m.ext.GetInt(configKeyMaxOutputBytes); v > 0 {
		return v
	}
	return defaultMaxOutputBytes
}

func upper(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
