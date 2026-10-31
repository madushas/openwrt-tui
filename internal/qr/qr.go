// Package qr is a small QR code encoder (byte mode, error correction L or M,
// versions 1 to 25), enough to show a WireGuard client configuration as a code
// a phone can scan from the terminal. It has no dependencies.
package qr

import (
	"errors"
	"strings"
)

// Level is the error correction level.
type Level int

const (
	L Level = iota // about 7% recoverable
	M              // about 15% recoverable
)

// Code is an encoded symbol; true is a dark module.
type Code struct {
	Modules [][]bool
	Version int
}

var errTooLong = errors.New("text is too long for a QR code")

// ---- Galois field ----

var gfExp [512]int
var gfLog [256]int

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = x
		gfLog[x] = i
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11d
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMul(a, b int) int {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[gfLog[a]+gfLog[b]]
}

func rsGenerator(n int) []int {
	g := []int{1}
	for i := 0; i < n; i++ {
		next := make([]int, len(g)+1)
		for j, c := range g {
			next[j] ^= c
			next[j+1] ^= gfMul(c, gfExp[i])
		}
		g = next
	}
	return g
}

// rsRemainder returns the n error correction bytes for data.
func rsRemainder(data []int, n int) []int {
	g := rsGenerator(n)
	rem := make([]int, n)
	for _, d := range data {
		f := d ^ rem[0]
		copy(rem, rem[1:])
		rem[n-1] = 0
		for i := 0; i < n; i++ {
			rem[i] ^= gfMul(g[i+1], f)
		}
	}
	return rem
}

// ---- encoding ----

func table(l Level) [][]blockGroup {
	if l == M {
		return tblM
	}
	return tblL
}

func dataCapacity(v int, l Level) int {
	n := 0
	for _, g := range table(l)[v] {
		n += g.count * g.data
	}
	return n
}

func ccBits(v int) int {
	if v < 10 {
		return 8
	}
	return 16
}

type bitBuf struct{ bits []bool }

func (b *bitBuf) put(val, n int) {
	for i := n - 1; i >= 0; i-- {
		b.bits = append(b.bits, (val>>i)&1 == 1)
	}
}

func buildCodewords(data []byte, v int, l Level) []int {
	var b bitBuf
	b.put(4, 4)
	b.put(len(data), ccBits(v))
	for _, c := range data {
		b.put(int(c), 8)
	}
	capBits := dataCapacity(v, l) * 8
	for i := 0; i < 4 && len(b.bits) < capBits; i++ {
		b.bits = append(b.bits, false)
	}
	for len(b.bits)%8 != 0 {
		b.bits = append(b.bits, false)
	}
	cw := make([]int, 0, dataCapacity(v, l))
	for i := 0; i < len(b.bits); i += 8 {
		x := 0
		for j := 0; j < 8; j++ {
			x <<= 1
			if b.bits[i+j] {
				x |= 1
			}
		}
		cw = append(cw, x)
	}
	for pad := 0; len(cw) < dataCapacity(v, l); pad++ {
		if pad%2 == 0 {
			cw = append(cw, 0xEC)
		} else {
			cw = append(cw, 0x11)
		}
	}
	// split into blocks, add error correction, interleave
	var blocks, ecs [][]int
	pos := 0
	for _, g := range table(l)[v] {
		for i := 0; i < g.count; i++ {
			d := cw[pos : pos+g.data]
			pos += g.data
			blocks = append(blocks, d)
			ecs = append(ecs, rsRemainder(d, g.total-g.data))
		}
	}
	var out []int
	maxData := 0
	for _, d := range blocks {
		if len(d) > maxData {
			maxData = len(d)
		}
	}
	for i := 0; i < maxData; i++ {
		for _, d := range blocks {
			if i < len(d) {
				out = append(out, d[i])
			}
		}
	}
	for i := 0; i < len(ecs[0]); i++ {
		for _, e := range ecs {
			out = append(out, e[i])
		}
	}
	return out
}

// ---- matrix ----

type matrix struct {
	n    int
	mod  [][]int8 // -1 unset, 0 light, 1 dark
	fixd [][]bool
}

func newMatrix(v int) *matrix {
	n := v*4 + 17
	m := &matrix{n: n, mod: make([][]int8, n), fixd: make([][]bool, n)}
	for i := range m.mod {
		m.mod[i] = make([]int8, n)
		m.fixd[i] = make([]bool, n)
		for j := range m.mod[i] {
			m.mod[i][j] = -1
		}
	}
	return m
}

func (m *matrix) set(r, c int, dark bool) {
	v := int8(0)
	if dark {
		v = 1
	}
	m.mod[r][c] = v
}

func (m *matrix) finder(r0, c0 int) {
	for r := -1; r <= 7; r++ {
		for c := -1; c <= 7; c++ {
			rr, cc := r0+r, c0+c
			if rr < 0 || cc < 0 || rr >= m.n || cc >= m.n {
				continue
			}
			dark := (0 <= r && r <= 6 && (c == 0 || c == 6)) || (0 <= c && c <= 6 && (r == 0 || r == 6)) || (2 <= r && r <= 4 && 2 <= c && c <= 4)
			m.set(rr, cc, dark)
		}
	}
}

func (m *matrix) functionPatterns(v int) {
	m.finder(0, 0)
	m.finder(m.n-7, 0)
	m.finder(0, m.n-7)
	pos := alignPos[v]
	for _, r := range pos {
		for _, c := range pos {
			if m.mod[r][c] != -1 {
				continue
			}
			for i := -2; i <= 2; i++ {
				for j := -2; j <= 2; j++ {
					m.set(r+i, c+j, i == -2 || i == 2 || j == -2 || j == 2 || (i == 0 && j == 0))
				}
			}
		}
	}
	for i := 8; i < m.n-8; i++ {
		if m.mod[6][i] == -1 {
			m.set(6, i, i%2 == 0)
		}
		if m.mod[i][6] == -1 {
			m.set(i, 6, i%2 == 0)
		}
	}
}

func bch(data, poly, shift int) int {
	hi := func(x int) int {
		n := 0
		for x != 0 {
			n++
			x >>= 1
		}
		return n
	}
	d := data << shift
	for hi(d)-hi(poly) >= 0 {
		d ^= poly << (hi(d) - hi(poly))
	}
	return d
}

func (m *matrix) typeInfo(l Level, mask int) {
	lv := 1 // L=01
	if l == M {
		lv = 0 // M=00
	}
	data := lv<<3 | mask
	bits := (data<<10 | bch(data, 0x537, 10)) ^ 0x5412
	for i := 0; i < 15; i++ {
		d := (bits>>i)&1 == 1
		switch {
		case i < 6:
			m.set(i, 8, d)
		case i < 8:
			m.set(i+1, 8, d)
		default:
			m.set(m.n-15+i, 8, d)
		}
		switch {
		case i < 8:
			m.set(8, m.n-i-1, d)
		case i < 9:
			m.set(8, 15-i-1+1, d)
		default:
			m.set(8, 15-i-1, d)
		}
	}
	m.set(m.n-8, 8, true)
}

func (m *matrix) versionInfo(v int) {
	if v < 7 {
		return
	}
	bits := v<<12 | bch(v, 0x1F25, 12)
	for i := 0; i < 18; i++ {
		d := (bits>>i)&1 == 1
		m.set(i/3, i%3+m.n-8-3, d)
		m.set(i%3+m.n-8-3, i/3, d)
	}
}

func maskFn(mask int) func(r, c int) bool {
	return [...]func(r, c int) bool{
		func(r, c int) bool { return (r+c)%2 == 0 },
		func(r, c int) bool { return r%2 == 0 },
		func(r, c int) bool { return c%3 == 0 },
		func(r, c int) bool { return (r+c)%3 == 0 },
		func(r, c int) bool { return (r/2+c/3)%2 == 0 },
		func(r, c int) bool { return (r*c)%2+(r*c)%3 == 0 },
		func(r, c int) bool { return ((r*c)%2+(r*c)%3)%2 == 0 },
		func(r, c int) bool { return ((r*c)%3+(r+c)%2)%2 == 0 },
	}[mask]
}

func (m *matrix) placeData(cw []int, mask int) {
	mf := maskFn(mask)
	inc, row, bit, idx := -1, m.n-1, 7, 0
	for start := m.n - 1; start > 0; start -= 2 {
		col := start
		if col <= 6 {
			col--
		}
		for {
			for _, c := range [2]int{col, col - 1} {
				if m.mod[row][c] != -1 {
					continue
				}
				dark := false
				if idx < len(cw) {
					dark = (cw[idx]>>bit)&1 == 1
				}
				if mf(row, c) {
					dark = !dark
				}
				m.set(row, c, dark)
				bit--
				if bit == -1 {
					idx++
					bit = 7
				}
			}
			row += inc
			if row < 0 || row >= m.n {
				row -= inc
				inc = -inc
				break
			}
		}
	}
}

func (m *matrix) clone() *matrix {
	c := newMatrix((m.n - 17) / 4)
	for i := range m.mod {
		copy(c.mod[i], m.mod[i])
	}
	return c
}

func (m *matrix) penalty() int {
	n, p := m.n, 0
	dark := func(r, c int) bool { return m.mod[r][c] == 1 }
	// runs of five or more in rows and columns
	for i := 0; i < n; i++ {
		for _, rowWise := range [2]bool{true, false} {
			run := 1
			for j := 1; j < n; j++ {
				a, b := dark(i, j), dark(i, j-1)
				if !rowWise {
					a, b = dark(j, i), dark(j-1, i)
				}
				if a == b {
					run++
					continue
				}
				if run >= 5 {
					p += run - 2
				}
				run = 1
			}
			if run >= 5 {
				p += run - 2
			}
		}
	}
	// 2x2 blocks
	for r := 0; r < n-1; r++ {
		for c := 0; c < n-1; c++ {
			d := dark(r, c)
			if dark(r, c+1) == d && dark(r+1, c) == d && dark(r+1, c+1) == d {
				p += 3
			}
		}
	}
	// finder-like patterns
	pat1 := []bool{true, false, true, true, true, false, true, false, false, false, false}
	pat2 := []bool{false, false, false, false, true, false, true, true, true, false, true}
	match := func(get func(k int) bool, pat []bool) bool {
		for k, v := range pat {
			if get(k) != v {
				return false
			}
		}
		return true
	}
	for i := 0; i < n; i++ {
		for j := 0; j+11 <= n; j++ {
			rg := func(k int) bool { return dark(i, j+k) }
			cg := func(k int) bool { return dark(j+k, i) }
			for _, g := range []func(int) bool{rg, cg} {
				if match(g, pat1) || match(g, pat2) {
					p += 40
				}
			}
		}
	}
	// dark proportion
	d := 0
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			if dark(r, c) {
				d++
			}
		}
	}
	diff := d*100/(n*n) - 50
	if diff < 0 {
		diff = -diff
	}
	return p + (diff/5)*10
}

// Encode builds the smallest symbol that holds data at the given level.
func Encode(data []byte, l Level) (*Code, error) {
	return encode(data, l, -1)
}

func encode(data []byte, l Level, forceMask int) (*Code, error) {
	v := 0
	for c := 1; c < len(alignPos); c++ {
		if 4+ccBits(c)+8*len(data) <= 8*dataCapacity(c, l) {
			v = c
			break
		}
	}
	if v == 0 {
		return nil, errTooLong
	}
	cw := buildCodewords(data, v, l)
	base := newMatrix(v)
	base.functionPatterns(v)
	build := func(mask int) *matrix {
		m := base.clone()
		m.typeInfo(l, mask)
		m.versionInfo(v)
		m.placeData(cw, mask)
		return m
	}
	var best *matrix
	if forceMask >= 0 {
		best = build(forceMask)
	} else {
		bp := 1 << 30
		for mask := 0; mask < 8; mask++ {
			m := build(mask)
			if p := m.penalty(); p < bp {
				bp, best = p, m
			}
		}
	}
	c := &Code{Version: v, Modules: make([][]bool, best.n)}
	for r := range c.Modules {
		c.Modules[r] = make([]bool, best.n)
		for k := range c.Modules[r] {
			c.Modules[r][k] = best.mod[r][k] == 1
		}
	}
	return c, nil
}

// Text draws the code with half-block characters and explicit black and white
// colours, so it scans the same on dark and light terminal themes. quiet is
// the border width in modules (4 is the standard).
func (c *Code) Text(quiet int) string {
	n := len(c.Modules)
	at := func(r, k int) bool {
		r, k = r-quiet, k-quiet
		return r >= 0 && k >= 0 && r < n && k < n && c.Modules[r][k]
	}
	const black, white = "0", "255"
	var b strings.Builder
	for r := -quiet % 2; r < n+2*quiet; r += 2 {
		for k := 0; k < n+2*quiet; k++ {
			top, bot := at(r, k), at(r+1, k)
			col := func(d bool) string {
				if d {
					return black
				}
				return white
			}
			b.WriteString("\x1b[38;5;" + col(top) + ";48;5;" + col(bot) + "m▀")
		}
		b.WriteString("\x1b[0m\n")
	}
	return b.String()
}

// TextWide draws one row of terminal text per module row, two spaces per
// module, using only ANSI background colours. It needs no block glyphs, so it
// works on the Linux console and PuTTY with any character set.
func (c *Code) TextWide(quiet int) string {
	n := len(c.Modules)
	var b strings.Builder
	for r := 0; r < n+2*quiet; r++ {
		for k := 0; k < n+2*quiet; k++ {
			rr, kk := r-quiet, k-quiet
			dark := rr >= 0 && kk >= 0 && rr < n && kk < n && c.Modules[rr][kk]
			if dark {
				b.WriteString("\x1b[48;5;0m  ")
			} else {
				b.WriteString("\x1b[48;5;255m  ")
			}
		}
		b.WriteString("\x1b[0m\n")
	}
	return b.String()
}
