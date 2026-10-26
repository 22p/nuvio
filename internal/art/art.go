// Package art renders deterministic, poster-like artwork for items that have no
// sidecar image. Every item gets a stable palette and composition derived from
// its id, which keeps the library visually rich without shipping stock images.
package art

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"sync"
	"sync/atomic"
)

var cache sync.Map // key -> []byte
var cacheCount atomic.Int64

type palette struct {
	hue    float64
	accent float64
	light  float64
}

func paletteFor(h [32]byte) palette {
	choices := []palette{
		{hue: 224, accent: 268, light: 0.44}, // indigo / violet
		{hue: 188, accent: 156, light: 0.42}, // cyan / mint
		{hue: 14, accent: 330, light: 0.46},  // ember / rose
		{hue: 258, accent: 200, light: 0.42}, // violet / sky
		{hue: 36, accent: 8, light: 0.46},    // gold / rust
	}
	p := choices[int(h[1])%len(choices)]
	p.hue += float64(int(h[2])%13) - 6
	p.light += (float64(int(h[3])%9) - 4) / 100
	return p
}

// Render returns a PNG of the requested size. Results are cached in memory.
func Render(seed string, width, height int) []byte {
	if width <= 0 {
		width = 600
	}
	if height <= 0 {
		height = 900
	}
	key := seed + "/" + itoa(width) + "x" + itoa(height)
	if v, ok := cache.Load(key); ok {
		return v.([]byte)
	}
	if cacheCount.Load() > 240 {
		cache.Range(func(k, _ any) bool {
			cache.Delete(k)
			return true
		})
		cacheCount.Store(0)
	}
	h := sha256.Sum256([]byte(seed))
	p := paletteFor(h)
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Diagonal base gradient.
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			t := (float64(x)*0.7 + float64(y)*1.1) / (float64(width)*0.7 + float64(height)*1.1)
			l := 0.055 + 0.10*(1-t)
			c := hsl(p.hue, 0.42, l)
			cr, cg, cb := c[0], c[1], c[2]
			// Purple-ish corner tint.
			t2 := float64(x) / float64(width)
			cr += 0.05 * t2
			cb += 0.05 * (1 - t2)
			img.SetRGBA(x, y, color.RGBA{to8(cr), to8(cg), to8(cb), 255})
		}
	}

	rnd := randSource(h)

	// Large soft light source.
	sunX := 0.2 + float64(rnd()%50)/100*0.6
	sunY := 0.18 + float64(rnd()%45)/100*0.5
	sunR := math.Min(float64(width), float64(height)) * (0.34 + float64(rnd()%18)/100)
	blob(img, sunX*float64(width), sunY*float64(height), sunR,
		hsl(p.accent, 0.78, 0.58), 0.62, 1.7)

	// Secondary atmospheric blobs.
	for i := 0; i < 3; i++ {
		cx := float64(rnd()%100) / 100 * float64(width)
		cy := 0.25 + float64(rnd()%80)/100*0.8
		cy *= float64(height)
		rr := math.Min(float64(width), float64(height)) * (0.18 + float64(rnd()%35)/100)
		alpha := 0.14 + float64(rnd()%14)/100
		hue := p.hue + float64(int(rnd()%50)-25)
		blob(img, cx, cy, rr, hsl(hue, 0.7, 0.55), alpha, 1.3)
	}

	// Thin orbital rings around the light source.
	if h[4]%2 == 0 {
		rings := 2 + int(h[5]%3)
		ringR := sunR * 0.55
		for i := 0; i < rings; i++ {
			ringR *= 1.22
			ring(img, sunX*float64(width), sunY*float64(height), ringR, hsl(p.accent, 0.6, 0.75), 0.13, 1.0, width, height)
		}
	}

	// Diagonal beam.
	if h[6]%3 != 0 {
		beam(img, float64(int(h[7])%60)/100+0.1, 0.10+float64(int(h[8])%8)/100, hsl(p.accent, 0.85, 0.7), width, height)
	}

	// Grain.
	for i := 0; i < width*height/14; i++ {
		x := int(rnd() % uint32(width))
		y := int(rnd() % uint32(height))
		v := int(rnd()%40) - 20
		c := img.RGBAAt(x, y)
		c.R = clamp8(int(c.R) + v)
		c.G = clamp8(int(c.G) + v)
		c.B = clamp8(int(c.B) + v)
		img.SetRGBA(x, y, c)
	}

	vignette(img)
	if height > width {
		bottomShade(img)
	}

	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 86})
	out := buf.Bytes()
	if _, loaded := cache.LoadOrStore(key, out); !loaded {
		cacheCount.Add(1)
	}
	return out
}

func randSource(h [32]byte) func() uint32 {
	state := binary.BigEndian.Uint64(h[8:16]) | 1
	return func() uint32 {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		return uint32(state >> 16)
	}
}

func blob(img *image.RGBA, cx, cy, radius float64, col [3]float64, alpha, falloff float64) {
	b := img.Bounds()
	minX := int(math.Max(0, cx-radius))
	maxX := int(math.Min(float64(b.Max.X), cx+radius))
	minY := int(math.Max(0, cy-radius))
	maxY := int(math.Min(float64(b.Max.Y), cy+radius))
	r2 := radius * radius
	for y := minY; y < maxY; y++ {
		dy := float64(y) - cy
		for x := minX; x < maxX; x++ {
			dx := float64(x) - cx
			d2 := (dx*dx + dy*dy) / r2
			if d2 >= 1 {
				continue
			}
			f := math.Pow(1-d2, falloff) * alpha
			add(img, x, y, col, f)
		}
	}
}

func ring(img *image.RGBA, cx, cy, radius float64, col [3]float64, alpha, width float64, w, h int) {
	minX := int(math.Max(0, cx-radius-width))
	maxX := int(math.Min(float64(w), cx+radius+width))
	minY := int(math.Max(0, cy-radius-width))
	maxY := int(math.Min(float64(h), cy+radius+width))
	for y := minY; y < maxY; y++ {
		dy := float64(y) - cy
		for x := minX; x < maxX; x++ {
			dx := float64(x) - cx
			d := math.Hypot(dx, dy)
			if math.Abs(d-radius) < width {
				add(img, x, y, col, alpha*(1-math.Abs(d-radius)/width))
			}
		}
	}
}

func beam(img *image.RGBA, cx, widthFrac float64, col [3]float64, w, h int) {
	slope := 0.45
	for y := 0; y < h; y++ {
		center := cx*float64(w) + slope*(float64(y)-float64(h)/2)
		half := widthFrac * float64(w) / 2
		for x := 0; x < w; x++ {
			d := math.Abs(float64(x) - center)
			if d < half {
				a := 0.11 * (1 - d/half)
				add(img, x, y, col, a)
			}
		}
	}
}

func vignette(img *image.RGBA) {
	b := img.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	cx, cy := w/2, h/2
	maxD := math.Hypot(cx, cy)
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			d := math.Hypot(float64(x)-cx, float64(y)-cy) / maxD
			f := 1 - 0.55*math.Pow(d, 2.4)
			c := img.RGBAAt(x, y)
			c.R = to8(float64(c.R) / 255 * f)
			c.G = to8(float64(c.G) / 255 * f)
			c.B = to8(float64(c.B) / 255 * f)
			img.SetRGBA(x, y, c)
		}
	}
}

func bottomShade(img *image.RGBA) {
	b := img.Bounds()
	h := b.Dy()
	start := int(float64(h) * 0.62)
	for y := start; y < h; y++ {
		f := 1 - 0.55*math.Pow(float64(y-start)/float64(h-start), 1.8)
		for x := 0; x < b.Dx(); x++ {
			c := img.RGBAAt(x, y)
			c.R = to8(float64(c.R) / 255 * f)
			c.G = to8(float64(c.G) / 255 * f)
			c.B = to8(float64(c.B) / 255 * f)
			img.SetRGBA(x, y, c)
		}
	}
}

func add(img *image.RGBA, x, y int, col [3]float64, a float64) {
	if a <= 0 {
		return
	}
	c := img.RGBAAt(x, y)
	r := float64(c.R)/255 + col[0]*a
	g := float64(c.G)/255 + col[1]*a
	bl := float64(c.B)/255 + col[2]*a
	img.SetRGBA(x, y, color.RGBA{to8(r), to8(g), to8(bl), 255})
}

func to8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return uint8(v*255 + 0.5)
}

func clamp8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func hsl(h, s, l float64) [3]float64 {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return [3]float64{r + m, g + m, b + m}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
