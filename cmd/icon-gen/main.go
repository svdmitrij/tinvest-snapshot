// icon-gen generates PNG and ICO icons for tinvest-snapshot from a
// programmatic candlestick-chart design. No external dependencies required.
//
// Usage: go run ./cmd/icon-gen [--out <dir>]
// Outputs: icon_128.png, icon_16.png, icon_32.png, icon_48.png,
//           icon_256.png, icon.ico (multi-resolution)
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
)

// Palette colours.
var (
	bgColor       = color.RGBA{0x16, 0x21, 0x3E, 0xFF}
	gridColor     = color.RGBA{0x2A, 0x2A, 0x4A, 0x80}
	greenCandle   = color.RGBA{0x00, 0xD4, 0xAA, 0xFF}
	redCandle     = color.RGBA{0xFF, 0x47, 0x57, 0xFF}
	trendColor    = color.RGBA{0xFF, 0xD9, 0x3D, 0xB0}
	maColor       = color.RGBA{0x4D, 0xA6, 0xFF, 0x99}
)

// candle holds one candlestick definition in relative [0..1] coords.
type candle struct {
	x     float64 // centre x
	low   float64
	open  float64
	close float64
	high  float64
}

// candles for the chart — five sticks, three up, two down.
var candles = []candle{
	{x: 0.22, low: 0.70, open: 0.55, close: 0.38, high: 0.28},  // green
	{x: 0.40, low: 0.78, open: 0.36, close: 0.60, high: 0.22},  // red
	{x: 0.58, low: 0.82, open: 0.50, close: 0.30, high: 0.18},  // green
	{x: 0.76, low: 0.68, open: 0.44, close: 0.36, high: 0.24},  // green
	{x: 0.94, low: 0.60, open: 0.46, close: 0.54, high: 0.34},  // red
}

func main() {
	outDir := flag.String("out", "assets", "output directory for icon files")
	flag.Parse()

	sizes := []int{16, 32, 48, 128, 256}
	var pngData [5][]byte

	for i, sz := range sizes {
		img := renderIcon(sz)
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			log.Fatalf("PNG encode %d: %v", sz, err)
		}
		pngData[i] = buf.Bytes()

		fname := filepath.Join(*outDir, fmt.Sprintf("icon_%d.png", sz))
		if err := os.WriteFile(fname, buf.Bytes(), 0644); err != nil {
			log.Fatalf("write %s: %v", fname, err)
		}
		fmt.Printf("  %s (%d bytes)\n", fname, len(buf.Bytes()))
	}

	// Build multi-resolution ICO.
	icoPath := filepath.Join(*outDir, "icon.ico")
	icoBytes := buildICO([][]byte{
		pngData[0], // 16
		pngData[1], // 32
		pngData[2], // 48
		pngData[3], // 128 (used as 256×256 reference in ICO; actual 256 would be too big)
		pngData[4], // 256
	})
	if err := os.WriteFile(icoPath, icoBytes, 0644); err != nil {
		log.Fatalf("write %s: %v", icoPath, err)
	}
	fmt.Printf("  %s (%d bytes)\n", icoPath, len(icoBytes))
}

func renderIcon(sz int) *image.RGBA {
	margin := int(math.Round(float64(sz) * 0.125)) // ~12.5% margin
	chartW := sz - 2*margin
	chartH := sz - 2*margin
	chartX0 := margin
	chartY0 := margin

	img := image.NewRGBA(image.Rect(0, 0, sz, sz))
	// Rounded-rect background: fill corners with bg, main rect with bg.
	draw.Draw(img, img.Bounds(), image.NewUniform(bgColor), image.Point{}, draw.Src)
	// Draw subtle rounded corners by clipping a few pixels
	cornerR := int(math.Round(float64(sz) * 0.094)) // ~12px for 128
	for y := 0; y < cornerR; y++ {
		for x := 0; x < cornerR; x++ {
			dx := cornerR - 1 - x
			dy := cornerR - 1 - y
			if dx*dx+dy*dy > cornerR*cornerR {
				// Top-left
				img.Set(x, y, color.Transparent)
				// Top-right
				img.Set(sz-1-x, y, color.Transparent)
				// Bottom-left
				img.Set(x, sz-1-y, color.Transparent)
				// Bottom-right
				img.Set(sz-1-x, sz-1-y, color.Transparent)
			}
		}
	}

	// Grid lines (horizontal)
	gridLines := 5
	for i := 0; i < gridLines; i++ {
		y := chartY0 + i*chartH/(gridLines-1)
		hLine(img, chartX0, y, chartW, gridColor)
	}

	// Draw candles.
	bodyW := int(math.Round(float64(chartW) * 0.08))
	if bodyW < 1 {
		bodyW = 1
	}
	for _, c := range candles {
		cx := chartX0 + int(math.Round(c.x*float64(chartW)))
		highY := chartY0 + int(math.Round(c.high*float64(chartH)))
		lowY := chartY0 + int(math.Round(c.low*float64(chartH)))
		openY := chartY0 + int(math.Round(c.open*float64(chartH)))
		closeY := chartY0 + int(math.Round(c.close*float64(chartH)))

		bodyTop := openY
		bodyBot := closeY
		clr := greenCandle
		if closeY > openY { // red candle: close below open
			bodyTop, bodyBot = bodyBot, bodyTop
			clr = redCandle
		}

		// Wick
		vLine(img, cx, highY, lowY, clr)

		// Body
		bodyH := bodyBot - bodyTop
		if bodyH < 1 {
			bodyH = 1
		}
		fillRect(img, cx-bodyW/2, bodyTop, bodyW, bodyH, clr)
	}

	// Trend line (dashed rising)
	trendY0 := chartY0 + int(math.Round(0.62*float64(chartH)))
	trendY1 := chartY0 + int(math.Round(0.34*float64(chartH)))
	trendX0 := chartX0 + int(math.Round(0.22*float64(chartW)))
	trendX1 := chartX0 + int(math.Round(0.94*float64(chartW)))
	dashedLine(img, trendX0, trendY0, trendX1, trendY1, trendColor, 3, 2)

	// Moving average (thin solid)
	maPoints := []struct{ x, y float64 }{
		{0.15, 0.58}, {0.24, 0.50}, {0.33, 0.54}, {0.42, 0.44},
		{0.50, 0.40}, {0.58, 0.34}, {0.66, 0.42}, {0.74, 0.32},
		{0.82, 0.30}, {0.90, 0.26}, {0.98, 0.28},
	}
	for i := 1; i < len(maPoints); i++ {
		x0 := chartX0 + int(math.Round(maPoints[i-1].x*float64(chartW)))
		y0 := chartY0 + int(math.Round(maPoints[i-1].y*float64(chartH)))
		x1 := chartX0 + int(math.Round(maPoints[i].x*float64(chartW)))
		y1 := chartY0 + int(math.Round(maPoints[i].y*float64(chartH)))
		bresenham(img, x0, y0, x1, y1, maColor)
	}

	return img
}

// ----- drawing helpers -----

func setPt(img *image.RGBA, x, y int, c color.RGBA) {
	if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
		return
	}
	img.SetRGBA(x, y, c)
}

func hLine(img *image.RGBA, x, y, w int, c color.RGBA) {
	for i := 0; i < w; i++ {
		setPt(img, x+i, y, c)
	}
}

func vLine(img *image.RGBA, x, y0, y1 int, c color.RGBA) {
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	for y := y0; y <= y1; y++ {
		setPt(img, x, y, c)
	}
}

func fillRect(img *image.RGBA, x, y, w, h int, c color.RGBA) {
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			setPt(img, x+dx, y+dy, c)
		}
	}
}

func bresenham(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	sx := 1
	if x0 > x1 {
		sx = -1
	}
	sy := 1
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy

	for {
		setPt(img, x0, y0, c)
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func dashedLine(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA, dashLen, gapLen int) {
	dx := abs(x1 - x0)
	dy := abs(y1 - y0)
	sx := 1
	if x0 > x1 {
		sx = -1
	}
	sy := 1
	if y0 > y1 {
		sy = -1
	}
	err := dx - dy
	step := 0
	mode := dashLen
	total := dashLen + gapLen

	for {
		if mode > 0 {
			setPt(img, x0, y0, c)
		}
		step++
		if step >= total {
			step = 0
		}
		if step < dashLen {
			mode = dashLen - step
		} else {
			mode = 0
		}
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x0 += sx
		}
		if e2 < dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// ----- ICO builder (PNG-embedded format, Vista+) -----

// buildICO creates a multi-resolution .ico file from a slice of PNG payloads.
// payloads must be ordered smallest to largest.
func buildICO(pngs [][]byte) []byte {
	count := uint16(len(pngs))
	// Header: 6 bytes (reserved + type + count)
	// Directory: 16 bytes per entry
	// Image data follows.
	dirSize := 6 + int(count)*16
	offset := uint32(dirSize)

	var buf bytes.Buffer
	// Header
	buf.Write([]byte{0, 0, 1, 0}) // reserved=0, type=ICO=1
	binary.Write(&buf, binary.LittleEndian, count)

	// Collect directory entries (write after we know offsets)
	type dirent struct {
		w, h    uint8
		palette uint8
		resvd   uint8
		planes  uint16
		bpp     uint16
		size    uint32
		off     uint32
	}
	var dirs []dirent

	for _, png := range pngs {
		sz := uint32(len(png))
		// PNG header has dimensions at bytes 16-23
		w := uint32(0)
		h := uint32(0)
		if len(png) >= 24 {
			w = binary.BigEndian.Uint32(png[16:20])
			h = binary.BigEndian.Uint32(png[20:24])
		}
		// ICO uses 0 for 256
		bw := uint8(w)
		bh := uint8(h)
		if w >= 256 {
			bw = 0
		}
		if h >= 256 {
			bh = 0
		}
		dirs = append(dirs, dirent{
			w: bw, h: bh, palette: 0, resvd: 0,
			planes: 1, bpp: 32,
			size: sz, off: offset,
		})
		offset += sz
	}

	for _, d := range dirs {
		binary.Write(&buf, binary.LittleEndian, d)
	}
	for _, png := range pngs {
		buf.Write(png)
	}
	return buf.Bytes()
}
