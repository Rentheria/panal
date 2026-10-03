package pet

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/AlbertoVasquezR/panal/internal/mascots"
)

// DefaultColor is the raster's "terminal default" color: a transparent
// pixel's background, or an empty cell's foreground and background.
const DefaultColor uint32 = 0x01000000

// Cell is one decoded raster cell.
type Cell struct {
	Char   rune
	FG, BG uint32 // 0x00RRGGBB, or DefaultColor
}

// EncodeRaster packs the cells as the contract says: columns*rows triplets
// of little-endian uint32 [code point, fg, bg], row-major, in standard
// padded base64.
func EncodeRaster(cells [][]mascots.Cell) Raster {
	r := Raster{Rows: len(cells)}
	if r.Rows > 0 {
		r.Columns = len(cells[0])
	}
	buf := make([]byte, 0, r.Columns*r.Rows*12)
	for _, line := range cells {
		for _, c := range line {
			ch, fg, bg := c.Char, rgb(c.FG), rgb(c.BG)
			if ch == ' ' {
				fg, bg = DefaultColor, DefaultColor
			}
			buf = binary.LittleEndian.AppendUint32(buf, uint32(ch))
			buf = binary.LittleEndian.AppendUint32(buf, fg)
			buf = binary.LittleEndian.AppendUint32(buf, bg)
		}
	}
	r.Cells = base64.StdEncoding.EncodeToString(buf)
	return r
}

// DecodeRaster is EncodeRaster backwards: rows of cells. It checks the size
// and that every code point is a printable width-1 BMP character.
func DecodeRaster(r Raster) ([][]Cell, error) {
	buf, err := base64.StdEncoding.DecodeString(r.Cells)
	if err != nil {
		return nil, err
	}
	if r.Columns < 0 || r.Rows < 0 || len(buf) != r.Columns*r.Rows*12 {
		return nil, fmt.Errorf("raster: %d bytes for %d×%d cells", len(buf), r.Columns, r.Rows)
	}
	out := make([][]Cell, r.Rows)
	for y := range out {
		out[y] = make([]Cell, r.Columns)
		for x := range out[y] {
			i := (y*r.Columns + x) * 12
			c := Cell{
				Char: rune(binary.LittleEndian.Uint32(buf[i:])),
				FG:   binary.LittleEndian.Uint32(buf[i+4:]),
				BG:   binary.LittleEndian.Uint32(buf[i+8:]),
			}
			if c.Char < 0x20 || c.Char > 0xFFFF || lipgloss.Width(string(c.Char)) != 1 {
				return nil, fmt.Errorf("raster: cell %d,%d has code point U+%04X", y, x, c.Char)
			}
			out[y][x] = c
		}
	}
	return out, nil
}

// rgb turns "#rrggbb" into 0x00RRGGBB; "" (terminal default) into DefaultColor.
func rgb(hex string) uint32 {
	if len(hex) != 7 || hex[0] != '#' {
		return DefaultColor
	}
	v, err := strconv.ParseUint(hex[1:], 16, 32)
	if err != nil {
		return DefaultColor
	}
	return uint32(v)
}

// Hex resolves a palette color to "#RRGGBB" for the active theme: the light
// or dark variant of an adaptive color, and xterm's value for a 256-color
// index.
func Hex(c lipgloss.TerminalColor) string {
	var s string
	switch v := c.(type) {
	case lipgloss.Color:
		s = string(v)
	case lipgloss.AdaptiveColor:
		s = v.Light
		if lipgloss.HasDarkBackground() {
			s = v.Dark
		}
	default:
		return "#808080"
	}
	if strings.HasPrefix(s, "#") && len(s) == 7 {
		return strings.ToUpper(s)
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 255 {
		return "#808080"
	}
	return xterm(n)
}

// xterm: the hex color of a 256-color index, as xterm defines it.
func xterm(n int) string {
	basic := [16]uint32{
		0x000000, 0x800000, 0x008000, 0x808000, 0x000080, 0x800080, 0x008080, 0xC0C0C0,
		0x808080, 0xFF0000, 0x00FF00, 0xFFFF00, 0x0000FF, 0xFF00FF, 0x00FFFF, 0xFFFFFF,
	}
	var v uint32
	switch {
	case n < 16:
		v = basic[n]
	case n < 232:
		level := [6]uint32{0, 95, 135, 175, 215, 255}
		i := n - 16
		v = level[i/36]<<16 | level[i/6%6]<<8 | level[i%6]
	default:
		g := uint32(8 + 10*(n-232))
		v = g<<16 | g<<8 | g
	}
	return fmt.Sprintf("#%06X", v)
}
