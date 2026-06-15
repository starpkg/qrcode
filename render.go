package qrcode

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"

	"go.starlark.net/starlark"
)

// qrValue is an encoded QR returned by encode(): a module matrix plus the
// default quiet-zone width, rendered four ways. maxOutput caps the projected
// size of any single render to guard against memory-amplification.
type qrValue struct {
	matrix    [][]bool
	quietZone int
	maxOutput int
}

var (
	_ starlark.Value    = (*qrValue)(nil)
	_ starlark.HasAttrs = (*qrValue)(nil)
)

// maxPlatformInt is the largest value a Go int holds on this platform
// (math.MaxInt64 on 64-bit, math.MaxInt32 on 32-bit). A projected dimension
// above it cannot be multiplied as a plain int without wrapping.
const maxPlatformInt = int64(^uint(0) >> 1)

func (q *qrValue) String() string        { return fmt.Sprintf("<qrcode.QR size=%d>", len(q.matrix)) }
func (q *qrValue) Type() string          { return "qrcode.QR" }
func (q *qrValue) Freeze()               {}
func (q *qrValue) Truth() starlark.Bool  { return starlark.True }
func (q *qrValue) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: qrcode.QR") }

func (q *qrValue) AttrNames() []string {
	return []string{"ascii", "pure_ascii", "svg", "bmp", "size"}
}

func (q *qrValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "size":
		return starlark.MakeInt(len(q.matrix)), nil
	case "ascii":
		return starlark.NewBuiltin("qrcode.QR.ascii", q.ascii), nil
	case "pure_ascii":
		return starlark.NewBuiltin("qrcode.QR.pure_ascii", q.pureASCII), nil
	case "svg":
		return starlark.NewBuiltin("qrcode.QR.svg", q.svg), nil
	case "bmp":
		return starlark.NewBuiltin("qrcode.QR.bmp", q.bmp), nil
	}
	return nil, nil
}

// padded returns the module matrix wrapped in a quiet zone of qz light modules.
func (q *qrValue) padded(qz int) [][]bool {
	n := len(q.matrix)
	dim := n + 2*qz
	g := make([][]bool, dim)
	for y := range g {
		g[y] = make([]bool, dim)
	}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			g[y+qz][x+qz] = q.matrix[y][x]
		}
	}
	return g
}

// checkOutputSize rejects a render whose projected output size would exceed the
// configured cap, before any large buffer is allocated. projected is computed
// in int64 by the caller via mulSat (saturating) so a huge scale/module_size
// can neither overflow int nor wrap negative. This turns a memory-amplification
// DoS (e.g. bmp(scale=2000) ≈ 840 MB) into a clean error instead of an OOM.
func (q *qrValue) checkOutputSize(b *starlark.Builtin, projected int64) error {
	limit := int64(q.maxOutput)
	if limit <= 0 {
		limit = defaultMaxOutputBytes
	}
	if projected > limit {
		return fmt.Errorf("%s: projected output of %d bytes exceeds the %d-byte limit (max_output_bytes); reduce scale/module_size/quiet_zone", b.Name(), projected, limit)
	}
	return nil
}

// mulSat multiplies two non-negative int64s, saturating at math.MaxInt64 on
// overflow so projected-size arithmetic stays monotonic (a larger input never
// wraps to a smaller, guard-bypassing value).
func mulSat(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	p := a * b
	if p/b != a || p < 0 {
		return math.MaxInt64
	}
	return p
}

// addSat adds two non-negative int64s, saturating at math.MaxInt64 on overflow.
// Projected-size formulas add a small header/newline constant to a (possibly
// already saturated) mulSat result; without saturation that final "+ N" can
// wrap a MaxInt64 product to a negative value that slips past checkOutputSize.
func addSat(a, b int64) int64 {
	s := a + b
	if s < a || s < 0 {
		return math.MaxInt64
	}
	return s
}

// paddedDim returns the padded matrix dimension (n + 2*qz) computed in
// saturating int64 arithmetic. The matrix size and quiet zone are both
// script-controlled; doing this in plain Go int (as `int64(n + 2*qz)`) lets a
// huge quiet_zone overflow to a negative dimension that escapes the size guard
// and crashes a later make(). Saturating keeps the projected size monotonic so
// an over-large quiet_zone trips checkOutputSize cleanly instead.
func paddedDim(n, qz int) int64 {
	return addSat(int64(n), mulSat(2, int64(qz)))
}

func (q *qrValue) quietArg(b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple, extra ...interface{}) (int, error) {
	qz := q.quietZone
	pairs := append([]interface{}{"quiet_zone?", &qz}, extra...)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, pairs...); err != nil {
		return 0, err
	}
	if qz < 0 {
		return 0, fmt.Errorf("%s: quiet_zone must not be negative", b.Name())
	}
	return qz, nil
}

// ascii renders with half-block characters (two module rows per text line).
//
//	QR.ascii(quiet_zone=<default>) -> str
func (q *qrValue) ascii(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	qz, err := q.quietArg(b, args, kwargs)
	if err != nil {
		return none, err
	}
	dim := paddedDim(len(q.matrix), qz)
	// Half-block: ⌈dim/2⌉ lines, each dim 3-byte block runes plus a newline.
	lines := (dim + 1) / 2
	if err := q.checkOutputSize(b, mulSat(lines, addSat(mulSat(dim, 3), 1))); err != nil {
		return none, err
	}
	g := q.padded(qz)
	var sb strings.Builder
	for y := 0; y < len(g); y += 2 {
		for x := 0; x < len(g); x++ {
			top := g[y][x]
			bottom := false
			if y+1 < len(g) {
				bottom = g[y+1][x]
			}
			switch {
			case top && bottom:
				sb.WriteRune('█')
			case top:
				sb.WriteRune('▀')
			case bottom:
				sb.WriteRune('▄')
			default:
				sb.WriteByte(' ')
			}
		}
		sb.WriteByte('\n')
	}
	return starlark.String(sb.String()), nil
}

// pureASCII renders with two ASCII characters per module.
//
//	QR.pure_ascii(quiet_zone=<default>, on="##", off="  ") -> str
func (q *qrValue) pureASCII(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	on := "##"
	off := "  "
	qz, err := q.quietArg(b, args, kwargs, "on?", &on, "off?", &off)
	if err != nil {
		return none, err
	}
	dim := paddedDim(len(q.matrix), qz)
	cell := int64(len(on))
	if len(off) > len(on) {
		cell = int64(len(off))
	}
	// dim lines, each dim cells of at most cell bytes plus a newline.
	if err := q.checkOutputSize(b, mulSat(dim, addSat(mulSat(dim, cell), 1))); err != nil {
		return none, err
	}
	g := q.padded(qz)
	var sb strings.Builder
	for y := 0; y < len(g); y++ {
		for x := 0; x < len(g); x++ {
			if g[y][x] {
				sb.WriteString(on)
			} else {
				sb.WriteString(off)
			}
		}
		sb.WriteByte('\n')
	}
	return starlark.String(sb.String()), nil
}

// svg renders a scalable vector image.
//
//	QR.svg(quiet_zone=<default>, module_size=4) -> str
func (q *qrValue) svg(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	moduleSize := 4
	qz, err := q.quietArg(b, args, kwargs, "module_size?", &moduleSize)
	if err != nil {
		return none, err
	}
	if moduleSize <= 0 {
		return none, fmt.Errorf("%s: module_size must be positive", b.Name())
	}
	gm := paddedDim(len(q.matrix), qz)
	pixelDim := mulSat(gm, int64(moduleSize))
	// Worst case: every module black ⇒ one <rect …/> each. The literal markup
	// is ~52 bytes plus four coordinate fields whose width grows with pixelDim.
	perRect := addSat(52, mulSat(4, int64(len(fmt.Sprintf("%d", pixelDim)))))
	if err := q.checkOutputSize(b, addSat(mulSat(mulSat(gm, gm), perRect), 128)); err != nil {
		return none, err
	}
	// SVG output is bounded by module *count*, so a huge module_size can pass
	// the byte cap above yet overflow the int pixel coordinates below
	// (dim = len(g)*moduleSize and x*moduleSize), silently emitting a corrupt
	// image. If that product does not fit a platform int — i.e. mulSat had to
	// saturate, or the value exceeds the platform int max — reject up front
	// (invariant 4) rather than emit a broken QR. (gm is small once the byte cap
	// passes, so this only fires for an absurd module_size.)
	if pixelDim == math.MaxInt64 || pixelDim > maxPlatformInt {
		return none, fmt.Errorf("%s: module_size %d too large for quiet_zone %d (pixel dimension overflow)", b.Name(), moduleSize, qz)
	}
	g := q.padded(qz)
	dim := len(g) * moduleSize
	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, dim, dim, dim, dim)
	fmt.Fprintf(&sb, `<rect width="%d" height="%d" fill="#ffffff"/>`, dim, dim)
	for y := 0; y < len(g); y++ {
		for x := 0; x < len(g); x++ {
			if g[y][x] {
				fmt.Fprintf(&sb, `<rect x="%d" y="%d" width="%d" height="%d" fill="#000000"/>`,
					x*moduleSize, y*moduleSize, moduleSize, moduleSize)
			}
		}
	}
	sb.WriteString("</svg>")
	return starlark.String(sb.String()), nil
}

// bmp renders a hand-written 1-bit BMP (no image/* dependency).
//
//	QR.bmp(quiet_zone=<default>, scale=4) -> bytes
func (q *qrValue) bmp(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	scale := 4
	qz, err := q.quietArg(b, args, kwargs, "scale?", &scale)
	if err != nil {
		return none, err
	}
	if scale <= 0 {
		return none, fmt.Errorf("%s: scale must be positive", b.Name())
	}
	// Worst case: rowBytes*pixelDim raster plus 62 header bytes. This is the
	// largest of the four forms, so the guard matters most here.
	pixelDim := mulSat(paddedDim(len(q.matrix), qz), int64(scale))
	projected := int64(math.MaxInt64)
	// (pixelDim+31) and the trailing "+ 62" are done in saturating arithmetic so
	// a near-MaxInt64 pixelDim cannot wrap the projected size negative and slip
	// past the cap into encodeBMP's make([]byte, …) (which would then panic).
	if pixelDim < math.MaxInt64 {
		rowBytes := (addSat(pixelDim, 31) / 32) * 4 // 1bpp rows padded to 4 bytes
		projected = addSat(mulSat(rowBytes, pixelDim), 62)
	}
	if err := q.checkOutputSize(b, projected); err != nil {
		return none, err
	}
	return starlark.Bytes(encodeBMP(q.padded(qz), scale)), nil
}

// encodeBMP emits a 1-bit (monochrome) BMP for the padded grid, each module
// scaled to scale×scale pixels. Bit 1 ⇒ palette index 1 (black). Rows are
// bottom-up and padded to a 4-byte boundary, per the BMP format.
func encodeBMP(g [][]bool, scale int) []byte {
	gm := len(g)
	dim := gm * scale
	rowBytes := ((dim + 31) / 32) * 4 // 1bpp rows padded to 4 bytes
	pixelSize := rowBytes * dim
	const fileHdr, infoHdr, palette = 14, 40, 8
	offset := fileHdr + infoHdr + palette
	fileSize := offset + pixelSize

	buf := make([]byte, 0, fileSize)
	le := binary.LittleEndian
	u16 := func(v uint16) { tmp := make([]byte, 2); le.PutUint16(tmp, v); buf = append(buf, tmp...) }
	u32 := func(v uint32) { tmp := make([]byte, 4); le.PutUint32(tmp, v); buf = append(buf, tmp...) }
	i32 := func(v int32) { u32(uint32(v)) }

	// BITMAPFILEHEADER
	buf = append(buf, 'B', 'M')
	u32(uint32(fileSize))
	u16(0)
	u16(0)
	u32(uint32(offset))
	// BITMAPINFOHEADER
	u32(infoHdr)
	i32(int32(dim)) // width
	i32(int32(dim)) // height (positive ⇒ bottom-up)
	u16(1)          // planes
	u16(1)          // bits per pixel
	u32(0)          // compression: BI_RGB
	u32(uint32(pixelSize))
	i32(2835) // x pixels/meter (~72 DPI)
	i32(2835) // y pixels/meter
	u32(2)    // colors used
	u32(2)    // important colors
	// Palette (B, G, R, reserved): index 0 white, index 1 black
	buf = append(buf, 0xFF, 0xFF, 0xFF, 0x00)
	buf = append(buf, 0x00, 0x00, 0x00, 0x00)
	// Pixel data, bottom-up.
	for fileRow := 0; fileRow < dim; fileRow++ {
		imgY := dim - 1 - fileRow
		row := make([]byte, rowBytes)
		gy := imgY / scale
		for px := 0; px < dim; px++ {
			if g[gy][px/scale] {
				row[px/8] |= 1 << (7 - uint(px%8))
			}
		}
		buf = append(buf, row...)
	}
	return buf
}
