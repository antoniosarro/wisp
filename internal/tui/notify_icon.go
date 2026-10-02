package tui

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/antoniosarro/wisp/assets"
)

// A notification icon is small, so it is the logo's mark (the ghost and its
// terminal) without the word under it, on a dark rounded tile: plain, the
// black mark vanishes on a dark panel and a light one on a light panel.
const (
	notifyIconSize   = 256
	notifyIconRadius = 56
	notifyIconMargin = 28
)

// notifyIconBackground is the tile's color.
var notifyIconBackground = color.NRGBA{22, 22, 30, 255}

// notifyIconPath writes the icon to the cache directory, once per run and
// only when it differs, and returns its path; "" when it can't, and the
// notification goes without one.
var notifyIconPath = sync.OnceValue(func() string {
	icon, err := notifyIconPNG()
	if err != nil {
		return ""
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(dir, "wisp", "notify-icon.png")
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, icon) {
		return path
	}
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil || os.WriteFile(path, icon, 0o644) != nil {
		return ""
	}
	return path
})

// notifyIconPNG draws the icon from the embedded logo.
func notifyIconPNG() ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(assets.Logo))
	if err != nil {
		return nil, err
	}
	mark := logoMark(src)
	tile := image.NewNRGBA(image.Rect(0, 0, notifyIconSize, notifyIconSize))
	roundedRect(tile, notifyIconBackground, notifyIconRadius)

	// The mark, scaled to fit inside the margin, centered, its dark
	// pixels in the TUI logo's gradient.
	inner := notifyIconSize - 2*notifyIconMargin
	scale := float64(inner) / float64(max(mark.Dx(), mark.Dy()))
	w, h := int(float64(mark.Dx())*scale), int(float64(mark.Dy())*scale)
	small := downscale(src, mark, w, h)
	for y := range h {
		for x := range w {
			c := small.NRGBAAt(x, y)
			if max(c.R, c.G, c.B)-min(c.R, c.G, c.B) < 48 {
				g := lerpColor(logoGradientFrom, logoGradientTo, float64(x)/float64(max(w-1, 1)))
				c.R, c.G, c.B = uint8(g[0]), uint8(g[1]), uint8(g[2])
				small.SetNRGBA(x, y, c)
			}
		}
	}
	at := image.Pt((notifyIconSize-w)/2, (notifyIconSize-h)/2)
	draw.Draw(tile, image.Rectangle{Min: at, Max: at.Add(image.Pt(w, h))}, small, image.Point{}, draw.Over)

	var buf bytes.Buffer
	err = png.Encode(&buf, tile)
	return buf.Bytes(), err
}

// logoMark is the bounds of the logo's mark: its opaque pixels from the
// top down to the first fully transparent row, which separates it from the
// word below.
func logoMark(src image.Image) image.Rectangle {
	opaque := func(x, y int) bool {
		_, _, _, a := src.At(x, y).RGBA()
		return a > 0x8000 // skip faint alpha noise
	}
	b := src.Bounds()
	var mark image.Rectangle
	for y := b.Min.Y; y < b.Max.Y; y++ {
		var row image.Rectangle
		for x := b.Min.X; x < b.Max.X; x++ {
			if opaque(x, y) {
				row = row.Union(image.Rect(x, y, x+1, y+1))
			}
		}
		if row.Empty() {
			if !mark.Empty() {
				break // the gap above the word
			}
			continue
		}
		mark = mark.Union(row)
	}
	return mark
}

// downscale averages each w×h output pixel's area of r in src, with alpha
// premultiplied so transparent edges don't darken.
func downscale(src image.Image, r image.Rectangle, w, h int) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	for oy := range h {
		y0, y1 := r.Min.Y+oy*r.Dy()/h, r.Min.Y+(oy+1)*r.Dy()/h
		for ox := range w {
			x0, x1 := r.Min.X+ox*r.Dx()/w, r.Min.X+(ox+1)*r.Dx()/w
			var sr, sg, sb, sa, n uint64
			for y := y0; y < max(y1, y0+1); y++ {
				for x := x0; x < max(x1, x0+1); x++ {
					cr, cg, cb, ca := src.At(x, y).RGBA() // premultiplied
					sr, sg, sb, sa, n = sr+uint64(cr), sg+uint64(cg), sb+uint64(cb), sa+uint64(ca), n+1
				}
			}
			out.Set(ox, oy, color.RGBA64{uint16(sr / n), uint16(sg / n), uint16(sb / n), uint16(sa / n)})
		}
	}
	return out
}

// roundedRect fills img with c inside a rectangle with corners of radius r,
// antialiased over one pixel.
func roundedRect(img *image.NRGBA, c color.NRGBA, r int) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			// Distance past the corner circle, for pixels in a corner square.
			cx := min(max(x, b.Min.X+r), b.Max.X-1-r)
			cy := min(max(y, b.Min.Y+r), b.Max.Y-1-r)
			dx, dy := float64(x-cx), float64(y-cy)
			d := dx*dx + dy*dy
			alpha := 1.0
			if out := math.Sqrt(d) - float64(r); out > 0 {
				alpha = max(0, 1-out)
			}
			p := c
			p.A = uint8(float64(c.A) * alpha)
			img.SetNRGBA(x, y, p)
		}
	}
}
